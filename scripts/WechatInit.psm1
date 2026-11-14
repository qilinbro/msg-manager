# Local-only helpers. No process capture, discovery, or network runs on import.
Set-StrictMode -Version Latest

function Assert-WechatInitPath {
    param([Parameter(Mandatory)][string]$Path)
    if (-not [IO.Path]::IsPathFullyQualified($Path) -or $Path.StartsWith('\\') -or $Path.StartsWith('//')) { throw 'INVALID_LOCAL_PATH' }
    $absolute = [IO.Path]::GetFullPath($Path)
    $volume = [IO.Path]::GetPathRoot($absolute)
    if ($absolute.Substring($volume.Length).Contains(':') -or ([IO.DriveInfo]::new($volume)).DriveType -ne [IO.DriveType]::Fixed) { throw 'INVALID_LOCAL_PATH' }
    for ($ancestor = $absolute; $ancestor; $ancestor = Split-Path -Parent $ancestor) {
        if (Test-Path -LiteralPath $ancestor) {
            if ((Get-Item -LiteralPath $ancestor -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'LINK_PATH_DENIED' }
        }
    }
    return $absolute
}

function Get-WechatInitSelection {
    param([Parameter(Mandatory)][string]$Directory, [Parameter(Mandatory)][string]$AccountID)
    $absolute = Assert-WechatInitPath $Directory
    if ($AccountID -cnotmatch '^[A-Za-z][A-Za-z0-9_-]{1,127}$' -or $AccountID.StartsWith('gh_') -or $AccountID.ToLowerInvariant() -in @('filehelper','weixin','fmessage','medianote','newsapp','notification_messages','floatbottle','qqmail','tmessage','qmessage','masssendapp','weibo','brandsessionholder','helper_entry')) { throw 'INVALID_ACCOUNT' }
    if ((Split-Path -Leaf $absolute) -cne 'db_storage' -or -not (Test-Path -LiteralPath $absolute -PathType Container)) { throw 'INVALID_ACCOUNT_DIRECTORY' }
    $parent = Split-Path -Leaf (Split-Path -Parent $absolute)
    if ($parent -cne $AccountID -and $parent -cnotmatch ('^' + [regex]::Escape($AccountID) + '_[A-Za-z0-9]{4}$')) { throw 'ACCOUNT_DIRECTORY_MISMATCH' }
    $database = Assert-WechatInitPath (Join-Path $absolute 'session/session.db')
    if (-not (Test-Path -LiteralPath $database -PathType Leaf)) { throw 'SOURCE_DATABASE_MISSING' }
    return @{ directory = $absolute; account_id = $AccountID; database = $database }
}

function Initialize-WechatPrivateDirectory {
    param([Parameter(Mandatory)][string]$Path)
    $absolute = Assert-WechatInitPath $Path
    if ((Test-Path -LiteralPath $absolute) -and -not (Test-Path -LiteralPath $absolute -PathType Container)) { throw 'INVALID_PRIVATE_DIRECTORY' }
    New-Item -ItemType Directory -Path $absolute -Force -ErrorAction Stop | Out-Null
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent().User
    $directory = [IO.DirectoryInfo]::new($absolute)
    $sections = [Security.AccessControl.AccessControlSections]::Access -bor [Security.AccessControl.AccessControlSections]::Owner
    $acl = [IO.FileSystemAclExtensions]::GetAccessControl($directory, $sections)
    if ($acl.GetOwner([Security.Principal.SecurityIdentifier]).Value -cne $identity.Value) { throw 'PRIVATE_DIRECTORY_OWNER_MISMATCH' }
    $rules = @($acl.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier]))
    $alreadyPrivate = $acl.AreAccessRulesProtected -and $rules.Count -gt 0
    $hasFullControl = $false
    foreach ($rule in $rules) {
        if ($rule.IdentityReference.Value -cne $identity.Value -or $rule.AccessControlType -ne [Security.AccessControl.AccessControlType]::Allow -or $rule.IsInherited) { $alreadyPrivate = $false }
        if (($rule.FileSystemRights -band [Security.AccessControl.FileSystemRights]::FullControl) -eq [Security.AccessControl.FileSystemRights]::FullControl -and ($rule.InheritanceFlags -band [Security.AccessControl.InheritanceFlags]::ContainerInherit) -and ($rule.InheritanceFlags -band [Security.AccessControl.InheritanceFlags]::ObjectInherit)) { $hasFullControl = $true }
    }
    if ($alreadyPrivate -and $hasFullControl) { return $absolute }
    # A fresh descriptor changes only its DACL. Never request owner/SACL writes,
    # which can require privileges an ordinary local launch does not possess.
    $restricted = [Security.AccessControl.DirectorySecurity]::new()
    $restricted.SetAccessRuleProtection($true, $false)
    $restricted.SetAccessRule([Security.AccessControl.FileSystemAccessRule]::new($identity, 'FullControl', 'ContainerInherit,ObjectInherit', 'None', 'Allow'))
    [IO.FileSystemAclExtensions]::SetAccessControl($directory, $restricted)
    return $absolute
}

function Read-WechatInitSecret {
    param([Parameter(Mandatory)][string]$Path, [Parameter(Mandatory)][string]$AccountID)
    $secret = $null
    try {
        $absolute = Assert-WechatInitPath $Path
        $info = Get-Item -LiteralPath $absolute -Force
        if ($info.PSIsContainer -or $info.Length -gt 16384 -or $info.Length -lt 1) { throw 'INVALID_CAPTURE' }
        $document = [System.Text.Json.JsonDocument]::Parse([IO.File]::ReadAllText($absolute))
        try {
            if ($document.RootElement.ValueKind -ne [System.Text.Json.JsonValueKind]::Object) { throw 'INVALID_CAPTURE' }
            $names = [Collections.Generic.HashSet[string]]::new([StringComparer]::Ordinal)
            foreach ($property in $document.RootElement.EnumerateObject()) {
                if (-not $names.Add($property.Name) -or $property.Name -cnotin @('format','account_id','client_version','protected_key')) { throw 'INVALID_CAPTURE' }
            }
            if ($names.Count -ne 4 -or $document.RootElement.GetProperty('format').GetInt32() -ne 1 -or $document.RootElement.GetProperty('account_id').GetString() -cne $AccountID -or $document.RootElement.GetProperty('client_version').GetString() -cne '4.1.15.56') { throw 'CAPTURE_IDENTITY_MISMATCH' }
            $cipher = [Convert]::FromBase64String($document.RootElement.GetProperty('protected_key').GetString())
        } finally { $document.Dispose() }
        Add-Type -AssemblyName System.Security.Cryptography.ProtectedData
        $entropy = [Text.Encoding]::UTF8.GetBytes('msg-manager/wechat-init/v1')
        $secret = [Security.Cryptography.ProtectedData]::Unprotect($cipher, $entropy, [Security.Cryptography.DataProtectionScope]::CurrentUser)
        if ($secret.Length -ne 32) { throw 'INVALID_CAPTURE' }
        $secure = ConvertTo-SecureString -String ([Convert]::ToHexString($secret)) -AsPlainText -Force
        return $secure
    } catch {
        throw 'CAPTURE_COULD_NOT_BE_VERIFIED'
    } finally {
        if ($null -ne $secret) { [Array]::Clear($secret, 0, $secret.Length) }
    }
}

function Invoke-WechatInitProbe {
    param([Parameter(Mandatory)][string]$Executable, [Parameter(Mandatory)][hashtable]$Selection, [Parameter(Mandatory)][Security.SecureString]$Secret)
    $process = $null
    $pointer = [IntPtr]::Zero
    try {
        $exe = Assert-WechatInitPath $Executable
        if (-not (Test-Path -LiteralPath $exe -PathType Leaf)) { throw 'PROBE_MISSING' }
        $pointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($Secret)
        $inputJSON = @{ directory = $Selection.directory; account_id = $Selection.account_id; key = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($pointer) } | ConvertTo-Json -Compress
        $startInfo = [Diagnostics.ProcessStartInfo]::new()
        $startInfo.FileName = $exe
        $startInfo.UseShellExecute = $false
        $startInfo.CreateNoWindow = $true
        $startInfo.WindowStyle = [Diagnostics.ProcessWindowStyle]::Hidden
        $startInfo.RedirectStandardInput = $true
        $startInfo.RedirectStandardOutput = $true
        $startInfo.RedirectStandardError = $true
        $process = [Diagnostics.Process]::Start($startInfo)
        $outputTask = $process.StandardOutput.ReadToEndAsync()
        $errorTask = $process.StandardError.ReadToEndAsync()
        $process.StandardInput.Write($inputJSON)
        $process.StandardInput.Close()
        if (-not $process.WaitForExit(35000)) { $process.Kill($true); throw 'PROBE_TIMEOUT' }
        $output = $outputTask.GetAwaiter().GetResult()
        $null = $errorTask.GetAwaiter().GetResult()
        if ($process.ExitCode -ne 0 -or $output.Length -gt 2048) { throw 'PROBE_FAILED' }
        $result = $output | ConvertFrom-Json
        if ($result.ok -cne $true -or $result.read_only -cne $true -or $result.account_id -cne $Selection.account_id) { throw 'PROBE_FAILED' }
    } catch {
        throw 'DATABASE_VERIFICATION_FAILED'
    } finally {
        if ($pointer -ne [IntPtr]::Zero) { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($pointer) }
        if ($null -ne $process) {
            if (-not $process.HasExited) { $process.Kill($true); $null = $process.WaitForExit(5000) }
            $process.Dispose()
        }
        Remove-Variable inputJSON,output -ErrorAction SilentlyContinue
    }
}

function Save-WechatInitConfiguration {
    param([Parameter(Mandatory)][string]$Destination, [Parameter(Mandatory)][hashtable]$Selection, [Parameter(Mandatory)][Security.SecureString]$Secret, [switch]$Replace)
    $pointer = [IntPtr]::Zero
    $secureConfig = $null
    $temporary = $null
    $stage = 'PATH'
    try {
        $target = Assert-WechatInitPath $Destination
        if (Test-Path -LiteralPath $target) {
            if ((Get-Item -LiteralPath $target -Force).PSIsContainer -or -not $Replace) { throw 'CONFIGURATION_ALREADY_EXISTS' }
        }
        $stage = 'DIRECTORY'
        $private = Initialize-WechatPrivateDirectory (Split-Path -Parent $target)
        $stage = 'SECRET'
        $pointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($Secret)
        $hex = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($pointer)
        if ($hex -cnotmatch '^[a-fA-F0-9]{64}$') { throw 'INVALID_KEY_FORMAT' }
        $stage = 'PROTECTION'
        $raw = @{ directory = $Selection.directory; account_id = $Selection.account_id; key = $hex; limit = 100 } | ConvertTo-Json -Compress
        $secureConfig = ConvertTo-SecureString -String $raw -AsPlainText -Force
        $temporary = Join-Path $private ('wechat-' + [guid]::NewGuid().ToString('N') + '.tmp')
        $stage = 'WRITE'
        $secureConfig | ConvertFrom-SecureString | Set-Content -LiteralPath $temporary -Encoding UTF8 -NoNewline -ErrorAction Stop
        $stage = 'MOVE'
        Move-Item -LiteralPath $temporary -Destination $target -Force:$Replace -ErrorAction Stop
    } catch {
        throw ('CONFIGURATION_NOT_SAVED_' + $stage)
    } finally {
        if ($pointer -ne [IntPtr]::Zero) { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($pointer) }
        if ($null -ne $secureConfig) { $secureConfig.Dispose() }
        if ($temporary -and (Test-Path -LiteralPath $temporary -PathType Leaf)) { Remove-Item -LiteralPath $temporary }
        Remove-Variable raw,hex -ErrorAction SilentlyContinue
    }
}

Export-ModuleMember -Function Assert-WechatInitPath,Get-WechatInitSelection,Initialize-WechatPrivateDirectory,Read-WechatInitSecret,Invoke-WechatInitProbe,Save-WechatInitConfiguration

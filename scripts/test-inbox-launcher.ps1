#requires -Version 7.2
# Synthetic launcher regression checks. No real inbox or WeChat files are used.
param([switch]$RunChild, [string]$FixtureRoot = '')
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
if (-not $IsWindows) { throw 'Inbox launcher validation requires Windows DPAPI.' }
$projectRoot = [IO.Path]::GetFullPath((Split-Path -Parent $PSScriptRoot))
$testParent = Join-Path $projectRoot '.runtime'

function Assert-LauncherTest([bool]$Condition, [string]$Message) {
    if (-not $Condition) { throw $Message }
}

function Assert-FixtureRoot([string]$Path) {
    $absolute = [IO.Path]::GetFullPath($Path)
    if ([IO.Path]::GetDirectoryName($absolute) -cne $testParent -or [IO.Path]::GetFileName($absolute) -notmatch '^inbox-launcher-test-[a-f0-9]{32}$') { throw 'Unsafe synthetic fixture path.' }
    return $absolute
}

if (-not $RunChild) {
    $testRoot = Assert-FixtureRoot (Join-Path $testParent ('inbox-launcher-test-' + [guid]::NewGuid().ToString('N')))
    $process = $null
    try {
        New-Item -ItemType Directory -Path $testRoot -Force | Out-Null
        $startInfo = [Diagnostics.ProcessStartInfo]::new()
        $startInfo.FileName = [Diagnostics.Process]::GetCurrentProcess().MainModule.FileName
        $startInfo.UseShellExecute = $false
        $startInfo.CreateNoWindow = $true
        $startInfo.WindowStyle = [Diagnostics.ProcessWindowStyle]::Hidden
        $startInfo.RedirectStandardOutput = $true
        $startInfo.RedirectStandardError = $true
        foreach ($argument in @('-NoLogo','-NoProfile','-NonInteractive','-File',$PSCommandPath,'-RunChild','-FixtureRoot',$testRoot)) { $startInfo.ArgumentList.Add($argument) }
        foreach ($name in @('SOCIALD_API_TOKEN','SOCIALD_CREDENTIAL_FILE','SOCIALD_URL','SOCIALD_ADDR','SOCIALD_DATABASE_KEY','SOCIALD_DATABASE_PATH','WECHAT_DB_DIR','WECHAT_DB_ACCOUNT_ID','WECHAT_DB_KEY','WECHAT_DB_LIMIT')) { $null = $startInfo.Environment.Remove($name) }
        $startInfo.Environment['SOCIALD_CREDENTIAL_FILE'] = Join-Path $testRoot 'must-not-use-default/service.dpapi'
        $process = [Diagnostics.Process]::Start($startInfo)
        $stdoutTask = $process.StandardOutput.ReadToEndAsync()
        $stderrTask = $process.StandardError.ReadToEndAsync()
        if (-not $process.WaitForExit(45000)) { throw 'Synthetic inbox launcher tests timed out.' }
        $stdout = $stdoutTask.GetAwaiter().GetResult()
        $stderr = $stderrTask.GetAwaiter().GetResult()
        if ($process.ExitCode -ne 0) { throw ('Synthetic launcher validation failed. ' + $stderr) }
        if ($stdout -notmatch '(?m)^Inbox launcher synthetic tests passed: [0-9]+ isolated launches\r?$') { throw 'Synthetic launcher validation did not report completion.' }
        Write-Output ($Matches[0].Trim() + '; DACL-only setup, stable service credentials, explicit clipboard/rotation, DPAPI loading and fail-closed startup. No real account data or service was accessed.')
    } finally {
        if ($null -ne $process) {
            if (-not $process.HasExited) { $process.Kill($true); $null = $process.WaitForExit(5000) }
            $process.Dispose()
        }
        $resolvedRoot = Assert-FixtureRoot $testRoot
        if (Test-Path -LiteralPath $resolvedRoot) { Remove-Item -LiteralPath $resolvedRoot -Recurse -Force }
    }
    exit 0
}

$FixtureRoot = Assert-FixtureRoot $FixtureRoot
if (-not (Test-Path -LiteralPath $FixtureRoot -PathType Container)) { throw 'Synthetic fixture root is missing.' }
# Keep all stubs and environment changes inside this isolated child host.
foreach ($name in @('WECHAT_DB_DIR','WECHAT_DB_ACCOUNT_ID','WECHAT_DB_KEY','WECHAT_DB_LIMIT','SOCIALD_DATABASE_KEY','SOCIALD_DATABASE_PATH')) { Remove-Item -LiteralPath "Env:$name" -ErrorAction SilentlyContinue }
$syntheticExplicitToken = 'synthetic-launcher-token-12345678901234567890'
$env:SOCIALD_API_TOKEN = $syntheticExplicitToken
$env:SOCIALD_CREDENTIAL_FILE = Join-Path $FixtureRoot 'must-not-use-default/service.dpapi'
$global:InboxLauncherTestState = @{ key_path = ''; key_writes = 0; clipboard_calls = 0; set_acl_calls = 0; backend_calls = 0; backend_token_hash = ''; launches = 0 }
$currentSID = [Security.Principal.WindowsIdentity]::GetCurrent().User
$sections = [Security.AccessControl.AccessControlSections]::Access -bor [Security.AccessControl.AccessControlSections]::Owner -bor [Security.AccessControl.AccessControlSections]::Group
Import-Module (Join-Path $PSScriptRoot 'ServiceCredentials.psm1') -Force

function Assert-SyntheticServiceEnvironment {
    Assert-LauncherTest ([Environment]::GetEnvironmentVariables('Process').Keys -icontains 'SOCIALD_CREDENTIAL_FILE') 'A synthetic test lost its explicit credential override; never use the real Known Folder.'
    $resolved = $null
    try { $resolved = Get-ServiceCredentialPath }
    catch { return } # Invalid/empty/relative overrides are deliberately tested.
    # The resolver has not opened a credential. Stop before any initialization
    # if a case-sensitivity/fallback regression would select a real user path.
    Assert-LauncherTest ($resolved.StartsWith($FixtureRoot.TrimEnd('\') + '\', [StringComparison]::OrdinalIgnoreCase)) 'Credential resolution escaped the synthetic fixture; no credential was opened.'
}

function Assert-PrivateFixtureDirectory([string]$Directory) {
    $acl = [IO.FileSystemAclExtensions]::GetAccessControl([IO.DirectoryInfo]::new($Directory), $sections)
    Assert-LauncherTest $acl.AreAccessRulesProtected 'The key directory still inherits access rules.'
    Assert-LauncherTest ($acl.GetOwner([Security.Principal.SecurityIdentifier]).Value -ceq $currentSID.Value) 'The key directory owner changed.'
    $rules = @($acl.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier]))
    Assert-LauncherTest ($rules.Count -gt 0) 'The key directory has no access rules.'
    $hasFullControl = $false
    foreach ($rule in $rules) {
        Assert-LauncherTest ($rule.IdentityReference.Value -ceq $currentSID.Value -and $rule.AccessControlType -eq [Security.AccessControl.AccessControlType]::Allow -and -not $rule.IsInherited) 'Another identity retained access to the key directory.'
        if (($rule.FileSystemRights -band [Security.AccessControl.FileSystemRights]::FullControl) -eq [Security.AccessControl.FileSystemRights]::FullControl -and ($rule.InheritanceFlags -band [Security.AccessControl.InheritanceFlags]::ContainerInherit) -and ($rule.InheritanceFlags -band [Security.AccessControl.InheritanceFlags]::ObjectInherit) -and $rule.PropagationFlags -eq [Security.AccessControl.PropagationFlags]::None) { $hasFullControl = $true }
    }
    Assert-LauncherTest $hasFullControl 'The current user lacks inheritable full control of the key directory.'
}

function global:Set-Acl {
    $global:InboxLauncherTestState.set_acl_calls++
    throw 'The launcher must not call the privilege-sensitive Set-Acl provider.'
}

function global:Set-Clipboard {
    param([Parameter(ValueFromPipeline)][object]$Value)
    process { $global:InboxLauncherTestState.clipboard_calls++ }
}

function global:Set-Content {
    [CmdletBinding()]
    param([Parameter(ValueFromPipeline)][object]$Value, [string]$LiteralPath, [string]$Encoding = 'utf8', [switch]$NoNewline)
    process {
        Assert-LauncherTest ($LiteralPath -ceq $global:InboxLauncherTestState.key_path) 'An unexpected file write reached the launcher stub.'
        Assert-PrivateFixtureDirectory (Split-Path -Parent $LiteralPath)
        $global:InboxLauncherTestState.key_writes++
        Microsoft.PowerShell.Management\Set-Content -LiteralPath $LiteralPath -Value $Value -Encoding $Encoding -NoNewline:$NoNewline
    }
}

function Save-SyntheticProtectedText([string]$Path, [string]$Text) {
    $secure = ConvertTo-SecureString -String $Text -AsPlainText -Force
    try { [IO.File]::WriteAllText($Path, (ConvertFrom-SecureString -SecureString $secure), [Text.UTF8Encoding]::new($false)) }
    finally { $secure.Dispose() }
}

function Get-SyntheticFreeAddress {
    $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 0)
    try { $listener.Start(); return '127.0.0.1:' + $listener.LocalEndpoint.Port }
    finally { $listener.Stop() }
}

function New-LauncherFixture([string]$Name) {
    $root = Join-Path $FixtureRoot $Name
    $scripts = Join-Path $root 'scripts'
    New-Item -ItemType Directory -Path $scripts -Force | Out-Null
    foreach ($name in @('start-inbox-core.ps1','WechatInit.psm1','ServiceCredentials.psm1')) { Copy-Item -LiteralPath (Join-Path $PSScriptRoot $name) -Destination (Join-Path $scripts $name) }
    $env:SOCIALD_CREDENTIAL_FILE = Join-Path $root 'service-private/service.dpapi'
    $env:SOCIALD_ADDR = Get-SyntheticFreeAddress
    $env:SOCIALD_URL = 'http://127.0.0.1:1' # Must never override the backend address.
    $stub = @'
$root = Split-Path -Parent $PSScriptRoot
if ($env:SOCIALD_DATABASE_PATH -cne (Join-Path $root 'data/messages.db') -or $env:SOCIALD_DATABASE_KEY -cnotmatch '^[A-Fa-f0-9]{64}$') { throw 'Synthetic backend did not receive the inbox configuration.' }
if (Test-Path -LiteralPath (Join-Path $root 'expect-wechat')) {
    if ($env:WECHAT_DB_ACCOUNT_ID -cne 'wxid_synthetic_launcher' -or $env:WECHAT_DB_KEY -cne ('22' * 32) -or $env:WECHAT_DB_DIR -cne (Join-Path $root 'synthetic-wechat/db_storage') -or $env:WECHAT_DB_LIMIT -cne '100') { throw 'Synthetic backend did not receive the protected WeChat configuration.' }
}
if ($env:SOCIALD_API_TOKEN.Length -lt 32) { throw 'Synthetic backend did not receive a valid API token.' }
$global:InboxLauncherTestState.backend_calls++
$global:InboxLauncherTestState.backend_token_hash = [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData([Text.Encoding]::UTF8.GetBytes($env:SOCIALD_API_TOKEN)))
[IO.File]::WriteAllText((Join-Path $root 'backend-reached'), $global:InboxLauncherTestState.backend_token_hash)
'@
    [IO.File]::WriteAllText((Join-Path $scripts 'dev-backend.ps1'), $stub, [Text.UTF8Encoding]::new($false))
    $global:InboxLauncherTestState.key_path = Join-Path $root 'data/private/inbox-key.dpapi'
    $global:InboxLauncherTestState.key_writes = 0
    return $root
}

function Invoke-LauncherFixture([string]$Root, [switch]$Reject, [switch]$CopyApiToken, [switch]$RotateApiToken) {
    Assert-SyntheticServiceEnvironment
    $rejected = $false
    $calls = $global:InboxLauncherTestState.backend_calls
    $copies = $global:InboxLauncherTestState.clipboard_calls
    $beforeToken = [Environment]::GetEnvironmentVariable('SOCIALD_API_TOKEN', 'Process')
    $output = ''
    try { $output = & (Join-Path $Root 'scripts/start-inbox-core.ps1') -CopyApiToken:$CopyApiToken -RotateApiToken:$RotateApiToken *>&1 | Out-String }
    catch { if (-not $Reject) { throw }; $rejected = $true }
    Assert-LauncherTest ($rejected -eq [bool]$Reject) 'Launcher acceptance did not match the expected result.'
    Assert-LauncherTest (($global:InboxLauncherTestState.backend_calls - $calls) -eq [int](-not $Reject)) 'An invalid startup reached the synthetic backend, or valid startup did not.'
    if (-not $CopyApiToken) { Assert-LauncherTest ($copies -eq $global:InboxLauncherTestState.clipboard_calls) 'Default startup touched the clipboard.' }
    elseif (-not $Reject) { Assert-LauncherTest (($global:InboxLauncherTestState.clipboard_calls - $copies) -eq 1) 'An explicit copy did not reach the clipboard exactly once.' }
    $afterToken = [Environment]::GetEnvironmentVariable('SOCIALD_API_TOKEN', 'Process')
    Assert-LauncherTest (($null -eq $beforeToken -and $null -eq $afterToken) -or ($null -ne $beforeToken -and $null -ne $afterToken -and $beforeToken -ceq $afterToken)) 'The launcher did not restore/clear the API token environment exactly.'
    Assert-LauncherTest ($output -cnotmatch '[A-F0-9]{64}' -and -not $output.Contains($syntheticExplicitToken)) 'Launcher output exposed a synthetic token/key.'
    Assert-LauncherTest (-not $env:SOCIALD_DATABASE_KEY) 'The launcher left the database key in its child environment.'
    foreach ($name in @('WECHAT_DB_DIR','WECHAT_DB_ACCOUNT_ID','WECHAT_DB_KEY','WECHAT_DB_LIMIT')) { Assert-LauncherTest (-not [Environment]::GetEnvironmentVariable($name)) 'The launcher left loaded WeChat configuration in its child environment.' }
    $global:InboxLauncherTestState.launches++
}

# A new private directory must be secured before the first protected-key write.
$root = New-LauncherFixture 'new-private-directory'
Invoke-LauncherFixture $root
Assert-LauncherTest ($global:InboxLauncherTestState.key_writes -eq 1) 'The new inbox key was not written once.'
Assert-PrivateFixtureDirectory (Join-Path $root 'data/private')

# The user's reported flow: the directory/config already exists after initialization.
$root = New-LauncherFixture 'existing-private-configuration'
Import-Module (Join-Path $root 'scripts/WechatInit.psm1') -Force
$private = Initialize-WechatPrivateDirectory (Join-Path $root 'data/private')
$keyPath = Join-Path $private 'inbox-key.dpapi'
Save-SyntheticProtectedText $keyPath ('11' * 32)
$configuration = @{ directory = (Join-Path $root 'synthetic-wechat/db_storage'); account_id = 'wxid_synthetic_launcher'; key = ('22' * 32); limit = 100 } | ConvertTo-Json -Compress
$configurationPath = Join-Path $private 'wechat-db.dpapi'
Save-SyntheticProtectedText $configurationPath $configuration
[IO.File]::WriteAllText((Join-Path $root 'expect-wechat'), 'synthetic')
$keyHash = (Get-FileHash -LiteralPath $keyPath -Algorithm SHA256).Hash
$configurationHash = (Get-FileHash -LiteralPath $configurationPath -Algorithm SHA256).Hash
$beforeACL = [IO.FileSystemAclExtensions]::GetAccessControl([IO.DirectoryInfo]::new($private), $sections).GetSecurityDescriptorSddlForm($sections)
Invoke-LauncherFixture $root
Assert-LauncherTest ($global:InboxLauncherTestState.key_writes -eq 0 -and (Get-FileHash -LiteralPath $keyPath -Algorithm SHA256).Hash -ceq $keyHash) 'An existing inbox key was replaced.'
Assert-LauncherTest ((Get-FileHash -LiteralPath $configurationPath -Algorithm SHA256).Hash -ceq $configurationHash) 'An existing WeChat configuration was replaced.'
$afterACL = [IO.FileSystemAclExtensions]::GetAccessControl([IO.DirectoryInfo]::new($private), $sections).GetSecurityDescriptorSddlForm($sections)
Assert-LauncherTest ($beforeACL -ceq $afterACL) 'An already-private directory was changed.'
Assert-PrivateFixtureDirectory $private

# Explicit access for another identity must be removed, retaining the current owner.
$root = New-LauncherFixture 'explicit-other-user-rule'
$private = Join-Path $root 'data/private'
New-Item -ItemType Directory -Path $private -Force | Out-Null
$unsafe = [Security.AccessControl.DirectorySecurity]::new()
$unsafe.SetAccessRuleProtection($true, $false)
$unsafe.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($currentSID, 'FullControl', 'ContainerInherit,ObjectInherit', 'None', 'Allow'))
$unsafe.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new([Security.Principal.SecurityIdentifier]::new('S-1-5-32-545'), 'ReadAndExecute', 'ContainerInherit,ObjectInherit', 'None', 'Allow'))
[IO.FileSystemAclExtensions]::SetAccessControl([IO.DirectoryInfo]::new($private), $unsafe)
$identitySections = [Security.AccessControl.AccessControlSections]::Owner -bor [Security.AccessControl.AccessControlSections]::Group
$beforeIdentity = [IO.FileSystemAclExtensions]::GetAccessControl([IO.DirectoryInfo]::new($private), $identitySections).GetSecurityDescriptorSddlForm($identitySections)
Invoke-LauncherFixture $root
Assert-PrivateFixtureDirectory $private
$afterIdentity = [IO.FileSystemAclExtensions]::GetAccessControl([IO.DirectoryInfo]::new($private), $identitySections).GetSecurityDescriptorSddlForm($identitySections)
Assert-LauncherTest ($beforeIdentity -ceq $afterIdentity) 'Directory hardening changed its owner or primary group.'
Assert-LauncherTest ($global:InboxLauncherTestState.key_writes -eq 1) 'The repaired private directory did not reach the first key write.'

# Never invent a new key for an existing product database.
$root = New-LauncherFixture 'existing-database-without-key'
New-Item -ItemType Directory -Path (Join-Path $root 'data') -Force | Out-Null
[IO.File]::WriteAllText((Join-Path $root 'data/messages.db'), 'synthetic-database-marker')
Invoke-LauncherFixture $root -Reject
Assert-LauncherTest ($global:InboxLauncherTestState.key_writes -eq 0 -and -not (Test-Path -LiteralPath $global:InboxLauncherTestState.key_path)) 'Startup generated a replacement key for an existing database.'

# A damaged saved config must not start the backend or replace either key file.
$root = New-LauncherFixture 'invalid-protected-configuration'
Import-Module (Join-Path $root 'scripts/WechatInit.psm1') -Force
$private = Initialize-WechatPrivateDirectory (Join-Path $root 'data/private')
$keyPath = Join-Path $private 'inbox-key.dpapi'
Save-SyntheticProtectedText $keyPath ('11' * 32)
$keyHash = (Get-FileHash -LiteralPath $keyPath -Algorithm SHA256).Hash
$configurationPath = Join-Path $private 'wechat-db.dpapi'
[IO.File]::WriteAllText($configurationPath, 'synthetic-invalid-dpapi')
Invoke-LauncherFixture $root -Reject
Assert-LauncherTest ((Get-FileHash -LiteralPath $keyPath -Algorithm SHA256).Hash -ceq $keyHash -and [IO.File]::ReadAllText($configurationPath) -ceq 'synthetic-invalid-dpapi') 'Rejected startup changed existing protected files.'

# Invalid private-directory targets must fail before writing or launching anything.
$root = New-LauncherFixture 'private-path-is-file'
New-Item -ItemType Directory -Path (Join-Path $root 'data') -Force | Out-Null
[IO.File]::WriteAllText((Join-Path $root 'data/private'), 'synthetic-path-marker')
Invoke-LauncherFixture $root -Reject
Assert-LauncherTest ($global:InboxLauncherTestState.key_writes -eq 0) 'A rejected private path reached key creation.'
Assert-LauncherTest ($global:InboxLauncherTestState.set_acl_calls -eq 0) 'The privilege-sensitive Set-Acl provider was used.'
# Service credentials never use a real Known Folder store in any branch.
Import-Module (Join-Path $PSScriptRoot 'ServiceCredentials.psm1') -Force
function New-SavedServiceFixture([string]$Name) {
    $root = New-LauncherFixture $Name
    Remove-Item Env:SOCIALD_API_TOKEN -ErrorAction SilentlyContinue
    Assert-SyntheticServiceEnvironment
    $secure = Initialize-ServiceCredential -ServiceURL (Get-ServiceCredentialURL)
    $secure.Dispose()
    return $root
}
function Save-SyntheticServiceJSON([string]$Path, [string]$JSON) {
    $bytes = [Text.Encoding]::UTF8.GetBytes($JSON)
    try {
        $ciphertext = [Security.Cryptography.ProtectedData]::Protect($bytes, [Text.Encoding]::UTF8.GetBytes('msg-manager/service-credential/v1'), [Security.Cryptography.DataProtectionScope]::CurrentUser)
        [IO.File]::WriteAllBytes($Path, $ciphertext)
    } finally { [Array]::Clear($bytes, 0, $bytes.Length) }
}

# First run provisions once, restart reuses exactly the same binary DPAPI file.
$root = New-LauncherFixture 'stable-service-credential'
Remove-Item Env:SOCIALD_API_TOKEN -ErrorAction SilentlyContinue
Invoke-LauncherFixture $root
$credentialHash = (Get-FileHash -LiteralPath $env:SOCIALD_CREDENTIAL_FILE -Algorithm SHA256).Hash
$backendTokenHash = $global:InboxLauncherTestState.backend_token_hash
Assert-PrivateFixtureDirectory (Split-Path -Parent $env:SOCIALD_CREDENTIAL_FILE)
Invoke-LauncherFixture $root
Assert-LauncherTest ($credentialHash -ceq (Get-FileHash -LiteralPath $env:SOCIALD_CREDENTIAL_FILE -Algorithm SHA256).Hash -and $backendTokenHash -ceq $global:InboxLauncherTestState.backend_token_hash) 'Restart changed the saved service token or ciphertext.'
Invoke-LauncherFixture $root -CopyApiToken
Assert-LauncherTest ($backendTokenHash -ceq $global:InboxLauncherTestState.backend_token_hash) 'Explicit copy rotated the token.'

# Explicit env mode never reads or repairs an existing bad store, nor resolves
# a deliberately invalid override. Explicit values survive launcher cleanup.
$root = New-LauncherFixture 'explicit-service-env'
$env:SOCIALD_API_TOKEN = $syntheticExplicitToken
New-Item -ItemType Directory -Path (Split-Path -Parent $env:SOCIALD_CREDENTIAL_FILE) | Out-Null
[IO.File]::WriteAllText($env:SOCIALD_CREDENTIAL_FILE, 'synthetic-bad-service-file')
$credentialHash = (Get-FileHash -LiteralPath $env:SOCIALD_CREDENTIAL_FILE -Algorithm SHA256).Hash
Invoke-LauncherFixture $root
Assert-LauncherTest ($credentialHash -ceq (Get-FileHash -LiteralPath $env:SOCIALD_CREDENTIAL_FILE -Algorithm SHA256).Hash) 'Explicit env mode changed a saved credential.'
$env:SOCIALD_CREDENTIAL_FILE = 'synthetic-relative-path-that-must-not-be-opened'
Invoke-LauncherFixture $root -CopyApiToken
Invoke-LauncherFixture $root -RotateApiToken -Reject

# Set an actually present empty Windows environment value on PS7.2 as well as
# PS7.5+: older .NET Environment setters otherwise delete empty values.
Add-Type -TypeDefinition @'
using System.Runtime.InteropServices;
public static class SyntheticLauncherEnvironmentV1 {
    [DllImport("kernel32.dll", EntryPoint="SetEnvironmentVariableW", CharSet=CharSet.Unicode, SetLastError=true)]
    public static extern bool Set(string name, string value);
}
'@
$root = New-SavedServiceFixture 'invalid-explicit-service-env'
$credentialHash = (Get-FileHash -LiteralPath $env:SOCIALD_CREDENTIAL_FILE -Algorithm SHA256).Hash
if (-not [SyntheticLauncherEnvironmentV1]::Set('SOCIALD_API_TOKEN', '')) { throw 'Could not establish a synthetic empty environment value.' }
Assert-LauncherTest ($null -ne [Environment]::GetEnvironmentVariable('SOCIALD_API_TOKEN', 'Process')) 'The empty-token fixture was absent, not explicitly empty.'
Invoke-LauncherFixture $root -Reject
Invoke-LauncherFixture $root -RotateApiToken -Reject
$env:SOCIALD_API_TOKEN = 'short-synthetic-token'
Invoke-LauncherFixture $root -Reject
$env:SOCIALD_API_TOKEN = $syntheticExplicitToken + ' '
Invoke-LauncherFixture $root -Reject
Assert-LauncherTest ($credentialHash -ceq (Get-FileHash -LiteralPath $env:SOCIALD_CREDENTIAL_FILE -Algorithm SHA256).Hash) 'Invalid explicit env fell back to or changed a saved credential.'

# Native Windows environment lookup ignores case; .NET's enumeration Hashtable
# does not. Cover both a valid and an actually present-empty mixed-case name.
$root = New-SavedServiceFixture 'mixed-case-explicit-service-env'
$servicePath = $env:SOCIALD_CREDENTIAL_FILE
$credentialHash = (Get-FileHash -LiteralPath $servicePath -Algorithm SHA256).Hash
if (-not [SyntheticLauncherEnvironmentV1]::Set('sOcIaLd_ApI_tOkEn', $syntheticExplicitToken)) { throw 'Could not establish the synthetic mixed-case token.' }
Assert-LauncherTest ([Environment]::GetEnvironmentVariables('Process').Keys -ccontains 'sOcIaLd_ApI_tOkEn') 'The mixed-case token fixture was not established.'
$unexpectedSecure = $null
$initializerDenied = $false
try { $unexpectedSecure = Initialize-ServiceCredential -ServiceURL (Get-ServiceCredentialURL) }
catch { $initializerDenied = $_.Exception.Message.Contains('explicit SOCIALD_API_TOKEN') }
finally { if ($null -ne $unexpectedSecure) { $unexpectedSecure.Dispose() } }
Assert-LauncherTest $initializerDenied 'The saved-store initializer ignored an explicit mixed-case environment token.'
Invoke-LauncherFixture $root
$explicitHash = [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData([Text.Encoding]::UTF8.GetBytes($syntheticExplicitToken)))
Assert-LauncherTest ($global:InboxLauncherTestState.backend_token_hash -ceq $explicitHash) 'Mixed-case explicit env silently used a saved token instead.'
Invoke-LauncherFixture $root -CopyApiToken
Invoke-LauncherFixture $root -RotateApiToken -Reject
Remove-Item Env:SOCIALD_API_TOKEN -ErrorAction SilentlyContinue
if (-not [SyntheticLauncherEnvironmentV1]::Set('sOcIaLd_ApI_tOkEn', '')) { throw 'Could not establish the synthetic mixed-case empty token.' }
Assert-LauncherTest ([Environment]::GetEnvironmentVariables('Process').Keys -ccontains 'sOcIaLd_ApI_tOkEn') 'The mixed-case empty token fixture was not established.'
Invoke-LauncherFixture $root -Reject
Invoke-LauncherFixture $root -RotateApiToken -Reject
Assert-LauncherTest ($credentialHash -ceq (Get-FileHash -LiteralPath $servicePath -Algorithm SHA256).Hash) 'Mixed-case explicit env changed the saved credential.'

$root = New-LauncherFixture 'rotate-missing-service-credential'
Remove-Item Env:SOCIALD_API_TOKEN -ErrorAction SilentlyContinue
Invoke-LauncherFixture $root -RotateApiToken -Reject
Assert-LauncherTest (-not (Test-Path -LiteralPath $env:SOCIALD_CREDENTIAL_FILE)) 'Rotation initialized a missing saved credential.'

$root = New-SavedServiceFixture 'invalid-saved-service-credential'
$servicePath = $env:SOCIALD_CREDENTIAL_FILE
[IO.File]::WriteAllText($servicePath, 'synthetic-corrupt-dpapi-no-repair')
$credentialHash = (Get-FileHash -LiteralPath $servicePath -Algorithm SHA256).Hash
Invoke-LauncherFixture $root -Reject
Invoke-LauncherFixture $root -RotateApiToken -Reject
Assert-LauncherTest ($credentialHash -ceq (Get-FileHash -LiteralPath $servicePath -Algorithm SHA256).Hash) 'Corrupt credentials were automatically repaired or rotated.'

$root = New-SavedServiceFixture 'mismatched-service-url'
$credentialHash = (Get-FileHash -LiteralPath $env:SOCIALD_CREDENTIAL_FILE -Algorithm SHA256).Hash
$env:SOCIALD_ADDR = Get-SyntheticFreeAddress
Invoke-LauncherFixture $root -Reject
Assert-LauncherTest ($credentialHash -ceq (Get-FileHash -LiteralPath $env:SOCIALD_CREDENTIAL_FILE -Algorithm SHA256).Hash) 'URL mismatch changed saved credentials.'

$root = New-SavedServiceFixture 'strict-service-records'
$url = Get-ServiceCredentialURL
foreach ($json in @(
    ('{"version":2,"service_url":"' + $url + '","api_token":"' + $syntheticExplicitToken + '"}'),
    ('{"version":1,"version":1,"service_url":"' + $url + '","api_token":"' + $syntheticExplicitToken + '"}'),
    ('{"version":1,"extra":true,"service_url":"' + $url + '","api_token":"' + $syntheticExplicitToken + '"}'),
    ('{"version":1.0,"service_url":"' + $url + '","api_token":"' + $syntheticExplicitToken + '"}'),
    ('{"version":1,"service_url":"' + $url + '","api_token":"short-synthetic"}')
)) {
    Save-SyntheticServiceJSON $env:SOCIALD_CREDENTIAL_FILE $json
    $credentialHash = (Get-FileHash -LiteralPath $env:SOCIALD_CREDENTIAL_FILE -Algorithm SHA256).Hash
    Invoke-LauncherFixture $root -Reject
    Assert-LauncherTest ($credentialHash -ceq (Get-FileHash -LiteralPath $env:SOCIALD_CREDENTIAL_FILE -Algorithm SHA256).Hash) 'Schema rejection changed saved credentials.'
}

# The service store is stricter than the legacy inbox private-dir guard: never
# repair an existing parent or file with extra access, even for this user.
foreach ($unsafeKind in @('parent','file')) {
    $root = New-SavedServiceFixture ('unsafe-service-' + $unsafeKind)
    $servicePath = $env:SOCIALD_CREDENTIAL_FILE
    if ($unsafeKind -eq 'parent') {
        $item = [IO.DirectoryInfo]::new((Split-Path -Parent $servicePath))
        $acl = [Security.AccessControl.DirectorySecurity]::new()
        $acl.SetAccessRuleProtection($true, $false)
        $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($currentSID, 'FullControl', 'ContainerInherit,ObjectInherit', 'None', 'Allow'))
        $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new([Security.Principal.SecurityIdentifier]::new('S-1-1-0'), 'ReadAndExecute', 'ContainerInherit,ObjectInherit', 'None', 'Allow'))
    } else {
        $item = [IO.FileInfo]::new($servicePath)
        $acl = [Security.AccessControl.FileSecurity]::new()
        $acl.SetAccessRuleProtection($true, $false)
        $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($currentSID, 'FullControl', 'Allow'))
        $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new([Security.Principal.SecurityIdentifier]::new('S-1-1-0'), 'Read', 'Allow'))
    }
    [IO.FileSystemAclExtensions]::SetAccessControl($item, $acl)
    $beforeSDDL = [IO.FileSystemAclExtensions]::GetAccessControl($item, $sections).GetSecurityDescriptorSddlForm($sections)
    $credentialHash = (Get-FileHash -LiteralPath $servicePath -Algorithm SHA256).Hash
    Invoke-LauncherFixture $root -Reject
    Assert-LauncherTest ($beforeSDDL -ceq [IO.FileSystemAclExtensions]::GetAccessControl($item, $sections).GetSecurityDescriptorSddlForm($sections) -and $credentialHash -ceq (Get-FileHash -LiteralPath $servicePath -Algorithm SHA256).Hash) 'Service startup repaired ACLs or changed an unsafe file.'
}

# Junctions need no symlink privilege and must be rejected without reading the
# target credential. Every target remains inside this synthetic fixture root.
$root = New-LauncherFixture 'linked-service-parent'
$linkedPath = $env:SOCIALD_CREDENTIAL_FILE
$env:SOCIALD_CREDENTIAL_FILE = Join-Path $root 'target-private/service.dpapi'
$secure = Initialize-ServiceCredential -ServiceURL (Get-ServiceCredentialURL)
$secure.Dispose()
$targetPath = $env:SOCIALD_CREDENTIAL_FILE
$credentialHash = (Get-FileHash -LiteralPath $targetPath -Algorithm SHA256).Hash
New-Item -ItemType Junction -Path (Split-Path -Parent $linkedPath) -Target (Split-Path -Parent $targetPath) | Out-Null
$env:SOCIALD_CREDENTIAL_FILE = $linkedPath
Invoke-LauncherFixture $root -Reject
Assert-LauncherTest ($credentialHash -ceq (Get-FileHash -LiteralPath $targetPath -Algorithm SHA256).Hash) 'Junction rejection changed its synthetic target.'

$root = New-SavedServiceFixture 'service-explicit-rotation'
$servicePath = $env:SOCIALD_CREDENTIAL_FILE
Invoke-LauncherFixture $root
$credentialHash = (Get-FileHash -LiteralPath $servicePath -Algorithm SHA256).Hash
$backendTokenHash = $global:InboxLauncherTestState.backend_token_hash
$port = [int]($env:SOCIALD_ADDR.Split(':')[1])
$listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, $port)
try {
    $listener.Start()
    Invoke-LauncherFixture $root -RotateApiToken -Reject
    Assert-LauncherTest ($credentialHash -ceq (Get-FileHash -LiteralPath $servicePath -Algorithm SHA256).Hash) 'Rotation changed credentials while the backend port was occupied.'
} finally { $listener.Stop() }
Invoke-LauncherFixture $root -RotateApiToken
Assert-LauncherTest ($credentialHash -cne (Get-FileHash -LiteralPath $servicePath -Algorithm SHA256).Hash -and $backendTokenHash -cne $global:InboxLauncherTestState.backend_token_hash) 'Explicit stopped-backend rotation did not replace the saved token.'
$credentialHash = (Get-FileHash -LiteralPath $servicePath -Algorithm SHA256).Hash
Invoke-LauncherFixture $root
Assert-LauncherTest ($credentialHash -ceq (Get-FileHash -LiteralPath $servicePath -Algorithm SHA256).Hash) 'Restart after rotation replaced credentials again.'

$root = New-LauncherFixture 'invalid-persistent-backend-address'
$env:SOCIALD_ADDR = '127.0.0.1:017890'
Invoke-LauncherFixture $root -Reject
Assert-LauncherTest (-not (Test-Path -LiteralPath $env:SOCIALD_CREDENTIAL_FILE)) 'Invalid backend address wrote a bound credential.'
$env:SOCIALD_ADDR = Get-SyntheticFreeAddress
if (-not [SyntheticLauncherEnvironmentV1]::Set('SOCIALD_CREDENTIAL_FILE', '')) { throw 'Could not establish the synthetic explicit-empty override.' }
Assert-LauncherTest ([Environment]::GetEnvironmentVariables('Process').Keys -icontains 'SOCIALD_CREDENTIAL_FILE') 'The explicit-empty override must remain present; never fall back to real Known Folders in a test.'
Invoke-LauncherFixture $root -Reject

$root = New-SavedServiceFixture 'mixed-case-credential-override'
$servicePath = $env:SOCIALD_CREDENTIAL_FILE
$credentialHash = (Get-FileHash -LiteralPath $servicePath -Algorithm SHA256).Hash
Remove-Item Env:SOCIALD_CREDENTIAL_FILE -ErrorAction SilentlyContinue
if (-not [SyntheticLauncherEnvironmentV1]::Set('sOcIaLd_CrEdEnTiAl_FiLe', $servicePath)) { throw 'Could not establish the synthetic mixed-case credential override.' }
Assert-LauncherTest ([Environment]::GetEnvironmentVariables('Process').Keys -ccontains 'sOcIaLd_CrEdEnTiAl_FiLe') 'The mixed-case credential fixture was not established.'
# This resolver-only preflight must succeed before calling a writer/reader: a
# regression may compute a real default path, but must never open its contents.
Assert-SyntheticServiceEnvironment
Assert-LauncherTest ((Get-ServiceCredentialPath) -ceq $servicePath) 'The mixed-case credential override was not honored.'
Invoke-LauncherFixture $root
Assert-LauncherTest ($credentialHash -ceq (Get-FileHash -LiteralPath $servicePath -Algorithm SHA256).Hash) 'Mixed-case override changed the saved credential.'
Remove-Item Env:SOCIALD_CREDENTIAL_FILE -ErrorAction SilentlyContinue
if (-not [SyntheticLauncherEnvironmentV1]::Set('sOcIaLd_CrEdEnTiAl_FiLe', '')) { throw 'Could not establish the synthetic mixed-case empty override.' }
Assert-LauncherTest ([Environment]::GetEnvironmentVariables('Process').Keys -ccontains 'sOcIaLd_CrEdEnTiAl_FiLe') 'The mixed-case empty credential fixture was not established.'
Invoke-LauncherFixture $root -Reject
Assert-LauncherTest ($global:InboxLauncherTestState.set_acl_calls -eq 0) 'The privilege-sensitive Set-Acl provider was used.'

# Exercise the actual thin wrapper and generic bootstrap on both Windows PS5.1
# and PS7, but replace the core with an inert switch recorder. It cannot load a
# credential, clipboard, database, backend or real initialization module.
$wrapperScripts = Join-Path $FixtureRoot "wrapper's 中文 scripts"
New-Item -ItemType Directory -Path $wrapperScripts | Out-Null
foreach ($name in @('start-inbox.ps1','PowerShellBootstrap.psm1')) { Copy-Item -LiteralPath (Join-Path $PSScriptRoot $name) -Destination (Join-Path $wrapperScripts $name) }
$wrapperStub = @'
#requires -Version 7.2
param([switch]$CopyApiToken, [switch]$RotateApiToken)
$result = @{ copy = [bool]$CopyApiToken; rotate = [bool]$RotateApiToken; version = $PSVersionTable.PSVersion.ToString(); pointer_size = [IntPtr]::Size; remaining_arguments = @($args) }
[IO.File]::WriteAllText($env:SYNTHETIC_SERVICE_WRAPPER_OUTPUT, ($result | ConvertTo-Json -Compress))
exit [int]$env:SYNTHETIC_SERVICE_WRAPPER_EXIT
'@
$wrapperDriver = @'
#requires -Version 5.1
$ErrorActionPreference = 'Stop'
$entry = Join-Path $PSScriptRoot 'start-inbox.ps1'
switch ($env:SYNTHETIC_SERVICE_WRAPPER_CASE) {
    'none' { & $entry }
    'copy' { & $entry -CopyApiToken }
    'rotate' { & $entry -RotateApiToken }
    'both' { & $entry -CopyApiToken -RotateApiToken }
    'false' { & $entry -CopyApiToken:$false -RotateApiToken:$false }
    'exit-code' { & $entry -CopyApiToken -RotateApiToken }
    default { throw 'Unknown synthetic wrapper case.' }
}
exit $LASTEXITCODE
'@
[IO.File]::WriteAllText((Join-Path $wrapperScripts 'start-inbox-core.ps1'), $wrapperStub, [Text.UTF8Encoding]::new($true))
[IO.File]::WriteAllText((Join-Path $wrapperScripts 'synthetic-driver.ps1'), $wrapperDriver, [Text.UTF8Encoding]::new($true))
$modernEngine = [Diagnostics.Process]::GetCurrentProcess().MainModule.FileName
$legacyEngine = Join-Path $env:WINDIR 'System32/WindowsPowerShell/v1.0/powershell.exe'
foreach ($engine in @($modernEngine, $legacyEngine)) {
    foreach ($mode in @('none','copy','rotate','both','false','exit-code')) {
        $expectedExit = if ($mode -eq 'exit-code') { 37 } else { 0 }
        $outputPath = Join-Path $wrapperScripts ([guid]::NewGuid().ToString('N') + '.json')
        $startInfo = [Diagnostics.ProcessStartInfo]::new()
        $startInfo.FileName = $engine
        $startInfo.UseShellExecute = $false
        $startInfo.CreateNoWindow = $true
        $startInfo.WindowStyle = [Diagnostics.ProcessWindowStyle]::Hidden
        $startInfo.RedirectStandardOutput = $true
        $startInfo.RedirectStandardError = $true
        foreach ($argument in @('-NoLogo','-NoProfile','-NonInteractive','-File',(Join-Path $wrapperScripts 'synthetic-driver.ps1'))) { $startInfo.ArgumentList.Add($argument) }
        $null = $startInfo.Environment.Remove('SOCIALD_API_TOKEN')
        $startInfo.Environment['SOCIALD_CREDENTIAL_FILE'] = Join-Path $FixtureRoot 'must-not-use-default/service.dpapi'
        $startInfo.Environment['PATH'] = (Split-Path -Parent $modernEngine) + ';' + (Join-Path $env:WINDIR 'System32')
        $startInfo.Environment['SYNTHETIC_SERVICE_WRAPPER_OUTPUT'] = $outputPath
        $startInfo.Environment['SYNTHETIC_SERVICE_WRAPPER_CASE'] = $mode
        $startInfo.Environment['SYNTHETIC_SERVICE_WRAPPER_EXIT'] = [string]$expectedExit
        $process = $null
        try {
            $process = [Diagnostics.Process]::Start($startInfo)
            $stdoutTask = $process.StandardOutput.ReadToEndAsync()
            $stderrTask = $process.StandardError.ReadToEndAsync()
            if (-not $process.WaitForExit(10000)) { throw 'Synthetic credential-switch forwarding timed out.' }
            $stdout = $stdoutTask.GetAwaiter().GetResult()
            $stderr = $stderrTask.GetAwaiter().GetResult()
            Assert-LauncherTest ($process.ExitCode -eq $expectedExit -and -not $stdout -and -not $stderr) 'Credential-switch wrapper did not preserve the exit code or clean output.'
            $result = [IO.File]::ReadAllText($outputPath) | ConvertFrom-Json
            Assert-LauncherTest ($result.copy -eq ($mode -in @('copy','both','exit-code')) -and $result.rotate -eq ($mode -in @('rotate','both','exit-code'))) 'A credential switch changed during modern/legacy bootstrap forwarding.'
            Assert-LauncherTest ([Version]$result.version -ge [Version]'7.2' -and $result.pointer_size -eq 8 -and $result.remaining_arguments.Count -eq 0) 'Credential switches ran in the wrong host or left unbound arguments.'
            $global:InboxLauncherTestState.launches++
        } finally {
            if ($null -ne $process) {
                if (-not $process.HasExited) { $process.Kill($true); $null = $process.WaitForExit(5000) }
                $process.Dispose()
            }
        }
    }
}
Write-Output ('Inbox launcher synthetic tests passed: ' + $global:InboxLauncherTestState.launches + ' isolated launches')

#requires -Version 7.2
param([switch]$Replace)
$ErrorActionPreference = 'Stop'
if (-not $IsWindows) { throw 'This configuration helper requires Windows DPAPI.' }
$projectRoot = [IO.Path]::GetFullPath((Split-Path -Parent $PSScriptRoot))
$privateDirectory = Join-Path $projectRoot 'data/private'
$configPath = Join-Path $privateDirectory 'wechat-db.dpapi'
foreach ($candidate in @($projectRoot, (Join-Path $projectRoot 'data'), $privateDirectory, $configPath)) {
    if (Test-Path -LiteralPath $candidate) {
        if ((Get-Item -LiteralPath $candidate -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Configuration paths must not be links.' }
    }
}
if ((Test-Path -LiteralPath $configPath) -and (Get-Item -LiteralPath $configPath -Force).PSIsContainer) { throw 'The configuration destination must be a file.' }
if ((Test-Path -LiteralPath $configPath) -and -not $Replace) { throw 'A local WeChat configuration already exists. Use -Replace only when you intend to replace it.' }
Write-Host 'Configure read-only access to your own WeChat 4.x databases. This helper does not obtain database keys.'
$wechatDirectory = Read-Host 'Full path ending in your account folder/db_storage'
$wechatAccount = Read-Host 'Your account wxid (not a display nickname)'
if (-not [IO.Path]::IsPathFullyQualified($wechatDirectory) -or $wechatDirectory.StartsWith('\\') -or $wechatDirectory.StartsWith('//')) { throw 'Use an absolute local account database directory.' }
$wechatDirectory = [IO.Path]::GetFullPath($wechatDirectory)
if ((Split-Path -Leaf $wechatDirectory) -ne 'db_storage' -or -not (Test-Path -LiteralPath $wechatDirectory -PathType Container)) { throw 'Select the existing db_storage directory for your account.' }
if ($wechatAccount -notmatch '^[A-Za-z][A-Za-z0-9_-]{1,127}$' -or $wechatAccount.StartsWith('gh_') -or $wechatAccount.ToLowerInvariant() -in @('filehelper','weixin','fmessage','medianote','newsapp','notification_messages','floatbottle','qqmail','tmessage','qmessage','masssendapp','weibo','brandsessionholder','helper_entry')) { throw 'Use your account identifier, not a system account or nickname.' }
$wechatParent = Split-Path -Leaf (Split-Path -Parent $wechatDirectory)
if ($wechatParent -ne $wechatAccount -and $wechatParent -notmatch ('^' + [regex]::Escape($wechatAccount) + '_[A-Za-z0-9]{4}$')) { throw 'The selected account directory does not match the account identifier.' }
$wechatDrive = [IO.DriveInfo]::new([IO.Path]::GetPathRoot($wechatDirectory))
if ($wechatDrive.DriveType -ne [IO.DriveType]::Fixed) { throw 'Use a local fixed drive.' }
$wechatAncestor = $wechatDirectory
while ($wechatAncestor) {
    if ((Get-Item -LiteralPath $wechatAncestor -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'The source directory must not contain links.' }
    $wechatAncestor = Split-Path -Parent $wechatAncestor
}
$wechatSecret = Read-Host 'Database key (64 hexadecimal characters; input is hidden)' -AsSecureString
$wechatPointer = [IntPtr]::Zero
$wechatProtectedConfig = $null
$wechatTempPath = $null
try {
    $wechatPointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($wechatSecret)
    $wechatKey = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($wechatPointer)
    if ($wechatKey -notmatch '^[a-fA-F0-9]{64}$') { throw 'The database key must contain exactly 64 hexadecimal characters.' }
    $wechatJSON = @{ directory = $wechatDirectory; account_id = $wechatAccount; key = $wechatKey; limit = 100 } | ConvertTo-Json -Compress
    $wechatProtectedConfig = ConvertTo-SecureString -String $wechatJSON -AsPlainText -Force
    New-Item -ItemType Directory -Path $privateDirectory -Force | Out-Null
    $wechatIdentity = [Security.Principal.WindowsIdentity]::GetCurrent().User
    $wechatACL = Get-Acl -LiteralPath $privateDirectory
    $wechatACL.SetAccessRuleProtection($true, $false)
    $wechatACL.SetAccessRule([Security.AccessControl.FileSystemAccessRule]::new($wechatIdentity, 'FullControl', 'ContainerInherit,ObjectInherit', 'None', 'Allow'))
    Set-Acl -LiteralPath $privateDirectory -AclObject $wechatACL
    $wechatTempPath = Join-Path $privateDirectory ('wechat-' + [guid]::NewGuid().ToString('N') + '.tmp')
    $wechatProtectedConfig | ConvertFrom-SecureString | Set-Content -LiteralPath $wechatTempPath -Encoding UTF8 -NoNewline
    Move-Item -LiteralPath $wechatTempPath -Destination $configPath -Force:$Replace
    Write-Host 'Protected local configuration saved. Restart the inbox, then manually load WeChat conversations in Messages.'
} finally {
    if ($wechatPointer -ne [IntPtr]::Zero) { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($wechatPointer) }
    $wechatSecret.Dispose()
    if ($null -ne $wechatProtectedConfig) { $wechatProtectedConfig.Dispose() }
    if ($wechatTempPath -and (Test-Path -LiteralPath $wechatTempPath)) { Remove-Item -LiteralPath $wechatTempPath }
    Remove-Variable wechatKey, wechatJSON -ErrorAction SilentlyContinue
}

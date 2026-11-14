#requires -Version 7.2
$ErrorActionPreference = 'Stop'
if (-not $IsWindows) { throw 'This local inbox launcher currently requires Windows DPAPI.' }
$projectRoot = [IO.Path]::GetFullPath((Split-Path -Parent $PSScriptRoot))
$dataDirectory = Join-Path $projectRoot 'data'
$privateDirectory = Join-Path $dataDirectory 'private'
foreach ($path in @($projectRoot,$dataDirectory,$privateDirectory)) {
    if (Test-Path -LiteralPath $path) {
        $item = Get-Item -LiteralPath $path -Force
        if (-not $item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw 'Local inbox data paths must not be links.' }
    }
}
New-Item -ItemType Directory -Path $privateDirectory -Force | Out-Null
$keyPath = Join-Path $privateDirectory 'inbox-key.dpapi'
$databasePath = Join-Path $dataDirectory 'messages.db'
if (Test-Path -LiteralPath $keyPath) {
    if ((Get-Item -LiteralPath $keyPath -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'The key file must not be a link.' }
    $inboxProtectedKey = Get-Content -Raw -LiteralPath $keyPath | ConvertTo-SecureString
} else {
    if (Test-Path -LiteralPath $databasePath) { throw 'An existing database has no protected key. Restore its key before continuing; no key was overwritten.' }
    $inboxKeyHex = [Convert]::ToHexString([Security.Cryptography.RandomNumberGenerator]::GetBytes(32))
    $inboxProtectedKey = ConvertTo-SecureString -String $inboxKeyHex -AsPlainText -Force
    $inboxProtectedKey | ConvertFrom-SecureString | Set-Content -LiteralPath $keyPath -Encoding UTF8 -NoNewline
    Remove-Variable inboxKeyHex
}
# The key is protected for this Windows user. Restrict its folder as well.
$inboxIdentity = [Security.Principal.WindowsIdentity]::GetCurrent().User
$inboxACL = Get-Acl -LiteralPath $privateDirectory
$inboxACL.SetAccessRuleProtection($true,$false)
$inboxRule = [Security.AccessControl.FileSystemAccessRule]::new($inboxIdentity,'FullControl','ContainerInherit,ObjectInherit','None','Allow')
$inboxACL.SetAccessRule($inboxRule)
Set-Acl -LiteralPath $privateDirectory -AclObject $inboxACL
$inboxPointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($inboxProtectedKey)
try {
    $env:SOCIALD_DATABASE_KEY = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($inboxPointer)
} finally {
    [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($inboxPointer)
    $inboxProtectedKey.Dispose()
    Remove-Variable inboxPointer,inboxProtectedKey
}
$env:SOCIALD_DATABASE_PATH = $databasePath
if ([string]::IsNullOrWhiteSpace($env:SOCIALD_API_TOKEN)) {
    $env:SOCIALD_API_TOKEN = [Convert]::ToHexString([Security.Cryptography.RandomNumberGenerator]::GetBytes(32))
}
$env:SOCIALD_API_TOKEN | Set-Clipboard
Write-Host 'Unified inbox starting. Paste the copied service token in Settings; open Messages after connecting.'
Write-Host 'The database key is protected by this Windows account. Keep data/private/inbox-key.dpapi; it is required to reopen your records.'
$wechatLocalConfig = Join-Path $privateDirectory 'wechat-db.dpapi'
$wechatLoadedFromFile = $false
try {
    # Operator-supplied environment settings take precedence as a complete set.
    if ((Test-Path -LiteralPath $wechatLocalConfig) -and -not ($env:WECHAT_DB_DIR -or $env:WECHAT_DB_ACCOUNT_ID -or $env:WECHAT_DB_KEY -or $env:WECHAT_DB_LIMIT)) {
        if ((Get-Item -LiteralPath $wechatLocalConfig -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'The WeChat configuration must not be a link.' }
        $wechatSecureConfig = Get-Content -Raw -LiteralPath $wechatLocalConfig | ConvertTo-SecureString
        $wechatConfigPointer = [IntPtr]::Zero
        try {
            $wechatConfigPointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($wechatSecureConfig)
            $wechatConfig = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($wechatConfigPointer) | ConvertFrom-Json
            $env:WECHAT_DB_DIR = [string]$wechatConfig.directory
            $env:WECHAT_DB_ACCOUNT_ID = [string]$wechatConfig.account_id
            $env:WECHAT_DB_KEY = [string]$wechatConfig.key
            $env:WECHAT_DB_LIMIT = [string]$wechatConfig.limit
            $wechatLoadedFromFile = $true
        } catch {
            throw 'Protected WeChat configuration could not be opened. Reconfigure it locally with the same Windows account.'
        } finally {
            if ($wechatConfigPointer -ne [IntPtr]::Zero) { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($wechatConfigPointer) }
            $wechatSecureConfig.Dispose()
            Remove-Variable wechatConfig, wechatConfigPointer, wechatSecureConfig -ErrorAction SilentlyContinue
        }
    }
    & (Join-Path $PSScriptRoot 'dev-backend.ps1')
} finally {
    Remove-Item Env:SOCIALD_DATABASE_KEY -ErrorAction SilentlyContinue
    if ($wechatLoadedFromFile) { 'WECHAT_DB_DIR','WECHAT_DB_ACCOUNT_ID','WECHAT_DB_KEY','WECHAT_DB_LIMIT' | ForEach-Object { Remove-Item -LiteralPath "Env:$_" -ErrorAction SilentlyContinue } }
}

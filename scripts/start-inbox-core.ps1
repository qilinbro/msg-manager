#requires -Version 7.2
param([switch]$CopyApiToken, [switch]$RotateApiToken)
$ErrorActionPreference = 'Stop'
if (-not $IsWindows) { throw 'This local inbox launcher currently requires Windows DPAPI.' }
Import-Module (Join-Path $PSScriptRoot 'ServiceCredentials.psm1') -Force
$hasExplicitApiToken = [Environment]::GetEnvironmentVariables('Process').Keys -icontains 'SOCIALD_API_TOKEN'
$explicitApiToken = [Environment]::GetEnvironmentVariable('SOCIALD_API_TOKEN', 'Process')
if ($hasExplicitApiToken) {
    if ($RotateApiToken) { throw '-RotateApiToken cannot be used with explicit SOCIALD_API_TOKEN, including an empty value.' }
    if (-not (Test-ServiceCredentialToken $explicitApiToken)) { throw 'SOCIALD_API_TOKEN requires 32 to 4096 printable ASCII characters without whitespace; saved credentials were not read.' }
}
$apiTokenInjected = $false
$wechatLoadedFromFile = $false
$emailLoadedFromFile = $false
$emailNames = @('EMAIL_IMAP_HOST','EMAIL_IMAP_USERNAME','EMAIL_IMAP_PORT','EMAIL_IMAP_FOLDER','EMAIL_IMAP_PASSWORD','EMAIL_IMAP_LIMIT')
try {
    $projectRoot = [IO.Path]::GetFullPath((Split-Path -Parent $PSScriptRoot))
    $dataDirectory = Join-Path $projectRoot 'data'
    $privateDirectory = Join-Path $dataDirectory 'private'
    foreach ($path in @($projectRoot,$dataDirectory,$privateDirectory)) {
        if (Test-Path -LiteralPath $path) {
            $item = Get-Item -LiteralPath $path -Force
            if (-not $item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw 'Local inbox data paths must not be links.' }
        }
    }
    Import-Module (Join-Path $PSScriptRoot 'WechatInit.psm1') -Force
    # Reuse the existing DACL-only guard for the separate database keys.
    $privateDirectory = Initialize-WechatPrivateDirectory -Path $privateDirectory
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
    $inboxPointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($inboxProtectedKey)
    try {
        $env:SOCIALD_DATABASE_KEY = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($inboxPointer)
    } finally {
        [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($inboxPointer)
        $inboxProtectedKey.Dispose()
        Remove-Variable inboxPointer,inboxProtectedKey
    }
    $env:SOCIALD_DATABASE_PATH = $databasePath
    if (-not $hasExplicitApiToken) {
        $serviceURL = Get-ServiceCredentialURL
        $protectedApiToken = Initialize-ServiceCredential -ServiceURL $serviceURL -Rotate:$RotateApiToken
        $apiPointer = [IntPtr]::Zero
        try {
            $apiPointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($protectedApiToken)
            $env:SOCIALD_API_TOKEN = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($apiPointer)
            $apiTokenInjected = $true
        } finally {
            if ($apiPointer -ne [IntPtr]::Zero) { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($apiPointer) }
            $protectedApiToken.Dispose()
            Remove-Variable apiPointer,protectedApiToken
        }
    }
    if ($CopyApiToken) {
        $env:SOCIALD_API_TOKEN | Set-Clipboard
        Write-Host 'Service token copied explicitly. Paste only into this local service Settings page; clipboard managers may retain it.'
    }
    if ($RotateApiToken) { Write-Host 'Saved service token rotated explicitly. Other clients must reconnect with the new token.' }
    if (-not $hasExplicitApiToken) {
        Write-Host 'Unified inbox starting. Saved service credentials are reused; use -CopyApiToken only when authorizing a new browser/client.'
    } else {
        Write-Host 'Unified inbox starting with explicit SOCIALD_API_TOKEN; saved credentials were not read or changed. Other clients may use a different saved token.'
    }
    Write-Host 'The database key is protected by this Windows account. Keep data/private/inbox-key.dpapi; it is required to reopen your records.'
    $wechatLocalConfig = Join-Path $privateDirectory 'wechat-db.dpapi'
    # Operator-supplied environment settings take precedence as a complete set.
    if ((Test-Path -LiteralPath $wechatLocalConfig) -and -not ($env:WECHAT_DB_DIR -or $env:WECHAT_DB_ACCOUNT_ID -or $env:WECHAT_DB_KEY -or $env:WECHAT_DB_LIMIT)) {
        if ((Get-Item -LiteralPath $wechatLocalConfig -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'The WeChat configuration must not be a link.' }
        $wechatSecureConfig = Get-Content -Raw -LiteralPath $wechatLocalConfig | ConvertTo-SecureString
        $wechatConfigPointer = [IntPtr]::Zero
        try {
            $wechatConfigPointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($wechatSecureConfig)
            $wechatConfig = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($wechatConfigPointer) | ConvertFrom-Json
            $wechatLoadedFromFile = $true
            $env:WECHAT_DB_DIR = [string]$wechatConfig.directory
            $env:WECHAT_DB_ACCOUNT_ID = [string]$wechatConfig.account_id
            $env:WECHAT_DB_KEY = [string]$wechatConfig.key
            $env:WECHAT_DB_LIMIT = [string]$wechatConfig.limit
        } catch {
            throw 'Protected WeChat configuration could not be opened. Reconfigure it locally with the same Windows account.'
        } finally {
            if ($wechatConfigPointer -ne [IntPtr]::Zero) { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($wechatConfigPointer) }
            $wechatSecureConfig.Dispose()
            Remove-Variable wechatConfig,wechatConfigPointer,wechatSecureConfig -ErrorAction SilentlyContinue
        }
    }
    $emailLocalConfig = Join-Path $privateDirectory 'email-imap.dpapi'
    $explicitMail = @([Environment]::GetEnvironmentVariables('Process').Keys | Where-Object { $_ -iin $emailNames })
    if ((Test-Path -LiteralPath $emailLocalConfig) -and $explicitMail.Count -eq 0) {
        $mailSecret = $null
        $mailPointer = [IntPtr]::Zero
        try {
            $mailPath = Assert-WechatInitPath $emailLocalConfig
            $mailFile = Get-Item -LiteralPath $mailPath -Force
            if ($mailFile.PSIsContainer -or $mailFile.Length -lt 1 -or $mailFile.Length -gt 65536) { throw 'Invalid protected mail settings.' }
            $mailSecret = ConvertTo-SecureString -String ([IO.File]::ReadAllText($mailPath))
            $mailPointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($mailSecret)
            $mailSettings = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($mailPointer) | ConvertFrom-Json -AsHashtable
            if ($mailSettings -isnot [System.Collections.IDictionary] -or $mailSettings.Count -ne 6 -or $mailSettings.format -ne 1 -or
                $mailSettings.host -isnot [string] -or [string]::IsNullOrWhiteSpace($mailSettings.host) -or
                $mailSettings.username -isnot [string] -or [string]::IsNullOrWhiteSpace($mailSettings.username) -or
                $mailSettings.folder -isnot [string] -or [string]::IsNullOrWhiteSpace($mailSettings.folder) -or
                $mailSettings.port -isnot [long] -or $mailSettings.port -lt 1 -or $mailSettings.port -gt 65535 -or
                $mailSettings.password -isnot [string] -or $mailSettings.password -cnotmatch '^[!-~]{1,4096}$') {
                throw 'Invalid protected mail settings.'
            }
            $emailLoadedFromFile = $true
            $env:EMAIL_IMAP_HOST = $mailSettings.host
            $env:EMAIL_IMAP_USERNAME = $mailSettings.username
            $env:EMAIL_IMAP_PORT = [string]$mailSettings.port
            $env:EMAIL_IMAP_FOLDER = $mailSettings.folder
            $env:EMAIL_IMAP_PASSWORD = $mailSettings.password
        } catch {
            throw 'Protected mail settings could not be opened. Run configure-email.ps1 locally with the same Windows account.'
        } finally {
            if ($mailPointer -ne [IntPtr]::Zero) { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($mailPointer) }
            if ($null -ne $mailSecret) { $mailSecret.Dispose() }
            Remove-Variable mailSecret,mailPointer,mailSettings -ErrorAction SilentlyContinue
        }
    }
    & (Join-Path $PSScriptRoot 'dev-backend.ps1')
} finally {
    Remove-Item Env:SOCIALD_DATABASE_KEY -ErrorAction SilentlyContinue
    if ($apiTokenInjected) { [Environment]::SetEnvironmentVariable('SOCIALD_API_TOKEN', [NullString]::Value, 'Process') }
    elseif ($hasExplicitApiToken) { [Environment]::SetEnvironmentVariable('SOCIALD_API_TOKEN', $explicitApiToken, 'Process') }
    if ($wechatLoadedFromFile) { 'WECHAT_DB_DIR','WECHAT_DB_ACCOUNT_ID','WECHAT_DB_KEY','WECHAT_DB_LIMIT' | ForEach-Object { Remove-Item -LiteralPath "Env:$_" -ErrorAction SilentlyContinue } }
    if ($emailLoadedFromFile) { $emailNames | ForEach-Object { Remove-Item -LiteralPath "Env:$_" -ErrorAction SilentlyContinue } }
    $explicitApiToken = $null
}

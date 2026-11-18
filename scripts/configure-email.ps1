#requires -Version 7.2
[CmdletBinding()]
param([switch]$Replace)
$ErrorActionPreference = 'Stop'
if (-not $IsWindows) { throw 'This local configuration helper requires Windows DPAPI.' }
Import-Module (Join-Path $PSScriptRoot 'WechatInit.psm1') -Force
$projectRoot = Split-Path -Parent $PSScriptRoot
$private = Initialize-WechatPrivateDirectory (Join-Path $projectRoot 'data/private')
$destination = Assert-WechatInitPath (Join-Path $private 'email-imap.dpapi')
if ((Test-Path -LiteralPath $destination) -and (-not $Replace -or -not (Test-Path -LiteralPath $destination -PathType Leaf))) {
    throw 'Mail configuration already exists or is not a file. Use -Replace only to replace an existing file.'
}
$server = (Read-Host 'Official IMAP TLS hostname (for example imap.qq.com)').Trim().ToLowerInvariant()
$username = (Read-Host 'Full email address').Trim()
$portText = (Read-Host 'IMAP TLS port (default 993)').Trim()
$folder = (Read-Host 'One folder to receive (default INBOX)').Trim()
if (-not $portText) { $portText = '993' }
if (-not $folder) { $folder = 'INBOX' }
if ($server -cnotmatch '^(?=.{3,253}$)(?:[a-z0-9-]+\.)+[a-z0-9-]+$' -or
    $username -cnotmatch '^[!-~]{1,254}$' -or $username -notmatch '@' -or
    $portText -notmatch '^\d{1,5}$' -or [int]$portText -lt 1 -or [int]$portText -gt 65535 -or
    $folder.Length -gt 128 -or $folder -match '[\p{Cc}]') { throw 'Invalid IMAP hostname, email address, TLS port or folder.' }
$secret = Read-Host 'IMAP application authorization code (hidden input)' -AsSecureString
$pointer = [IntPtr]::Zero
$configuration = $null
$tempPath = Join-Path $private ('email-imap-' + [guid]::NewGuid().ToString('N') + '.tmp')
try {
    $pointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secret)
    $applicationCode = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($pointer)
    if ($applicationCode -cnotmatch '^[!-~]{1,4096}$') { throw 'The application code must be printable ASCII without whitespace.' }
    $json = [ordered]@{ format = 1; host = $server; username = $username; port = [int]$portText; folder = $folder; password = $applicationCode } | ConvertTo-Json -Compress
    $configuration = ConvertTo-SecureString -String $json -AsPlainText -Force
    [IO.File]::WriteAllText($tempPath, (ConvertFrom-SecureString -SecureString $configuration), [Text.UTF8Encoding]::new($false))
    [IO.File]::Move($tempPath, $destination, $Replace.IsPresent)
    Write-Host 'Protected mail configuration saved. Restart the inbox and enable background mail receipt once in Messages.'
} catch {
    throw 'Could not save protected mail settings. Check the inputs and local configuration file.'
} finally {
    if ($pointer -ne [IntPtr]::Zero) { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($pointer) }
    $secret.Dispose()
    if ($null -ne $configuration) { $configuration.Dispose() }
    if (Test-Path -LiteralPath $tempPath -PathType Leaf) { Remove-Item -LiteralPath $tempPath -Force }
    Remove-Variable applicationCode,json -ErrorAction SilentlyContinue
}

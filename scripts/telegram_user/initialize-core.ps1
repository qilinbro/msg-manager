#requires -Version 7.2
[CmdletBinding()]
param([switch]$Replace)
$ErrorActionPreference = 'Stop'
if (-not $IsWindows) { throw 'Telegram initialization currently requires Windows CurrentUser DPAPI.' }
$projectRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
& (Join-Path $PSScriptRoot 'prepare-core.ps1') -CheckOnly
Import-Module (Join-Path $projectRoot 'scripts/WechatInit.psm1') -Force
$privateDirectory = Initialize-WechatPrivateDirectory -Path (Join-Path $projectRoot 'data/private')
$credential = Join-Path $privateDirectory 'telegram-user.dpapi'
if ((Test-Path -LiteralPath $credential) -and -not $Replace) { throw 'Telegram configuration exists. Use -Replace only when intentionally replacing the saved session.' }
Write-Host 'Initialize your own Telegram personal account. Obtain API ID / API Hash from https://my.telegram.org/apps.'
Write-Host 'Enter credentials, phone, login code and optional two-step password only in this local terminal.'
Write-Host 'No chats are imported or sent during initialization. The session will be protected for this Windows account.'
if ($Replace) { Write-Host 'Replacing the local session does not revoke the old Telegram device. Review Telegram Settings > Devices if needed.' }
$python = Join-Path $projectRoot '.tools/telegram-user-venv/Scripts/python.exe'
$worker = Join-Path $PSScriptRoot 'worker.py'
$arguments = @('-I', $worker, 'initialize')
if ($Replace) { $arguments += '--replace' }
& $python @arguments
if ($LASTEXITCODE -ne 0) { throw 'Telegram account initialization did not complete. No credential was printed or copied to the clipboard.' }
$global:LASTEXITCODE = 0

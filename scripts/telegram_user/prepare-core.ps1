#requires -Version 7.2
[CmdletBinding()]
param([string]$PythonPath = '', [switch]$CheckOnly)
$ErrorActionPreference = 'Stop'
if (-not $IsWindows) { throw 'The personal Telegram connector currently requires Windows CurrentUser DPAPI.' }
$projectRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$venv = Join-Path $projectRoot '.tools/telegram-user-venv'
$python = Join-Path $venv 'Scripts/python.exe'
$requirements = Join-Path $PSScriptRoot 'requirements.txt'
if (-not (Test-Path -LiteralPath $python -PathType Leaf)) {
    if ($CheckOnly) { throw 'Telegram dependencies are not prepared. Run scripts/prepare-telegram.ps1 first.' }
    $candidates = @()
    if ($PythonPath) { $candidates += $PythonPath }
    $candidates += Join-Path $projectRoot '.tools/wechat-init-venv/Scripts/python.exe'
    foreach ($command in @(Get-Command python.exe -CommandType Application -All -ErrorAction SilentlyContinue)) { $candidates += $command.Source }
    $basePython = $null
    foreach ($candidate in $candidates) {
        if (-not $candidate -or -not [IO.Path]::IsPathFullyQualified($candidate) -or $candidate.StartsWith('\\') -or -not (Test-Path -LiteralPath $candidate -PathType Leaf)) { continue }
        try {
            $version = & $candidate -I -c 'import struct,sys; print(int(sys.version_info >= (3,10) and struct.calcsize("P") == 8))' 2>$null
            if ($LASTEXITCODE -eq 0 -and ($version -join '').Trim() -eq '1') { $basePython = $candidate; break }
        } catch { }
    }
    if (-not $basePython) { throw 'Python 3.10+ x64 is required. Run prepare-telegram.ps1 -PythonPath <absolute python.exe path>.' }
    & $basePython -I -m venv $venv
    if ($LASTEXITCODE -ne 0) { throw 'Could not prepare the isolated Telegram Python environment.' }
}
if (-not $CheckOnly) {
    & $python -I -m pip install --disable-pip-version-check -r $requirements
    if ($LASTEXITCODE -ne 0) { throw 'Telegram dependency installation did not complete; no account login was attempted.' }
}
& $python -I -c 'from importlib.metadata import version; import telethon,python_socks; assert version("Telethon")=="1.45.0"; assert version("python-socks")=="2.8.1"; print("Telegram helper dependencies ready; no account data was accessed.")'
if ($LASTEXITCODE -ne 0) { throw 'The Telegram Python dependencies do not match the project requirements.' }
$global:LASTEXITCODE = 0

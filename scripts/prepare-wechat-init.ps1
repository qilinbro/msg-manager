#requires -Version 5.1
[CmdletBinding()]
param([string]$PythonPath = (Join-Path $env:LOCALAPPDATA 'Programs/Python/Python313/python.exe'), [switch]$CheckOnly)
$ErrorActionPreference = 'Stop'
$core = Join-Path $PSScriptRoot 'prepare-wechat-init-core.ps1'
if ($PSVersionTable.PSVersion -ge [Version]'7.2' -and [IntPtr]::Size -eq 8) {
    $global:LASTEXITCODE = 0
    & $core @PSBoundParameters
    exit $LASTEXITCODE
}
Import-Module (Join-Path $PSScriptRoot 'PowerShellBootstrap.psm1') -Force
$engine = Find-CompatiblePowerShell -ProjectRoot (Split-Path -Parent $PSScriptRoot)
$forward = @(New-PowerShell7FileArguments -ScriptPath $core -Parameters $PSBoundParameters)
& $engine @forward
exit $LASTEXITCODE

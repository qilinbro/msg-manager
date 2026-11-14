#requires -Version 5.1
[CmdletBinding()]
param([int]$WechatProcessId = 0, [string]$CompilerPath = '', [switch]$Replace)
$ErrorActionPreference = 'Stop'
$core = Join-Path $PSScriptRoot 'initialize-wechat-core.ps1'
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

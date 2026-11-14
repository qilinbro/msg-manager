#requires -Version 7.2
# Synthetic host/argument tests only. The real initialization core never runs.
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
if (-not $IsWindows) { throw 'These bootstrap tests require Windows PowerShell 5.1.' }
$projectRoot = Split-Path -Parent $PSScriptRoot
$bootstrapModule = Join-Path $PSScriptRoot 'PowerShellBootstrap.psm1'
Import-Module $bootstrapModule -Force
$legacyEngine = Join-Path $env:WINDIR 'System32/WindowsPowerShell/v1.0/powershell.exe'
$currentEngine = [Diagnostics.Process]::GetCurrentProcess().MainModule.FileName
$modernEngine = Find-CompatiblePowerShell -ProjectRoot $projectRoot -CandidatePaths @($currentEngine)
$temporaryBase = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
$testRoot = [IO.Path]::GetFullPath((Join-Path $temporaryBase ('msg-manager-bootstrap-' + [guid]::NewGuid().ToString('N'))))
$fixtureScripts = Join-Path $testRoot "stub's 中文 scripts"
$caseCount = 0

function Assert-BootstrapTest {
    param([bool]$Condition, [string]$Message)
    if (-not $Condition) { throw $Message }
}

function Assert-NoCompatibleEngine {
    param([string[]]$Candidates)
    $found = $null
    try { $found = Find-CompatiblePowerShell -ProjectRoot $projectRoot -CandidatePaths $Candidates }
    catch { return }
    Assert-BootstrapTest (-not $found) 'An absent or legacy engine was accepted.'
}

function Invoke-BootstrapFixture {
    param([string]$Engine, [string]$Entry, [string[]]$Arguments, [int]$ExpectedExitCode = 0, [switch]$NoExplicitExit, [string]$ExpectedCore = '')
    $outputPath = Join-Path $testRoot ([guid]::NewGuid().ToString('N') + '.json')
    $startInfo = [Diagnostics.ProcessStartInfo]::new()
    $startInfo.FileName = $Engine
    $startInfo.UseShellExecute = $false
    $startInfo.CreateNoWindow = $true
    $startInfo.WindowStyle = [Diagnostics.ProcessWindowStyle]::Hidden
    $startInfo.WorkingDirectory = $testRoot
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    foreach ($argument in (@('-NoLogo','-NoProfile','-NonInteractive','-File',(Join-Path $fixtureScripts $Entry)) + $Arguments)) {
        $startInfo.ArgumentList.Add($argument)
    }
    $startInfo.Environment['MSG_MANAGER_BOOTSTRAP_TEST_OUTPUT'] = $outputPath
    $startInfo.Environment['MSG_MANAGER_BOOTSTRAP_TEST_EXIT'] = [string]$ExpectedExitCode
    $startInfo.Environment['MSG_MANAGER_BOOTSTRAP_TEST_NO_EXIT'] = if ($NoExplicitExit) { '1' } else { '0' }
    $process = $null
    try {
        $process = [Diagnostics.Process]::Start($startInfo)
        $stdoutTask = $process.StandardOutput.ReadToEndAsync()
        $stderrTask = $process.StandardError.ReadToEndAsync()
        if (-not $process.WaitForExit(20000)) { throw 'Synthetic bootstrap timed out.' }
        $stdout = $stdoutTask.GetAwaiter().GetResult()
        $stderr = $stderrTask.GetAwaiter().GetResult()
        Assert-BootstrapTest ($process.ExitCode -eq $ExpectedExitCode) ('Bootstrap exit code mismatch: expected {0}, got {1}. {2} {3}' -f $ExpectedExitCode,$process.ExitCode,$stdout,$stderr)
        Assert-BootstrapTest (Test-Path -LiteralPath $outputPath -PathType Leaf) 'The synthetic core did not write its result.'
        $result = [IO.File]::ReadAllText($outputPath) | ConvertFrom-Json
        Assert-BootstrapTest ([Version]$result.version -ge [Version]'7.2') 'The synthetic core ran in a legacy host.'
        Assert-BootstrapTest ($result.pointer_size -eq 8) 'The synthetic core did not run in x64.'
        Assert-BootstrapTest ($result.remaining_arguments.Count -eq 0) 'Forwarding left unbound parameters.'
        Assert-BootstrapTest ($result.working_directory -ceq $testRoot) 'Forwarding changed the current directory.'
        $coreName = if ($ExpectedCore) { $ExpectedCore } else { $Entry -replace '\.ps1$','-core.ps1' }
        Assert-BootstrapTest ($result.script_path -ceq (Join-Path $fixtureScripts $coreName)) 'Forwarding selected the wrong core script.'
        return $result
    } finally {
        if ($null -ne $process) {
            if (-not $process.HasExited) { $process.Kill($true); $null = $process.WaitForExit(5000) }
            $process.Dispose()
        }
    }
}

try {
    New-Item -ItemType Directory -Path $fixtureScripts -Force | Out-Null
    Copy-Item -LiteralPath $bootstrapModule -Destination (Join-Path $fixtureScripts 'PowerShellBootstrap.psm1')
    # Copy the real thin wrappers, replacing every core with this inert stub.
    # The stub accepts the wrappers' union of options and only records values.
    $stub = @'
#requires -Version 7.2
param([int]$WechatProcessId = 0, [string]$CompilerPath = '', [switch]$Replace, [string]$PythonPath = '', [switch]$CheckOnly)
$ErrorActionPreference = 'Stop'
$result = [ordered]@{
    version = $PSVersionTable.PSVersion.ToString()
    pointer_size = [IntPtr]::Size
    wechat_process_id = $WechatProcessId
    compiler_path = $CompilerPath
    replace = [bool]$Replace
    python_path = $PythonPath
    check_only = [bool]$CheckOnly
    remaining_arguments = @($args)
    working_directory = (Get-Location).Path
    script_path = $PSCommandPath
}
[IO.File]::WriteAllText($env:MSG_MANAGER_BOOTSTRAP_TEST_OUTPUT, ($result | ConvertTo-Json -Compress), [Text.UTF8Encoding]::new($false))
if ($env:MSG_MANAGER_BOOTSTRAP_TEST_NO_EXIT -ne '1') { exit [int]$env:MSG_MANAGER_BOOTSTRAP_TEST_EXIT }
'@
    foreach ($name in @('initialize-wechat','prepare-wechat-init','configure-wechat','start-inbox')) {
        Copy-Item -LiteralPath (Join-Path $PSScriptRoot ($name + '.ps1')) -Destination (Join-Path $fixtureScripts ($name + '.ps1'))
        [IO.File]::WriteAllText((Join-Path $fixtureScripts ($name + '-core.ps1')), $stub, [Text.UTF8Encoding]::new($true))
    }
    $sameHostDriver = @'
#requires -Version 7.2
$ErrorActionPreference = 'Stop'
$global:LASTEXITCODE = 37
& (Join-Path $PSScriptRoot 'initialize-wechat.ps1') -WechatProcessId 909
$marker = @{ survived = $true; exit_code = $LASTEXITCODE } | ConvertTo-Json -Compress
[IO.File]::WriteAllText((Join-Path $PSScriptRoot 'parent-marker.json'), $marker, [Text.UTF8Encoding]::new($false))
exit 0
'@
    [IO.File]::WriteAllText((Join-Path $fixtureScripts 'same-host-driver.ps1'), $sameHostDriver, [Text.UTF8Encoding]::new($true))
    Assert-NoCompatibleEngine -Candidates @()
    Assert-NoCompatibleEngine -Candidates @($legacyEngine)
    $selected = Find-CompatiblePowerShell -ProjectRoot $projectRoot -CandidatePaths @($legacyEngine,$modernEngine)
    Assert-BootstrapTest ($selected -ceq $modernEngine) 'The candidate selector did not skip Windows PowerShell 5.1.'

    $compiler = "C:\fake tools\GCC's `$directory; & 中文\gcc.exe"
    $corePath = Join-Path $fixtureScripts 'initialize-wechat-core.ps1'
    $parameters = [ordered]@{ WechatProcessId = 24680; CompilerPath = $compiler; Replace = [switch]::new($true) }
    $forward = @(New-PowerShell7FileArguments -ScriptPath $corePath -Parameters $parameters)
    Assert-BootstrapTest ($forward.Count -eq 9 -and $forward[0] -ceq '-NoLogo' -and $forward[1] -ceq '-NoProfile' -and $forward[2] -ceq '-File' -and $forward[3] -ceq $corePath) 'Native host arguments changed the script path or host flags.'
    Assert-BootstrapTest ($forward[-1] -ceq '-Replace' -and $forward[-2] -ceq $compiler) 'A true switch or compiler path was serialized incorrectly.'
    $empty = @(New-PowerShell7FileArguments -ScriptPath $corePath -Parameters @{ CompilerPath = ''; Replace = [switch]::new($false) })
    Assert-BootstrapTest ($empty.Count -eq 4) 'An empty compiler path or false switch was forwarded.'
    $falseBoolean = @(New-PowerShell7FileArguments -ScriptPath $corePath -Parameters @{ Replace = $false })
    Assert-BootstrapTest ($falseBoolean.Count -eq 4) 'A false boolean was forwarded as a flag.'

    $result = Invoke-BootstrapFixture -Engine $legacyEngine -Entry 'initialize-wechat.ps1' -Arguments @('-WechatProcessId','24680','-CompilerPath',$compiler,'-Replace')
    Assert-BootstrapTest ($result.wechat_process_id -eq 24680 -and $result.compiler_path -ceq $compiler -and $result.replace) 'PS5.1 forwarding changed the PID, special-character path or true switch.'
    $caseCount++
    $result = Invoke-BootstrapFixture -Engine $legacyEngine -Entry 'initialize-wechat.ps1' -Arguments @()
    Assert-BootstrapTest ($result.wechat_process_id -eq 0 -and $result.compiler_path -ceq '' -and -not $result.replace) 'PS5.1 forwarding changed default option values.'
    $caseCount++
    $null = Invoke-BootstrapFixture -Engine $legacyEngine -Entry 'initialize-wechat.ps1' -Arguments @('-WechatProcessId','13579') -ExpectedExitCode 37
    $caseCount++
    $result = Invoke-BootstrapFixture -Engine $legacyEngine -Entry 'prepare-wechat-init.ps1' -Arguments @('-PythonPath',"C:\fake tools\Python's 中文\python.exe",'-CheckOnly')
    Assert-BootstrapTest ($result.python_path -ceq "C:\fake tools\Python's 中文\python.exe" -and $result.check_only) 'Preparation forwarding changed the Python path or CheckOnly.'
    $caseCount++
    $result = Invoke-BootstrapFixture -Engine $legacyEngine -Entry 'configure-wechat.ps1' -Arguments @('-Replace')
    Assert-BootstrapTest $result.replace 'Configuration forwarding dropped Replace.'
    $caseCount++
    $null = Invoke-BootstrapFixture -Engine $legacyEngine -Entry 'start-inbox.ps1' -Arguments @()
    $caseCount++
    $result = Invoke-BootstrapFixture -Engine $modernEngine -Entry 'initialize-wechat.ps1' -Arguments @('-WechatProcessId','11223','-CompilerPath',$compiler)
    Assert-BootstrapTest ($result.wechat_process_id -eq 11223 -and $result.compiler_path -ceq $compiler -and -not $result.replace) 'Direct PS7 forwarding changed values.'
    $caseCount++
    $null = Invoke-BootstrapFixture -Engine $modernEngine -Entry 'initialize-wechat.ps1' -Arguments @() -ExpectedExitCode 37
    $caseCount++
    $result = Invoke-BootstrapFixture -Engine $modernEngine -Entry 'same-host-driver.ps1' -Arguments @() -NoExplicitExit -ExpectedCore 'initialize-wechat-core.ps1'
    Assert-BootstrapTest ($result.wechat_process_id -eq 909) 'The same-host driver did not run the stub wrapper.'
    $marker = Get-Content -Raw -LiteralPath (Join-Path $fixtureScripts 'parent-marker.json') | ConvertFrom-Json
    Assert-BootstrapTest ($marker.survived -and $marker.exit_code -eq 0) 'The wrapper exited its parent host or reused a stale failure exit code.'
    $caseCount++
    Write-Host ('PowerShell bootstrap synthetic tests passed: {0} child cases plus selector and argument checks.' -f $caseCount)
} finally {
    if (Test-Path -LiteralPath $testRoot) {
        $resolved = [IO.Path]::GetFullPath($testRoot)
        $parent = [IO.Path]::GetDirectoryName($resolved).TrimEnd('\')
        if (-not $parent.Equals($temporaryBase.TrimEnd('\'), [StringComparison]::OrdinalIgnoreCase) -or [IO.Path]::GetFileName($resolved) -notmatch '^msg-manager-bootstrap-[a-f0-9]{32}$') { throw 'Refusing cleanup outside the synthetic test directory.' }
        Remove-Item -LiteralPath $resolved -Recurse -Force
    }
}

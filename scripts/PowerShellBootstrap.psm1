# Windows PowerShell 5.1-compatible host selection. No installation or policy changes.
Set-StrictMode -Version 2.0

function Find-CompatiblePowerShell {
    param([string]$ProjectRoot = '', [string[]]$CandidatePaths)
    $candidates = @()
    if ($PSBoundParameters.ContainsKey('CandidatePaths')) {
        $candidates = @($CandidatePaths)
    } else {
        $commands = @(Get-Command pwsh.exe -CommandType Application -All -ErrorAction SilentlyContinue)
        foreach ($command in $commands) { $candidates += $command.Source }
        if ($ProjectRoot) { $candidates += Join-Path $ProjectRoot '.tools/powershell/pwsh.exe' }
        foreach ($programDirectory in @($env:ProgramW6432, $env:ProgramFiles)) {
            if ($programDirectory) { $candidates += Join-Path $programDirectory 'PowerShell/7/pwsh.exe' }
        }
        if ($env:LOCALAPPDATA) { $candidates += Join-Path $env:LOCALAPPDATA 'Programs/PowerShell/7/pwsh.exe' }
        if ($env:USERPROFILE) { $candidates += Join-Path $env:USERPROFILE '.cache/codex-runtimes/codex-primary-runtime/dependencies/native/powershell/pwsh.exe' }
    }
    $seen = @{}
    foreach ($candidate in $candidates) {
        if (-not $candidate -or -not [IO.Path]::IsPathRooted($candidate) -or $candidate.StartsWith('\\') -or $candidate.StartsWith('//')) { continue }
        if ($seen.ContainsKey($candidate)) { continue }
        $seen[$candidate] = $true
        if (-not (Test-Path -LiteralPath $candidate -PathType Leaf) -or [IO.Path]::GetExtension($candidate) -ine '.exe') { continue }
        try {
            $report = & $candidate -NoLogo -NoProfile -NonInteractive -Command '[Console]::WriteLine($PSVersionTable.PSVersion.ToString() + [char]124 + [IntPtr]::Size)' 2>$null
            $candidateExit = $LASTEXITCODE
            if ($candidateExit -ne 0) { continue }
            $parts = ($report -join '').Trim().Split('|')
            $version = [Version]'0.0'
            if ($parts.Count -ne 2 -or -not [Version]::TryParse($parts[0], [ref]$version) -or $version -lt [Version]'7.2' -or $parts[1] -ne '8') { continue }
            return [string]$candidate
        } catch { }
    }
    throw 'PowerShell 7.2+ x64 was not found. Install PowerShell 7, then run this command again. No software or execution policy was changed.'
}

function New-PowerShell7FileArguments {
    param([Parameter(Mandatory)][string]$ScriptPath, [Parameter(Mandatory)][System.Collections.IDictionary]$Parameters)
    if (-not [IO.Path]::IsPathRooted($ScriptPath) -or -not (Test-Path -LiteralPath $ScriptPath -PathType Leaf)) { throw 'The PowerShell 7 script is missing.' }
    $forward = @('-NoLogo','-NoProfile','-File',$ScriptPath)
    foreach ($name in $Parameters.Keys) {
        if ([string]$name -notmatch '^[A-Za-z][A-Za-z0-9]*$') { throw 'Invalid script parameter name.' }
        $value = $Parameters[$name]
        if ($value -is [System.Management.Automation.SwitchParameter] -or $value -is [bool]) {
            if ([bool]$value) { $forward += '-' + [string]$name }
        } elseif ($null -ne $value -and [string]$value -ne '') {
            $forward += '-' + [string]$name
            $forward += [string]$value
        }
    }
    return $forward
}

Export-ModuleMember -Function Find-CompatiblePowerShell,New-PowerShell7FileArguments

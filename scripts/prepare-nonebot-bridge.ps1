#requires -Version 7.2
[CmdletBinding()]
param(
    [switch]$CheckOnly,
    [switch]$Install,
    [string]$PythonExecutable,
    [string]$VenvDirectory,
    [string]$Wheelhouse
)

$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
$script:failureMessage = 'Preparation failed; inspect the trusted local interpreter and tool paths.'

function Stop-Preparation {
    param([Parameter(Mandatory)][string]$SafeMessage)
    $script:failureMessage = $SafeMessage
    throw 'NoneBot bridge preparation stopped.'
}

# Match the existing Windows tool preparation policy: no UNC/device path,
# removable drive, symlink or junction, including in an existing ancestor.
function Assert-LocalPath {
    param([Parameter(Mandatory)][string]$Candidate)
    if (-not [IO.Path]::IsPathFullyQualified($Candidate) -or $Candidate.StartsWith('\\')) {
        Stop-Preparation 'Select absolute paths on fixed local drives; UNC and device paths are not supported.'
    }
    $absolutePath = [IO.Path]::GetFullPath($Candidate)
    $drive = [IO.DriveInfo]::new([IO.Path]::GetPathRoot($absolutePath))
    if ($drive.DriveType -ne [IO.DriveType]::Fixed) {
        Stop-Preparation 'Tool paths must stay on fixed local drives.'
    }
    $currentPath = $absolutePath
    while ($currentPath) {
        try {
            if ([IO.File]::GetAttributes($currentPath) -band [IO.FileAttributes]::ReparsePoint) {
                Stop-Preparation 'Tool paths and their ancestors must not be links or junctions.'
            }
        } catch [IO.FileNotFoundException] {
            # A new empty venv may have nonexistent descendants.
        } catch [IO.DirectoryNotFoundException] {
            # Continue checking the existing ancestors.
        }
        $parentPath = [IO.Path]::GetDirectoryName($currentPath)
        if ($parentPath -eq $currentPath) { break }
        $currentPath = $parentPath
    }
    return $absolutePath
}

function Invoke-PreparedPython {
    param(
        [Parameter(Mandatory)][string]$Executable,
        [Parameter(Mandatory)][string[]]$Arguments,
        [int]$TimeoutSeconds = 60
    )
    $info = [Diagnostics.ProcessStartInfo]::new()
    $info.FileName = $Executable
    $info.WorkingDirectory = $projectRoot
    $info.UseShellExecute = $false
    $info.CreateNoWindow = $true
    $info.RedirectStandardInput = $true
    $info.RedirectStandardOutput = $true
    $info.RedirectStandardError = $true
    foreach ($argument in @('-I', '-u', '-B') + $Arguments) {
        $info.ArgumentList.Add($argument)
    }
    # Never inherit product secrets, dotenv/home settings, proxy/CA overrides,
    # PYTHONPATH or pip indexes. Do not enumerate or print the parent environment.
    $info.Environment.Clear()
    foreach ($name in @('SystemRoot', 'WINDIR', 'TEMP', 'TMP')) {
        $value = [Environment]::GetEnvironmentVariable($name)
        if ($value) { $info.Environment[$name] = $value }
    }
    $path = Split-Path -Parent $Executable
    $systemRoot = [Environment]::GetEnvironmentVariable('SystemRoot')
    if ($systemRoot) { $path += [IO.Path]::PathSeparator + (Join-Path $systemRoot 'System32') }
    $info.Environment['PATH'] = $path
    # pip --isolated still has system configuration sources; os.devnull disables
    # those too. Lowercase matches Windows Python's os.devnull exactly.
    $info.Environment['PIP_CONFIG_FILE'] = 'nul'
    $process = [Diagnostics.Process]::new()
    $process.StartInfo = $info
    $started = $false
    try {
        $started = $process.Start()
        if (-not $started) { throw 'Python did not start.' }
        $process.StandardInput.Close() # No credentials, prompts or code from stdin.
        # Drain without retaining or forwarding raw errors, paths or metadata.
        $stdout = $process.StandardOutput.BaseStream.CopyToAsync([IO.Stream]::Null)
        $stderr = $process.StandardError.BaseStream.CopyToAsync([IO.Stream]::Null)
        if (-not $process.WaitForExit($TimeoutSeconds * 1000)) {
            throw 'Python exceeded the preparation deadline.'
        }
        $drained = [Threading.Tasks.Task]::WhenAll([Threading.Tasks.Task[]]@($stdout, $stderr))
        if (-not $drained.Wait(2000)) { throw 'Python output did not close within the deadline.' }
        $null = $drained.GetAwaiter().GetResult()
        return [int]$process.ExitCode
    } finally {
        if ($started -and -not $process.HasExited) {
            $process.Kill($true)
            $null = $process.WaitForExit(2000)
        }
        $process.Dispose()
    }
}

# Fixed local checks only. Do not import adapters, initialize NoneBot, read
# application credentials/databases, run bridge.py, or make platform requests.
$checkPython = @'
import pathlib, struct, sys, sysconfig
try:
    if (sys.implementation.name != "cpython" or sys.platform != "win32"
            or sys.version_info[:2] != (3, 12) or struct.calcsize("P") != 8
            or sysconfig.get_platform() != "win-amd64"):
        raise ValueError()
    if sys.argv[1] != "base":
        root = pathlib.Path(sys.argv[1]).resolve()
        if pathlib.Path(sys.prefix).resolve() != root or sys.prefix == sys.base_prefix:
            raise ValueError()
        cfg = (root / "pyvenv.cfg").read_text(encoding="utf-8")
        isolation = [line.partition("=")[2].strip().lower() for line in cfg.splitlines()
                     if line.partition("=")[0].strip().lower() == "include-system-site-packages"]
        if isolation != ["false"]:
            raise ValueError()
except Exception:
    sys.exit(1)
'@

$checkRequirements = @'
import importlib.metadata as metadata
import pathlib, re, sys
try:
    mode, lock_path, root_path = sys.argv[1:]
    if mode not in {"lock", "installed"}:
        raise ValueError()
    expected = {}
    for line in pathlib.Path(lock_path).read_text(encoding="utf-8").splitlines():
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        match = re.fullmatch(r"([A-Za-z0-9][A-Za-z0-9_.-]*)==([0-9][A-Za-z0-9.!+_-]*) --hash=sha256:([0-9a-f]{64})", line)
        if not match:
            raise ValueError()
        name, version, _ = match.groups()
        name = re.sub(r"[-_.]+", "-", name).lower()
        if name in expected:
            raise ValueError()
        expected[name] = version
    core = {"nonebot2": "2.5.0", "nonebot-adapter-telegram": "0.1.0b21",
            "nonebot-adapter-feishu": "3.1.0", "larkify": "0.2.0"}
    if len(expected) != 34 or any(expected.get(name) != version for name, version in core.items()):
        raise ValueError()
    if mode == "installed":
        root = pathlib.Path(root_path).resolve()
        for name, version in expected.items():
            distribution = metadata.distribution(name)
            if (distribution.version != version
                    or not pathlib.Path(distribution.locate_file("")).resolve().is_relative_to(root)):
                raise ValueError()
except Exception:
    sys.exit(1)
'@

try {
    if (-not $IsWindows -or -not [Environment]::Is64BitOperatingSystem) {
        Stop-Preparation 'NoneBot bridge preparation requires Windows x64 and PowerShell 7.2 or newer.'
    }
    if ($CheckOnly -and $Install) {
        Stop-Preparation 'Choose -CheckOnly or -Install, not both. The default is check-only.'
    }
    $projectRoot = Assert-LocalPath (Split-Path -Parent $PSScriptRoot)
    if (-not $VenvDirectory) { $VenvDirectory = Join-Path $projectRoot '.tools/nonebot-bridge-venv' }
    $venvRoot = Assert-LocalPath $VenvDirectory
    $toolPython = Assert-LocalPath (Join-Path $venvRoot 'Scripts/python.exe')
    $lockPath = Assert-LocalPath (Join-Path $projectRoot 'integrations/nonebot_bridge/requirements-windows-py312.lock.txt')
    if (-not (Test-Path -LiteralPath $lockPath -PathType Leaf)) {
        Stop-Preparation 'The reviewed Windows CPython 3.12 dependency lock is missing.'
    }
    $created = $false
    if (-not (Test-Path -LiteralPath $toolPython -PathType Leaf)) {
        if (-not $Install) {
            Stop-Preparation 'The selected venv is not installed. Check-only made no changes; installation requires explicit -Install.'
        }
        if ((Test-Path -LiteralPath $venvRoot) -and
            (-not (Test-Path -LiteralPath $venvRoot -PathType Container) -or
             @(Get-ChildItem -LiteralPath $venvRoot -Force).Count -ne 0)) {
            Stop-Preparation 'The target is not an empty directory. Inspect it or choose a fresh venv; no files were removed.'
        }
        if (-not $PythonExecutable) {
            Stop-Preparation 'Creating a venv requires -PythonExecutable pointing to a trusted CPython 3.12 x64 executable.'
        }
        $basePython = Assert-LocalPath $PythonExecutable
        if (-not (Test-Path -LiteralPath $basePython -PathType Leaf) -or
            [IO.Path]::GetExtension($basePython) -ine '.exe') {
            Stop-Preparation 'Select a trusted CPython 3.12 x64 executable using -PythonExecutable.'
        }
        $script:failureMessage = 'The base interpreter or 34-entry hash-pinned lock failed validation; nothing was installed.'
        if ((Invoke-PreparedPython -Executable $basePython -Arguments @('-c', $checkPython, 'base')) -ne 0 -or
            (Invoke-PreparedPython -Executable $basePython -Arguments @('-c', $checkRequirements, 'lock', $lockPath, $venvRoot)) -ne 0) {
            throw 'Base or lock validation failed.'
        }
        if ($Wheelhouse) {
            $Wheelhouse = Assert-LocalPath $Wheelhouse
            if (-not (Test-Path -LiteralPath $Wheelhouse -PathType Container)) {
                Stop-Preparation 'An offline wheelhouse must be an existing absolute local directory.'
            }
        }
        $script:failureMessage = 'Fresh venv creation failed. No existing environment was replaced; inspect any partial target manually.'
        $null = [IO.Directory]::CreateDirectory((Split-Path -Parent $venvRoot))
        $null = Assert-LocalPath $venvRoot
        if ((Invoke-PreparedPython -Executable $basePython -Arguments @('-m', 'venv', $venvRoot) -TimeoutSeconds 120) -ne 0) {
            throw 'Venv creation failed.'
        }
        $created = $true
    }
    $null = Assert-LocalPath $toolPython
    $script:failureMessage = 'The target must be its own CPython 3.12 x64 venv without system site-packages; no existing packages were modified.'
    if ((Invoke-PreparedPython -Executable $toolPython -Arguments @('-c', $checkPython, $venvRoot)) -ne 0) {
        throw 'Venv validation failed.'
    }
    if ($created) {
        $script:failureMessage = 'Pinned wheel installation failed. TLS and hash checks remain enabled; inspect the partial fresh venv manually.'
        $pipArguments = @('-m', 'pip', '--isolated', '--disable-pip-version-check', 'install',
            '--require-virtualenv', '--no-input', '--no-cache-dir', '--retries', '0', '--timeout', '30',
            '--require-hashes', '--only-binary=:all:', '--no-deps', '-r', $lockPath)
        if ($Wheelhouse) { $pipArguments += @('--no-index', '--find-links', $Wheelhouse) }
        else { $pipArguments += @('--index-url', 'https://pypi.org/simple') }
        if ((Invoke-PreparedPython -Executable $toolPython -Arguments $pipArguments -TimeoutSeconds 600) -ne 0) {
            throw 'Pinned installation failed.'
        }
    }
    $script:failureMessage = 'The 34 exact locked package versions are missing or mismatched. Existing environments are never repaired, reinstalled or upgraded automatically.'
    if ((Invoke-PreparedPython -Executable $toolPython -Arguments @('-c', $checkRequirements, 'installed', $lockPath, $venvRoot)) -ne 0) {
        throw 'Installed version validation failed.'
    }
    $script:failureMessage = 'pip check failed or timed out; no raw dependency or Python error output was forwarded.'
    if ((Invoke-PreparedPython -Executable $toolPython -Arguments @('-m', 'pip', '--isolated', '--disable-pip-version-check', 'check')) -ne 0) {
        throw 'Dependency consistency validation failed.'
    }
    Write-Host 'NoneBot bridge check passed (CPython 3.12 x64; 34 exact pinned distributions; pip check).'
    if ($created) { Write-Host 'A fresh venv was prepared. No worker was started and no runtime was activated.' }
    else { Write-Host 'Read-only validation completed; no venv was created and no packages were installed or changed.' }
    exit 0
} catch {
    # Only fixed stage messages: never echo exception text, process output,
    # command lines, parent environment values or any credential-bearing input.
    [Console]::Error.WriteLine('NoneBot bridge: ' + $script:failureMessage)
    exit 1
}

#requires -Version 7.2
[CmdletBinding()]
param(
    [string]$PythonPath = (Join-Path $env:LOCALAPPDATA 'Programs/Python/Python313/python.exe'),
    [switch]$CheckOnly
)

$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
if (-not $IsWindows -or -not [Environment]::Is64BitOperatingSystem) {
    throw 'WeChat initialization tools currently require Windows x64.'
}

# The installation target cannot be changed by a command-line argument. Never
# install to a global interpreter or follow a junction out of this workspace.
function Assert-LocalPath {
    param([Parameter(Mandatory)][string]$Candidate)
    if (-not [IO.Path]::IsPathFullyQualified($Candidate) -or $Candidate.StartsWith('\\')) {
        throw 'Tool paths must be absolute paths on a fixed local drive.'
    }
    $absolutePath = [IO.Path]::GetFullPath($Candidate)
    $driveRoot = [IO.Path]::GetPathRoot($absolutePath)
    $drive = [IO.DriveInfo]::new($driveRoot)
    if ($drive.DriveType -ne [IO.DriveType]::Fixed) {
        throw 'Tool paths must stay on fixed local drives.'
    }
    $currentPath = $absolutePath
    while ($currentPath) {
        try {
            $attributes = [IO.File]::GetAttributes($currentPath)
            if ($attributes -band [IO.FileAttributes]::ReparsePoint) {
                throw 'Tool paths and all their ancestors must not be links or junctions.'
            }
        } catch [IO.FileNotFoundException] {
            # Nonexistent descendants are valid before the venv is created.
        } catch [IO.DirectoryNotFoundException] {
            # Keep checking existing ancestors, including dangling links.
        }
        $parentPath = [IO.Path]::GetDirectoryName($currentPath)
        if ($parentPath -eq $currentPath) { break }
        $currentPath = $parentPath
    }
    return $absolutePath
}

function Assert-ToolTree {
    param([Parameter(Mandatory)][string]$Directory)
    if (-not (Test-Path -LiteralPath $Directory)) { return }
    $pending = [Collections.Generic.Stack[string]]::new()
    $bootstrapTemp = Join-Path $Directory '.tmp'
    $pending.Push($Directory)
    while ($pending.Count) {
        foreach ($entry in Get-ChildItem -LiteralPath $pending.Pop() -Force) {
            if ($entry.Attributes -band [IO.FileAttributes]::ReparsePoint) {
                throw 'The isolated tool environment must not contain links or junctions.'
            }
            # Python 3.13 creates private temporary directories whose ACL may
            # exclude a restricted token after interrupted bootstrapping. They
            # are neither imported nor executed; check .tmp itself, but do not
            # traverse its abandoned contents during a dependency-only check.
            if ($entry.PSIsContainer -and -not [string]::Equals($entry.FullName, $bootstrapTemp, [StringComparison]::OrdinalIgnoreCase)) {
                $pending.Push($entry.FullName)
            }
        }
    }
}

function Get-PythonInfo {
    param([Parameter(Mandatory)][string]$Executable)
    $infoText = & $Executable -I -c 'import json,struct,sys; print(json.dumps({"major":sys.version_info.major,"minor":sys.version_info.minor,"bits":struct.calcsize("P")*8,"prefix":sys.prefix}))'
    if ($LASTEXITCODE -ne 0) { throw 'The selected Python interpreter could not be checked.' }
    try { $info = ($infoText -join "`n") | ConvertFrom-Json }
    catch { throw 'The selected Python interpreter returned unexpected metadata.' }
    if ($info.major -ne 3 -or $info.minor -ne 13 -or $info.bits -ne 64) {
        throw 'Select a CPython 3.13 x64 interpreter using -PythonPath.'
    }
    return $info
}

$projectRoot = Assert-LocalPath ([IO.Path]::GetFullPath((Split-Path -Parent $PSScriptRoot)))
$toolRoot = Assert-LocalPath (Join-Path $projectRoot '.tools/wechat-init-venv')
$toolPython = Assert-LocalPath (Join-Path $toolRoot 'Scripts/python.exe')
$lockPath = Assert-LocalPath (Join-Path $PSScriptRoot 'wechat_init/requirements-lock.txt')
$expectedVersion = '17.26.3'
if (-not (Test-Path -LiteralPath $lockPath -PathType Leaf)) {
    throw 'The pinned initialization dependency lock file is missing.'
}

Assert-ToolTree $toolRoot
if ($CheckOnly) {
    if (-not (Test-Path -LiteralPath $toolPython -PathType Leaf)) {
        throw 'Initialization tools are not installed. Run scripts/prepare-wechat-init.ps1 first.'
    }
} else {
    $basePython = Assert-LocalPath $PythonPath
    if (-not (Test-Path -LiteralPath $basePython -PathType Leaf)) {
        throw 'CPython 3.13 x64 was not found. Set -PythonPath to your installed interpreter.'
    }
    $null = Get-PythonInfo $basePython
    if (-not (Test-Path -LiteralPath $toolPython -PathType Leaf)) {
        if (Test-Path -LiteralPath $toolRoot) {
            if (@(Get-ChildItem -LiteralPath $toolRoot -Force).Count -ne 0) {
                throw 'The tool directory is incomplete. Inspect it before retrying; no files were removed.'
            }
        }
        $toolParent = Assert-LocalPath (Split-Path -Parent $toolRoot)
        New-Item -ItemType Directory -Path $toolParent -Force | Out-Null
        $null = Assert-LocalPath $toolRoot
        & $basePython -I -m venv $toolRoot
        if ($LASTEXITCODE -ne 0) { throw 'The isolated Python environment could not be created.' }
    }
}

$null = Assert-LocalPath $toolPython
Assert-ToolTree $toolRoot
$toolInfo = Get-PythonInfo $toolPython
if (-not [string]::Equals([IO.Path]::GetFullPath($toolInfo.prefix), $toolRoot, [StringComparison]::OrdinalIgnoreCase)) {
    throw 'The tool interpreter must belong to this project''s isolated environment.'
}

if (-not $CheckOnly) {
    $temporaryPath = Assert-LocalPath (Join-Path $toolRoot '.tmp')
    New-Item -ItemType Directory -Path $temporaryPath -Force | Out-Null
    $previousTemp = $env:TEMP
    $previousTmp = $env:TMP
    try {
        $env:TEMP = $temporaryPath
        $env:TMP = $temporaryPath
        # Recover a venv whose bundled pip bootstrap was interrupted. ensurepip
        # uses Python's bundled wheel only; it never downloads or upgrades from
        # the network, and cannot install into the global interpreter here.
        & $toolPython -I -c 'import importlib.util,sys; sys.exit(0 if importlib.util.find_spec("pip") is not None else 1)'
        if ($LASTEXITCODE -ne 0) {
            & $toolPython -I -m ensurepip --default-pip
            if ($LASTEXITCODE -ne 0) { throw 'The isolated environment''s bundled pip could not be prepared.' }
        }
        & $toolPython -I -m pip --isolated --disable-pip-version-check install --require-virtualenv --no-input --no-cache-dir --force-reinstall --require-hashes --only-binary=:all: --no-deps --index-url 'https://pypi.org/simple' --requirement $lockPath
        if ($LASTEXITCODE -ne 0) {
            throw 'Pinned dependency installation failed. Check access to official PyPI; TLS and hash checks remain enabled.'
        }
    } finally {
        $env:TEMP = $previousTemp
        $env:TMP = $previousTmp
    }
}

Assert-ToolTree $toolRoot
# Import/version checks only: do not obtain a device, enumerate processes, or
# attach to any application as part of preparing the tool environment.
$installedVersion = & $toolPython -I -c 'import frida; print(frida.__version__)'
if ($LASTEXITCODE -ne 0 -or ($installedVersion -join "`n").Trim() -ne $expectedVersion) {
    throw 'The isolated Frida installation is missing or does not match the pinned version.'
}
Write-Host "WeChat initialization tools are ready (Python 3.13 x64, Frida $expectedVersion)."

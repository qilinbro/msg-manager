#requires -Version 7.2
param([string]$OutputDirectory = '.runtime/handoff')
$ErrorActionPreference = 'Stop'
$projectRoot = [IO.Path]::GetFullPath((Split-Path -Parent $PSScriptRoot))
$rootPrefix = $projectRoot.TrimEnd('\', '/') + [IO.Path]::DirectorySeparatorChar
$outputPath = [IO.Path]::GetFullPath((Join-Path $projectRoot $OutputDirectory))
if (-not $outputPath.StartsWith($rootPrefix, [StringComparison]::OrdinalIgnoreCase)) {
    throw 'The handoff output must stay inside the project directory.'
}
$ancestor = $outputPath
while ($ancestor.StartsWith($rootPrefix, [StringComparison]::OrdinalIgnoreCase) -or $ancestor -eq $projectRoot) {
    if (Test-Path -LiteralPath $ancestor) {
        $ancestorItem = Get-Item -LiteralPath $ancestor -Force
        if (-not $ancestorItem.PSIsContainer -or ($ancestorItem.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
            throw 'The handoff output and its existing project ancestors must be ordinary directories, not links.'
        }
    }
    if ($ancestor -eq $projectRoot) { break }
    $ancestor = [IO.Path]::GetDirectoryName($ancestor)
}

$files = [Collections.Generic.SortedDictionary[string,string]]::new([StringComparer]::Ordinal)
$excludedDirectories = @('node_modules', '.npm-cache', 'dist', 'target', 'gen', '.git', '.cache', '.tools', '.runtime', 'data', 'secrets', 'credentials', 'private', 'backups', 'uploads')
$excludedExtensions = @('.log', '.tsbuildinfo', '.exe', '.zip', '.db', '.db3', '.sqlite', '.sqlite3', '.wal', '.shm', '.pem', '.key', '.pfx', '.p12', '.keystore')
$desktopIcons = @('32x32.png', '128x128.png', '128x128@2x.png', 'icon.ico', 'icon.icns')

function Add-HandoffFile([IO.FileInfo]$File) {
    if ($File.Attributes -band [IO.FileAttributes]::ReparsePoint) { return }
    $relative = [IO.Path]::GetRelativePath($projectRoot, $File.FullName).Replace('\', '/')
    if ($relative.StartsWith('../') -or [IO.Path]::IsPathRooted($relative)) { throw 'Invalid handoff source path.' }
    if ($File.Name -like '.env*' -and $File.Name -ne '.env.example') { return }
    if ($File.Extension -in $excludedExtensions -or $File.Name -match '(-wal|-shm|-journal)$') { return }
    if ($relative.StartsWith('apps/desktop/src-tauri/icons/') -and $File.Name -notin $desktopIcons) { return }
    $files[$relative] = $File.FullName
}

function Add-HandoffTree([string]$Directory) {
    foreach ($item in Get-ChildItem -LiteralPath $Directory -Force) {
        if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) { continue }
        if ($item.PSIsContainer) {
            if ($item.Name -notin $excludedDirectories) { Add-HandoffTree $item.FullName }
        } else { Add-HandoffFile $item }
    }
}

foreach ($name in @('.editorconfig', '.env.example', '.gitignore', 'go.mod', 'go.sum', 'AGENTS.md', 'README.md', '技术文档.md', '任务规划.md', 'feat.md')) {
    $path = Join-Path $projectRoot $name
    if (Test-Path -LiteralPath $path -PathType Leaf) { Add-HandoffFile (Get-Item -LiteralPath $path) }
}
foreach ($name in @('cmd', 'internal', 'tests', 'scripts', 'api', 'apps/desktop', 'docs', 'deploy', 'migrations')) {
    $path = Join-Path $projectRoot $name
    if (Test-Path -LiteralPath $path -PathType Container) { Add-HandoffTree $path }
}
$references = Join-Path $projectRoot '参考项目'
if (Test-Path -LiteralPath $references) {
    Get-ChildItem -LiteralPath $references -File | Where-Object { $_.Extension -in @('.md', '.json') } | ForEach-Object { Add-HandoffFile $_ }
    Add-HandoffTree (Join-Path $references '项目卡片')
}

if ($files.Count -eq 0) { throw 'No handoff files were found.' }
New-Item -ItemType Directory -Path $outputPath -Force | Out-Null
$createdAt = [TimeZoneInfo]::ConvertTimeBySystemTimeZoneId([DateTimeOffset]::UtcNow, 'China Standard Time')
$archivePath = Join-Path $outputPath ('T04-architecture-' + $createdAt.ToString('yyyyMMdd-HHmmss-fff') + '.zip')
$manifest = [ordered]@{
    change = 'CHG-T04-002'
    feat = 'FEAT-T04'
    task_status = 'in_progress'
    created_at = $createdAt.ToString('o')
    timezone = 'Asia/Shanghai'
    native_desktop_validated = $false
    remote_branch_created = $false
    files = @()
}
$archive = [IO.Compression.ZipFile]::Open($archivePath, [IO.Compression.ZipArchiveMode]::Create)
try {
    foreach ($pair in $files.GetEnumerator()) {
        $bytes = [IO.File]::ReadAllBytes($pair.Value)
        $sha256 = [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData($bytes)).ToLowerInvariant()
        $entry = $archive.CreateEntry($pair.Key, [IO.Compression.CompressionLevel]::Optimal)
        $stream = $entry.Open()
        try { $stream.Write($bytes, 0, $bytes.Length) } finally { $stream.Dispose() }
        $manifest.files += [ordered]@{ path = $pair.Key; bytes = $bytes.Length; sha256 = $sha256 }
    }
    $entry = $archive.CreateEntry('HANDOFF-MANIFEST.json')
    $writer = [IO.StreamWriter]::new($entry.Open(), [Text.UTF8Encoding]::new($false))
    try { $writer.Write(($manifest | ConvertTo-Json -Depth 5)) } finally { $writer.Dispose() }
}
finally { $archive.Dispose() }

$archiveHash = (Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash.ToLowerInvariant()
[IO.File]::WriteAllText($archivePath + '.sha256', $archiveHash + '  ' + [IO.Path]::GetFileName($archivePath) + "`n", [Text.UTF8Encoding]::new($false))
[pscustomobject]@{ Archive = $archivePath; Files = $files.Count; SHA256 = $archiveHash }

$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
$goCommand = Get-Command go -ErrorAction SilentlyContinue
$goBinary = if ($goCommand) { $goCommand.Source } else { Join-Path $projectRoot '.tools/go/bin/go.exe' }
if (-not (Test-Path -LiteralPath $goBinary)) {
    throw 'Go is required. Install Go 1.26+ or place the portable toolchain under .tools/go.'
}
if ([string]::IsNullOrWhiteSpace($env:SOCIALD_API_TOKEN)) {
    throw 'Set SOCIALD_API_TOKEN to a random value with at least 32 characters before starting sociald.'
}
$env:GOCACHE = Join-Path $projectRoot '.cache/go-build'
$env:GOPATH = Join-Path $projectRoot '.cache/go'
$env:GOTOOLCHAIN = 'local'
$env:CGO_ENABLED = '1'
$inboxCompiler = Get-Command gcc -ErrorAction SilentlyContinue
if (-not $inboxCompiler) { throw 'GCC is required for the encrypted SQLCipher inbox.' }
$env:CC = $inboxCompiler.Source
$env:PATH = (Split-Path -Parent $inboxCompiler.Source) + [IO.Path]::PathSeparator + $env:PATH
Push-Location $projectRoot
try {
    & $goBinary run -tags sqlite_fts5 ./cmd/sociald
    if ($LASTEXITCODE -ne 0) { throw 'sociald exited with an error.' }
}
finally { Pop-Location }

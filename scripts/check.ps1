param([switch]$SkipFrontend, [switch]$IncludeDesktop, [switch]$IncludeWechatInit)
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
$goCommand = Get-Command go -ErrorAction SilentlyContinue
$goBinary = if ($goCommand) { $goCommand.Source } else { Join-Path $projectRoot '.tools/go/bin/go.exe' }
if (-not (Test-Path -LiteralPath $goBinary)) { throw 'Go 1.26+ is required.' }
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
    $goFiles = @(Get-ChildItem -LiteralPath cmd,internal,tests -Recurse -Filter '*.go' -File -ErrorAction SilentlyContinue | ForEach-Object FullName)
    $gofmtBinary = Join-Path (Split-Path -Parent $goBinary) 'gofmt.exe'
    $unformatted = @(& $gofmtBinary -l $goFiles)
    if ($LASTEXITCODE -ne 0) { throw 'Go formatting check failed.' }
    if ($unformatted.Count -gt 0) { throw 'Go files need formatting. Run gofmt and review the changes.' }
    & $goBinary test -tags sqlite_fts5 ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go tests failed.' }
    & $goBinary vet -tags sqlite_fts5 ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go vet failed.' }
    node scripts/check-contract.mjs
    if ($LASTEXITCODE -ne 0) { throw 'API contract checks failed.' }
    if ($IsWindows) {
        & (Join-Path $PSScriptRoot 'test-powershell-bootstrap.ps1')
        & (Join-Path $PSScriptRoot 'test-inbox-launcher.ps1')
    }
    if ($IncludeWechatInit) {
        & (Join-Path $PSScriptRoot 'prepare-wechat-init.ps1') -CheckOnly
        $initPython = Join-Path $projectRoot '.tools/wechat-init-venv/Scripts/python.exe'
        & $initPython -m unittest scripts.wechat_init.test_worker
        if ($LASTEXITCODE -ne 0) { throw 'WeChat initialization synthetic tests failed.' }
        $initProbe = Join-Path $projectRoot '.runtime/wechat-init-test-probe.exe'
        & $goBinary build -tags sqlite_fts5 -o $initProbe ./cmd/wechat-init-check
        if ($LASTEXITCODE -ne 0) { throw 'WeChat configuration probe could not be built.' }
        & (Join-Path $PSScriptRoot 'test-wechat-init.ps1') -ProbeExecutable $initProbe
    }
    if (-not $SkipFrontend) {
        node scripts/test-wechat-monitor.mjs
        if ($LASTEXITCODE -ne 0) { throw 'Wechat monitor client regression checks failed.' }
        node scripts/test-inbox-import.mjs
        if ($LASTEXITCODE -ne 0) { throw 'Inbox import regression checks failed.' }
        npm.cmd --prefix apps/desktop run format:check
        if ($LASTEXITCODE -ne 0) { throw 'Frontend formatting check failed.' }
        npm.cmd --prefix apps/desktop run check
        if ($LASTEXITCODE -ne 0) { throw 'Frontend type check failed.' }
        npm.cmd --prefix apps/desktop run build
        if ($LASTEXITCODE -ne 0) { throw 'Frontend build failed.' }
    }
    if ($IncludeDesktop) {
        $cargo = Get-Command cargo -ErrorAction SilentlyContinue
        if (-not $cargo) { throw 'Rust/Cargo with Windows build prerequisites is required for desktop checks.' }
        cargo check --manifest-path apps/desktop/src-tauri/Cargo.toml
        if ($LASTEXITCODE -ne 0) { throw 'Desktop cargo check failed.' }
    }
}
finally { Pop-Location }

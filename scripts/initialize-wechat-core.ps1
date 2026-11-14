#requires -Version 7.2
param([int]$WechatProcessId = 0, [string]$CompilerPath = '', [switch]$Replace)
$ErrorActionPreference = 'Stop'
if (-not $IsWindows) { throw 'This one-time initializer requires Windows.' }
Import-Module (Join-Path $PSScriptRoot 'WechatInit.psm1') -Force
$projectRoot = [IO.Path]::GetFullPath((Split-Path -Parent $PSScriptRoot))
$privateDirectory = Join-Path $projectRoot 'data/private'
$destination = Join-Path $privateDirectory 'wechat-db.dpapi'
$python = Join-Path $projectRoot '.tools/wechat-init-venv/Scripts/python.exe'
$worker = Join-Path $PSScriptRoot 'wechat_init/worker.py'
$probe = Join-Path $projectRoot '.runtime/wechat-init/wechat-init-check.exe'
$capturePath = $null
$secret = $null
$captureProcess = $null
try {
    $null = Assert-WechatInitPath $destination
    if ((Test-Path -LiteralPath $destination) -and -not $Replace) { throw 'Configuration exists. Use -Replace only when you intend to replace it.' }
    if ((Test-Path -LiteralPath $destination) -and (Get-Item -LiteralPath $destination -Force).PSIsContainer) { throw 'The configuration destination must be a file.' }
    if (-not (Test-Path -LiteralPath $python -PathType Leaf)) { throw 'Run scripts/prepare-wechat-init.ps1 first to prepare the isolated Python dependency.' }
    & (Join-Path $PSScriptRoot 'prepare-wechat-init.ps1') -CheckOnly
    $null = Assert-WechatInitPath $python
    $null = Assert-WechatInitPath $worker
    Write-Host '一次性初始化：仅支持你自己的 Windows 微信 4.1.15.56 账号。'
    Write-Host '将在你重新登录时，使用 Frida 临时挂钩一个选定的微信进程，取得本机数据库密钥。'
    Write-Host '由你自行操作微信登录；工具不会发送消息或保存原始内存材料。'
    $directory = Read-Host '请填写本人账号的 db_storage 完整路径'
    $account = Read-Host '请填写原始 wxid（账号目录若带四位后缀，请去掉该后缀）'
    $selection = Get-WechatInitSelection -Directory $directory -AccountID $account
    if ($WechatProcessId -le 0) {
        $sessionCandidates = @(Get-Process -Name Weixin -ErrorAction SilentlyContinue | Where-Object { $_.SessionId -eq [Diagnostics.Process]::GetCurrentProcess().SessionId })
        $currentUserSID = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
        $candidates = @(foreach ($candidate in $sessionCandidates) {
            try {
                $candidateOwner = Get-CimInstance Win32_Process -Filter ('ProcessId = ' + $candidate.Id) | Invoke-CimMethod -MethodName GetOwnerSid
                if ($candidateOwner.ReturnValue -eq 0 -and $candidateOwner.Sid -ceq $currentUserSID) { $candidate }
            } catch { }
        })
        $visible = @($candidates | Where-Object { $_.MainWindowHandle -ne [IntPtr]::Zero })
        if ($visible.Count) { $candidates = $visible }
        if (-not $candidates.Count) { throw '请先打开自己的微信，再重新运行初始化。' }
        if ($candidates.Count -gt 1) {
            Write-Host '发现多个微信窗口，请选择要初始化的那个：'
            for ($index = 0; $index -lt $candidates.Count; $index++) {
                $title = $candidates[$index].MainWindowTitle
                if (-not $title) { $title = '后台微信进程' }
                if ($title.Length -gt 80) { $title = $title.Substring(0,80) }
                Write-Host ('{0}. {1}（进程 {2}）' -f ($index+1),$title,$candidates[$index].Id)
            }
            $choice = [int](Read-Host '请输入上面的编号')
            if ($choice -lt 1 -or $choice -gt $candidates.Count) { throw '未选择有效的微信窗口。' }
            $WechatProcessId = $candidates[$choice-1].Id
        } else { $WechatProcessId = $candidates[0].Id }
    }
    if ($WechatProcessId -le 0) { throw 'Choose one valid WeChat process.' }
    $wechat = Get-Process -Id $WechatProcessId -ErrorAction Stop
    if ($wechat.ProcessName -cne 'Weixin' -or $wechat.SessionId -ne [Diagnostics.Process]::GetCurrentProcess().SessionId) { throw 'The process is not WeChat in your current desktop session.' }
    $owner = Get-CimInstance Win32_Process -Filter ('ProcessId = ' + $WechatProcessId) | Invoke-CimMethod -MethodName GetOwnerSid
    if ($owner.ReturnValue -ne 0 -or $owner.Sid -cne [Security.Principal.WindowsIdentity]::GetCurrent().User.Value) { throw 'Choose a WeChat process owned by your Windows account.' }
    $dlls = @($wechat.Modules | Where-Object { $_.ModuleName -ieq 'Weixin.dll' })
    if ($dlls.Count -ne 1) { throw 'Cannot verify the selected WeChat module.' }
    $dll = Assert-WechatInitPath $dlls[0].FileName
    $version = $dlls[0].FileVersionInfo
    $versionText = '{0}.{1}.{2}.{3}' -f $version.FileMajorPart,$version.FileMinorPart,$version.FileBuildPart,$version.FilePrivatePart
    if ($versionText -cne '4.1.15.56') { throw 'This initializer supports exactly WeChat 4.1.15.56. No fallback hook will be attempted.' }
    $created = $wechat.StartTime.ToUniversalTime().ToFileTimeUtc().ToString([Globalization.CultureInfo]::InvariantCulture)
    Write-Host ('所选账号：' + $account + '；微信版本：' + $versionText + '；进程：' + $WechatProcessId)
    Write-Host '请保持该微信程序运行，等出现“准备就绪”后，再退出登录并重新登录同一个账号。'
    if ((Read-Host ('确认临时挂钩该进程，请输入 INIT ' + $account)) -cne ('INIT ' + $account)) { throw '已取消；尚未挂钩微信进程。' }
    $null = Initialize-WechatPrivateDirectory $privateDirectory
    $capturePath = Join-Path $privateDirectory ('wechat-init-' + [guid]::NewGuid().ToString('N') + '.json')
    $null = Assert-WechatInitPath $probe
    $null = Initialize-WechatPrivateDirectory (Split-Path -Parent $probe)
    $goCommand = Get-Command go -ErrorAction SilentlyContinue
    $goBinary = if ($goCommand) { $goCommand.Source } else { Join-Path $projectRoot '.tools/go/bin/go.exe' }
    $compiler = Get-Command gcc -CommandType Application -ErrorAction SilentlyContinue
    if ($CompilerPath) {
        $compilerExecutable = Assert-WechatInitPath $CompilerPath
    } elseif ($compiler) {
        $compilerExecutable = $compiler.Source
    } else {
        $knownCompilers = @((Join-Path $projectRoot '.tools/gcc/bin/gcc.exe'),'C:\msys64\ucrt64\bin\gcc.exe','D:\msys2\ucrt64\bin\gcc.exe')
        $compilerExecutable = $knownCompilers | Where-Object { Test-Path -LiteralPath $_ -PathType Leaf } | Select-Object -First 1
    }
    if (-not (Test-Path -LiteralPath $goBinary) -or -not $compilerExecutable -or -not (Test-Path -LiteralPath $compilerExecutable -PathType Leaf)) { throw 'Go 1.26+ and GCC are required. Set -CompilerPath to your existing gcc.exe.' }
    $null = Assert-WechatInitPath $compilerExecutable
    $oldGoCache,$oldGoPath,$oldCgo,$oldCC,$oldPath = $env:GOCACHE,$env:GOPATH,$env:CGO_ENABLED,$env:CC,$env:PATH
    try {
        $env:GOCACHE = Join-Path $projectRoot '.cache/go-build'
        $env:GOPATH = Join-Path $projectRoot '.cache/go'
        $env:CGO_ENABLED = '1'
        $env:CC = $compilerExecutable
        $env:PATH = (Split-Path -Parent $compilerExecutable) + [IO.Path]::PathSeparator + $env:PATH
        Push-Location $projectRoot
        try { & $goBinary build -tags sqlite_fts5 -o $probe ./cmd/wechat-init-check; if ($LASTEXITCODE -ne 0) { throw 'Local database verification tool could not be built.' } }
        finally { Pop-Location }
    } finally { $env:GOCACHE,$env:GOPATH,$env:CGO_ENABLED,$env:CC,$env:PATH = $oldGoCache,$oldGoPath,$oldCgo,$oldCC,$oldPath }
    $startInfo = [Diagnostics.ProcessStartInfo]::new()
    $startInfo.FileName = $python
    $startInfo.UseShellExecute = $false
    $startInfo.CreateNoWindow = $true
    $startInfo.WindowStyle = [Diagnostics.ProcessWindowStyle]::Hidden
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    $startInfo.StandardOutputEncoding = [Text.Encoding]::UTF8
    $startInfo.StandardErrorEncoding = [Text.Encoding]::UTF8
    foreach ($argument in @('-I',$worker,'--pid',[string]$WechatProcessId,'--created-filetime',$created,'--dll',$dll,'--database',$selection.database,'--account-id',$account,'--consent-account',$account,'--output',$capturePath,'--timeout','240')) { $startInfo.ArgumentList.Add($argument) }
    $captureProcess = [Diagnostics.Process]::Start($startInfo)
    $errors = $captureProcess.StandardError.ReadToEndAsync()
    $captureTimer = [Diagnostics.Stopwatch]::StartNew()
    while ($true) {
        $nextLine = $captureProcess.StandardOutput.ReadLineAsync()
        while (-not $nextLine.Wait(500)) {
            if ($captureTimer.Elapsed.TotalSeconds -gt 300) { throw 'Initialization timed out; the previous configuration was kept.' }
        }
        if ($captureTimer.Elapsed.TotalSeconds -gt 300) { throw 'Initialization timed out; the previous configuration was kept.' }
        $line = $nextLine.GetAwaiter().GetResult()
        if ($null -eq $line) { break }
        if ($line.Length -gt 4096) { throw 'Invalid initialization response.' }
        try { $event = $line | ConvertFrom-Json } catch { throw 'Invalid initialization response.' }
        switch -CaseSensitive ($event.event) {
            'ready' { Write-Host '准备就绪。现在请在微信里退出登录，再重新登录同一个账号；不要完全退出微信程序。' }
            'waiting' { Write-Host '等待读取所选账号的本机数据库密钥……' }
            'saved' { Write-Host '密钥已在本机加密，正在校验你选定的数据库……' }
            'error' { throw '初始化未完成，旧配置已保留。请核对同一微信窗口与账号，再重新发起。' }
            default { throw 'Invalid initialization response.' }
        }
    }
    if (-not $captureProcess.WaitForExit(5000)) { throw 'Initialization did not exit as expected.' }
    $null = $errors.GetAwaiter().GetResult()
    if ($captureProcess.ExitCode -ne 0) { throw 'Initialization failed; your previous configuration was kept.' }
    $secret = Read-WechatInitSecret -Path $capturePath -AccountID $account
    Invoke-WechatInitProbe -Executable $probe -Selection $selection -Secret $secret
    Save-WechatInitConfiguration -Destination $destination -Selection $selection -Secret $secret -Replace:$Replace
    Write-Host '初始化完成，加密配置已保存。请重启 scripts/start-inbox.ps1，再到消息页手动加载微信私聊。'
} finally {
    if ($null -ne $captureProcess) {
        if (-not $captureProcess.HasExited) { $captureProcess.Kill($true); $captureProcess.WaitForExit() }
        $captureProcess.Dispose()
    }
    if ($null -ne $secret) { $secret.Dispose() }
    if ($capturePath -and (Test-Path -LiteralPath $capturePath -PathType Leaf)) { Remove-Item -LiteralPath $capturePath }
}

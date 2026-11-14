#requires -Version 7.2
# Synthetic launcher regression checks. No real inbox or WeChat files are used.
param([switch]$RunChild, [string]$FixtureRoot = '')
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
if (-not $IsWindows) { throw 'Inbox launcher validation requires Windows DPAPI.' }
$projectRoot = [IO.Path]::GetFullPath((Split-Path -Parent $PSScriptRoot))
$testParent = Join-Path $projectRoot '.runtime'

function Assert-LauncherTest([bool]$Condition, [string]$Message) {
    if (-not $Condition) { throw $Message }
}

function Assert-FixtureRoot([string]$Path) {
    $absolute = [IO.Path]::GetFullPath($Path)
    if ([IO.Path]::GetDirectoryName($absolute) -cne $testParent -or [IO.Path]::GetFileName($absolute) -notmatch '^inbox-launcher-test-[a-f0-9]{32}$') { throw 'Unsafe synthetic fixture path.' }
    return $absolute
}

if (-not $RunChild) {
    $testRoot = Assert-FixtureRoot (Join-Path $testParent ('inbox-launcher-test-' + [guid]::NewGuid().ToString('N')))
    $process = $null
    try {
        New-Item -ItemType Directory -Path $testRoot -Force | Out-Null
        $startInfo = [Diagnostics.ProcessStartInfo]::new()
        $startInfo.FileName = [Diagnostics.Process]::GetCurrentProcess().MainModule.FileName
        $startInfo.UseShellExecute = $false
        $startInfo.CreateNoWindow = $true
        $startInfo.WindowStyle = [Diagnostics.ProcessWindowStyle]::Hidden
        $startInfo.RedirectStandardOutput = $true
        $startInfo.RedirectStandardError = $true
        foreach ($argument in @('-NoLogo','-NoProfile','-NonInteractive','-File',$PSCommandPath,'-RunChild','-FixtureRoot',$testRoot)) { $startInfo.ArgumentList.Add($argument) }
        $process = [Diagnostics.Process]::Start($startInfo)
        $stdoutTask = $process.StandardOutput.ReadToEndAsync()
        $stderrTask = $process.StandardError.ReadToEndAsync()
        if (-not $process.WaitForExit(45000)) { throw 'Synthetic inbox launcher tests timed out.' }
        $stdout = $stdoutTask.GetAwaiter().GetResult()
        $stderr = $stderrTask.GetAwaiter().GetResult()
        if ($process.ExitCode -ne 0) { throw ('Synthetic launcher validation failed. ' + $stderr) }
        if ($stdout -notmatch 'Inbox launcher synthetic tests passed: 6 cases') { throw 'Synthetic launcher validation did not report completion.' }
        Write-Output 'Inbox launcher synthetic tests passed: 6 cases; DACL-only setup, idempotence, DPAPI loading, existing-key preservation and startup rejection. No real account data or service was accessed.'
    } finally {
        if ($null -ne $process) {
            if (-not $process.HasExited) { $process.Kill($true); $null = $process.WaitForExit(5000) }
            $process.Dispose()
        }
        $resolvedRoot = Assert-FixtureRoot $testRoot
        if (Test-Path -LiteralPath $resolvedRoot) { Remove-Item -LiteralPath $resolvedRoot -Recurse -Force }
    }
    exit 0
}

$FixtureRoot = Assert-FixtureRoot $FixtureRoot
if (-not (Test-Path -LiteralPath $FixtureRoot -PathType Container)) { throw 'Synthetic fixture root is missing.' }
# Keep all stubs and environment changes inside this isolated child host.
foreach ($name in @('WECHAT_DB_DIR','WECHAT_DB_ACCOUNT_ID','WECHAT_DB_KEY','WECHAT_DB_LIMIT','SOCIALD_DATABASE_KEY','SOCIALD_DATABASE_PATH')) { Remove-Item -LiteralPath "Env:$name" -ErrorAction SilentlyContinue }
$env:SOCIALD_API_TOKEN = 'synthetic-launcher-token'
$global:InboxLauncherTestState = @{ key_path = ''; key_writes = 0; clipboard_calls = 0; set_acl_calls = 0 }
$currentSID = [Security.Principal.WindowsIdentity]::GetCurrent().User
$sections = [Security.AccessControl.AccessControlSections]::Access -bor [Security.AccessControl.AccessControlSections]::Owner -bor [Security.AccessControl.AccessControlSections]::Group

function Assert-PrivateFixtureDirectory([string]$Directory) {
    $acl = [IO.FileSystemAclExtensions]::GetAccessControl([IO.DirectoryInfo]::new($Directory), $sections)
    Assert-LauncherTest $acl.AreAccessRulesProtected 'The key directory still inherits access rules.'
    Assert-LauncherTest ($acl.GetOwner([Security.Principal.SecurityIdentifier]).Value -ceq $currentSID.Value) 'The key directory owner changed.'
    $rules = @($acl.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier]))
    Assert-LauncherTest ($rules.Count -gt 0) 'The key directory has no access rules.'
    $hasFullControl = $false
    foreach ($rule in $rules) {
        Assert-LauncherTest ($rule.IdentityReference.Value -ceq $currentSID.Value -and $rule.AccessControlType -eq [Security.AccessControl.AccessControlType]::Allow -and -not $rule.IsInherited) 'Another identity retained access to the key directory.'
        if (($rule.FileSystemRights -band [Security.AccessControl.FileSystemRights]::FullControl) -eq [Security.AccessControl.FileSystemRights]::FullControl -and ($rule.InheritanceFlags -band [Security.AccessControl.InheritanceFlags]::ContainerInherit) -and ($rule.InheritanceFlags -band [Security.AccessControl.InheritanceFlags]::ObjectInherit) -and $rule.PropagationFlags -eq [Security.AccessControl.PropagationFlags]::None) { $hasFullControl = $true }
    }
    Assert-LauncherTest $hasFullControl 'The current user lacks inheritable full control of the key directory.'
}

function global:Set-Acl {
    $global:InboxLauncherTestState.set_acl_calls++
    throw 'The launcher must not call the privilege-sensitive Set-Acl provider.'
}

function global:Set-Clipboard {
    param([Parameter(ValueFromPipeline)][object]$Value)
    process { $global:InboxLauncherTestState.clipboard_calls++ }
}

function global:Set-Content {
    [CmdletBinding()]
    param([Parameter(ValueFromPipeline)][object]$Value, [string]$LiteralPath, [string]$Encoding = 'utf8', [switch]$NoNewline)
    process {
        Assert-LauncherTest ($LiteralPath -ceq $global:InboxLauncherTestState.key_path) 'An unexpected file write reached the launcher stub.'
        Assert-PrivateFixtureDirectory (Split-Path -Parent $LiteralPath)
        $global:InboxLauncherTestState.key_writes++
        Microsoft.PowerShell.Management\Set-Content -LiteralPath $LiteralPath -Value $Value -Encoding $Encoding -NoNewline:$NoNewline
    }
}

function Save-SyntheticProtectedText([string]$Path, [string]$Text) {
    $secure = ConvertTo-SecureString -String $Text -AsPlainText -Force
    try { [IO.File]::WriteAllText($Path, (ConvertFrom-SecureString -SecureString $secure), [Text.UTF8Encoding]::new($false)) }
    finally { $secure.Dispose() }
}

function New-LauncherFixture([string]$Name) {
    $root = Join-Path $FixtureRoot $Name
    $scripts = Join-Path $root 'scripts'
    New-Item -ItemType Directory -Path $scripts -Force | Out-Null
    foreach ($name in @('start-inbox-core.ps1','WechatInit.psm1')) { Copy-Item -LiteralPath (Join-Path $PSScriptRoot $name) -Destination (Join-Path $scripts $name) }
    $stub = @'
$root = Split-Path -Parent $PSScriptRoot
if ($env:SOCIALD_DATABASE_PATH -cne (Join-Path $root 'data/messages.db') -or $env:SOCIALD_DATABASE_KEY -cnotmatch '^[A-Fa-f0-9]{64}$') { throw 'Synthetic backend did not receive the inbox configuration.' }
if (Test-Path -LiteralPath (Join-Path $root 'expect-wechat')) {
    if ($env:WECHAT_DB_ACCOUNT_ID -cne 'wxid_synthetic_launcher' -or $env:WECHAT_DB_KEY -cne ('22' * 32) -or $env:WECHAT_DB_DIR -cne (Join-Path $root 'synthetic-wechat/db_storage') -or $env:WECHAT_DB_LIMIT -cne '100') { throw 'Synthetic backend did not receive the protected WeChat configuration.' }
}
[IO.File]::WriteAllText((Join-Path $root 'backend-reached'), 'synthetic')
'@
    [IO.File]::WriteAllText((Join-Path $scripts 'dev-backend.ps1'), $stub, [Text.UTF8Encoding]::new($false))
    $global:InboxLauncherTestState.key_path = Join-Path $root 'data/private/inbox-key.dpapi'
    $global:InboxLauncherTestState.key_writes = 0
    return $root
}

function Invoke-LauncherFixture([string]$Root, [switch]$Reject) {
    $rejected = $false
    try { & (Join-Path $Root 'scripts/start-inbox-core.ps1') | Out-Null }
    catch { if (-not $Reject) { throw }; $rejected = $true }
    Assert-LauncherTest ($rejected -eq [bool]$Reject) 'Launcher acceptance did not match the expected result.'
    Assert-LauncherTest ((Test-Path -LiteralPath (Join-Path $Root 'backend-reached')) -ne [bool]$Reject) 'An invalid startup reached the synthetic backend, or valid startup did not.'
    Assert-LauncherTest (-not $env:SOCIALD_DATABASE_KEY) 'The launcher left the database key in its child environment.'
    foreach ($name in @('WECHAT_DB_DIR','WECHAT_DB_ACCOUNT_ID','WECHAT_DB_KEY','WECHAT_DB_LIMIT')) { Assert-LauncherTest (-not [Environment]::GetEnvironmentVariable($name)) 'The launcher left loaded WeChat configuration in its child environment.' }
}

# A new private directory must be secured before the first protected-key write.
$root = New-LauncherFixture 'new-private-directory'
Invoke-LauncherFixture $root
Assert-LauncherTest ($global:InboxLauncherTestState.key_writes -eq 1) 'The new inbox key was not written once.'
Assert-PrivateFixtureDirectory (Join-Path $root 'data/private')

# The user's reported flow: the directory/config already exists after initialization.
$root = New-LauncherFixture 'existing-private-configuration'
Import-Module (Join-Path $root 'scripts/WechatInit.psm1') -Force
$private = Initialize-WechatPrivateDirectory (Join-Path $root 'data/private')
$keyPath = Join-Path $private 'inbox-key.dpapi'
Save-SyntheticProtectedText $keyPath ('11' * 32)
$configuration = @{ directory = (Join-Path $root 'synthetic-wechat/db_storage'); account_id = 'wxid_synthetic_launcher'; key = ('22' * 32); limit = 100 } | ConvertTo-Json -Compress
$configurationPath = Join-Path $private 'wechat-db.dpapi'
Save-SyntheticProtectedText $configurationPath $configuration
[IO.File]::WriteAllText((Join-Path $root 'expect-wechat'), 'synthetic')
$keyHash = (Get-FileHash -LiteralPath $keyPath -Algorithm SHA256).Hash
$configurationHash = (Get-FileHash -LiteralPath $configurationPath -Algorithm SHA256).Hash
$beforeACL = [IO.FileSystemAclExtensions]::GetAccessControl([IO.DirectoryInfo]::new($private), $sections).GetSecurityDescriptorSddlForm($sections)
Invoke-LauncherFixture $root
Assert-LauncherTest ($global:InboxLauncherTestState.key_writes -eq 0 -and (Get-FileHash -LiteralPath $keyPath -Algorithm SHA256).Hash -ceq $keyHash) 'An existing inbox key was replaced.'
Assert-LauncherTest ((Get-FileHash -LiteralPath $configurationPath -Algorithm SHA256).Hash -ceq $configurationHash) 'An existing WeChat configuration was replaced.'
$afterACL = [IO.FileSystemAclExtensions]::GetAccessControl([IO.DirectoryInfo]::new($private), $sections).GetSecurityDescriptorSddlForm($sections)
Assert-LauncherTest ($beforeACL -ceq $afterACL) 'An already-private directory was changed.'
Assert-PrivateFixtureDirectory $private

# Explicit access for another identity must be removed, retaining the current owner.
$root = New-LauncherFixture 'explicit-other-user-rule'
$private = Join-Path $root 'data/private'
New-Item -ItemType Directory -Path $private -Force | Out-Null
$unsafe = [Security.AccessControl.DirectorySecurity]::new()
$unsafe.SetAccessRuleProtection($true, $false)
$unsafe.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($currentSID, 'FullControl', 'ContainerInherit,ObjectInherit', 'None', 'Allow'))
$unsafe.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new([Security.Principal.SecurityIdentifier]::new('S-1-5-32-545'), 'ReadAndExecute', 'ContainerInherit,ObjectInherit', 'None', 'Allow'))
[IO.FileSystemAclExtensions]::SetAccessControl([IO.DirectoryInfo]::new($private), $unsafe)
$identitySections = [Security.AccessControl.AccessControlSections]::Owner -bor [Security.AccessControl.AccessControlSections]::Group
$beforeIdentity = [IO.FileSystemAclExtensions]::GetAccessControl([IO.DirectoryInfo]::new($private), $identitySections).GetSecurityDescriptorSddlForm($identitySections)
Invoke-LauncherFixture $root
Assert-PrivateFixtureDirectory $private
$afterIdentity = [IO.FileSystemAclExtensions]::GetAccessControl([IO.DirectoryInfo]::new($private), $identitySections).GetSecurityDescriptorSddlForm($identitySections)
Assert-LauncherTest ($beforeIdentity -ceq $afterIdentity) 'Directory hardening changed its owner or primary group.'
Assert-LauncherTest ($global:InboxLauncherTestState.key_writes -eq 1) 'The repaired private directory did not reach the first key write.'

# Never invent a new key for an existing product database.
$root = New-LauncherFixture 'existing-database-without-key'
New-Item -ItemType Directory -Path (Join-Path $root 'data') -Force | Out-Null
[IO.File]::WriteAllText((Join-Path $root 'data/messages.db'), 'synthetic-database-marker')
Invoke-LauncherFixture $root -Reject
Assert-LauncherTest ($global:InboxLauncherTestState.key_writes -eq 0 -and -not (Test-Path -LiteralPath $global:InboxLauncherTestState.key_path)) 'Startup generated a replacement key for an existing database.'

# A damaged saved config must not start the backend or replace either key file.
$root = New-LauncherFixture 'invalid-protected-configuration'
Import-Module (Join-Path $root 'scripts/WechatInit.psm1') -Force
$private = Initialize-WechatPrivateDirectory (Join-Path $root 'data/private')
$keyPath = Join-Path $private 'inbox-key.dpapi'
Save-SyntheticProtectedText $keyPath ('11' * 32)
$keyHash = (Get-FileHash -LiteralPath $keyPath -Algorithm SHA256).Hash
$configurationPath = Join-Path $private 'wechat-db.dpapi'
[IO.File]::WriteAllText($configurationPath, 'synthetic-invalid-dpapi')
Invoke-LauncherFixture $root -Reject
Assert-LauncherTest ((Get-FileHash -LiteralPath $keyPath -Algorithm SHA256).Hash -ceq $keyHash -and [IO.File]::ReadAllText($configurationPath) -ceq 'synthetic-invalid-dpapi') 'Rejected startup changed existing protected files.'

# Invalid private-directory targets must fail before writing or launching anything.
$root = New-LauncherFixture 'private-path-is-file'
New-Item -ItemType Directory -Path (Join-Path $root 'data') -Force | Out-Null
[IO.File]::WriteAllText((Join-Path $root 'data/private'), 'synthetic-path-marker')
Invoke-LauncherFixture $root -Reject
Assert-LauncherTest ($global:InboxLauncherTestState.key_writes -eq 0) 'A rejected private path reached key creation.'
Assert-LauncherTest ($global:InboxLauncherTestState.set_acl_calls -eq 0) 'The privilege-sensitive Set-Acl provider was used.'
Write-Output 'Inbox launcher synthetic tests passed: 6 cases'

#requires -Version 7.2
param([string]$ProbeExecutable = '')
$ErrorActionPreference = 'Stop'
Import-Module (Join-Path $PSScriptRoot 'WechatInit.psm1') -Force
if (-not $IsWindows) { throw 'DPAPI validation requires Windows.' }
$projectRoot = [IO.Path]::GetFullPath((Split-Path -Parent $PSScriptRoot))
$testParent = Join-Path $projectRoot '.runtime'
$testRoot = Join-Path $testParent ('wechat-init-test-' + [guid]::NewGuid().ToString('N'))
$testSecret = $null
$protectedSecret = $null
$keyBytes = [byte[]](0..31)
function Assert-Rejected([scriptblock]$Action) {
    $rejected = $false
    try { & $Action | Out-Null } catch { $rejected = $true; if ($_.Exception.Message.Contains([Convert]::ToHexString($keyBytes))) { throw 'A test failure exposed secret material.' } }
    if (-not $rejected) { throw 'Invalid initialization input was accepted.' }
}
try {
    New-Item -ItemType Directory -Path $testRoot | Out-Null
    $account = 'wxid_synthetic_init'
    $directory = Join-Path $testRoot ($account + '_a1B2/db_storage')
    $sessionDirectory = Join-Path $directory 'session'
    New-Item -ItemType Directory -Path $sessionDirectory -Force | Out-Null
    [IO.File]::WriteAllBytes((Join-Path $sessionDirectory 'session.db'), [byte[]]::new(16))
    $selection = Get-WechatInitSelection -Directory $directory -AccountID $account
    Assert-Rejected { Get-WechatInitSelection -Directory $directory -AccountID 'wxid_other' }
    Assert-Rejected { Get-WechatInitSelection -Directory $directory -AccountID $account.ToUpperInvariant() }
    Assert-Rejected { Get-WechatInitSelection -Directory $directory -AccountID 'filehelper' }
    Assert-Rejected { Assert-WechatInitPath '\\remote\share\db_storage' }
    Assert-Rejected { Assert-WechatInitPath ($directory + ':alternate') }
    $private = Initialize-WechatPrivateDirectory (Join-Path $testRoot 'private')
    $unsafeDirectory = Join-Path $testRoot 'unsafe-private'
    New-Item -ItemType Directory -Path $unsafeDirectory | Out-Null
    $unsafeDirectoryInfo = [IO.DirectoryInfo]::new($unsafeDirectory)
    $unsafeACL = [IO.FileSystemAclExtensions]::GetAccessControl($unsafeDirectoryInfo, [Security.AccessControl.AccessControlSections]::Access)
    $unsafeACL.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new([Security.Principal.SecurityIdentifier]::new('S-1-5-32-545'), 'ReadAndExecute', 'ContainerInherit,ObjectInherit', 'None', 'Allow'))
    [IO.FileSystemAclExtensions]::SetAccessControl($unsafeDirectoryInfo, $unsafeACL)
    $null = Initialize-WechatPrivateDirectory $unsafeDirectory
    $currentSID = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    $securedACL = Get-Acl -LiteralPath $unsafeDirectory
    foreach ($rule in $securedACL.GetAccessRules($true,$true,[Security.Principal.SecurityIdentifier])) {
        if ($rule.IdentityReference.Value -cne $currentSID -or $rule.AccessControlType -ne [Security.AccessControl.AccessControlType]::Allow) { throw 'Explicit access for another user survived private-directory initialization.' }
    }
    Add-Type -AssemblyName System.Security.Cryptography.ProtectedData
    $cipher = [Security.Cryptography.ProtectedData]::Protect($keyBytes, [Text.Encoding]::UTF8.GetBytes('msg-manager/wechat-init/v1'), [Security.Cryptography.DataProtectionScope]::CurrentUser)
    $capture = Join-Path $private 'synthetic-capture.json'
    $payload = @{format=1;account_id=$account;client_version='4.1.15.56';protected_key=[Convert]::ToBase64String($cipher)}
    $payload | ConvertTo-Json -Compress | Set-Content -LiteralPath $capture -Encoding UTF8 -NoNewline
    $testSecret = Read-WechatInitSecret -Path $capture -AccountID $account
    if ($testSecret -isnot [Security.SecureString]) { throw 'Capture secret is not protected in the PowerShell pipeline.' }
    Assert-Rejected { Read-WechatInitSecret -Path $capture -AccountID 'wxid_other' }
    $python = Join-Path $projectRoot '.tools/wechat-init-venv/Scripts/python.exe'
    if (-not (Test-Path -LiteralPath $python -PathType Leaf)) { throw 'Prepare the isolated initialization dependency before cross-language checks.' }
    $crossCheck = @'
import base64,json,pathlib,sys
from scripts.wechat_init.native import Native
native=Native()
try:
    directory=pathlib.Path(sys.argv[1])
    native._private_directory(directory)
    ciphertext=native.protect(bytes(range(32)))
    payload={"format":1,"account_id":"wxid_synthetic_init","client_version":"4.1.15.56","protected_key":base64.b64encode(ciphertext).decode("ascii")}
    pathlib.Path(sys.argv[2]).write_text(json.dumps(payload),encoding="utf8")
finally:
    native.close()
'@
    Push-Location $projectRoot
    try { & $python -c $crossCheck $private $capture; if ($LASTEXITCODE -ne 0) { throw 'Python/.NET private-directory and DPAPI interop failed.' } }
    finally { Pop-Location }
    $crossSecret = Read-WechatInitSecret -Path $capture -AccountID $account
    $crossPointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($crossSecret)
    try { if ([Runtime.InteropServices.Marshal]::PtrToStringBSTR($crossPointer) -cne [Convert]::ToHexString($keyBytes)) { throw 'Python DPAPI ciphertext did not match the synthetic .NET key.' } }
    finally { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($crossPointer); $crossSecret.Dispose() }
    $payload.client_version = '4.1.15.57'
    $payload | ConvertTo-Json -Compress | Set-Content -LiteralPath $capture -Encoding UTF8 -NoNewline
    Assert-Rejected { Read-WechatInitSecret -Path $capture -AccountID $account }
    $payload.client_version = '4.1.15.56'
    $payload.protected_key = 'invalid-base64!'
    $payload | ConvertTo-Json -Compress | Set-Content -LiteralPath $capture -Encoding UTF8 -NoNewline
    Assert-Rejected { Read-WechatInitSecret -Path $capture -AccountID $account }
    $wrongEntropy = [Security.Cryptography.ProtectedData]::Protect($keyBytes,[Text.Encoding]::UTF8.GetBytes('wrong-entropy'),[Security.Cryptography.DataProtectionScope]::CurrentUser)
    $payload.protected_key = [Convert]::ToBase64String($wrongEntropy)
    $payload | ConvertTo-Json -Compress | Set-Content -LiteralPath $capture -Encoding UTF8 -NoNewline
    Assert-Rejected { Read-WechatInitSecret -Path $capture -AccountID $account }
    $duplicate = '{"format":1,"format":1,"account_id":"wxid_synthetic_init","client_version":"4.1.15.56","protected_key":"invalid"}'
    $duplicate | Set-Content -LiteralPath $capture -Encoding UTF8 -NoNewline
    Assert-Rejected { Read-WechatInitSecret -Path $capture -AccountID $account }
    $destination = Join-Path $private 'wechat-db.dpapi'
    Save-WechatInitConfiguration -Destination $destination -Selection $selection -Secret $testSecret
    $before = (Get-FileHash -LiteralPath $destination -Algorithm SHA256).Hash
    Assert-Rejected { Save-WechatInitConfiguration -Destination $destination -Selection $selection -Secret $testSecret }
    if ((Get-FileHash -LiteralPath $destination -Algorithm SHA256).Hash -cne $before) { throw 'Default configuration write replaced an existing file.' }
    if ($ProbeExecutable) {
        Assert-Rejected { Invoke-WechatInitProbe -Executable $ProbeExecutable -Selection $selection -Secret $testSecret }
        if ((Get-FileHash -LiteralPath $destination -Algorithm SHA256).Hash -cne $before) { throw 'Failed database verification changed existing configuration.' }
    }
    $protectedSecret = Get-Content -Raw -LiteralPath $destination | ConvertTo-SecureString
    $pointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($protectedSecret)
    try {
        $decoded = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($pointer) | ConvertFrom-Json
        if ($decoded.key -cne [Convert]::ToHexString($keyBytes) -or $decoded.account_id -cne $account -or $decoded.directory -cne $directory) { throw 'Saved configuration did not survive DPAPI roundtrip.' }
    } finally { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($pointer); Remove-Variable decoded }
    $folderTarget = Join-Path $private 'folder.dpapi'
    New-Item -ItemType Directory -Path $folderTarget | Out-Null
    Assert-Rejected { Save-WechatInitConfiguration -Destination $folderTarget -Selection $selection -Secret $testSecret -Replace }
    foreach ($file in @('WechatInit.psm1','initialize-wechat.ps1','configure-wechat.ps1','start-inbox.ps1','test-wechat-init.ps1')) {
        $tokens = $null; $parseErrors = $null
        [Management.Automation.Language.Parser]::ParseFile((Join-Path $PSScriptRoot $file),[ref]$tokens,[ref]$parseErrors) | Out-Null
        if ($parseErrors.Count) { throw 'PowerShell parse validation failed.' }
    }
    Write-Output 'WeChat initialization synthetic DPAPI, account/path, overwrite protection and PowerShell syntax checks passed. No client process was accessed.'
} finally {
    if ($null -ne $testSecret) { $testSecret.Dispose() }
    if ($null -ne $protectedSecret) { $protectedSecret.Dispose() }
    [Array]::Clear($keyBytes, 0, $keyBytes.Length)
    $resolvedRoot = [IO.Path]::GetFullPath($testRoot)
    if ([IO.Path]::GetDirectoryName($resolvedRoot) -cne $testParent -or -not [IO.Path]::GetFileName($resolvedRoot).StartsWith('wechat-init-test-')) { throw 'Test cleanup path rejected.' }
    if (Test-Path -LiteralPath $resolvedRoot) { Remove-Item -LiteralPath $resolvedRoot -Recurse -Force }
}

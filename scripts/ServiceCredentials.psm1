#requires -Version 7.2
# Native DPAPI service API credentials, separate from inbox/WeChat key formats.
Set-StrictMode -Version Latest

$script:ServiceCredentialPurpose = [Text.Encoding]::UTF8.GetBytes('msg-manager/service-credential/v1')
$script:ServiceCredentialMaximum = 32768

# Handle-based checks prevent following reparse points or replacing ancestors
# between a path check and a read/publication. No owner, DACL or SACL of an
# existing object is changed. Native errors intentionally do not echo paths.
if (-not ('SocialIntelligenceOS.ServiceCredentialNativeV1' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.IO;
using System.Runtime.InteropServices;
using System.Security.AccessControl;
using Microsoft.Win32.SafeHandles;
namespace SocialIntelligenceOS {
    public static class ServiceCredentialNativeV1 {
        [StructLayout(LayoutKind.Sequential)]
        private struct Info {
            public uint Attributes;
            public System.Runtime.InteropServices.ComTypes.FILETIME Creation, Access, Write;
            public uint Volume, SizeHigh, SizeLow, Links, IndexHigh, IndexLow;
        }
        [DllImport("kernel32.dll", CharSet=CharSet.Unicode, SetLastError=true)]
        private static extern SafeFileHandle CreateFileW(string path, uint access, uint share, IntPtr security, uint creation, uint flags, IntPtr template);
        [DllImport("kernel32.dll", SetLastError=true)]
        private static extern bool GetFileInformationByHandle(SafeFileHandle file, out Info info);
        [DllImport("kernel32.dll", SetLastError=true)]
        private static extern uint GetFileType(SafeFileHandle file);
        [DllImport("advapi32.dll", SetLastError=true)]
        private static extern uint GetSecurityInfo(SafeFileHandle file, uint type, uint sections, out IntPtr owner, out IntPtr group, out IntPtr dacl, out IntPtr sacl, out IntPtr descriptor);
        [DllImport("advapi32.dll")]
        private static extern uint GetSecurityDescriptorLength(IntPtr descriptor);
        [DllImport("kernel32.dll")]
        private static extern IntPtr LocalFree(IntPtr memory);

        private static SafeFileHandle Open(string path, bool directory) {
            // OPEN_EXISTING; OPEN_REPARSE_POINT | BACKUP_SEMANTICS. Directory
            // handles allow read/write sharing, but never deletion/renaming.
            var file = CreateFileW(path, directory ? 0x20080u : 0x80000000u,
                directory ? 3u : 1u, IntPtr.Zero, 3, 0x02200000, IntPtr.Zero);
            if (file.IsInvalid) {
                int error = Marshal.GetLastWin32Error();
                file.Dispose();
                if (error == 2 || error == 3) throw new FileNotFoundException("SOCIALD_CREDENTIAL_FILE is missing.");
                throw new IOException("SOCIALD_CREDENTIAL_FILE could not be opened securely.");
            }
            Info info;
            if (!GetFileInformationByHandle(file, out info) || (info.Attributes & 0x400) != 0 || ((info.Attributes & 0x10) != 0) != directory) {
                file.Dispose();
                throw new IOException("SOCIALD_CREDENTIAL_FILE contains a linked or invalid path component.");
            }
            if (!directory && (GetFileType(file) != 1 || info.Links != 1 || info.SizeHigh != 0 || info.SizeLow == 0 || info.SizeLow > 32768)) {
                file.Dispose();
                throw new IOException("SOCIALD_CREDENTIAL_FILE has an invalid file type or size.");
            }
            return file;
        }
        public static SafeFileHandle OpenDirectory(string path) { return Open(path, true); }
        public static SafeFileHandle OpenCredential(string path) { return Open(path, false); }

        public static void AssertPrivate(SafeFileHandle file, string currentSID) {
            IntPtr owner, group, dacl, sacl, descriptor;
            if (GetSecurityInfo(file, 1, 5, out owner, out group, out dacl, out sacl, out descriptor) != 0)
                throw new IOException("SOCIALD_CREDENTIAL_FILE security could not be verified.");
            try {
                uint size = GetSecurityDescriptorLength(descriptor);
                if (size == 0 || size > 65536) throw new IOException("Invalid credential security descriptor.");
                var bytes = new byte[size];
                Marshal.Copy(descriptor, bytes, 0, checked((int)size));
                var sd = new RawSecurityDescriptor(bytes, 0);
                var acl = sd.DiscretionaryAcl;
                if (sd.Owner == null || sd.Owner.Value != currentSID ||
                    (sd.ControlFlags & ControlFlags.DiscretionaryAclProtected) == 0 ||
                    (sd.ControlFlags & ControlFlags.DiscretionaryAclPresent) == 0 ||
                    acl == null || acl.Count == 0 || acl.Count > 128)
                    throw new IOException("SOCIALD_CREDENTIAL_FILE requires current-user ownership and a private protected DACL.");
                bool fullControl = false;
                foreach (GenericAce entry in acl) {
                    var ace = entry as CommonAce;
                    if (ace == null || ace.AceType != AceType.AccessAllowed ||
                        (ace.AceFlags & AceFlags.Inherited) != 0 || ace.SecurityIdentifier.Value != currentSID)
                        throw new IOException("SOCIALD_CREDENTIAL_FILE is accessible by another identity or an unsupported ACE.");
                    if ((ace.AceFlags & AceFlags.InheritOnly) == 0 && (ace.AccessMask & 0x1f01ff) == 0x1f01ff) fullControl = true;
                }
                if (!fullControl) throw new IOException("SOCIALD_CREDENTIAL_FILE requires private current-user full control.");
            } finally { LocalFree(descriptor); }
        }
    }
}
'@
}

function Test-ServiceCredentialToken {
    param([AllowNull()][AllowEmptyString()][string]$Token)
    if ($null -eq $Token -or $Token.Length -lt 32 -or $Token.Length -gt 4096) { return $false }
    foreach ($character in $Token.ToCharArray()) {
        if ([int]$character -lt 33 -or [int]$character -gt 126) { return $false }
    }
    return $true
}

function Assert-ServiceCredentialURL {
    param([string]$ServiceURL)
    if ($ServiceURL -cnotmatch '^http://127\.0\.0\.1:([1-9][0-9]{0,4})$') { throw 'Service credentials require a canonical literal-loopback service URL.' }
    $port = [int]$Matches[1]
    if ($port -gt 65535) { throw 'Service credentials require a port between 1 and 65535.' }
}

function Get-ServiceCredentialURL {
    # SOCIALD_ADDR is what sociald actually listens on; SOCIALD_URL is an MCP
    # client setting and must not determine the stored credential's binding.
    $address = [Environment]::GetEnvironmentVariable('SOCIALD_ADDR', 'Process')
    if ([string]::IsNullOrEmpty($address)) { $address = '127.0.0.1:17890' }
    $url = 'http://' + $address
    Assert-ServiceCredentialURL $url
    return $url
}

function Get-ServiceCredentialPath {
    $variables = [Environment]::GetEnvironmentVariables('Process')
    $path = $null
    # Windows environment names are case-insensitive, but the returned .NET
    # Hashtable is not. Enumerate keys case-insensitively and use the native getter.
    if ($variables.Keys -icontains 'SOCIALD_CREDENTIAL_FILE') { $path = [Environment]::GetEnvironmentVariable('SOCIALD_CREDENTIAL_FILE', 'Process') }
    else {
        # .NET resolves the Known Folder, not an untrusted LOCALAPPDATA value.
        $root = [Environment]::GetFolderPath([Environment+SpecialFolder]::LocalApplicationData, [Environment+SpecialFolderOption]::DoNotVerify)
        if ([string]::IsNullOrEmpty($root)) { throw 'The current user LocalApplicationData Known Folder could not be resolved.' }
        $path = Join-Path $root 'SocialIntelligenceOS/credentials/service-v1.dpapi'
    }
    if ([string]::IsNullOrEmpty($path) -or $path -cnotmatch '^[A-Za-z]:[\\/]' -or -not [IO.Path]::IsPathFullyQualified($path) -or $path.Substring(2).Contains(':')) { throw 'SOCIALD_CREDENTIAL_FILE requires an absolute file on a local fixed drive.' }
    $parts = $path.Substring(3).Replace('/', '\').Split('\')
    foreach ($part in $parts) {
        if ($part -eq '' -or $part -in @('.', '..') -or $part.TrimEnd(' ', '.') -cne $part -or $part -match '[<>"|?*\x00]' -or ($part.Split('.')[0].TrimEnd(' ') -match '^(CON|PRN|AUX|NUL|COM[1-9¹²³]|LPT[1-9¹²³]|CONIN\$|CONOUT\$)$')) { throw 'SOCIALD_CREDENTIAL_FILE contains an ambiguous or device path component.' }
    }
    $absolute = [IO.Path]::GetFullPath($path)
    if ([IO.DriveInfo]::new([IO.Path]::GetPathRoot($absolute)).DriveType -ne [IO.DriveType]::Fixed) { throw 'SOCIALD_CREDENTIAL_FILE must use a local fixed drive.' }
    return $absolute
}

function New-ServiceCredentialSecurity {
    param([switch]$Directory)
    $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User
    if ($Directory) {
        $acl = [Security.AccessControl.DirectorySecurity]::new()
        $rule = [Security.AccessControl.FileSystemAccessRule]::new($sid, 'FullControl', 'ContainerInherit,ObjectInherit', 'None', 'Allow')
    } else {
        $acl = [Security.AccessControl.FileSecurity]::new()
        $rule = [Security.AccessControl.FileSystemAccessRule]::new($sid, 'FullControl', 'Allow')
    }
    # Security is attached at creation, never repaired on existing objects.
    $acl.SetOwner($sid)
    $acl.SetAccessRuleProtection($true, $false)
    $acl.AddAccessRule($rule)
    return $acl
}

function Open-ServiceCredentialDirectories {
    param([string]$Parent, [switch]$CreateMissing)
    $paths = [Collections.Generic.List[string]]::new()
    for ($directory = $Parent; ; $directory = [IO.Path]::GetDirectoryName($directory)) {
        $paths.Add($directory)
        if ($directory -ceq [IO.Path]::GetPathRoot($directory)) { break }
    }
    $handles = [Collections.Generic.List[Microsoft.Win32.SafeHandles.SafeFileHandle]]::new()
    $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    try {
        for ($index = $paths.Count - 1; $index -ge 0; $index--) {
            $created = $false
            try { $handle = [SocialIntelligenceOS.ServiceCredentialNativeV1]::OpenDirectory($paths[$index]) }
            catch [IO.FileNotFoundException] {
                if (-not $CreateMissing) { throw 'SOCIALD_CREDENTIAL_FILE is missing; initialize it with the Windows inbox launcher.' }
                $security = New-ServiceCredentialSecurity -Directory
                [IO.FileSystemAclExtensions]::Create([IO.DirectoryInfo]::new($paths[$index]), $security)
                $handle = [SocialIntelligenceOS.ServiceCredentialNativeV1]::OpenDirectory($paths[$index])
                $created = $true
            }
            $handles.Add($handle)
            if ($created -or $index -eq 0) { [SocialIntelligenceOS.ServiceCredentialNativeV1]::AssertPrivate($handle, $sid) }
        }
        return ,$handles
    } catch {
        foreach ($handle in $handles) { $handle.Dispose() }
        throw 'SOCIALD_CREDENTIAL_FILE requires non-linked local paths, current-user ownership and a private protected parent DACL; no existing permissions were changed.'
    }
}

function Read-ServiceCredential {
    param([Parameter(Mandatory)][string]$Path, [Parameter(Mandatory)][string]$ServiceURL)
    Assert-ServiceCredentialURL $ServiceURL
    $handles = $null
    $stream = $null
    $file = $null
    $plain = $null
    $document = $null
    try {
        $handles = Open-ServiceCredentialDirectories -Parent ([IO.Path]::GetDirectoryName($Path))
        $file = [SocialIntelligenceOS.ServiceCredentialNativeV1]::OpenCredential($Path)
        [SocialIntelligenceOS.ServiceCredentialNativeV1]::AssertPrivate($file, [Security.Principal.WindowsIdentity]::GetCurrent().User.Value)
        $stream = [IO.FileStream]::new($file, [IO.FileAccess]::Read)
        if ($stream.Length -lt 1 -or $stream.Length -gt $script:ServiceCredentialMaximum) { throw 'Invalid protected service credential size.' }
        $ciphertext = [byte[]]::new([int]$stream.Length)
        $offset = 0
        while ($offset -lt $ciphertext.Length) {
            $count = $stream.Read($ciphertext, $offset, $ciphertext.Length - $offset)
            if ($count -eq 0) { throw 'Truncated protected service credential.' }
            $offset += $count
        }
        if ($stream.ReadByte() -ne -1) { throw 'Oversized protected service credential.' }
        # .NET ProtectedData uses native DPAPI with CRYPTPROTECT_UI_FORBIDDEN;
        # CurrentUser is intentional. Never switch to LocalMachine.
        $plain = [Security.Cryptography.ProtectedData]::Unprotect($ciphertext, $script:ServiceCredentialPurpose, [Security.Cryptography.DataProtectionScope]::CurrentUser)
        if ($plain.Length -lt 1 -or $plain.Length -gt $script:ServiceCredentialMaximum) { throw 'Invalid protected service record size.' }
        $text = [Text.UTF8Encoding]::new($false, $true).GetString($plain)
        $options = [Text.Json.JsonDocumentOptions]::new()
        $options.MaxDepth = 8
        $document = [Text.Json.JsonDocument]::Parse($text, $options)
        if ($document.RootElement.ValueKind -ne [Text.Json.JsonValueKind]::Object) { throw 'Invalid protected service record.' }
        $names = [Collections.Generic.HashSet[string]]::new([StringComparer]::Ordinal)
        foreach ($property in $document.RootElement.EnumerateObject()) {
            if (-not $names.Add($property.Name) -or $property.Name -cnotin @('version', 'service_url', 'api_token')) { throw 'Duplicate or unknown protected service record field.' }
        }
        $version = 0
        if ($names.Count -ne 3 -or -not $document.RootElement.GetProperty('version').TryGetInt32([ref]$version) -or $version -ne 1 -or $document.RootElement.GetProperty('service_url').ValueKind -ne [Text.Json.JsonValueKind]::String -or $document.RootElement.GetProperty('api_token').ValueKind -ne [Text.Json.JsonValueKind]::String) { throw 'Unsupported protected service record.' }
        $boundURL = $document.RootElement.GetProperty('service_url').GetString()
        Assert-ServiceCredentialURL $boundURL
        if ($boundURL -cne $ServiceURL) { throw 'Protected service credential URL binding mismatch.' }
        $token = $document.RootElement.GetProperty('api_token').GetString()
        if (-not (Test-ServiceCredentialToken $token)) { throw 'Invalid protected service API token.' }
        # Returning SecureString prevents an accidental unassigned invocation
        # from printing the bearer token. The launcher unwraps only for its child.
        return ConvertTo-SecureString -String $token -AsPlainText -Force
    } catch {
        throw 'SOCIALD_CREDENTIAL_FILE could not be read securely by this Windows account (path/ACL/DPAPI/version/URL binding). Existing bytes were preserved; restore the original account/file or explicitly reauthorize.'
    } finally {
        if ($null -ne $document) { $document.Dispose() }
        if ($null -ne $plain) { [Array]::Clear($plain, 0, $plain.Length) }
        if ($null -ne $stream) { $stream.Dispose() }
        elseif ($null -ne $file) { $file.Dispose() }
        if ($null -ne $handles) { foreach ($handle in $handles) { $handle.Dispose() } }
        $token = $null
        $text = $null
    }
}

function Initialize-ServiceCredential {
    param([Parameter(Mandatory)][string]$ServiceURL, [switch]$Rotate)
    # An explicit token, even empty, must never cause saved-store I/O.
    if ([Environment]::GetEnvironmentVariables('Process').Keys -icontains 'SOCIALD_API_TOKEN') { throw 'Stored credentials cannot be initialized or rotated with explicit SOCIALD_API_TOKEN.' }
    Assert-ServiceCredentialURL $ServiceURL
    $path = Get-ServiceCredentialPath
    $parent = [IO.Path]::GetDirectoryName($path)
    $handles = $null
    $stream = $null
    $temporary = $null
    $plain = $null
    $reservation = $null
    try {
        # Rotation is not an initialization/repair path.
        if ($Rotate) {
            $existing = Read-ServiceCredential -Path $path -ServiceURL $ServiceURL
            $existing.Dispose()
            $port = [int]($ServiceURL.Substring($ServiceURL.LastIndexOf(':') + 1))
            $reservation = [Net.Sockets.Socket]::new([Net.Sockets.AddressFamily]::InterNetwork, [Net.Sockets.SocketType]::Stream, [Net.Sockets.ProtocolType]::Tcp)
            try {
                $reservation.ExclusiveAddressUse = $true
                $reservation.Bind([Net.IPEndPoint]::new([Net.IPAddress]::Loopback, $port))
            } catch {
                throw 'Stop the backend before -RotateApiToken: its loopback port must be free. No process was stopped and no credential was changed.'
            }
        }
        $handles = Open-ServiceCredentialDirectories -Parent $parent -CreateMissing:(-not $Rotate)
        if (-not $Rotate -and ([IO.File]::Exists($path) -or [IO.Directory]::Exists($path))) { return Read-ServiceCredential -Path $path -ServiceURL $ServiceURL }
        $token = [Convert]::ToHexString([Security.Cryptography.RandomNumberGenerator]::GetBytes(32))
        $text = [ordered]@{ version = 1; service_url = $ServiceURL; api_token = $token } | ConvertTo-Json -Compress
        $plain = [Text.UTF8Encoding]::new($false, $true).GetBytes($text)
        $ciphertext = [Security.Cryptography.ProtectedData]::Protect($plain, $script:ServiceCredentialPurpose, [Security.Cryptography.DataProtectionScope]::CurrentUser)
        if ($ciphertext.Length -lt 1 -or $ciphertext.Length -gt $script:ServiceCredentialMaximum) { throw 'Invalid protected service credential size.' }
        $temporary = Join-Path $parent ('.service-v1.' + [guid]::NewGuid().ToString('N') + '.tmp')
        $security = New-ServiceCredentialSecurity
        $stream = [IO.FileSystemAclExtensions]::Create([IO.FileInfo]::new($temporary), [IO.FileMode]::CreateNew, [Security.AccessControl.FileSystemRights]::FullControl, [IO.FileShare]::None, 4096, [IO.FileOptions]::WriteThrough, $security)
        $stream.Write($ciphertext, 0, $ciphertext.Length)
        $stream.Flush($true)
        $stream.Dispose()
        $stream = $null
        if ($Rotate) {
            # Replace requires an existing destination; never recreate one
            # removed while rotation was running. Both files are already private.
            [IO.File]::Replace($temporary, $path, [NullString]::Value, $false)
        } else {
            try { [IO.File]::Move($temporary, $path, $false) }
            catch [IO.IOException] {
                # Another initializer may have won. Never overwrite its token;
                # load its fully published record and validate every boundary.
                return Read-ServiceCredential -Path $path -ServiceURL $ServiceURL
            }
        }
        return Read-ServiceCredential -Path $path -ServiceURL $ServiceURL
    } catch {
        if ($_.Exception.Message.StartsWith('SOCIALD_CREDENTIAL_FILE ', [StringComparison]::Ordinal) -or $_.Exception.Message.StartsWith('Stop the backend before -RotateApiToken:', [StringComparison]::Ordinal)) { throw }
        throw 'Protected service credential publication failed. No plaintext fallback or automatic reset was attempted; inspect the protected store before retrying.'
    } finally {
        if ($null -ne $stream) { $stream.Dispose() }
        if ($null -ne $plain) { [Array]::Clear($plain, 0, $plain.Length) }
        if ($null -ne $temporary) {
            if ([IO.Path]::GetDirectoryName($temporary) -cne $parent -or [IO.Path]::GetFileName($temporary) -cnotmatch '^\.service-v1\.[a-f0-9]{32}\.tmp$') { throw 'Unsafe service credential temporary cleanup path.' }
            if ([IO.File]::Exists($temporary)) { [IO.File]::Delete($temporary) }
        }
        if ($null -ne $handles) { foreach ($handle in $handles) { $handle.Dispose() } }
        if ($null -ne $reservation) { $reservation.Dispose() }
        $token = $null
        $text = $null
    }
}

Export-ModuleMember -Function Test-ServiceCredentialToken,Get-ServiceCredentialURL,Get-ServiceCredentialPath,Initialize-ServiceCredential

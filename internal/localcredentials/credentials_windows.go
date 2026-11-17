//go:build windows

package localcredentials

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func defaultPath() (string, error) {
	// Known Folder API, not LOCALAPPDATA/APPDATA supplied by a client. Do not
	// create or repair anything while resolving the current user's location.
	root, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, windows.KF_FLAG_DONT_VERIFY)
	if err != nil || root == "" {
		return "", ErrPath
	}
	return filepath.Join(root, "SocialIntelligenceOS", "credentials", "service-v1.dpapi"), nil
}

func credentialPath(path string) (string, error) {
	if len(path) < 4 || !filepath.IsAbs(path) || path[1] != ':' || (path[2] != '\\' && path[2] != '/') || !asciiDriveLetter(path[0]) || strings.Contains(path[2:], ":") {
		return "", ErrPath
	}
	// Reject Win32 aliases/device names and ambiguous normalization before any
	// filesystem operation. In particular, never accept UNC, ADS or \\?\ paths.
	parts := strings.Split(strings.ReplaceAll(path[3:], "/", `\`), `\`)
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.TrimRight(part, " .") != part || !filepath.IsLocal(part) || strings.ContainsAny(part, "<>\"|?*\x00") {
			return "", ErrPath
		}
	}
	absolute := filepath.Clean(path)
	volume, err := windows.UTF16PtrFromString(filepath.VolumeName(absolute) + `\`)
	if err != nil || windows.GetDriveType(volume) != windows.DRIVE_FIXED {
		return "", ErrPath
	}
	return absolute, nil
}

func asciiDriveLetter(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func openPath(path string, directory bool) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return windows.InvalidHandle, ErrPath
	}
	access := uint32(windows.FILE_READ_ATTRIBUTES | windows.READ_CONTROL)
	share := uint32(windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE)
	if !directory {
		access = windows.GENERIC_READ
		share = windows.FILE_SHARE_READ
	}
	handle, err := windows.CreateFile(name, access, share, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			return windows.InvalidHandle, ErrMissing
		}
		return windows.InvalidHandle, ErrRead
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || (info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0) != directory {
		_ = windows.CloseHandle(handle)
		return windows.InvalidHandle, ErrPath
	}
	if !directory {
		kind, err := windows.GetFileType(handle)
		if err != nil || kind != windows.FILE_TYPE_DISK || info.NumberOfLinks != 1 || info.FileSizeHigh != 0 || info.FileSizeLow == 0 || info.FileSizeLow > MaxFileBytes {
			_ = windows.CloseHandle(handle)
			return windows.InvalidHandle, ErrRecord
		}
	}
	return handle, nil
}

func privateSecurity(handle windows.Handle, current *windows.SID) error {
	sd, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil || !sd.IsValid() {
		return ErrUnsafe
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !owner.IsValid() || !owner.Equals(current) {
		return ErrUnsafe
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return ErrUnsafe
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil || acl.AceCount == 0 || acl.AceCount > 128 {
		return ErrUnsafe
	}
	fullControl := false
	for index := uint32(0); index < uint32(acl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(acl, index, &ace) != nil || ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags&windows.INHERITED_ACE != 0 || int(ace.Header.AceSize) < int(unsafe.Offsetof(ace.SidStart))+8 {
			return ErrUnsafe
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.IsValid() || sid.Len() > int(ace.Header.AceSize)-int(unsafe.Offsetof(ace.SidStart)) || !sid.Equals(current) {
			return ErrUnsafe
		}
		// FileSystemRights.FullControl, shared with the PowerShell writer.
		const fileFullControl = 0x1f01ff
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE == 0 && uint32(ace.Mask)&fileFullControl == fileFullControl {
			fullControl = true
		}
	}
	runtime.KeepAlive(sd)
	if !fullControl {
		return ErrUnsafe
	}
	return nil
}

func loadToken(path, serviceURL string) (string, error) {
	path, err := credentialPath(path)
	if err != nil {
		return "", err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", ErrUnsafe
	}
	// Hold all ancestor directories without delete sharing until the read is
	// finished. No component is followed through a reparse point, and the
	// credential's immediate parent must itself already be private.
	parent := filepath.Dir(path)
	var directories []string
	for directory := parent; ; directory = filepath.Dir(directory) {
		directories = append(directories, directory)
		if filepath.Dir(directory) == directory {
			break
		}
	}
	for index := len(directories) - 1; index >= 0; index-- {
		handle, err := openPath(directories[index], true)
		if err != nil {
			return "", err
		}
		defer windows.CloseHandle(handle)
		if index == 0 {
			if err := privateSecurity(handle, user.User.Sid); err != nil {
				return "", err
			}
		}
	}
	handle, err := openPath(path, false)
	if err != nil {
		return "", err
	}
	file := os.NewFile(uintptr(handle), "protected service credential")
	defer file.Close()
	if err := privateSecurity(handle, user.User.Sid); err != nil {
		return "", err
	}
	ciphertext, err := io.ReadAll(io.LimitReader(file, MaxFileBytes+1))
	if err != nil {
		return "", ErrRead
	}
	if len(ciphertext) == 0 || len(ciphertext) > MaxFileBytes {
		return "", ErrRecord
	}
	entropy := []byte(purpose)
	input := windows.DataBlob{Size: uint32(len(ciphertext)), Data: &ciphertext[0]}
	optionalEntropy := windows.DataBlob{Size: uint32(len(entropy)), Data: &entropy[0]}
	var output windows.DataBlob
	err = windows.CryptUnprotectData(&input, nil, &optionalEntropy, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output)
	runtime.KeepAlive(ciphertext)
	runtime.KeepAlive(entropy)
	if err != nil {
		return "", ErrDecrypt
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(output.Data))))
	if output.Data == nil || output.Size == 0 || output.Size > MaxFileBytes {
		return "", ErrRecord
	}
	plain := unsafe.Slice(output.Data, int(output.Size))
	defer clear(plain)
	return decodeRecord(plain, serviceURL)
}

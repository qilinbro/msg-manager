//go:build windows

package store

import (
	"path/filepath"
	"syscall"
	"unsafe"
)

// WAL and local key protection require a local fixed drive. Reject reparse
// ancestors so a junction cannot silently redirect the database elsewhere.
func localDatabasePath(path string) bool {
	volume := filepath.VolumeName(path)
	if len(volume) != 2 || volume[1] != ':' {
		return false
	}
	root, err := syscall.UTF16PtrFromString(volume + `\`)
	if err != nil {
		return false
	}
	driveType, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("GetDriveTypeW").Call(uintptr(unsafe.Pointer(root)))
	if driveType != 3 {
		return false
	}
	for current := path; ; current = filepath.Dir(current) {
		pointer, err := syscall.UTF16PtrFromString(current)
		if err != nil {
			return false
		}
		attributes, err := syscall.GetFileAttributes(pointer)
		if err == nil && attributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return false
		}
		if err != nil && err != syscall.ERROR_FILE_NOT_FOUND && err != syscall.ERROR_PATH_NOT_FOUND {
			return false
		}
		if parent := filepath.Dir(current); parent == current {
			break
		}
	}
	return true
}

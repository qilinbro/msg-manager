//go:build windows

package wechatdb

import (
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

func localPath(path string) bool {
	volume := filepath.VolumeName(path)
	if len(volume) != 2 || volume[1] != ':' || strings.Contains(path[len(volume):], ":") {
		return false
	}
	root, err := syscall.UTF16PtrFromString(volume + `\`)
	if err != nil {
		return false
	}
	kernel := syscall.NewLazyDLL("kernel32.dll")
	typ, _, _ := kernel.NewProc("GetDriveTypeW").Call(uintptr(unsafe.Pointer(root)))
	if typ != 3 {
		return false
	}
	for current := path; ; current = filepath.Dir(current) {
		p, err := syscall.UTF16PtrFromString(current)
		if err != nil {
			return false
		}
		attrs, err := syscall.GetFileAttributes(p)
		if err == nil && attrs&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return false
		}
		if err != nil && err != syscall.ERROR_FILE_NOT_FOUND && err != syscall.ERROR_PATH_NOT_FOUND {
			return false
		}
		if filepath.Dir(current) == current {
			return true
		}
	}
}

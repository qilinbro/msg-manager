//go:build !windows

package store

import (
	"os"
	"path/filepath"
)

func localDatabasePath(path string) bool {
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return false
		}
		if err != nil && !os.IsNotExist(err) {
			return false
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	return true
}

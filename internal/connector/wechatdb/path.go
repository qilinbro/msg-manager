package wechatdb

import (
	"os"
	"path/filepath"
)

func safePath(path string) bool {
	if !filepath.IsAbs(path) || !localPath(path) {
		return false
	}
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil && !os.IsNotExist(err) {
			return false
		}
		if info != nil && info.Mode()&os.ModeSymlink != 0 {
			return false
		}
		if filepath.Dir(current) == current {
			return true
		}
	}
}

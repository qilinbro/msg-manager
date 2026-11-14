//go:build !windows

package wechatdb

import "strings"

// Other systems are useful for synthetic fixtures; no account discovery runs.
func localPath(path string) bool { return !strings.HasPrefix(path, "//") }

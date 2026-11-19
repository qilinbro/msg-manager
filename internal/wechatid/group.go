// Package wechatid validates identifiers shared by the WeChat reader and inbox.
package wechatid

import "regexp"

var groupPattern = regexp.MustCompile(`^(?:@@[A-Za-z0-9_-]{2,126}|[0-9]{1,32}@chatroom)$`)

// IsGroup reports whether id is a supported local WeChat group identifier.
func IsGroup(id string) bool {
	return groupPattern.MatchString(id)
}

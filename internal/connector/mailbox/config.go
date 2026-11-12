package mailbox

import (
	"net/mail"
	"net/netip"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

func normalizedConfig(c Config) (Config, error) {
	if c.Host == "" || c.Username == "" || c.Password == "" {
		return Config{}, fail(NotConfigured)
	}
	if c.Port == 0 {
		c.Port = 993
	}
	if c.Folder == "" {
		c.Folder = "INBOX"
	}
	if strings.EqualFold(c.Folder, "INBOX") {
		c.Folder = "INBOX"
	}
	if c.Limit == 0 {
		c.Limit = 50
	}
	if c.Port < 1 || c.Port > 65535 || c.Limit < 1 || c.Limit > 100 || len(c.Password) > 4096 || !validLabel(c.Folder, 128) {
		return Config{}, fail(InvalidConfig)
	}
	for _, ch := range c.Password {
		if ch < 33 || ch > 126 {
			return Config{}, fail(InvalidConfig)
		}
	}
	address, err := mail.ParseAddress(c.Username)
	if err != nil || address.Address != c.Username || address.Name != "" || !validLabel(c.Username, 254) {
		return Config{}, fail(InvalidConfig)
	}
	for _, ch := range c.Username {
		if ch < 33 || ch > 126 {
			return Config{}, fail(InvalidConfig)
		}
	}
	if c.Host != strings.ToLower(c.Host) || len(c.Host) > 253 || strings.ContainsAny(c.Host, "/\\?#@%[] \t\r\n") {
		return Config{}, fail(InvalidConfig)
	}
	if ip, err := netip.ParseAddr(c.Host); err == nil {
		if !publicIP(ip) {
			return Config{}, fail(AddressDenied)
		}
		return c, nil
	}
	labels := strings.Split(c.Host, ".")
	if len(labels) < 2 {
		return Config{}, fail(InvalidConfig)
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return Config{}, fail(InvalidConfig)
		}
		for _, ch := range label {
			if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-') {
				return Config{}, fail(InvalidConfig)
			}
		}
	}
	for _, suffix := range []string{".local", ".localhost", ".internal", ".invalid", ".onion"} {
		if strings.HasSuffix(c.Host, suffix) {
			return Config{}, fail(AddressDenied)
		}
	}
	// All-numeric hostnames are ambiguous alternate IPv4 spellings.
	if _, err := strconv.ParseUint(strings.ReplaceAll(c.Host, ".", ""), 10, 64); err == nil {
		return Config{}, fail(InvalidConfig)
	}
	return c, nil
}

func validLabel(value string, limit int) bool {
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > limit || strings.TrimSpace(value) != value {
		return false
	}
	for _, ch := range value {
		if unicode.IsControl(ch) {
			return false
		}
	}
	return true
}

var deniedIPv4 = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("172.16.0.0/12"), netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("192.168.0.0/16"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("224.0.0.0/4"), netip.MustParsePrefix("240.0.0.0/4"),
}

func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.Zone() != "" {
		return false
	}
	if ip.Is4() {
		for _, prefix := range deniedIPv4 {
			if prefix.Contains(ip) {
				return false
			}
		}
		return true
	}
	if !netip.MustParsePrefix("2000::/3").Contains(ip) {
		return false
	}
	for _, prefix := range []string{"2001::/23", "2001:db8::/32", "2002::/16"} {
		if netip.MustParsePrefix(prefix).Contains(ip) {
			return false
		}
	}
	return true
}

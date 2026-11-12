package agent

import (
	"context"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

type validatedConfig struct {
	endpoint *url.URL
	host     string
	port     string
	local    bool
	key      string
	model    string
}

func validateConfig(c Config) (validatedConfig, error) {
	if c.BaseURL == "" || c.Model == "" {
		return validatedConfig{}, failure(NotConfigured)
	}
	if len(c.BaseURL) > 2048 || !printable(c.Model, 1, 128) || (c.APIKey != "" && !printable(c.APIKey, 1, 4096)) || strings.ContainsAny(c.BaseURL, "\\#%") {
		return validatedConfig{}, failure(InvalidConfig)
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || u == nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || u.RawPath != "" {
		return validatedConfig{}, failure(InvalidConfig)
	}
	if u.Path != "" && u.Path != "/" && u.Path != "/v1" && u.Path != "/v1/" {
		return validatedConfig{}, failure(InvalidConfig)
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port || u.Host != net.JoinHostPort(u.Hostname(), port) {
			return validatedConfig{}, failure(InvalidConfig)
		}
	}
	local := u.Scheme == "http" && (host == "127.0.0.1" || host == "::1")
	if u.Scheme == "http" && (!local || port == "") {
		return validatedConfig{}, failure(InvalidConfig)
	}
	if u.Scheme == "https" {
		if c.APIKey == "" {
			return validatedConfig{}, failure(NotConfigured)
		}
		if ip, err := netip.ParseAddr(host); err == nil {
			if !publicIP(ip) {
				return validatedConfig{}, failure(InvalidConfig)
			}
		} else if !publicHostname(host) {
			return validatedConfig{}, failure(InvalidConfig)
		}
		if port == "" {
			port = "443"
		}
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/chat/completions"
	} else {
		u.Path = "/v1/chat/completions"
	}
	return validatedConfig{endpoint: u, host: host, port: port, local: local, key: c.APIKey, model: c.Model}, nil
}

func printable(s string, min, max int) bool {
	if len(s) < min || len(s) > max {
		return false
	}
	for _, ch := range s {
		if ch < 33 || ch > 126 {
			return false
		}
	}
	return true
}

func publicHostname(host string) bool {
	if len(host) > 253 || strings.ContainsAny(host, "/\\?#@%[] \t\r\n:") {
		return false
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, ch := range label {
			if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-') {
				return false
			}
		}
	}
	for _, suffix := range []string{".local", ".localhost", ".internal", ".invalid", ".onion"} {
		if strings.HasSuffix(host, suffix) {
			return false
		}
	}
	if _, err := strconv.ParseUint(strings.ReplaceAll(host, ".", ""), 10, 64); err == nil {
		return false
	}
	return true
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

var deniedIPv4 = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("172.16.0.0/12"), netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("192.168.0.0/16"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("224.0.0.0/4"), netip.MustParsePrefix("240.0.0.0/4"),
}

func (c *Client) destination(ctx context.Context) (string, error) {
	if c.cfg.local {
		return net.JoinHostPort(c.cfg.host, c.cfg.port), nil
	}
	var addresses []net.IPAddr
	if ip, err := netip.ParseAddr(c.cfg.host); err == nil {
		addresses = []net.IPAddr{{IP: net.IP(ip.AsSlice())}}
	} else {
		var err error
		addresses, err = c.lookupIP(ctx, c.cfg.host)
		if err != nil {
			return "", failure(Unavailable)
		}
	}
	if len(addresses) < 1 || len(addresses) > 32 {
		return "", failure(Unavailable)
	}
	for _, address := range addresses {
		ip, ok := netip.AddrFromSlice(address.IP)
		if !ok || address.Zone != "" || !publicIP(ip) {
			return "", failure(InvalidConfig)
		}
	}
	return net.JoinHostPort(addresses[0].IP.String(), c.cfg.port), nil
}

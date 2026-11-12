package onebot

import (
	"net"
	"net/url"
	"strconv"
	"strings"
)

type Config struct {
	URL            string
	AccessToken    string
	ExpectedSelfID string
}

// String keeps incidental formatting of config values from exposing secrets.
func (Config) String() string { return "OneBot config (redacted)" }

type validatedConfig struct {
	url      *url.URL
	token    string
	selfID   string
	dialHost string
	port     string
}

func validateConfig(c Config) (validatedConfig, error) {
	if c.URL == "" || c.AccessToken == "" || c.ExpectedSelfID == "" {
		return validatedConfig{}, failure(NotConfigured)
	}
	if len(c.AccessToken) > 4096 || len(c.URL) > 4096 {
		return validatedConfig{}, failure(InvalidConfig)
	}
	for _, ch := range c.AccessToken {
		if ch < 33 || ch > 126 {
			return validatedConfig{}, failure(InvalidConfig)
		}
	}
	selfID, ok := positiveID(c.ExpectedSelfID)
	if !ok {
		return validatedConfig{}, failure(InvalidConfig)
	}
	u, err := url.Parse(c.URL)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "ws") || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.ContainsAny(c.URL, "#\\") {
		return validatedConfig{}, failure(InvalidConfig)
	}
	host := u.Hostname()
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return validatedConfig{}, failure(InvalidConfig)
	}
	port := u.Port()
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
		return validatedConfig{}, failure(InvalidConfig)
	}
	if u.Host != net.JoinHostPort(u.Hostname(), port) {
		return validatedConfig{}, failure(InvalidConfig)
	}
	if host == "localhost" {
		host = "127.0.0.1" // Never resolve a configurable hostname using DNS.
	}
	return validatedConfig{url: u, token: c.AccessToken, selfID: selfID, dialHost: host, port: port}, nil
}

func positiveID(s string) (string, bool) {
	if len(s) == 0 || len(s) > 128 {
		return "", false
	}
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return "", false
		}
	}
	s = strings.TrimLeft(s, "0")
	return s, s != ""
}

package mailbox

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"testing"
)

func testConfig() Config {
	return Config{Host: "mail.example.com", Username: "synthetic@example.com", Password: "synthetic-app-code"}
}

func expectKind(t *testing.T, err error, kind Kind) {
	t.Helper()
	var typed *Error
	if !errors.As(err, &typed) || typed.Kind != kind {
		t.Fatalf("kind=%v want=%s", err, kind)
	}
}

func TestConfigurationAndDestinationsAreRestricted(t *testing.T) {
	cfg := testConfig()
	cl, err := NewClient(cfg)
	if err != nil || cl.cfg.Port != 993 || cl.cfg.Limit != 50 || cl.cfg.Folder != "INBOX" {
		t.Fatal("default config invalid")
	}
	for _, host := range []string{"127.0.0.1", "::1", "10.0.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1", "198.19.0.1", "192.0.2.4", "2001:db8::1", "::ffff:127.0.0.1", "fc00::1", "fe80::1", "224.0.0.1", "mail.local"} {
		c := cfg
		c.Host = host
		_, err := NewClient(c)
		expectKind(t, err, AddressDenied)
	}
	for _, host := range []string{"localhost", "https://mail.example.com", "mail.example.com:993", "mail.example.com/", "Mail.Example.com", "mail.example.com.", "0177.0.0.1", "2130706433", "a..com", "-bad.example.com", "[2606:4700::1111]"} {
		c := cfg
		c.Host = host
		if _, err := NewClient(c); err == nil {
			t.Fatalf("unsafe host accepted %s", host)
		}
	}
	for _, port := range []int{-1, 65536} {
		c := cfg
		c.Port = port
		if _, err := NewClient(c); err == nil {
			t.Fatal("invalid port accepted")
		}
	}
	for _, limit := range []int{-1, 101} {
		c := cfg
		c.Limit = limit
		if _, err := NewClient(c); err == nil {
			t.Fatal("invalid limit accepted")
		}
	}
	for _, password := range []string{"", "secret\r\nLOGOUT", "secret code", strings.Repeat("x", 4097)} {
		c := cfg
		c.Password = password
		if _, err := NewClient(c); err == nil {
			t.Fatal("unsafe auth code accepted")
		}
	}
	if strings.Contains(fmt.Sprint(cfg), cfg.Password) || strings.Contains(fmt.Sprint(cfg), cfg.Username) {
		t.Fatal("config formatting exposes credentials")
	}
	for _, ips := range [][]net.IPAddr{{{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("127.0.0.1")}}, {}, {{IP: net.ParseIP("2606:4700:4700::1111"), Zone: "local"}}} {
		cl.lookupIP = func(context.Context, string) ([]net.IPAddr, error) { return ips, nil }
		_, err := cl.destination(context.Background())
		expectKind(t, err, AddressDenied)
	}
	cl.lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	}
	if addr, err := cl.destination(context.Background()); err != nil || addr != "8.8.8.8:993" {
		t.Fatal("public destination not pinned")
	}
	for _, ip := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if !publicIP(netip.MustParseAddr(ip)) {
			t.Fatal("public address rejected")
		}
	}
}

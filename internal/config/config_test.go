package config_test

import (
	"strings"
	"testing"

	"github.com/qilinbro/msg-manager/internal/config"
)

func TestConfigRejectsUnsafeListenersAndTokens(t *testing.T) {
	token := strings.Repeat("a", 32)
	for _, addr := range []string{"0.0.0.0:17890", "[::]:17890", ":17890", "192.168.1.1:17890", "example.com:17890", "localhost:17890", "127.0.0.1:0", "127.0.0.1:65536", "127.0.0.1:017890", "127.0.0.1"} {
		t.Run(addr, func(t *testing.T) {
			if err := (config.Config{Addr: addr, APIToken: token}).Validate(); err == nil {
				t.Fatal("accepted non-loopback, ambiguous or invalid listener")
			}
		})
	}
	for _, addr := range []string{config.DefaultAddr, "[::1]:17890", "127.0.0.2:17890"} {
		if err := (config.Config{Addr: addr, APIToken: token}).Validate(); err != nil {
			t.Fatalf("valid loopback address rejected: %v", err)
		}
	}
	for _, bad := range []string{"", strings.Repeat("a", 31), token + "\n", token + " ", strings.Repeat("界", 32), strings.Repeat("a", 4097)} {
		err := (config.Config{Addr: config.DefaultAddr, APIToken: bad}).Validate()
		if err == nil {
			t.Fatal("accepted an invalid token")
		}
		if bad != "" && strings.Contains(err.Error(), bad) {
			t.Fatal("validation error exposed the token")
		}
	}
}

func TestLoadFromEnvRequiresTokenAndUsesDefaultAddress(t *testing.T) {
	t.Setenv("SOCIALD_ADDR", "")
	t.Setenv("SOCIALD_API_TOKEN", "")
	if _, err := config.LoadFromEnv(); err == nil {
		t.Fatal("service can start without a token")
	}
	t.Setenv("SOCIALD_API_TOKEN", strings.Repeat("k", 32))
	cfg, err := config.LoadFromEnv()
	if err != nil || cfg.Addr != config.DefaultAddr {
		t.Fatalf("default config: addr=%q, error=%v", cfg.Addr, err)
	}
}

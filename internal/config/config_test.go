package config_test

import (
	"fmt"
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

func TestAIEnvironmentLimitAndSecretRedaction(t *testing.T) {
	for _, key := range []string{"SOCIALD_DATABASE_PATH", "SOCIALD_DATABASE_KEY", "SOCIALD_ADDR", "EMAIL_IMAP_PORT", "EMAIL_IMAP_LIMIT"} {
		t.Setenv(key, "")
	}
	t.Setenv("SOCIALD_API_TOKEN", strings.Repeat("t", 32))
	t.Setenv("AI_BASE_URL", "https://api.deepseek.com")
	t.Setenv("AI_MODEL", "configured-model-id")
	t.Setenv("AI_API_KEY", "synthetic-private-key-for-redaction")
	t.Setenv("AI_DAILY_LIMIT", "20")
	cfg, err := config.LoadFromEnv()
	if err != nil || cfg.AIDailyLimit != 20 || cfg.AI.Model != "configured-model-id" || cfg.AI.APIKey == "" {
		t.Fatal("AI environment not loaded")
	}
	for _, format := range []string{"%v", "%+v", "%#v"} {
		if strings.Contains(fmt.Sprintf(format, cfg), cfg.AI.APIKey) {
			t.Fatal("AI secret exposed through configuration formatting")
		}
	}
	for _, invalid := range []string{"0", "1001", "abc"} {
		t.Setenv("AI_DAILY_LIMIT", invalid)
		if _, err := config.LoadFromEnv(); err == nil {
			t.Fatal("invalid AI call limit accepted")
		}
	}
}

func TestLoadFromEnvRequiresTokenAndUsesDefaultAddress(t *testing.T) {
	t.Setenv("SOCIALD_DATABASE_PATH", "")
	t.Setenv("SOCIALD_DATABASE_KEY", "")
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

func TestDatabaseConfigurationRequiresPairedAbsolutePathAndRawKey(t *testing.T) {
	valid := config.Config{Addr: config.DefaultAddr, APIToken: strings.Repeat("t", 32)}
	for _, pair := range [][2]string{{"relative.db", strings.Repeat("a", 64)}, {`\\remote\share\messages.db`, strings.Repeat("a", 64)}, {"", strings.Repeat("a", 64)}, {`D:\local\messages.db`, ""}, {`D:\local\messages.db`, strings.Repeat("z", 64)}} {
		cfg := valid
		cfg.DatabasePath = pair[0]
		cfg.DatabaseKey = pair[1]
		if err := cfg.Validate(); err == nil {
			t.Fatal("unsafe database config accepted")
		}
	}
}

func TestWechatEnvironmentLimitAndRedaction(t *testing.T) {
	for _, key := range []string{"SOCIALD_DATABASE_PATH", "SOCIALD_DATABASE_KEY", "SOCIALD_ADDR", "EMAIL_IMAP_PORT", "EMAIL_IMAP_LIMIT", "AI_DAILY_LIMIT"} {
		t.Setenv(key, "")
	}
	t.Setenv("SOCIALD_API_TOKEN", strings.Repeat("t", 32))
	t.Setenv("WECHAT_DB_DIR", `D:\synthetic\wxid_owner\db_storage`)
	t.Setenv("WECHAT_DB_ACCOUNT_ID", "wxid_owner")
	t.Setenv("WECHAT_DB_KEY", strings.Repeat("ab", 32))
	t.Setenv("WECHAT_DB_LIMIT", "150")
	cfg, err := config.LoadFromEnv()
	if err != nil || cfg.WechatDB.AccountID != "wxid_owner" || cfg.WechatDB.Limit != 150 || cfg.WechatDB.Key == "" {
		t.Fatal("wechat config not loaded")
	}
	for _, format := range []string{"%v", "%+v", "%#v"} {
		for _, value := range []any{cfg, cfg.WechatDB} {
			printed := fmt.Sprintf(format, value)
			if strings.Contains(printed, cfg.WechatDB.Key) || strings.Contains(printed, cfg.WechatDB.Directory) {
				t.Fatal("wechat config formatting leaked a secret")
			}
		}
	}
	for _, invalid := range []string{"0", "501", "invalid"} {
		t.Setenv("WECHAT_DB_LIMIT", invalid)
		if _, err := config.LoadFromEnv(); err == nil {
			t.Fatal("invalid wechat limit accepted")
		}
	}
}

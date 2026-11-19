package config_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qilinbro/msg-manager/internal/config"
	"github.com/qilinbro/msg-manager/internal/connector/nonebotbridge"
	"github.com/qilinbro/msg-manager/internal/connector/telegrambot"
)

func clearNoneBotTestEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{"SOCIALD_DATABASE_PATH", "SOCIALD_DATABASE_KEY", "SOCIALD_ADDR", "SOCIALD_NONEBOT_PYTHON", "SOCIALD_NONEBOT_WORKER", "SOCIALD_NONEBOT_TIMEOUT_SECONDS", "SOCIALD_FEISHU_TRANSPORT", "TELEGRAM_BOT_TOKEN", "TELEGRAM_BOT_ID", "TELEGRAM_BOT_ALLOWED_PRIVATE_CHAT_IDS", "TELEGRAM_BOT_LIMIT", "QQ_MILKY_URL", "QQ_MILKY_ACCESS_TOKEN", "QQ_MILKY_ACCOUNT_ID", "QQ_MILKY_HISTORY_LIMIT", "EMAIL_IMAP_HOST", "EMAIL_IMAP_PORT", "EMAIL_IMAP_USERNAME", "EMAIL_IMAP_PASSWORD", "EMAIL_IMAP_FOLDER", "EMAIL_IMAP_LIMIT", "AI_BASE_URL", "AI_API_KEY", "AI_MODEL", "AI_DAILY_LIMIT", "WECHAT_DB_DIR", "WECHAT_DB_ACCOUNT_ID", "WECHAT_DB_KEY", "WECHAT_DB_LIMIT", "TAVILY_API_KEY", "PUBLIC_SEARCH_DAILY_LIMIT"} {
		t.Setenv(key, "")
	}
	t.Setenv("SOCIALD_API_TOKEN", strings.Repeat("t", 32))
}

func TestNoneBotTransportIsDefaultEvenWhenRuntimeIsMissing(t *testing.T) {
	clearNoneBotTestEnvironment(t)
	cfg, err := config.LoadFromEnv()
	if err != nil || cfg.EffectiveFeishuTransport() != "nonebot" || cfg.NoneBot.Configured() || cfg.TelegramBot.Configured() {
		t.Fatal("missing runtime silently selected legacy transport")
	}
	for _, transport := range []string{"nonebot", "legacy-go"} {
		t.Setenv("SOCIALD_FEISHU_TRANSPORT", transport)
		cfg, err = config.LoadFromEnv()
		if err != nil || cfg.EffectiveFeishuTransport() != transport {
			t.Fatal("explicit transport not respected")
		}
	}
	for _, transport := range []string{"go", "auto", "NONEBOT", " legacy-go", "legacy-go\n"} {
		t.Setenv("SOCIALD_FEISHU_TRANSPORT", transport)
		if _, err = config.LoadFromEnv(); err == nil {
			t.Fatal("accepted ambiguous or fallback transport")
		}
	}
}

func TestNoneBotEnvironmentIsPairedBoundedAndRedacted(t *testing.T) {
	clearNoneBotTestEnvironment(t)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	worker := filepath.Join(t.TempDir(), "synthetic-private-worker.py")
	t.Setenv("SOCIALD_NONEBOT_PYTHON", binary)
	if _, err = config.LoadFromEnv(); err == nil {
		t.Fatal("accepted partial runtime")
	}
	t.Setenv("SOCIALD_NONEBOT_WORKER", worker)
	t.Setenv("SOCIALD_NONEBOT_TIMEOUT_SECONDS", "25")
	cfg, err := config.LoadFromEnv()
	if err != nil || cfg.NoneBot.Timeout != 25*time.Second || cfg.NoneBot.PythonExecutable != binary || cfg.NoneBot.WorkerScript != worker {
		t.Fatal("runtime environment not loaded")
	}
	for _, value := range []any{cfg, cfg.NoneBot} {
		for _, format := range []string{"%v", "%+v", "%#v"} {
			if strings.Contains(fmt.Sprintf(format, value), worker) {
				t.Fatal("worker path leaked in configuration diagnostic")
			}
		}
	}
	for _, timeout := range []string{"0", "61", "1.5", "not-a-duration"} {
		t.Setenv("SOCIALD_NONEBOT_TIMEOUT_SECONDS", timeout)
		if _, err = config.LoadFromEnv(); err == nil {
			t.Fatal("accepted unbounded worker timeout")
		}
	}
	t.Setenv("SOCIALD_NONEBOT_TIMEOUT_SECONDS", "25")
	t.Setenv("SOCIALD_NONEBOT_WORKER", "relative.py")
	if _, err = config.LoadFromEnv(); err == nil {
		t.Fatal("accepted relative worker")
	}
}

func TestTelegramBotEnvironmentRequiresExactAllowlistWithoutExposingIt(t *testing.T) {
	clearNoneBotTestEnvironment(t)
	token := "123456:" + strings.Repeat("SyntheticOnly_", 3)
	t.Setenv("TELEGRAM_BOT_TOKEN", token)
	t.Setenv("TELEGRAM_BOT_ID", "123456")
	if _, err := config.LoadFromEnv(); err == nil {
		t.Fatal("accepted bot without receive allowlist")
	}
	t.Setenv("TELEGRAM_BOT_ALLOWED_PRIVATE_CHAT_IDS", "700001,700002")
	t.Setenv("TELEGRAM_BOT_LIMIT", "100")
	cfg, err := config.LoadFromEnv()
	if err != nil || len(cfg.TelegramBot.AllowedPrivateChatIDs) != 2 || cfg.TelegramBot.Limit != 100 || cfg.TelegramBot.Token != token || cfg.NoneBot.Configured() {
		t.Fatal("complete bot config should load without pretending runtime is configured")
	}
	for _, value := range []any{cfg, cfg.TelegramBot} {
		for _, format := range []string{"%v", "%+v", "%#v"} {
			text := fmt.Sprintf(format, value)
			if strings.Contains(text, token) || strings.Contains(text, "700001") {
				t.Fatal("bot secret or allowlist leaked")
			}
		}
	}
	for _, ids := range []string{"700001,", ",700001", "700001, 700002", "700001,700001", "0700001", "-700001", "0"} {
		t.Setenv("TELEGRAM_BOT_ALLOWED_PRIVATE_CHAT_IDS", ids)
		if _, err = config.LoadFromEnv(); err == nil || strings.Contains(err.Error(), token) {
			t.Fatal("invalid receive allowlist accepted or secret exposed")
		}
	}
	t.Setenv("TELEGRAM_BOT_ALLOWED_PRIVATE_CHAT_IDS", "700001")
	for _, limit := range []string{"0", "101", "1.5", "invalid"} {
		t.Setenv("TELEGRAM_BOT_LIMIT", limit)
		if _, err = config.LoadFromEnv(); err == nil {
			t.Fatal("invalid batch limit accepted")
		}
	}
}

func TestNoneBotProgrammaticConfigurationValidatesBeforeStartup(t *testing.T) {
	base := config.Config{Addr: config.DefaultAddr, APIToken: strings.Repeat("t", 32)}
	for _, mutation := range []func(*config.Config){
		func(c *config.Config) { c.NoneBot = nonebotbridge.Config{WorkerScript: "relative.py"} },
		func(c *config.Config) { c.TelegramBot = telegrambot.Config{Token: "synthetic-private-token"} },
		func(c *config.Config) { c.FeishuTransport = "fallback" },
	} {
		cfg := base
		mutation(&cfg)
		if cfg.Validate() == nil {
			t.Fatal("unsafe programmatic configuration bypassed validation")
		}
	}
}

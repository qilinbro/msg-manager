// Package config validates the local service boundary before any listener opens.
package config

import (
	"encoding/hex"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/qilinbro/msg-manager/internal/agent"
	"github.com/qilinbro/msg-manager/internal/connector/adapter"
	"github.com/qilinbro/msg-manager/internal/connector/mailbox"
	"github.com/qilinbro/msg-manager/internal/connector/nonebotbridge"
	"github.com/qilinbro/msg-manager/internal/connector/qqmilky"
	"github.com/qilinbro/msg-manager/internal/connector/telegrambot"
	"github.com/qilinbro/msg-manager/internal/connector/wechatdb"
	"github.com/qilinbro/msg-manager/internal/publicinfo"
)

const DefaultAddr = "127.0.0.1:17890"

type Config struct {
	Addr             string
	APIToken         string
	QQMilky          qqmilky.Config
	NoneBot          nonebotbridge.Config
	TelegramBot      telegrambot.Config
	FeishuTransport  string
	Email            mailbox.Config
	DatabasePath     string
	DatabaseKey      string
	AI               agent.Config
	AIDailyLimit     int
	WechatDB         wechatdb.Config
	PublicSearch     publicinfo.Config
	SearchDailyLimit int
}

func (Config) String() string   { return "sociald configuration (redacted)" }
func (Config) GoString() string { return "sociald configuration (redacted)" }

func LoadFromEnv() (Config, error) {
	addr := os.Getenv("SOCIALD_ADDR")
	if addr == "" {
		addr = DefaultAddr
	}
	cfg := Config{
		Addr: addr, APIToken: os.Getenv("SOCIALD_API_TOKEN"),
		DatabasePath: os.Getenv("SOCIALD_DATABASE_PATH"), DatabaseKey: os.Getenv("SOCIALD_DATABASE_KEY"),
		QQMilky: qqmilky.Config{Endpoint: adapter.Endpoint{
			URL:            os.Getenv("QQ_MILKY_URL"),
			AccessToken:    os.Getenv("QQ_MILKY_ACCESS_TOKEN"),
			ExpectedSelfID: os.Getenv("QQ_MILKY_ACCOUNT_ID"),
		}},
		NoneBot:         nonebotbridge.Config{PythonExecutable: os.Getenv("SOCIALD_NONEBOT_PYTHON"), WorkerScript: os.Getenv("SOCIALD_NONEBOT_WORKER")},
		TelegramBot:     telegrambot.Config{Token: os.Getenv("TELEGRAM_BOT_TOKEN"), ExpectedBotID: os.Getenv("TELEGRAM_BOT_ID")},
		FeishuTransport: os.Getenv("SOCIALD_FEISHU_TRANSPORT"),
		Email:           mailbox.Config{Host: os.Getenv("EMAIL_IMAP_HOST"), Username: os.Getenv("EMAIL_IMAP_USERNAME"), Password: os.Getenv("EMAIL_IMAP_PASSWORD"), Folder: os.Getenv("EMAIL_IMAP_FOLDER")},
		AI:              agent.Config{BaseURL: os.Getenv("AI_BASE_URL"), APIKey: os.Getenv("AI_API_KEY"), Model: os.Getenv("AI_MODEL")},
		WechatDB:        wechatdb.Config{Directory: os.Getenv("WECHAT_DB_DIR"), AccountID: os.Getenv("WECHAT_DB_ACCOUNT_ID"), Key: os.Getenv("WECHAT_DB_KEY")},
		PublicSearch:    publicinfo.Config{APIKey: os.Getenv("TAVILY_API_KEY")},
	}
	if value := os.Getenv("TELEGRAM_BOT_ALLOWED_PRIVATE_CHAT_IDS"); value != "" {
		// Do not silently normalize IDs, whitespace or empty list members.
		cfg.TelegramBot.AllowedPrivateChatIDs = strings.Split(value, ",")
	}
	if value := os.Getenv("TELEGRAM_BOT_LIMIT"); value != "" {
		limit, err := strconv.Atoi(value)
		if err != nil || limit < 1 || limit > telegrambot.MaxLimit {
			return Config{}, errors.New("TELEGRAM_BOT_LIMIT must be between 1 and 100")
		}
		cfg.TelegramBot.Limit = limit
	}
	if value := os.Getenv("SOCIALD_NONEBOT_TIMEOUT_SECONDS"); value != "" {
		seconds, err := strconv.Atoi(value)
		if err != nil || seconds < 1 || seconds > 60 {
			return Config{}, errors.New("SOCIALD_NONEBOT_TIMEOUT_SECONDS must be between 1 and 60")
		}
		cfg.NoneBot.Timeout = time.Duration(seconds) * time.Second
	}
	if value := os.Getenv("QQ_MILKY_HISTORY_LIMIT"); value != "" {
		limit, err := strconv.Atoi(value)
		if err != nil || limit < 1 || limit > qqmilky.MaxHistoryLimit {
			return Config{}, errors.New("QQ_MILKY_HISTORY_LIMIT must be between 1 and 30")
		}
		cfg.QQMilky.HistoryLimit = limit
	}
	if value := os.Getenv("WECHAT_DB_LIMIT"); value != "" {
		limit, err := strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 500 {
			return Config{}, errors.New("WECHAT_DB_LIMIT must be between 1 and 500")
		}
		cfg.WechatDB.Limit = limit
	}
	if value := os.Getenv("EMAIL_IMAP_PORT"); value != "" {
		port, err := strconv.Atoi(value)
		if err != nil {
			return Config{}, errors.New("EMAIL_IMAP_PORT must be a valid TLS port")
		}
		cfg.Email.Port = port
	}
	if value := os.Getenv("EMAIL_IMAP_LIMIT"); value != "" {
		limit, err := strconv.Atoi(value)
		if err != nil {
			return Config{}, errors.New("EMAIL_IMAP_LIMIT must be between 1 and 100")
		}
		cfg.Email.Limit = limit
	}
	if value := os.Getenv("AI_DAILY_LIMIT"); value != "" {
		limit, err := strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 1000 {
			return Config{}, errors.New("AI_DAILY_LIMIT must be between 1 and 1000")
		}
		cfg.AIDailyLimit = limit
	}
	if value := os.Getenv("PUBLIC_SEARCH_DAILY_LIMIT"); value != "" {
		limit, err := strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 1000 {
			return Config{}, errors.New("PUBLIC_SEARCH_DAILY_LIMIT must be between 1 and 1000")
		}
		cfg.SearchDailyLimit = limit
	}
	return cfg, cfg.Validate()
}

// EffectiveFeishuTransport never falls back after a NoneBot failure. The legacy
// Go sender is available only through an explicit operator compatibility choice.
func (c Config) EffectiveFeishuTransport() string {
	if c.FeishuTransport == "" {
		return "nonebot"
	}
	return c.FeishuTransport
}

func (c Config) Validate() error {
	if c.NoneBot.Validate() != nil {
		return errors.New("NoneBot runtime configuration is invalid; provide paired absolute local Python and worker paths with a bounded timeout")
	}
	if telegrambot.ValidateConfig(c.TelegramBot) != nil {
		return errors.New("Telegram bot configuration is invalid; provide the expected bot identity and an explicit private-chat allowlist")
	}
	if transport := c.EffectiveFeishuTransport(); transport != "nonebot" && transport != "legacy-go" {
		return errors.New("SOCIALD_FEISHU_TRANSPORT must be nonebot or legacy-go; automatic fallback is disabled")
	}
	if publicinfo.ValidateConfig(c.PublicSearch) != nil || c.SearchDailyLimit < 0 || c.SearchDailyLimit > 1000 {
		return errors.New("public search configuration is invalid; check the locally configured key and daily limit")
	}
	if c.DatabasePath != "" || c.DatabaseKey != "" {
		key, keyErr := hex.DecodeString(c.DatabaseKey)
		if !filepath.IsAbs(c.DatabasePath) || strings.HasPrefix(c.DatabasePath, `\\`) || len(key) != 32 || keyErr != nil {
			return errors.New("SOCIALD_DATABASE_PATH must be an absolute local path and SOCIALD_DATABASE_KEY must contain 64 hex characters")
		}
	}
	if len(c.APIToken) < 32 || len(c.APIToken) > 4096 {
		return errors.New("SOCIALD_API_TOKEN must contain 32 to 4096 printable ASCII characters")
	}
	for _, ch := range c.APIToken {
		if ch < 33 || ch > 126 {
			return errors.New("SOCIALD_API_TOKEN must contain printable ASCII characters without whitespace")
		}
	}
	host, port, err := net.SplitHostPort(c.Addr)
	if err != nil {
		return errors.New("SOCIALD_ADDR must be a loopback IP address and port")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("SOCIALD_ADDR must use a literal loopback IP; wildcard and remote listeners are disabled")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
		return errors.New("SOCIALD_ADDR must use a port between 1 and 65535")
	}
	return nil
}

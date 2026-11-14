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

	"github.com/qilinbro/msg-manager/internal/agent"
	"github.com/qilinbro/msg-manager/internal/connector/mailbox"
	"github.com/qilinbro/msg-manager/internal/connector/onebot"
	"github.com/qilinbro/msg-manager/internal/connector/wechatdb"
)

const DefaultAddr = "127.0.0.1:17890"

type Config struct {
	Addr         string
	APIToken     string
	QQOneBot     onebot.Config
	Email        mailbox.Config
	DatabasePath string
	DatabaseKey  string
	AI           agent.Config
	AIDailyLimit int
	WechatDB     wechatdb.Config
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
		QQOneBot: onebot.Config{
			URL:            os.Getenv("QQ_ONEBOT_URL"),
			AccessToken:    os.Getenv("QQ_ONEBOT_ACCESS_TOKEN"),
			ExpectedSelfID: os.Getenv("QQ_ONEBOT_ACCOUNT_ID"),
		},
		Email:    mailbox.Config{Host: os.Getenv("EMAIL_IMAP_HOST"), Username: os.Getenv("EMAIL_IMAP_USERNAME"), Password: os.Getenv("EMAIL_IMAP_PASSWORD"), Folder: os.Getenv("EMAIL_IMAP_FOLDER")},
		AI:       agent.Config{BaseURL: os.Getenv("AI_BASE_URL"), APIKey: os.Getenv("AI_API_KEY"), Model: os.Getenv("AI_MODEL")},
		WechatDB: wechatdb.Config{Directory: os.Getenv("WECHAT_DB_DIR"), AccountID: os.Getenv("WECHAT_DB_ACCOUNT_ID"), Key: os.Getenv("WECHAT_DB_KEY")},
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
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
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

// Package config validates the local service boundary before any listener opens.
package config

import (
	"errors"
	"net"
	"os"
	"strconv"

	"github.com/qilinbro/msg-manager/internal/connector/onebot"
)

const DefaultAddr = "127.0.0.1:17890"

type Config struct {
	Addr     string
	APIToken string
	QQOneBot onebot.Config
}

func LoadFromEnv() (Config, error) {
	addr := os.Getenv("SOCIALD_ADDR")
	if addr == "" {
		addr = DefaultAddr
	}
	cfg := Config{
		Addr: addr, APIToken: os.Getenv("SOCIALD_API_TOKEN"),
		QQOneBot: onebot.Config{
			URL:            os.Getenv("QQ_ONEBOT_URL"),
			AccessToken:    os.Getenv("QQ_ONEBOT_ACCESS_TOKEN"),
			ExpectedSelfID: os.Getenv("QQ_ONEBOT_ACCOUNT_ID"),
		},
	}
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
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

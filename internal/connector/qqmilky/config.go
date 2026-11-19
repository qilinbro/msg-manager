// Package qqmilky adapts a locally deployed Milky 1.2 or 1.3 protocol endpoint
// for the user's own QQ account. It never starts the provider, never logs in
// and never exposes platform credentials to the browser or the model.
package qqmilky

import (
	"time"

	"github.com/qilinbro/msg-manager/internal/connector/adapter"
)

const DefaultMilkyVersion = "1.3"
const MilkyVersion12 = "1.2"
const MilkyVersion13 = "1.3"
const DefaultHistoryLimit = 20
const MaxHistoryLimit = 30

// SupportedVersions are explicit configuration values, not a silent fallback.
// Milky guarantees old clients against new providers, not the reverse, so a
// 1.3-configured client never accepts a 1.2 provider.
func SupportedVersions() []string { return []string{MilkyVersion12, MilkyVersion13} }

type Config struct {
	adapter.Endpoint
	MilkyVersion string
	HistoryLimit int
}

func (Config) String() string   { return "QQ Milky configuration (redacted)" }
func (Config) GoString() string { return "QQ Milky configuration (redacted)" }

func (c Config) normalized() Config {
	if c.MilkyVersion == "" {
		c.MilkyVersion = DefaultMilkyVersion
	}
	if c.HistoryLimit == 0 {
		c.HistoryLimit = DefaultHistoryLimit
	}
	if c.Timeout == 0 {
		c.Timeout = 8 * time.Second
	}
	return c
}

func (c Config) valid() bool {
	supported := false
	for _, version := range SupportedVersions() {
		if c.MilkyVersion == version {
			supported = true
			break
		}
	}
	return supported && c.HistoryLimit >= 1 && c.HistoryLimit <= MaxHistoryLimit
}

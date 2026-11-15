package config

import (
	"fmt"
	"strings"
	"testing"

	"github.com/qilinbro/msg-manager/internal/publicinfo"
)

func TestPublicSearchEnvOptionalLimitsAndRedaction(t *testing.T) {
	t.Setenv("SOCIALD_API_TOKEN", strings.Repeat("x", 32))
	t.Setenv("TAVILY_API_KEY", "synthetic-search-key")
	t.Setenv("PUBLIC_SEARCH_DAILY_LIMIT", "20")
	cfg, err := LoadFromEnv()
	if err != nil || cfg.PublicSearch.APIKey != "synthetic-search-key" || cfg.SearchDailyLimit != 20 {
		t.Fatal("search environment not configured", err)
	}
	if strings.Contains(fmt.Sprintf("%+v %#v", cfg, cfg), "synthetic-search-key") {
		t.Fatal("search credential escaped redacted configuration")
	}
	for _, value := range []string{"0", "1001", "invalid", "-1"} {
		t.Setenv("PUBLIC_SEARCH_DAILY_LIMIT", value)
		if _, err := LoadFromEnv(); err == nil {
			t.Fatal("invalid search budget accepted")
		}
	}
	t.Setenv("PUBLIC_SEARCH_DAILY_LIMIT", "")
	t.Setenv("TAVILY_API_KEY", "")
	if _, err := LoadFromEnv(); err != nil {
		t.Fatal("disabled search blocked startup", err)
	}
	for _, bad := range []Config{
		{Addr: DefaultAddr, APIToken: strings.Repeat("x", 32), SearchDailyLimit: -1},
		{Addr: DefaultAddr, APIToken: strings.Repeat("x", 32), PublicSearch: publicinfo.Config{APIKey: "unsafe\nkey"}},
	} {
		if bad.Validate() == nil {
			t.Fatal("invalid programmatic search configuration accepted")
		}
	}
}

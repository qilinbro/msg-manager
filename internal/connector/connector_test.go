package connector_test

import (
	"testing"

	"github.com/qilinbro/msg-manager/internal/connector"
)

func TestQQCatalogUsesOnlyMilky(t *testing.T) {
	qq := 0
	for _, item := range connector.Planned() {
		if item.Platform != "qq" {
			continue
		}
		qq++
		if item.ID != "qq-milky" || item.Capabilities.SendText {
			t.Fatalf("unexpected QQ connector or enabled sending: %#v", item)
		}
	}
	if qq != 1 {
		t.Fatalf("expected one Milky QQ connector, got %d", qq)
	}
}

func TestTelegramCatalogUsesPersonalReadOnlyAccount(t *testing.T) {
	count := 0
	for _, item := range connector.Planned() {
		if item.Platform != "telegram" {
			continue
		}
		count++
		if item.ID != "telegram-user" || item.ImplementationStatus != "manual_sync_ready" || item.Capabilities.SendText || item.Capabilities.ReceiveText {
			t.Fatal("unexpected Telegram capability", item)
		}
	}
	if count != 1 {
		t.Fatal("expected one personal account connector")
	}
}

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

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qilinbro/msg-manager/internal/config"
	"github.com/qilinbro/msg-manager/internal/connector"
)

func TestMilkyOnlyConnectionCatalogAndRetiredRoute(t *testing.T) {
	const token = "synthetic-milky-only-local-api-token"
	h, err := NewHandler(config.Config{Addr: config.DefaultAddr, APIToken: token})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.(*handler).Close() })
	for _, path := range []string{"/api/v1/system", "/api/v1/connections"} {
		r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:17890"+path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "qq-onebot") {
			t.Fatalf("catalog failed or advertised the retired connector: status=%d", w.Code)
		}
		var response struct {
			Items      []connector.Descriptor `json:"items"`
			Connectors []connector.Descriptor `json:"connectors"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		items := append(response.Items, response.Connectors...)
		qq := 0
		for _, item := range items {
			if item.Platform == "qq" {
				qq++
				if item.ID != "qq-milky" || item.Capabilities.SendText {
					t.Fatalf("unexpected QQ descriptor: %#v", item)
				}
			}
		}
		if qq != 1 {
			t.Fatalf("expected exactly one QQ connector, got %d", qq)
		}
	}
	for _, authenticated := range []bool{false, true} {
		r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:17890/api/v1/connections/qq-onebot/verify", nil)
		if authenticated {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := http.StatusUnauthorized
		if authenticated {
			want = http.StatusNotFound
		}
		if w.Code != want {
			t.Fatalf("retired route: authenticated=%v status=%d, want %d", authenticated, w.Code, want)
		}
	}
}

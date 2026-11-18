package integration_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestReceiveMonitorRoutesRequireLocalAuthorization(t *testing.T) {
	handler := newHandler(t)
	for _, path := range []string{"/api/v1/connections/email-imap/monitor", "/api/v1/connections/telegram-user/monitor"} {
		t.Run(path, func(t *testing.T) {
			checkError(t, request(handler, http.MethodGet, path, nil), http.StatusUnauthorized, "UNAUTHORIZED")
			checkError(t, request(handler, http.MethodPost, path, nil), http.StatusUnauthorized, "UNAUTHORIZED")
			checkError(t, inboxRequest(handler, http.MethodPatch, path, `{}`), http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
			response := inboxRequest(handler, http.MethodGet, path, nil)
			var status struct {
				Enabled        bool `json:"enabled"`
				ReadOnly       bool `json:"read_only"`
				SendingEnabled bool `json:"sending_enabled"`
			}
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &status) != nil || status.Enabled || !status.ReadOnly || status.SendingEnabled {
				t.Fatal("unconfigured read-only monitor status", response.Code, response.Body.String())
			}
			preflight := map[string]string{
				"Origin":                         "http://127.0.0.1:1420",
				"Access-Control-Request-Method":  "POST",
				"Access-Control-Request-Headers": "authorization,content-type",
			}
			if response := request(handler, http.MethodOptions, path, preflight); response.Code != http.StatusNoContent {
				t.Fatal("authorized UI cannot preflight", response.Code)
			}
			preflight["Origin"] = "https://not-the-local-workbench.example"
			checkError(t, request(handler, http.MethodOptions, path, preflight), http.StatusForbidden, "ORIGIN_DENIED")
		})
	}
}

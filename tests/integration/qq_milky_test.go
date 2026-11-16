package integration_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/qilinbro/msg-manager/internal/api"
	"github.com/qilinbro/msg-manager/internal/config"
	"github.com/qilinbro/msg-manager/internal/connector/adapter"
	"github.com/qilinbro/msg-manager/internal/connector/qqmilky"
)

const milkyToken = "synthetic-milky-token"
const milkyAccount = "123456789"

func milkyProvider(t *testing.T) *httptest.Server {
	t.Helper()
	return milkyProviderWithHistory(t, `{"messages":[],"next_message_seq":null}`)
}

func milkyProviderWithHistory(t *testing.T, history string) *httptest.Server {
	return milkyProviderWithHistoryAndEventPeer(t, history, "987654321")
}

func milkyProviderWithHistoryAndEventPeer(t *testing.T, history, eventPeer string) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/event" {
			if r.Header.Get("Authorization") != "Bearer "+milkyToken {
				http.Error(w, "denied", http.StatusUnauthorized)
				return
			}
			conn, err := upgrader.Upgrade(w, r, nil)
			if err == nil {
				defer conn.Close()
				payload := fmt.Sprintf(`{"event_type":"message_receive","time":1730000000,"self_id":123456789,"data":{"message_scene":"friend","peer_id":%s,"message_seq":9,"sender_id":%s,"time":1730000000,"segments":[{"type":"text","data":{"text":"实时文本"}}]}}`, eventPeer, eventPeer)
				_ = conn.WriteMessage(websocket.TextMessage, []byte(payload))
			}
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+milkyToken {
			http.Error(w, "denied", http.StatusUnauthorized)
			return
		}
		var body string
		switch r.URL.Path {
		case "/api/get_login_info":
			body = `{"status":"ok","retcode":0,"data":{"uin":123456789,"nickname":"self"}}`
		case "/api/get_impl_info":
			body = `{"status":"ok","retcode":0,"data":{"impl_name":"Lagrange.Milky","impl_version":"test","milky_version":"1.3","qq_protocol_type":"linux"}}`
		case "/api/get_history_messages":
			body = fmt.Sprintf(`{"status":"ok","retcode":0,"data":%s}`, history)
		default:
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
}

func milkyHandler(t *testing.T, providerURL string) http.Handler {
	t.Helper()
	h, err := api.NewHandler(config.Config{
		Addr: config.DefaultAddr, APIToken: token,
		QQMilky: qqmilky.Config{Endpoint: adapter.Endpoint{
			URL: providerURL, AccessToken: milkyToken, ExpectedSelfID: milkyAccount, Timeout: 3 * time.Second,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closer, ok := h.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
	})
	return h
}

func milkyHandlerWithStore(t *testing.T, providerURL string) http.Handler {
	t.Helper()
	h, err := api.NewHandler(config.Config{
		Addr: config.DefaultAddr, APIToken: token,
		DatabasePath: filepath.Join(t.TempDir(), "msg.db"),
		DatabaseKey:  strings.Repeat("0123456789abcdef", 4),
		QQMilky: qqmilky.Config{Endpoint: adapter.Endpoint{
			URL: providerURL, AccessToken: milkyToken, ExpectedSelfID: milkyAccount, Timeout: 3 * time.Second,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closer, ok := h.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
	})
	return h
}

func milkyRequest(h http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "127.0.0.1:17890"
	req.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, req)
	return recorder
}

func TestMilkyRequiresLocalAuthAndKeepsSendingDisabled(t *testing.T) {
	provider := milkyProvider(t)
	defer provider.Close()
	h := milkyHandler(t, provider.URL)
	if recorder := milkyRequest(h, http.MethodGet, "/api/v1/connections/qq-milky/status", "", map[string]string{"Authorization": "Bearer wrong"}); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", recorder.Code)
	}
	if recorder := milkyRequest(h, http.MethodGet, "/api/v1/connections/qq-milky/status", "", map[string]string{"Origin": "https://evil.example"}); recorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", recorder.Code)
	}
	if recorder := milkyRequest(h, http.MethodPost, "/api/v1/send", `{}`, nil); recorder.Code != http.StatusNotImplemented {
		t.Fatalf("sending must remain 501, got %d", recorder.Code)
	}
	status := milkyRequest(h, http.MethodGet, "/api/v1/connections/qq-milky/status", "", nil)
	if status.Code != http.StatusOK {
		t.Fatalf("status: %d %s", status.Code, status.Body.String())
	}
	var payload struct {
		Configured     bool `json:"configured"`
		SendingEnabled bool `json:"sending_enabled"`
		ReadOnly       bool `json:"read_only"`
	}
	if err := json.Unmarshal(status.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Configured || payload.SendingEnabled || !payload.ReadOnly {
		t.Fatalf("unexpected status: %+v", payload)
	}
}

func TestMilkyVerificationAndRejectionsNeverLeakCredentials(t *testing.T) {
	provider := milkyProvider(t)
	defer provider.Close()
	h := milkyHandler(t, provider.URL)
	verification := milkyRequest(h, http.MethodGet, "/api/v1/connections/qq-milky/verify", "", nil)
	if verification.Code != http.StatusOK {
		t.Fatalf("verify: %d %s", verification.Code, verification.Body.String())
	}
	var payload struct {
		ConnectionID   string `json:"connection_id"`
		AccountID      string `json:"account_id"`
		MilkyVersion   string `json:"milky_version"`
		SendingEnabled bool   `json:"sending_enabled"`
	}
	if err := json.Unmarshal(verification.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ConnectionID != "qq-milky" || payload.AccountID != milkyAccount || payload.MilkyVersion != "1.3" || payload.SendingEnabled {
		t.Fatalf("unexpected verification: %+v", payload)
	}
	if strings.Contains(verification.Body.String(), milkyToken) || strings.Contains(verification.Body.String(), provider.URL) {
		t.Fatal("provider credentials leaked")
	}
	// Storage is disabled in this handler, so sync must refuse rather than pretend.
	if recorder := milkyRequest(h, http.MethodPost, "/api/v1/connections/qq-milky/sync", `{"peer_id":"987654321"}`, nil); recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected storage refusal, got %d %s", recorder.Code, recorder.Body.String())
	}
	// Invalid peer IDs and query strings are rejected before any provider call.
	for _, body := range []string{`{"peer_id":"../../etc"}`, `{"peer_id":"987654321","limit":1000}`} {
		if recorder := milkyRequest(h, http.MethodPost, "/api/v1/connections/qq-milky/sync", body, nil); recorder.Code != http.StatusBadRequest && recorder.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected rejection for %s, got %d", body, recorder.Code)
		}
	}
}

func TestMilkyMonitorRequiresVerificationAndCanBeStopped(t *testing.T) {
	provider := milkyProvider(t)
	defer provider.Close()
	h := milkyHandlerWithStore(t, provider.URL)
	// Before verification the monitor refuses to start.
	if recorder := milkyRequest(h, http.MethodPost, "/api/v1/connections/qq-milky/monitor", `{"enabled":true,"peer_ids":["987654321"]}`, nil); recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected MILKY_NOT_VERIFIED, got %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder := milkyRequest(h, http.MethodGet, "/api/v1/connections/qq-milky/verify", "", nil); recorder.Code != http.StatusOK {
		t.Fatalf("verify failed: %s", recorder.Body.String())
	}
	started := milkyRequest(h, http.MethodPost, "/api/v1/connections/qq-milky/monitor", `{"enabled":true,"peer_ids":["987654321"]}`, nil)
	if started.Code != http.StatusOK {
		t.Fatalf("monitor start: %d %s", started.Code, started.Body.String())
	}
	var payload struct {
		ReceivingEnabled bool `json:"receiving_enabled"`
		Monitor          struct {
			Enabled bool `json:"enabled"`
		} `json:"monitor"`
	}
	if err := json.Unmarshal(started.Body.Bytes(), &payload); err != nil || !payload.ReceivingEnabled || !payload.Monitor.Enabled {
		t.Fatalf("unexpected monitor state: %+v %v", payload, err)
	}
	stopped := milkyRequest(h, http.MethodPost, "/api/v1/connections/qq-milky/monitor", `{"enabled":false,"peer_ids":[]}`, nil)
	if stopped.Code != http.StatusOK {
		t.Fatalf("monitor stop: %d %s", stopped.Code, stopped.Body.String())
	}
	if err := json.Unmarshal(stopped.Body.Bytes(), &payload); err != nil || payload.ReceivingEnabled || payload.Monitor.Enabled {
		t.Fatalf("monitor did not stop: %+v %v", payload, err)
	}
}

func TestMilkyMonitorIgnoresUnselectedRealtimePeer(t *testing.T) {
	provider := milkyProviderWithHistoryAndEventPeer(t, `{"messages":[],"next_message_seq":null}`, "111111111")
	defer provider.Close()
	h := milkyHandlerWithStore(t, provider.URL)
	if recorder := milkyRequest(h, http.MethodGet, "/api/v1/connections/qq-milky/verify", "", nil); recorder.Code != http.StatusOK {
		t.Fatalf("verify: %d %s", recorder.Code, recorder.Body.String())
	}
	started := milkyRequest(h, http.MethodPost, "/api/v1/connections/qq-milky/monitor", `{"enabled":true,"peer_ids":["987654321"]}`, nil)
	if started.Code != http.StatusOK {
		t.Fatalf("monitor start: %d %s", started.Code, started.Body.String())
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status := milkyRequest(h, http.MethodGet, "/api/v1/connections/qq-milky/status", "", nil)
		if strings.Contains(status.Body.String(), "ignored:unselected_peer") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	conversations := milkyRequest(h, http.MethodGet, "/api/v1/conversations", "", nil)
	if conversations.Code != http.StatusOK {
		t.Fatalf("conversations: %d %s", conversations.Code, conversations.Body.String())
	}
	if strings.Contains(conversations.Body.String(), "实时文本") {
		t.Fatal("unselected realtime peer was persisted")
	}
}

func TestMilkyMonitorAuthorizationSurvivesRestartAndWaitsForVerification(t *testing.T) {
	provider := milkyProvider(t)
	defer provider.Close()
	dbPath := filepath.Join(t.TempDir(), "persisted-milky-monitor.db")
	cfg := config.Config{
		Addr: config.DefaultAddr, APIToken: token,
		DatabasePath: dbPath, DatabaseKey: strings.Repeat("0123456789abcdef", 4),
		QQMilky: qqmilky.Config{Endpoint: adapter.Endpoint{
			URL: provider.URL, AccessToken: milkyToken, ExpectedSelfID: milkyAccount, Timeout: 3 * time.Second,
		}},
	}
	h1, err := api.NewHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if recorder := milkyRequest(h1, http.MethodGet, "/api/v1/connections/qq-milky/verify", "", nil); recorder.Code != http.StatusOK {
		t.Fatalf("first verify: %d %s", recorder.Code, recorder.Body.String())
	}
	started := milkyRequest(h1, http.MethodPost, "/api/v1/connections/qq-milky/monitor", `{"enabled":true,"peer_ids":["987654321"]}`, nil)
	if started.Code != http.StatusOK {
		t.Fatalf("first monitor start: %d %s", started.Code, started.Body.String())
	}
	if closer, ok := h1.(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil {
			t.Fatal(err)
		}
	}

	h2, err := api.NewHandler(cfg)
	if err != nil {
		t.Fatal("reopen handler", err)
	}
	defer func() {
		if closer, ok := h2.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
	}()
	pending := milkyRequest(h2, http.MethodGet, "/api/v1/connections/qq-milky/status", "", nil)
	if pending.Code != http.StatusOK || !strings.Contains(pending.Body.String(), `"state":"pending_verification"`) || strings.Contains(pending.Body.String(), `"receiving_enabled":true`) {
		t.Fatalf("saved monitor was not held pending verification: %d %s", pending.Code, pending.Body.String())
	}
	if recorder := milkyRequest(h2, http.MethodGet, "/api/v1/connections/qq-milky/verify", "", nil); recorder.Code != http.StatusOK {
		t.Fatalf("second verify: %d %s", recorder.Code, recorder.Body.String())
	}
	running := milkyRequest(h2, http.MethodGet, "/api/v1/connections/qq-milky/status", "", nil)
	if running.Code != http.StatusOK || !strings.Contains(running.Body.String(), `"state":"running"`) || !strings.Contains(running.Body.String(), `"receiving_enabled":true`) {
		t.Fatalf("saved monitor did not resume after verification: %d %s", running.Code, running.Body.String())
	}
}

func TestMilkySyncPersistsAndDeduplicates(t *testing.T) {
	history := `{"messages":[{"message_scene":"friend","peer_id":987654321,"message_seq":9007199254740993,"sender_id":987654321,"time":1730000000,"segments":[{"type":"text","data":{"text":"真实同步文本"}}]}],"next_message_seq":9007199254740992}`
	provider := milkyProviderWithHistory(t, history)
	defer provider.Close()
	h := milkyHandlerWithStore(t, provider.URL)
	if recorder := milkyRequest(h, http.MethodGet, "/api/v1/connections/qq-milky/verify", "", nil); recorder.Code != http.StatusOK {
		t.Fatalf("verify: %s", recorder.Body.String())
	}
	first := milkyRequest(h, http.MethodPost, "/api/v1/connections/qq-milky/sync", `{"peer_id":"987654321"}`, nil)
	if first.Code != http.StatusOK {
		t.Fatalf("sync: %d %s", first.Code, first.Body.String())
	}
	var result struct {
		Inserted       int    `json:"inserted"`
		Duplicates     int    `json:"duplicates"`
		SourceKind     string `json:"source_kind"`
		SendingEnabled bool   `json:"sending_enabled"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Inserted != 1 || result.SourceKind != "protocol_sync" || result.SendingEnabled {
		t.Fatalf("unexpected sync result: %+v", result)
	}
	second := milkyRequest(h, http.MethodPost, "/api/v1/connections/qq-milky/sync", `{"peer_id":"987654321"}`, nil)
	if second.Code != http.StatusOK {
		t.Fatalf("re-sync: %d %s", second.Code, second.Body.String())
	}
	if err := json.Unmarshal(second.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Inserted != 0 || result.Duplicates != 1 {
		t.Fatalf("repeat sync must deduplicate: %+v", result)
	}
	conversations := milkyRequest(h, http.MethodGet, "/api/v1/conversations", "", nil)
	if conversations.Code != http.StatusOK || !strings.Contains(conversations.Body.String(), "真实同步文本") {
		t.Fatalf("synced message missing from inbox: %d %s", conversations.Code, conversations.Body.String())
	}
	messages := milkyRequest(h, http.MethodGet, "/api/v1/inbox/connections", "", nil)
	if messages.Code != http.StatusOK || !strings.Contains(messages.Body.String(), "qq") {
		t.Fatalf("connection missing: %d %s", messages.Code, messages.Body.String())
	}
}

func TestMilkyConnectorAppearsInSystemStatus(t *testing.T) {
	provider := milkyProvider(t)
	defer provider.Close()
	h := milkyHandler(t, provider.URL)
	recorder := milkyRequest(h, http.MethodGet, "/api/v1/system", "", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("system: %d", recorder.Code)
	}
	var payload struct {
		Connectors []struct {
			ID           string `json:"id"`
			Platform     string `json:"platform"`
			Capabilities struct {
				SendText bool `json:"send_text"`
			} `json:"capabilities"`
		} `json:"connectors"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range payload.Connectors {
		if c.ID == "qq-milky" && c.Platform == "qq" && !c.Capabilities.SendText {
			found = true
		}
	}
	if !found {
		t.Fatalf("qq-milky descriptor missing: %s", fmt.Sprint(recorder.Body.String()))
	}
}

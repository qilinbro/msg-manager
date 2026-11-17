package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qilinbro/msg-manager/internal/config"
	"github.com/qilinbro/msg-manager/internal/connector/adapter"
	"github.com/qilinbro/msg-manager/internal/connector/qqmilky"
	"github.com/qilinbro/msg-manager/internal/store"
)

const milkySetupAPIToken = "synthetic-milky-setup-api-token-01234567890"
const milkySetupProviderToken = "synthetic-milky-provider-token-do-not-expose"

func milkySetupHandler(t *testing.T, databasePath string) (*handler, config.Config) {
	t.Helper()
	cfg := config.Config{Addr: config.DefaultAddr, APIToken: milkySetupAPIToken, DatabasePath: databasePath}
	if databasePath != "" {
		cfg.DatabaseKey = strings.Repeat("0123456789abcdef", 4)
	}
	h, err := NewHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.(*handler).Close() })
	return h.(*handler), cfg
}

func milkySetupInput(base string, revision int64, credential string, save bool) map[string]any {
	input := map[string]any{"url": base, "account_id": "123456789", "access_token": credential, "history_limit": 12, "expected_revision": revision}
	if save {
		input["confirmed"] = true
	}
	return input
}

func milkySetupRequest(t *testing.T, h http.Handler, method, path string, payload any, auth bool) *httptest.ResponseRecorder {
	t.Helper()
	var body []byte
	if raw, ok := payload.(string); ok {
		body = []byte(raw)
	} else if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, "http://127.0.0.1:17890"+path, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if auth {
		r.Header.Set("Authorization", "Bearer "+milkySetupAPIToken)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func milkySetupConfig(t *testing.T, h http.Handler) milkyConfigResponse {
	t.Helper()
	w := milkySetupRequest(t, h, http.MethodGet, milkyConfigPath, nil, true)
	var result milkyConfigResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
		t.Fatalf("bad config GET: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), milkySetupProviderToken) || strings.Contains(w.Body.String(), "access_token") {
		t.Fatal("GET leaked the protocol credential")
	}
	return result
}

func milkySetupProvider(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer "+milkySetupProviderToken {
			w.WriteHeader(401)
			_, _ = w.Write([]byte("private-upstream-body " + milkySetupProviderToken))
			return
		}
		if r.Method != http.MethodPost {
			t.Error("provider probe used an unexpected method")
		}
		switch r.URL.Path {
		case "/api/get_login_info":
			_, _ = w.Write([]byte(`{"status":"ok","retcode":0,"data":{"uin":123456789,"nickname":"合成账号"}}`))
		case "/api/get_impl_info":
			_, _ = w.Write([]byte(`{"status":"ok","retcode":0,"data":{"impl_name":"synthetic","impl_version":"1","milky_version":"1.3","qq_protocol_type":"linux"}}`))
		default:
			t.Error("configuration probe attempted another provider action")
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(server.Close)
	return server, &calls
}

func TestMilkyConfigSaveTestRetainAndRestore(t *testing.T) {
	provider, calls := milkySetupProvider(t)
	h, cfg := milkySetupHandler(t, filepath.Join(t.TempDir(), "milky.db"))
	initial := milkySetupConfig(t, h)
	if initial.Source != "none" || initial.Configured || initial.TokenConfigured || initial.Revision != 0 || initial.HistoryLimit != 20 {
		t.Fatalf("misleading initial config: %+v", initial)
	}
	w := milkySetupRequest(t, h, http.MethodPatch, milkyConfigPath, milkySetupInput(provider.URL, 0, milkySetupProviderToken, true), true)
	if w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	if calls.Load() != 0 {
		t.Fatal("saving configuration performed provider I/O")
	}
	if strings.Contains(w.Body.String(), milkySetupProviderToken) || strings.Contains(w.Body.String(), "access_token") {
		t.Fatal("saved response leaked provider credential")
	}
	saved := milkySetupConfig(t, h)
	if saved.Source != "saved" || !saved.Configured || !saved.TokenConfigured || saved.Revision != 1 || saved.HistoryLimit != 12 || saved.SendingEnabled {
		t.Fatalf("save metadata: %+v", saved)
	}
	// A failed draft test does not replace the saved token or cached identity.
	bad := milkySetupRequest(t, h, http.MethodPost, milkyConfigPath+"/test", milkySetupInput(provider.URL, 1, "wrong-secret", false), true)
	if bad.Code != 503 || !strings.Contains(bad.Body.String(), "MILKY_AUTH_FAILED") || strings.Contains(bad.Body.String(), "private-upstream-body") || strings.Contains(bad.Body.String(), milkySetupProviderToken) {
		t.Fatalf("unsafe probe error: %d %s", bad.Code, bad.Body.String())
	}
	if current := milkySetupConfig(t, h); current != saved {
		t.Fatal("failed draft test changed the saved configuration")
	}
	// Existing authorization may be pending verification after restart. Testing
	// drafts must not turn that saved authorization into an active worker.
	grant := store.QQMilkyMonitorSettings{Enabled: true, PeerIDs: []string{"987654321"}}
	if err := h.inbox.SaveQQMilkyMonitor(context.Background(), "123456789", grant); err != nil {
		t.Fatal(err)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	cfg.QQMilky = qqmilky.Config{Endpoint: adapter.Endpoint{URL: "invalid-environment-value", ExpectedSelfID: "invalid"}}
	restored, err := NewHandler(cfg)
	if err != nil {
		t.Fatal("saved connection did not take precedence over old environment settings", err)
	}
	defer restored.(*handler).Close()
	current := restored.(*handler)
	if current.milky.status.Monitor.State != "pending_verification" || current.milky.cancel != nil {
		t.Fatal("restoring configuration started receiving without fresh verification")
	}
	beforeProbe := calls.Load()
	verified := milkySetupRequest(t, current, http.MethodPost, milkyConfigPath+"/test", milkySetupInput(provider.URL, 1, "", false), true)
	if verified.Code != 200 {
		t.Fatalf("test saved token: %d %s", verified.Code, verified.Body.String())
	}
	var result MilkyVerification
	if json.Unmarshal(verified.Body.Bytes(), &result) != nil || result.AccountID != "123456789" || result.MilkyVersion != "1.3" || result.ReceivingEnabled || result.SendingEnabled {
		t.Fatal("draft test overstated receiving/sending permissions")
	}
	if calls.Load()-beforeProbe != 2 || current.milky.status.State != "configured" || current.milky.status.CheckedAt != "" || current.milky.cancel != nil {
		t.Fatal("draft test mutated live state or used unexpected actions")
	}
	// Saving a new history limit retains the credential only for the same
	// canonical target and pauses/resets all prior receiving authorization.
	input := milkySetupInput(provider.URL+"/", 1, "", true)
	input["history_limit"] = 7
	count := calls.Load()
	w = milkySetupRequest(t, current, http.MethodPatch, milkyConfigPath, input, true)
	if w.Code != 200 {
		t.Fatalf("retain token: %d %s", w.Code, w.Body.String())
	}
	if calls.Load() != count || current.milky.cancel != nil || current.milky.status.Monitor.Enabled || current.milky.configuration.AccessToken != milkySetupProviderToken || current.milky.configuration.HistoryLimit != 7 {
		t.Fatal("save changed token, performed I/O or preserved old receive permissions")
	}
	if grant, err := current.inbox.LoadQQMilkyMonitor(context.Background(), "123456789"); err != nil || grant.Enabled || len(grant.PeerIDs) != 0 {
		t.Fatal("saving credentials did not revoke old receive authorization", err)
	}
	stale := milkySetupRequest(t, current, http.MethodPatch, milkyConfigPath, input, true)
	if stale.Code != 409 || !strings.Contains(stale.Body.String(), "MILKY_CONFIG_STALE") {
		t.Fatal("stale writer accepted")
	}
	changed := milkySetupInput(provider.URL, 2, "", true)
	changed["account_id"] = "123456788"
	if w := milkySetupRequest(t, current, http.MethodPatch, milkyConfigPath, changed, true); w.Code != 400 || !strings.Contains(w.Body.String(), "MILKY_CONFIG_TOKEN_REQUIRED") {
		t.Fatal("saved token followed another account")
	}
	changed = milkySetupInput("http://127.0.0.1:65534", 2, "", true)
	if w := milkySetupRequest(t, current, http.MethodPost, milkyConfigPath+"/test", milkySetupInput("http://127.0.0.1:65534", 2, "", false), true); w.Code != 400 || !strings.Contains(w.Body.String(), "MILKY_CONFIG_TOKEN_REQUIRED") {
		t.Fatal("saved token followed another endpoint")
	}
}

func TestMilkyConfigurationRejectsInvalidRequestsWithoutNetwork(t *testing.T) {
	provider, calls := milkySetupProvider(t)
	h, _ := milkySetupHandler(t, filepath.Join(t.TempDir(), "milky-invalid.db"))
	input := milkySetupInput(provider.URL, 0, milkySetupProviderToken, true)
	for _, path := range []string{milkyConfigPath, milkyConfigPath + "/test"} {
		if w := milkySetupRequest(t, h, http.MethodGet, path, nil, false); w.Code != 401 {
			t.Fatal("configuration route omitted authentication")
		}
	}
	for _, raw := range []string{
		`{"url":null,"account_id":"123456789","access_token":"secret","history_limit":20,"expected_revision":0,"confirmed":true}`,
		`{"URL":"http://127.0.0.1:3010","account_id":"123456789","access_token":"secret","history_limit":20,"expected_revision":0,"confirmed":true}`,
		`{"url":"http://127.0.0.1:3010","url":"http://remote.example","account_id":"123456789","access_token":"secret","history_limit":20,"expected_revision":0,"confirmed":true}`,
		`{"url":"http://127.0.0.1:3010","account_id":"123456789","access_token":"secret","history_limit":20,"expected_revision":null,"confirmed":true}`,
		`{"url":"http://127.0.0.1:3010","account_id":"123456789","access_token":"secret","history_limit":20,"expected_revision":0,"confirmed":true,"sql":"private-marker"}`,
		`{"url":"http://127.0.0.1:3010","account_id":"123456789","access_token":"secret","history_limit":20,"expected_revision":0,"confirmed":false}`,
		`[]`, `null`, `{}`,
	} {
		if w := milkySetupRequest(t, h, http.MethodPatch, milkyConfigPath, raw, true); w.Code != 400 || strings.Contains(w.Body.String(), "private-marker") {
			t.Fatalf("invalid body was accepted or echoed: %d", w.Code)
		}
	}
	if w := milkySetupRequest(t, h, http.MethodPatch, milkyConfigPath, strings.Repeat("x", (16<<10)+1), true); w.Code != 413 {
		t.Fatalf("oversized settings body: status=%d, want 413", w.Code)
	}
	for _, value := range []string{"http://remote.example:3010", "http://127.0.0.1:3010/api", "http://127.0.0.1:3010?", "http://user:secret@127.0.0.1:3010", "http://localhost:0", "https://127.0.0.1:3010"} {
		input["url"] = value
		if w := milkySetupRequest(t, h, http.MethodPatch, milkyConfigPath, input, true); w.Code != 400 {
			t.Fatalf("invalid endpoint accepted: %q", value)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid configuration attempted provider I/O")
	}
	withoutStore, _ := milkySetupHandler(t, "")
	if w := milkySetupRequest(t, withoutStore, http.MethodPatch, milkyConfigPath, milkySetupInput(provider.URL, 0, milkySetupProviderToken, true), true); w.Code != 503 || !strings.Contains(w.Body.String(), "MILKY_CONFIG_STORAGE_DISABLED") {
		t.Fatal("save without encrypted storage was allowed")
	}
	// A cross-origin browser may neither configure nor probe local credentials.
	r := httptest.NewRequest(http.MethodPatch, "http://127.0.0.1:17890"+milkyConfigPath, strings.NewReader(`{}`))
	r.Header.Set("Origin", "https://untrusted.example")
	r.Header.Set("Authorization", "Bearer "+milkySetupAPIToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("untrusted origin accessed credential setting")
	}
}

func TestMilkyConfigHotSaveJoinsWorkerAndReadConcurrency(t *testing.T) {
	provider, calls := milkySetupProvider(t)
	h, _ := milkySetupHandler(t, filepath.Join(t.TempDir(), "milky-lifecycle.db"))
	if w := milkySetupRequest(t, h, http.MethodPatch, milkyConfigPath, milkySetupInput(provider.URL, 0, milkySetupProviderToken, true), true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	workerContext, cancelWorker := context.WithCancel(context.Background())
	joined := make(chan struct{})
	h.milky.cancel, h.milky.done = cancelWorker, joined
	h.milky.persisted = store.QQMilkyMonitorSettings{Enabled: true, PeerIDs: []string{"987654321"}}
	h.milky.status.Monitor.Enabled, h.milky.status.ReceivingEnabled = true, true
	go func() { <-workerContext.Done(); close(joined) }()
	if w := milkySetupRequest(t, h, http.MethodPatch, milkyConfigPath, milkySetupInput(provider.URL, 1, "", true), true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	select {
	case <-joined:
	default:
		t.Fatal("save changed credentials before joining the old receiver")
	}
	if h.milky.cancel != nil || h.milky.status.ReceivingEnabled || h.milky.persisted.Enabled {
		t.Fatal("save left old receiver authorized")
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 10 {
			_ = milkySetupRequest(t, h, http.MethodGet, milkyConfigPath, nil, true)
			_ = milkySetupRequest(t, h, http.MethodGet, "/api/v1/system", nil, true)
		}
	}()
	for revision := int64(2); revision < 5; revision++ {
		if w := milkySetupRequest(t, h, http.MethodPatch, milkyConfigPath, milkySetupInput(provider.URL, revision, "", true), true); w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("config/status requests deadlocked")
	}
	if calls.Load() != 0 {
		t.Fatal("hot saving initiated provider network calls")
	}
}

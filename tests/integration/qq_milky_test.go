package integration_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
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

const milkyBoundaryPath = "/api/v1/connections/qq-milky"

type milkyBoundaryFixture struct {
	mu               sync.Mutex
	account, version string
	history          string
	calls            map[string]int
	streams          chan *websocket.Conn
	implEntered      chan struct{}
	implRelease      <-chan struct{}
}

func newMilkyBoundaryProvider(t *testing.T, history string) (*milkyBoundaryFixture, *httptest.Server) {
	t.Helper()
	fixture := &milkyBoundaryFixture{
		account: milkyAccount, version: "1.3", history: history,
		calls: make(map[string]int), streams: make(chan *websocket.Conn, 8),
	}
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+milkyToken {
			http.Error(w, "denied", http.StatusUnauthorized)
			return
		}
		fixture.mu.Lock()
		fixture.calls[r.URL.Path]++
		account, version, page := fixture.account, fixture.version, fixture.history
		var entered chan struct{}
		var release <-chan struct{}
		if r.URL.Path == "/api/get_impl_info" {
			entered, release = fixture.implEntered, fixture.implRelease
			fixture.implEntered, fixture.implRelease = nil, nil
		}
		fixture.mu.Unlock()
		if entered != nil {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		switch r.URL.Path {
		case "/api/get_login_info":
			_, _ = fmt.Fprintf(w, `{"status":"ok","retcode":0,"data":{"uin":%s,"nickname":"self"}}`, account)
		case "/api/get_impl_info":
			_, _ = fmt.Fprintf(w, `{"status":"ok","retcode":0,"data":{"impl_name":"synthetic","milky_version":%q}}`, version)
		case "/api/get_history_messages":
			_, _ = fmt.Fprintf(w, `{"status":"ok","retcode":0,"data":%s}`, page)
		case "/event":
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			select {
			case fixture.streams <- conn:
			default:
				return
			}
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return fixture, server
}

func (f *milkyBoundaryFixture) setIdentity(account, version string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.account, f.version = account, version
}

func (f *milkyBoundaryFixture) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[path]
}

func (f *milkyBoundaryFixture) nextStream(t *testing.T) *websocket.Conn {
	t.Helper()
	select {
	case conn := <-f.streams:
		return conn
	case <-time.After(3 * time.Second):
		t.Fatal("verified monitor did not establish a stream")
		return nil
	}
}

func (f *milkyBoundaryFixture) blockNextImpl(t *testing.T) (<-chan struct{}, func()) {
	t.Helper()
	entered, release := make(chan struct{}), make(chan struct{})
	f.mu.Lock()
	f.implEntered, f.implRelease = entered, release
	f.mu.Unlock()
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	return entered, unblock
}

func milkyBoundaryMessage(scene, peer string, seq int) string {
	return fmt.Sprintf(`{"message_scene":%q,"peer_id":%s,"message_seq":%d,"sender_id":987654321,"time":1730000000,"segments":[{"type":"text","data":{"text":"界内历史"}}]}`, scene, peer, seq)
}

func milkyBoundaryRequest(t *testing.T, h http.Handler, method, suffix, body string, want int) *httptest.ResponseRecorder {
	t.Helper()
	response := milkyRequest(h, method, milkyBoundaryPath+suffix, body, nil)
	if response.Code != want {
		t.Fatalf("%s %s: got %d want %d: %s", method, suffix, response.Code, want, response.Body.String())
	}
	return response
}

func waitMilkyBoundaryStatus(t *testing.T, h http.Handler, accept func(api.MilkyStatus) bool) api.MilkyStatus {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		response := milkyBoundaryRequest(t, h, http.MethodGet, "/status", "", http.StatusOK)
		var status api.MilkyStatus
		if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
			t.Fatal(err)
		}
		if accept(status) {
			return status
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatalf("monitor did not reach expected state: %s", response.Body.String())
		}
	}
}

func assertMilkyBoundaryInboxEmpty(t *testing.T, h http.Handler) {
	t.Helper()
	response := milkyRequest(h, http.MethodGet, "/api/v1/conversations", "", nil)
	var page struct {
		Items []json.RawMessage `json:"items"`
	}
	if response.Code != http.StatusOK {
		t.Fatalf("conversations: %d %s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("rejected page/event was partially imported: %s", response.Body.String())
	}
}

func TestMilkySyncFreshVerificationAndAtomicPeerScope(t *testing.T) {
	good := milkyBoundaryMessage("friend", "987654321", 1)
	wrong := milkyBoundaryMessage("friend", "111111111", 2)
	for _, scenario := range []struct {
		name, account, version, messages, failure string
		priorVerification                         bool
	}{
		{"never verified matching account", milkyAccount, "1.3", good, "", false},
		{"never verified wrong account", "999", "1.3", good, "IDENTITY_MISMATCH", false},
		{"account switched after verification", "999", "1.3", good, "IDENTITY_MISMATCH", true},
		{"version switched after verification", milkyAccount, "1.2", good, "VERSION_MISMATCH", true},
		{"unknown version", milkyAccount, "", good, "VERSION_MISMATCH", false},
		{"wrong peer", milkyAccount, "1.3", wrong, "PROTOCOL_ERROR", false},
		{"allowed then wrong peer", milkyAccount, "1.3", good + "," + wrong, "PROTOCOL_ERROR", true},
		{"wrong then allowed peer", milkyAccount, "1.3", wrong + "," + good, "PROTOCOL_ERROR", false},
		{"wrong scene", milkyAccount, "1.3", milkyBoundaryMessage("group", "987654321", 3), "PROTOCOL_ERROR", false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			fixture, provider := newMilkyBoundaryProvider(t, `{"messages":[`+scenario.messages+`],"next_message_seq":41}`)
			h := milkyHandlerWithStore(t, provider.URL)
			if scenario.priorVerification {
				milkyBoundaryRequest(t, h, http.MethodGet, "/verify", "", http.StatusOK)
			}
			fixture.setIdentity(scenario.account, scenario.version)
			beforeLogin, beforeImpl := fixture.count("/api/get_login_info"), fixture.count("/api/get_impl_info")
			wantCode := http.StatusOK
			if scenario.failure != "" {
				wantCode = http.StatusServiceUnavailable
			}
			response := milkyBoundaryRequest(t, h, http.MethodPost, "/sync", `{"peer_id":"987654321"}`, wantCode)
			if fixture.count("/api/get_login_info") != beforeLogin+1 {
				t.Fatal("manual sync did not freshly verify the account")
			}
			wantImpl, wantHistory := 1, 1
			if scenario.account != milkyAccount {
				wantImpl, wantHistory = 0, 0
			} else if scenario.version != "1.3" {
				wantHistory = 0
			}
			if fixture.count("/api/get_impl_info") != beforeImpl+wantImpl || fixture.count("/api/get_history_messages") != wantHistory {
				t.Fatal("history was requested before fresh account/version verification")
			}
			if scenario.failure != "" {
				if !strings.Contains(response.Body.String(), "MILKY_"+scenario.failure) {
					t.Fatalf("unexpected failure: %s", response.Body.String())
				}
				assertMilkyBoundaryInboxEmpty(t, h)
			} else {
				var result api.MilkySyncResult
				if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Inserted != 1 {
					t.Fatalf("freshly verified manual import failed: %+v %v", result, err)
				}
				// A successful history probe is not explicit monitor authorization.
				milkyBoundaryRequest(t, h, http.MethodPost, "/monitor", `{"enabled":true,"peer_ids":["987654321"]}`, http.StatusServiceUnavailable)
			}
		})
	}
}

func TestMilkyFatalFailureRevokesMonitorAndRequiresExplicitFreshRecovery(t *testing.T) {
	for _, mode := range []string{"event identity", "initial identity", "initial version", "history identity", "history version", "manual identity", "manual version"} {
		t.Run(mode, func(t *testing.T) {
			fixture, provider := newMilkyBoundaryProvider(t, `{"messages":[`+milkyBoundaryMessage("friend", "987654321", 1)+`]}`)
			h := milkyHandlerWithStore(t, provider.URL)
			milkyBoundaryRequest(t, h, http.MethodGet, "/verify", "", http.StatusOK)
			badAccount, badVersion, failure := "999", "1.3", "identity_mismatch"
			if strings.HasSuffix(mode, "version") {
				badAccount, badVersion, failure = milkyAccount, "1.2", "version_mismatch"
			}
			if strings.HasPrefix(mode, "initial") {
				fixture.setIdentity(badAccount, badVersion)
			}
			milkyBoundaryRequest(t, h, http.MethodPost, "/monitor", `{"enabled":true,"peer_ids":["987654321"]}`, http.StatusOK)
			if !strings.HasPrefix(mode, "initial") {
				conn := fixture.nextStream(t)
				if mode == "event identity" {
					if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"event_type":"message_receive","self_id":999,"data":{}}`)); err != nil {
						t.Fatal(err)
					}
				} else {
					fixture.setIdentity(badAccount, badVersion)
					if strings.HasPrefix(mode, "manual") {
						milkyBoundaryRequest(t, h, http.MethodPost, "/sync", `{"peer_id":"987654321"}`, http.StatusServiceUnavailable)
					} else {
						_ = conn.Close() // Ordinary gap, but its fresh history probe must fail.
					}
				}
			}
			status := waitMilkyBoundaryStatus(t, h, func(status api.MilkyStatus) bool {
				return status.State == failure && !status.ReceivingEnabled && status.Monitor.State == "pending_verification"
			})
			if !status.Monitor.Enabled || status.Monitor.LastError != failure || status.SendingEnabled {
				t.Fatalf("fatal failure lost authorization or failed to revoke reception: %+v", status)
			}
			if fixture.count("/api/get_history_messages") != 0 {
				t.Fatal("fatal identity/version failure triggered history")
			}
			assertMilkyBoundaryInboxEmpty(t, h)
			streams := fixture.count("/event")
			fixture.setIdentity(milkyAccount, "1.3")
			// A manual read may verify for that request, but must not restart the
			// saved monitor or clear the requirement for an explicit fresh verify.
			milkyBoundaryRequest(t, h, http.MethodPost, "/sync", `{"peer_id":"987654321"}`, http.StatusOK)
			waitMilkyBoundaryStatus(t, h, func(status api.MilkyStatus) bool {
				return status.State == failure && !status.ReceivingEnabled && status.Monitor.State == "pending_verification"
			})
			if fixture.count("/event") != streams {
				t.Fatal("manual sync silently resumed the receive worker")
			}
			// The old terminal worker still has a join handle. Recovery must not
			// require a second verify or deadlock by joining itself.
			milkyBoundaryRequest(t, h, http.MethodGet, "/verify", "", http.StatusOK)
			conn := fixture.nextStream(t)
			if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"event_type":"message_receive","self_id":123456789,"data":{"message_scene":"friend","peer_id":987654321,"message_seq":77,"sender_id":987654321,"time":1730000001,"segments":[{"type":"text","data":{"text":"恢复接收"}}]}}`)); err != nil {
				t.Fatal(err)
			}
			waitMilkyBoundaryStatus(t, h, func(status api.MilkyStatus) bool {
				inbox := milkyRequest(h, http.MethodGet, "/api/v1/conversations", "", nil)
				return status.State == "identity_verified" && status.ReceivingEnabled && strings.Contains(inbox.Body.String(), "恢复接收")
			})
			milkyBoundaryRequest(t, h, http.MethodPost, "/monitor", `{"enabled":false,"peer_ids":[]}`, http.StatusOK)
		})
	}
}

func TestMilkyMonitorRejectsEntireWrongPeerHistoryPage(t *testing.T) {
	good, wrong := milkyBoundaryMessage("friend", "987654321", 1), milkyBoundaryMessage("friend", "111111111", 2)
	for _, scenario := range []struct{ name, messages string }{
		{"wrong peer", wrong}, {"mixed page", good + "," + wrong},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			fixture, provider := newMilkyBoundaryProvider(t, `{"messages":[`+scenario.messages+`]}`)
			h := milkyHandlerWithStore(t, provider.URL)
			milkyBoundaryRequest(t, h, http.MethodGet, "/verify", "", http.StatusOK)
			milkyBoundaryRequest(t, h, http.MethodPost, "/monitor", `{"enabled":true,"peer_ids":["987654321"]}`, http.StatusOK)
			_ = fixture.nextStream(t).Close()
			waitMilkyBoundaryStatus(t, h, func(status api.MilkyStatus) bool {
				for _, gap := range status.Gaps {
					if gap.Reason == "reconcile:protocol_error" {
						return true
					}
				}
				return false
			})
			if fixture.count("/api/get_history_messages") == 0 {
				t.Fatal("synthetic gap never exercised history reconciliation")
			}
			assertMilkyBoundaryInboxEmpty(t, h)
			milkyBoundaryRequest(t, h, http.MethodPost, "/monitor", `{"enabled":false,"peer_ids":[]}`, http.StatusOK)
		})
	}
}

func TestMilkyInFlightVerificationCannotUndoNewerFatalGap(t *testing.T) {
	fixture, provider := newMilkyBoundaryProvider(t, `{"messages":[]}`)
	h := milkyHandlerWithStore(t, provider.URL)
	milkyBoundaryRequest(t, h, http.MethodGet, "/verify", "", http.StatusOK)
	milkyBoundaryRequest(t, h, http.MethodPost, "/monitor", `{"enabled":true,"peer_ids":["987654321"]}`, http.StatusOK)
	conn := fixture.nextStream(t)
	entered, unblock := fixture.blockNextImpl(t)
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		result <- milkyRequest(h, http.MethodGet, milkyBoundaryPath+"/verify", "", nil)
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("explicit verification did not reach the synthetic barrier")
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"event_type":"message_receive","self_id":999,"data":{}}`)); err != nil {
		t.Fatal(err)
	}
	waitMilkyBoundaryStatus(t, h, func(status api.MilkyStatus) bool {
		return status.State == "identity_mismatch" && !status.ReceivingEnabled
	})
	unblock()
	select {
	case response := <-result:
		if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "MILKY_IDENTITY_MISMATCH") {
			t.Fatalf("stale verification undid newer revocation: %d %s", response.Code, response.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("verification could not join the terminal worker")
	}
	assertMilkyBoundaryInboxEmpty(t, h)
	milkyBoundaryRequest(t, h, http.MethodGet, "/verify", "", http.StatusOK)
	_ = fixture.nextStream(t)
	milkyBoundaryRequest(t, h, http.MethodPost, "/monitor", `{"enabled":false,"peer_ids":[]}`, http.StatusOK)
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

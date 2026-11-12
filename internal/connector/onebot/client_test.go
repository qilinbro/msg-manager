package onebot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const testToken = "private-test-token"
const testSelfID = "9007199254740993"

func testClient(t *testing.T, endpoint string) *Client {
	t.Helper()
	c, err := NewClient(Config{URL: endpoint, AccessToken: testToken, ExpectedSelfID: testSelfID})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func expectKind(t *testing.T, err error, want Kind) {
	t.Helper()
	var failure *Error
	if !errors.As(err, &failure) || failure.Kind != want {
		t.Fatalf("error kind=%v, want=%s", err, want)
	}
	if strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), testSelfID) || strings.Contains(err.Error(), "provider-body-secret") || strings.Contains(err.Error(), "http://") || strings.Contains(err.Error(), "ws://") {
		t.Fatal("error exposed a credential, account, endpoint or provider body")
	}
}

func TestConfigRejectsUnsafeOrIncompleteInputs(t *testing.T) {
	for _, endpoint := range []string{
		"https://127.0.0.1:1234", "wss://localhost:1234", "http://example.com:1234", "http://127.0.0.2:1234", "http://0.0.0.0:1234", "http://[::]:1234", "http://127.0.0.1", "http://localhost:0", "http://localhost:65536", "http://localhost:01234", "http://user:password@127.0.0.1:1234", "http://127.0.0.1:1234?access_token=" + testToken, "http://127.0.0.1:1234?", "http://127.0.0.1:1234#", "http://::1:1234", "http://localhost.:1234", "http://127.1:1234", "http://127.0.0.1:1234/\\api",
	} {
		t.Run(endpoint, func(t *testing.T) {
			_, err := NewClient(Config{URL: endpoint, AccessToken: testToken, ExpectedSelfID: testSelfID})
			expectKind(t, err, InvalidConfig)
		})
	}
	for _, endpoint := range []string{"http://localhost:1234/prefix/", "ws://127.0.0.1:1234/api", "ws://[::1]:1234"} {
		c := testClient(t, endpoint)
		if c.config.dialHost == "localhost" || c.timeout != 8*time.Second {
			t.Fatal("localhost remains DNS-resolved or default timeout changed")
		}
	}
	for _, value := range []string{"0", "-1", "1e5", "1.5", "+123", "123 456", "１２３"} {
		_, err := NewClient(Config{URL: "http://localhost:1234", AccessToken: testToken, ExpectedSelfID: value})
		expectKind(t, err, InvalidConfig)
	}
	for _, value := range []string{"has space", "line\nbreak", "中文", strings.Repeat("t", 4097)} {
		_, err := NewClient(Config{URL: "http://localhost:1234", AccessToken: value, ExpectedSelfID: testSelfID})
		expectKind(t, err, InvalidConfig)
	}
	for _, cfg := range []Config{{}, {URL: "http://localhost:1234"}, {URL: "http://localhost:1234", AccessToken: testToken}} {
		_, err := NewClient(cfg)
		expectKind(t, err, NotConfigured)
	}
}

func TestHTTPProbeVerifiesIdentityStatusAndPrefixWithoutSending(t *testing.T) {
	var mu sync.Mutex
	var actions []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer "+testToken || r.URL.RawQuery != "" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("invalid OneBot HTTP request or credential location")
		}
		mu.Lock()
		actions = append(actions, r.URL.Path)
		mu.Unlock()
		var params map[string]any
		if json.NewDecoder(r.Body).Decode(&params) != nil || len(params) != 0 {
			t.Error("expected empty API params without credentials")
		}
		switch r.URL.Path {
		case "/prefix/get_login_info":
			fmt.Fprintf(w, `{"status":"ok","retcode":0,"data":{"user_id":%s,"nickname":"测试昵称"}}`, testSelfID)
		case "/prefix/get_status":
			fmt.Fprint(w, `{"status":"ok","retcode":0,"data":{"online":true,"good":true}}`)
		default:
			t.Error("probe attempted an unexpected or sending action")
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	// localhost must dial loopback without trusting host DNS configuration.
	c := testClient(t, strings.Replace(server.URL, "127.0.0.1", "localhost", 1)+"/prefix/")
	info, err := c.Probe(context.Background())
	if err != nil || info.UserID != testSelfID || info.Nickname != "测试昵称" || !info.Online {
		t.Fatalf("probe result=%+v error=%v", info, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(actions) != 2 || actions[0] != "/prefix/get_login_info" || actions[1] != "/prefix/get_status" {
		t.Fatalf("unexpected read-only action sequence: %v", actions)
	}
}

func TestHTTPProbeNeverTreatsHTTP200AsVerification(t *testing.T) {
	cases := []struct {
		name   string
		status int
		login  string
		state  string
		want   Kind
	}{
		{"unauthorized", 401, "provider-body-secret", "", AuthFailed},
		{"forbidden", 403, "provider-body-secret", "", AuthFailed},
		{"server-error", 500, "provider-body-secret", "", ProtocolError},
		{"failed-envelope", 200, `{"status":"failed","retcode":100,"data":null,"message":"provider-body-secret"}`, "", ProtocolError},
		{"async-envelope", 200, `{"status":"async","retcode":1,"data":null}`, "", ProtocolError},
		{"auth-envelope", 200, `{"status":"failed","retcode":1403,"data":null}`, "", AuthFailed},
		{"missing-retcode", 200, `{"status":"ok","data":{"user_id":9007199254740993,"nickname":"x"}}`, "", ProtocolError},
		{"wrong-account", 200, `{"status":"ok","retcode":0,"data":{"user_id":"12345","nickname":"x"}}`, "", IdentityMismatch},
		{"float-account", 200, `{"status":"ok","retcode":0,"data":{"user_id":9007199254740993.0,"nickname":"x"}}`, "", ProtocolError},
		{"duplicate-account", 200, `{"status":"ok","retcode":0,"data":{"user_id":12345,"user_id":9007199254740993,"nickname":"x"}}`, "", ProtocolError},
		{"offline", 200, "", `{"status":"ok","retcode":0,"data":{"online":false,"good":true}}`, LoggedOut},
		{"unhealthy", 200, "", `{"status":"ok","retcode":0,"data":{"online":true,"good":false}}`, LoggedOut},
		{"missing-good", 200, "", `{"status":"ok","retcode":0,"data":{"online":true}}`, ProtocolError},
		{"string-online", 200, "", `{"status":"ok","retcode":0,"data":{"online":"true","good":true}}`, ProtocolError},
		{"missing-status-envelope", 200, "", `{"online":true,"good":true}`, ProtocolError},
		{"trailing-object", 200, `{"status":"ok","retcode":0,"data":{}} {}`, "", ProtocolError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				if r.URL.Path == "/get_login_info" {
					if tc.login != "" {
						fmt.Fprint(w, tc.login)
					} else {
						fmt.Fprintf(w, `{"status":"ok","retcode":0,"data":{"user_id":"%s","nickname":"x"}}`, testSelfID)
					}
				} else {
					fmt.Fprint(w, tc.state)
				}
			}))
			defer server.Close()
			info, err := testClient(t, server.URL).Probe(context.Background())
			expectKind(t, err, tc.want)
			if info != (LoginInfo{}) {
				t.Fatal("failed verification exposed a partial identity")
			}
		})
	}
}

func TestHTTPProbeRejectsRedirectsOversizeTimeoutAndCancellation(t *testing.T) {
	var redirected atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Store(true) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	_, err := testClient(t, server.URL).Probe(context.Background())
	server.Close()
	expectKind(t, err, ProtocolError)
	if redirected.Load() {
		t.Fatal("followed redirect and potentially exposed credentials")
	}
	large := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, strings.Repeat(" ", maxResponseBytes+1)) }))
	_, err = testClient(t, large.URL).Probe(context.Background())
	large.Close()
	expectKind(t, err, ProtocolError)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(200 * time.Millisecond):
		}
	}))
	c := testClient(t, slow.URL)
	c.timeout = 20 * time.Millisecond
	_, err = c.Probe(context.Background())
	expectKind(t, err, Unreachable)
	slow.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = c.Probe(ctx)
	expectKind(t, err, Unreachable)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "http://" + listener.Addr().String()
	_ = listener.Close()
	_, err = testClient(t, endpoint).Probe(context.Background())
	expectKind(t, err, Unreachable)
}

func TestWebSocketProbeCorrelatesEchoAndIgnoresInterleavedEvents(t *testing.T) {
	var calls atomic.Int32
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testToken || r.URL.RawQuery != "" {
			t.Error("WebSocket token was not confined to Authorization")
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var previous string
		for _, expectedAction := range []string{"get_login_info", "get_status"} {
			var req struct {
				Action string         `json:"action"`
				Params map[string]any `json:"params"`
				Echo   string         `json:"echo"`
			}
			if err := conn.ReadJSON(&req); err != nil {
				t.Error("did not receive read-only request")
				return
			}
			if req.Action != expectedAction || len(req.Params) != 0 || req.Echo == "" || req.Echo == previous {
				t.Error("invalid action, parameters or non-unique echo")
				return
			}
			previous = req.Echo
			calls.Add(1)
			_ = conn.WriteJSON(map[string]any{"post_type": "message", "self_id": testSelfID, "message": "interleaved event"})
			_ = conn.WriteJSON(map[string]any{"echo": "unrelated", "status": "failed", "retcode": 1403, "data": nil})
			data := map[string]any{"online": true, "good": true}
			if req.Action == "get_login_info" {
				data = map[string]any{"user_id": json.Number(testSelfID), "nickname": "WS测试"}
			}
			_ = conn.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "data": data, "echo": req.Echo})
		}
	}))
	defer server.Close()
	info, err := testClient(t, strings.Replace(server.URL, "http:", "ws:", 1)).Probe(context.Background())
	if err != nil || !info.Online || info.UserID != testSelfID || calls.Load() != 2 {
		t.Fatalf("WebSocket verification failed: info=%+v err=%v calls=%d", info, err, calls.Load())
	}
}

func TestWebSocketProbeFailureBoundaries(t *testing.T) {
	for _, scenario := range []string{"auth", "echo-flood", "timeout", "oversize", "matching-failure", "binary"} {
		t.Run(scenario, func(t *testing.T) {
			upgrader := websocket.Upgrader{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if scenario == "auth" {
					w.WriteHeader(http.StatusForbidden)
					fmt.Fprint(w, "provider-body-secret")
					return
				}
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				var req struct {
					Echo string `json:"echo"`
				}
				if conn.ReadJSON(&req) != nil {
					return
				}
				switch scenario {
				case "echo-flood":
					for i := 0; i < maxSkippedFrames+2; i++ {
						if conn.WriteJSON(map[string]any{"echo": "wrong", "status": "ok", "retcode": 0, "data": map[string]any{}}) != nil {
							return
						}
					}
				case "oversize":
					_ = conn.WriteMessage(websocket.TextMessage, []byte(strings.Repeat("x", maxResponseBytes+1)))
				case "matching-failure":
					_ = conn.WriteJSON(map[string]any{"echo": req.Echo, "status": "failed", "retcode": 1403, "data": nil})
				case "binary":
					_ = conn.WriteMessage(websocket.BinaryMessage, []byte(`{}`))
				case "timeout":
					_, _, _ = conn.ReadMessage() // Wait for the client's deadline/close.
				}
			}))
			defer server.Close()
			c := testClient(t, strings.Replace(server.URL, "http:", "ws:", 1))
			c.timeout = 100 * time.Millisecond
			_, err := c.Probe(context.Background())
			want := ProtocolError
			if scenario == "auth" || scenario == "matching-failure" {
				want = AuthFailed
			}
			if scenario == "timeout" {
				want = Unreachable
			}
			expectKind(t, err, want)
		})
	}
}

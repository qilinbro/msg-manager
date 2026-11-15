package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func testEndpoint(rawURL string) Endpoint {
	return Endpoint{URL: rawURL, AccessToken: "synthetic-access-token", ExpectedSelfID: "123456789", Timeout: 3 * time.Second}
}

func TestNewRejectsNonLocalAndUnsafeEndpoints(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Endpoint)
	}{
		{"empty", func(e *Endpoint) { e.URL = "" }},
		{"remote", func(e *Endpoint) { e.URL = "http://example.com:3000" }},
		{"https", func(e *Endpoint) { e.URL = "https://127.0.0.1:3000" }},
		{"no_port", func(e *Endpoint) { e.URL = "http://127.0.0.1" }},
		{"query", func(e *Endpoint) { e.URL = "http://127.0.0.1:3000/?token=leak" }},
		{"credentials_in_url", func(e *Endpoint) { e.URL = "http://user:pass@127.0.0.1:3000" }},
		{"path", func(e *Endpoint) { e.URL = "http://127.0.0.1:3000/api" }},
		{"empty_token", func(e *Endpoint) { e.AccessToken = "" }},
		{"whitespace_token", func(e *Endpoint) { e.AccessToken = "has space" }},
		{"bad_account", func(e *Endpoint) { e.ExpectedSelfID = "12a" }},
		{"bad_timeout", func(e *Endpoint) { e.Timeout = time.Minute }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testEndpoint("http://127.0.0.1:3000")
			tc.edit(&cfg)
			if _, err := New(cfg); err == nil {
				t.Fatal("expected configuration rejection")
			}
		})
	}
	if _, err := New(testEndpoint("http://localhost:3000")); err != nil {
		t.Fatalf("loopback hostname should be pinned locally: %v", err)
	}
}

func TestCallSendsBearerAndDecodesEnvelope(t *testing.T) {
	var gotPath, gotAuth, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		buf := make([]byte, 256)
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n])
		_, _ = w.Write([]byte(`{"status":"ok","retcode":0,"data":{"uin":123456789,"nickname":"self"}}`))
	}))
	defer server.Close()
	base, err := New(testEndpoint(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	data, err := base.Call(context.Background(), "get_login_info", nil)
	if err != nil {
		t.Fatal(err)
	}
	var info struct {
		UIN int64 `json:"uin"`
	}
	if err := base.Decode(data, &info); err != nil || info.UIN != 123456789 {
		t.Fatalf("unexpected data: %s %v", data, err)
	}
	if gotPath != "/api/get_login_info" || gotAuth != "Bearer synthetic-access-token" || gotBody != "{}" {
		t.Fatalf("unexpected request: %s %s %s", gotPath, gotAuth, gotBody)
	}
}

func TestCallClassifiesFailuresWithoutEchoingProviderText(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		kind   Kind
	}{
		{"unauthorized", 401, `{"secret":"PRIVATE"}`, AuthFailed},
		{"logged_out", 200, `{"status":"failed","retcode":-403,"message":"PRIVATE"}`, LoggedOut},
		{"bad_envelope", 200, `{"status":"failed","retcode":-400,"message":"PRIVATE"}`, ProtocolError},
		{"duplicate_keys", 200, `{"status":"ok","status":"ok","retcode":0,"data":{}}`, ProtocolError},
		{"unknown_status", 500, `PRIVATE`, ProtocolError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			base, err := New(testEndpoint(server.URL))
			if err != nil {
				t.Fatal(err)
			}
			_, err = base.Call(context.Background(), "get_impl_info", nil)
			var failure *Error
			if !errors.As(err, &failure) || failure.Kind != tc.kind {
				t.Fatalf("expected %s, got %v", tc.kind, err)
			}
			if strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("provider text leaked")
			}
		})
	}
}

func TestDialEventUsesAuthorizationHeader(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/event" || r.Header.Get("Authorization") != "Bearer synthetic-access-token" {
			http.Error(w, "denied", http.StatusUnauthorized)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			defer conn.Close()
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"event_type":"message_receive"}`))
			time.Sleep(20 * time.Millisecond)
		}
	}))
	defer server.Close()
	base, err := New(testEndpoint(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	conn, err := base.DialEvent(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var event struct {
		Type string `json:"event_type"`
	}
	if err := json.Unmarshal(raw, &event); err != nil || event.Type != "message_receive" {
		t.Fatalf("unexpected event: %s %v", raw, err)
	}
	if !strings.Contains(base.eventURL.String(), "/event") || strings.Contains(base.eventURL.String(), "?") {
		t.Fatal("event URL must not carry credentials in the query")
	}
}

package qqmilky

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/qilinbro/msg-manager/internal/connector/adapter"
)

const testSelfID = "123456789"

func newTestClient(t *testing.T, rawURL string) *Client {
	t.Helper()
	client, err := NewClient(Config{Endpoint: adapter.Endpoint{
		URL: rawURL, AccessToken: "synthetic-token", ExpectedSelfID: testSelfID, Timeout: 3 * time.Second,
	}})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func milkyHandler(t *testing.T, implBody string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-token" {
			http.Error(w, "denied", http.StatusUnauthorized)
			return
		}
		var body string
		switch r.URL.Path {
		case "/api/get_login_info":
			body = `{"status":"ok","retcode":0,"data":{"uin":123456789,"nickname":"self"}}`
		case "/api/get_impl_info":
			body = fmt.Sprintf(`{"status":"ok","retcode":0,"data":%s}`, implBody)
		case "/api/get_friend_list":
			body = `{"status":"ok","retcode":0,"data":{"friends":[{"user_id":987654321,"nickname":"Alice","remark":"A"}]}}`
		case "/api/get_history_messages":
			body = `{"status":"ok","retcode":0,"data":{"messages":[{"message_scene":"friend","peer_id":987654321,"message_seq":9007199254740993,"sender_id":987654321,"time":1730000000,"segments":[{"type":"text","data":{"text":"你好"}}]}],"next_message_seq":41}}`
		case "/api/send_private_message":
			body = `{"status":"ok","retcode":0,"data":{"message_seq":77,"time":1730000001}}`
		default:
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}
}

func impl(version string) string {
	return fmt.Sprintf(`{"impl_name":"Lagrange.Milky","impl_version":"test","milky_version":"%s","qq_protocol_type":"linux"}`, version)
}

func TestProbeVerifiesAccountAndVersion(t *testing.T) {
	server := httptest.NewServer(milkyHandler(t, impl("1.3")))
	defer server.Close()
	verification, err := newTestClient(t, server.URL).Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if verification.AccountID != testSelfID || verification.MilkyVersion != "1.3" || !verification.SelfIDVerified {
		t.Fatalf("unexpected verification: %+v", verification)
	}
}

func TestProbeAcceptsConfiguredVersion12(t *testing.T) {
	server := httptest.NewServer(milkyHandler(t, impl("1.2")))
	defer server.Close()
	client, err := NewClient(Config{
		Endpoint:     adapter.Endpoint{URL: server.URL, AccessToken: "synthetic-token", ExpectedSelfID: testSelfID, Timeout: 3 * time.Second},
		MilkyVersion: MilkyVersion12,
	})
	if err != nil {
		t.Fatal(err)
	}
	verification, err := client.Probe(context.Background())
	if err != nil || verification.MilkyVersion != MilkyVersion12 || client.ConfiguredVersion() != MilkyVersion12 {
		t.Fatalf("1.2 verification failed: %+v %v", verification, err)
	}
}

func TestProbeRejectsVersionMismatch(t *testing.T) {
	server := httptest.NewServer(milkyHandler(t, impl("1.2")))
	defer server.Close()
	_, err := newTestClient(t, server.URL).Probe(context.Background())
	var failure *adapter.Error
	if !errors.As(err, &failure) || failure.Kind != adapter.VersionMismatch {
		t.Fatalf("expected 1.3 client to reject 1.2 provider, got %v", err)
	}
}

func TestConfigRejectsUnsupportedMilkyVersion(t *testing.T) {
	for _, version := range []string{"1.1", "1.4", "2.0"} {
		_, err := NewClient(Config{
			Endpoint:     adapter.Endpoint{URL: "http://127.0.0.1:3000", AccessToken: "synthetic-token", ExpectedSelfID: testSelfID},
			MilkyVersion: version,
		})
		if err == nil {
			t.Fatalf("version %s must be rejected", version)
		}
	}
}

func TestProbeRejectsWrongAccount(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/get_login_info" {
			_, _ = w.Write([]byte(`{"status":"ok","retcode":0,"data":{"uin":999,"nickname":"other"}}`))
			return
		}
		milkyHandler(t, impl("1.3"))(w, r)
	}))
	defer server.Close()
	_, err := newTestClient(t, server.URL).Probe(context.Background())
	var failure *adapter.Error
	if !errors.As(err, &failure) || failure.Kind != adapter.IdentityMismatch {
		t.Fatalf("expected identity mismatch, got %v", err)
	}
}

func TestConfigRejectsInvalidLimit(t *testing.T) {
	cfg := Config{HistoryLimit: 100, Endpoint: adapter.Endpoint{URL: "http://127.0.0.1:3000", AccessToken: "synthetic-token", ExpectedSelfID: testSelfID}}
	if _, err := NewClient(cfg); err == nil {
		t.Fatal("expected invalid limit rejection")
	}
}

func TestFriendsHistoryAndSend(t *testing.T) {
	server := httptest.NewServer(milkyHandler(t, impl("1.3")))
	defer server.Close()
	client := newTestClient(t, server.URL)
	friends, err := client.Friends(context.Background())
	if err != nil || len(friends) != 1 {
		t.Fatalf("friends: %+v %v", friends, err)
	}
	messages, next, err := client.History(context.Background(), "friend", "987654321", nil, 20)
	if err != nil || len(messages) != 1 || next == nil || *next != 41 {
		t.Fatalf("history: %+v %v %v", messages, next, err)
	}
	normalized, err := client.Normalize(messages[0], testSelfID)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.ExternalID != "milky:friend:987654321:9007199254740993" || normalized.Body != "你好" || normalized.Direction != "incoming" {
		t.Fatalf("large integer ID or body corrupted: %+v", normalized)
	}
	receipt, err := client.SendPrivate(context.Background(), "987654321", "test")
	if err != nil || receipt.MessageSeq != "77" {
		t.Fatalf("send receipt: %+v %v", receipt, err)
	}
}

func TestNormalizeRejectsOtherScenesAndRecordsSkippedSegments(t *testing.T) {
	client := &Client{}
	base := IncomingMessage{
		Scene: "friend", Time: 1730000000,
		PeerID: json.RawMessage(`987654321`), MessageSeq: json.RawMessage(`5`), SenderID: json.RawMessage(testSelfID),
	}
	group := base
	group.Scene = "group"
	group.PeerID = json.RawMessage(`8888`)
	result, err := client.Normalize(group, testSelfID)
	if err != nil || result.SkipReason != "unsupported_scene" {
		t.Fatalf("group must be skipped: %+v %v", result, err)
	}
	mixed := base
	mixed.Segments = []Segment{{Type: "image", Data: json.RawMessage(`{}`)}}
	result, err = client.Normalize(mixed, testSelfID)
	if err != nil || result.SkipReason != "non_text_only" || len(result.Unsupported) != 1 || result.Direction != "outgoing" {
		t.Fatalf("mixed message handling: %+v %v", result, err)
	}
}

func TestRunReceivesEventsAndReportsGaps(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var connections int
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/event" {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			mu.Lock()
			connections++
			mu.Unlock()
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"event_type":"message_receive","time":1730000000,"self_id":123456789,"data":{"message_scene":"friend","peer_id":987654321,"message_seq":9,"sender_id":987654321,"time":1730000000,"segments":[{"type":"text","data":{"text":"实时消息"}}]}}`))
			return
		}
		milkyHandler(t, impl("1.3"))(w, r)
	}))
	defer server.Close()
	client := newTestClient(t, server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var received []NormalizedMessage
	var gaps []Gap
	var stateMu sync.Mutex
	done := make(chan struct{})
	go func() {
		client.Run(ctx,
			func(message NormalizedMessage) error {
				stateMu.Lock()
				received = append(received, message)
				stateMu.Unlock()
				return nil
			},
			func(gap Gap) {
				stateMu.Lock()
				gaps = append(gaps, gap)
				count := len(gaps)
				stateMu.Unlock()
				if count >= 1 {
					cancel()
				}
			})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		cancel()
		t.Fatal("event stream did not settle")
	}
	stateMu.Lock()
	defer stateMu.Unlock()
	if len(received) == 0 || received[0].Body != "实时消息" || received[0].MessageSeq != "9" {
		t.Fatalf("event not normalized: %+v", received)
	}
	if len(gaps) == 0 || gaps[0].Reason == "" {
		t.Fatal("disconnect gap was not reported")
	}
	if strings.Contains(gaps[0].Reason, "PRIVATE") {
		t.Fatal("gap reason leaked provider data")
	}
}

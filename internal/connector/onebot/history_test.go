package onebot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/qilinbro/msg-manager/internal/store"
)

func historyClient(t *testing.T, rawURL string, selfID string) *Client {
	t.Helper()
	c, err := NewClient(Config{URL: rawURL, AccessToken: "synthetic-token", ExpectedSelfID: selfID})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestFriendHistoryBoundedAndIDSafe(t *testing.T) {
	var gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/get_friend_msg_history" || r.Header.Get("Authorization") != "Bearer synthetic-token" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		buf := make([]byte, 1024)
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n])
		_, _ = w.Write([]byte(`{"status":"ok","retcode":0,"data":[{"message_id":9007199254740993,"user_id":987654321,"time":1730000000,"message":"hello","sender":{"user_id":987654321,"nickname":"Alice"}}]}`))
	}))
	defer server.Close()
	client := historyClient(t, server.URL, "123456789")
	messages, err := client.FriendHistory(context.Background(), "987654321", 20)
	if err != nil || len(messages) != 1 {
		t.Fatalf("history: %+v %v", messages, err)
	}
	if !strings.Contains(gotBody, `"count":20`) || !strings.Contains(gotBody, `"user_id":"987654321"`) {
		t.Fatalf("unexpected request: %s", gotBody)
	}
	item, reason, err := client.NormalizeHistory(messages[0], "987654321", client.SelfID())
	if err != nil || reason != "" {
		t.Fatal(err)
	}
	if item.ExternalID != "onebot:friend:987654321:9007199254740993" || item.Body != "hello" || item.PeerName != "Alice" {
		t.Fatalf("normalization: %+v", item)
	}
}

func TestFriendHistoryRejectsBadParams(t *testing.T) {
	client := historyClient(t, "http://127.0.0.1:3000", "123456789")
	if _, err := client.FriendHistory(context.Background(), "abc", 20); err == nil {
		t.Fatal("invalid peer must be rejected")
	}
	if _, err := client.FriendHistory(context.Background(), "987654321", 151); err == nil {
		t.Fatal("count over 150 must be rejected")
	}
}

func TestNormalizeHistorySkipsUnsupportedAndRejectsMismatch(t *testing.T) {
	client := historyClient(t, "http://127.0.0.1:3000", "123456789")
	base := HistoryMessage{MessageID: json.RawMessage(`7`), UserID: json.RawMessage(`987654321`), Time: json.RawMessage(`1730000000`), Message: json.RawMessage(`[{"type":"image","data":{}}]`)}
	_, reason, err := client.NormalizeHistory(base, "987654321", client.SelfID())
	if err != nil || reason != "unsupported_text" {
		t.Fatalf("media should be skipped: reason=%q err=%v", reason, err)
	}
	wrong := base
	wrong.UserID = json.RawMessage(`999`)
	_, _, err = client.NormalizeHistory(wrong, "987654321", client.SelfID())
	var failure *Error
	if !errors.As(err, &failure) || failure.Kind != IdentityMismatch {
		t.Fatalf("peer mismatch must be rejected: %v", err)
	}
}

func TestNormalizeHistorySkipsSelfSentLoop(t *testing.T) {
	client := historyClient(t, "http://127.0.0.1:3000", "858494731")
	message := HistoryMessage{
		MessageID: json.RawMessage(`30016`), UserID: json.RawMessage(`858494731`),
		Time:    json.RawMessage(`1795178353`),
		Message: json.RawMessage(`[{"type":"text","data":{"text":"self sent"}}]`),
	}
	_, _, err := client.NormalizeHistory(message, "858494731", client.SelfID())
	var failure *Error
	if !errors.As(err, &failure) || failure.Kind != IdentityMismatch {
		t.Fatalf("self-sent loop must be rejected: %v", err)
	}
}

func TestImportOneBotContextRequiresServerGeneratedPrefix(t *testing.T) {
	// Build a Store fixture through the existing encrypted test helper in store
	// package is internal, so here we verify the guard shape indirectly via the
	// exported method on a zero Store (it rejects before touching the database).
	s := &store.Store{}
	req := store.ImportRequest{Platform: "qq", AccountID: "123456789", Messages: []store.ImportMessage{{ExternalID: "client-supplied", PeerID: "987654321", Direction: "incoming", Body: "x", OccurredAt: "2026-01-01T00:00:00Z"}}}
	if _, err := s.ImportOneBotContext(context.Background(), req); !errors.Is(err, store.ErrInvalidInput) {
		t.Fatalf("client-supplied source must be rejected: %v", err)
	}
}

func TestOneBotCallUsesPrivateBoundaries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"status":"ok","retcode":0,"data":[]}`)
	}))
	defer server.Close()
	client := historyClient(t, server.URL, "123456789")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := client.Call(ctx, "../admin", nil); err == nil {
		t.Fatal("invalid action name must be rejected")
	}
	if _, err := client.Call(ctx, "get_friend_list", map[string]any{}); err != nil {
		t.Fatal(err)
	}
}

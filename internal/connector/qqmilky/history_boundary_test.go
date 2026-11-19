package qqmilky

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/qilinbro/msg-manager/internal/connector/adapter"
)

func boundaryHistoryMessage(scene, peer string, seq int) string {
	return fmt.Sprintf(`{"message_scene":%q,"peer_id":%s,"message_seq":%d,"sender_id":987654321,"time":1730000000,"segments":[{"type":"text","data":{"text":"synthetic history"}}]}`, scene, peer, seq)
}

func TestHistoryFreshlyVerifiesBeforeEveryPage(t *testing.T) {
	for _, scenario := range []struct {
		name       string
		priorProbe bool
		account    string
		version    string
		want       adapter.Kind
	}{
		{"never verified matching account", false, testSelfID, "1.3", ""},
		{"never verified wrong account", false, "999", "1.3", adapter.IdentityMismatch},
		{"account changed after verification", true, "999", "1.3", adapter.IdentityMismatch},
		{"version changed after verification", true, testSelfID, "1.2", adapter.VersionMismatch},
		{"unknown version", false, testSelfID, "", adapter.VersionMismatch},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var mu sync.Mutex
			account, version := testSelfID, "1.3"
			var calls []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				calls = append(calls, r.URL.Path)
				self, protocol := account, version
				mu.Unlock()
				switch r.URL.Path {
				case "/api/get_login_info":
					_, _ = fmt.Fprintf(w, `{"status":"ok","retcode":0,"data":{"uin":%s}}`, self)
				case "/api/get_impl_info":
					_, _ = fmt.Fprintf(w, `{"status":"ok","retcode":0,"data":%s}`, impl(protocol))
				default:
					milkyHandler(t, impl("1.3"))(w, r)
				}
			}))
			defer server.Close()
			client := newTestClient(t, server.URL)
			if scenario.priorProbe {
				if _, err := client.Probe(t.Context()); err != nil {
					t.Fatal("initial verification", err)
				}
			}
			mu.Lock()
			account, version, calls = scenario.account, scenario.version, nil
			mu.Unlock()
			messages, next, err := client.History(t.Context(), "friend", "987654321", nil, 20)
			wantCalls := "/api/get_login_info"
			if scenario.account == testSelfID {
				wantCalls += ",/api/get_impl_info"
			}
			if scenario.want == "" {
				wantCalls += ",/api/get_history_messages"
				if err != nil || len(messages) != 1 || next == nil {
					t.Fatalf("verified history failed: messages=%d next=%v err=%v", len(messages), next, err)
				}
			} else {
				var failure *adapter.Error
				if !errors.As(err, &failure) || failure.Kind != scenario.want || len(messages) != 0 || next != nil {
					t.Fatalf("history escaped verification: messages=%d next=%v err=%v", len(messages), next, err)
				}
			}
			mu.Lock()
			gotCalls := strings.Join(calls, ",")
			mu.Unlock()
			if gotCalls != wantCalls {
				t.Fatalf("fresh verification order: got %s want %s", gotCalls, wantCalls)
			}
		})
	}
}

func TestHistoryRejectsEntireMisroutedPage(t *testing.T) {
	good := boundaryHistoryMessage("friend", "987654321", 1)
	wrong := boundaryHistoryMessage("friend", "111111111", 2)
	for _, scenario := range []struct {
		name, messages string
	}{
		{"wrong peer", wrong},
		{"allowed then wrong peer", good + "," + wrong},
		{"wrong then allowed peer", wrong + "," + good},
		{"wrong scene", boundaryHistoryMessage("group", "987654321", 2)},
		{"missing peer", strings.Replace(good, `"peer_id":987654321,`, "", 1)},
		{"more than requested limit", good + "," + good + "," + good},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/get_history_messages" {
					_, _ = fmt.Fprintf(w, `{"status":"ok","retcode":0,"data":{"messages":[%s],"next_message_seq":41}}`, scenario.messages)
					return
				}
				milkyHandler(t, impl("1.3"))(w, r)
			}))
			defer server.Close()
			messages, next, err := newTestClient(t, server.URL).History(t.Context(), "friend", "987654321", nil, 2)
			var failure *adapter.Error
			if !errors.As(err, &failure) || failure.Kind != adapter.ProtocolError || len(messages) != 0 || next != nil {
				t.Fatalf("page did not fail closed: messages=%d next=%v err=%v", len(messages), next, err)
			}
		})
	}
}

func TestRunStopsOnFatalIdentityOrVersionFailure(t *testing.T) {
	for _, scenario := range []struct {
		name, account, version string
		wrongEvent, reconnect  bool
		want                   adapter.Kind
	}{
		{"initial wrong account", "999", "1.3", false, false, adapter.IdentityMismatch},
		{"initial wrong version", testSelfID, "1.2", false, false, adapter.VersionMismatch},
		{"initial unknown version", testSelfID, "", false, false, adapter.VersionMismatch},
		{"wrong event account", testSelfID, "1.3", true, false, adapter.IdentityMismatch},
		{"account changed on reconnect", "999", "1.3", false, true, adapter.IdentityMismatch},
		{"version changed on reconnect", testSelfID, "1.2", false, true, adapter.VersionMismatch},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var mu sync.Mutex
			account, version := scenario.account, scenario.version
			if scenario.reconnect {
				account, version = testSelfID, "1.3"
			}
			var streams, logins, histories atomic.Int32
			upgrader := websocket.Upgrader{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				self, protocol := account, version
				mu.Unlock()
				switch r.URL.Path {
				case "/api/get_login_info":
					logins.Add(1)
					_, _ = fmt.Fprintf(w, `{"status":"ok","retcode":0,"data":{"uin":%s}}`, self)
				case "/api/get_impl_info":
					_, _ = fmt.Fprintf(w, `{"status":"ok","retcode":0,"data":%s}`, impl(protocol))
				case "/event":
					streams.Add(1)
					conn, err := upgrader.Upgrade(w, r, nil)
					if err != nil {
						return
					}
					defer conn.Close()
					if scenario.wrongEvent {
						_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"event_type":"message_receive","self_id":999,"data":{}}`))
					}
				case "/api/get_history_messages":
					histories.Add(1)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
			defer cancel()
			var gaps []Gap
			var received int
			client := newTestClient(t, server.URL)
			done := make(chan struct{})
			go func() {
				client.Run(ctx, func(NormalizedMessage) error {
					received++
					return nil
				}, func(gap Gap) {
					gaps = append(gaps, gap)
					if scenario.reconnect && len(gaps) == 1 {
						mu.Lock()
						account, version = scenario.account, scenario.version
						mu.Unlock()
					}
				})
				close(done)
			}()
			select {
			case <-done:
			case <-ctx.Done():
				t.Fatal("fatal failure did not terminate Run without caller cancellation")
			}
			if ctx.Err() != nil || len(gaps) == 0 || gaps[len(gaps)-1].Reason != string(scenario.want) || received != 0 || histories.Load() != 0 {
				t.Fatalf("fatal stream escaped: gaps=%+v received=%d histories=%d ctx=%v", gaps, received, histories.Load(), ctx.Err())
			}
			wantStreams, wantLogins := int32(0), int32(1)
			if scenario.wrongEvent || scenario.reconnect {
				wantStreams = 1
			}
			if scenario.reconnect {
				wantLogins = 2
			}
			if streams.Load() != wantStreams || logins.Load() != wantLogins {
				t.Fatalf("missing fresh probe or retried fatal stream: streams=%d logins=%d", streams.Load(), logins.Load())
			}
		})
	}
}

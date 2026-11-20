package integration_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qilinbro/msg-manager/internal/api"
	"github.com/qilinbro/msg-manager/internal/config"
	"github.com/qilinbro/msg-manager/internal/connector/onebot"
)

const obAccount = "858494731"
const obPeer = "3221565903"

func onebotProvider(t *testing.T, history string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization)") != "" {
			http.Error(w, "bad", 400)
			return
		}
		switch r.URL.Path {
		case "/get_login_info":
			_, _ = w.Write([]byte(`{"status":"ok","retcode":0,"data":{"user_id":858494731,"nickname":"widemiao～"}}`))
		case "/get_status":
			_, _ = w.Write([]byte(`{"status":"ok","retcode":0,"data":{"online":true,"good":true}}`))
		case "/get_friend_msg_history":
			_, _ = w.Write([]byte(fmt.Sprintf(`{"status":"ok","retcode":0,"data":%s}`, history)))
		default:
			http.Error(w, "unsupported", http.StatusNotFound)
		}
	}))
}

func TestOnebotReceiveSyncPersistsInboundTextAndSkipsSelf(t *testing.T) {
	history := `[
 {"message_id":1001,"user_id":3221565903,"time":1795178000,"message":[{"type":"text","data":{"text":"好友真实文本"}}],"sender":{"user_id":3221565903,"nickname":"幺力~禾隹"}},
 {"message_id":1002,"user_id":858494731,"time":1795178010,"message":"self sent","direction":"out"}
 ]`
	provider := onebotProvider(t, history)
	defer provider.Close()
	h, err := api.NewHandler(config.Config{
		Addr: config.DefaultAddr, APIToken: token,
		DatabasePath: filepath.Join(t.TempDir(), "msg.db"), DatabaseKey: strings.Repeat("0123456789abcdef", 4),
		QQOneBot: onebot.Config{URL: provider.URL, AccessToken: "synthetic-onebot", ExpectedSelfID: obAccount},
	})
	if err != nil {
		t.Fatal(err)
	}
	if closer, ok := h.(interface{ Close() error }); ok {
		defer closer.Close()
	}
	body := `{"peer_id":"3221565903","count":20}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/connections/qq-onebot/receive", strings.NewReader(body))
	req.Host = "127.0.0.1:17890"
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("sync: %d %s", w.Code, w.Body.String())
	}
	var result struct {
		Inserted int `json:"inserted"`
		Skipped  int `json:"skipped"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Inserted != 1 || result.Skipped != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/conversations", nil)
	req2.Host = "127.0.0.1:17890"
	req2.Header.Set("Authorization", "Bearer "+token)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK || !strings.Contains(w2.Body.String(), "好友真实文本") {
		t.Fatalf("message not visible: %d %s", w2.Code, w2.Body.String())
	}
}

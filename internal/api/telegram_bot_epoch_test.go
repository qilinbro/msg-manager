package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/qilinbro/msg-manager/internal/config"
	"github.com/qilinbro/msg-manager/internal/connector/telegrambot"
	"github.com/qilinbro/msg-manager/internal/store"
)

func TestTelegramBotAPIIdleEpochReseeksBeforeAckAndSurvivesRestarts(t *testing.T) {
	// This test owns only a new encrypted t.TempDir database and a synthetic
	// Caller. It never reads ambient config, starts a service or opens a socket.
	cfg := config.Config{Addr: config.DefaultAddr, APIToken: telegramAPITestToken,
		DatabasePath: filepath.Join(t.TempDir(), "synthetic-idle-epoch.db"), DatabaseKey: strings.Repeat("ab", 32)}
	polls := 0
	caller := telegramAPICaller(func(_ context.Context, action string, in, out any) error {
		if action == "telegram.probe" {
			return telegramAPIReply(out, telegrambot.Verification{BotID: "123456", IsBot: true})
		}
		if action != "telegram.poll" {
			return errors.New("unexpected synthetic bridge action")
		}
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		var request struct {
			Offset string `json:"offset"`
		}
		if err := json.Unmarshal(raw, &request); err != nil {
			return err
		}
		want := "0"
		if polls == 2 {
			want = "200000001"
		}
		if request.Offset != want {
			t.Errorf("manual poll %d submitted offset %s, want %s before any platform read", polls, request.Offset, want)
			return errors.New("unsafe synthetic acknowledgement")
		}
		polls++
		switch polls {
		case 1:
			page := telegramAPIPage("200000000", "7", "old first", "900000001")
			page.Events = append(page.Events, telegramAPIPage("900000000", "8", "old last", "900000001").Events...)
			return telegramAPIReply(out, page)
		case 2:
			// The new randomly restarted ID also collides with an old event.
			// It is a different stable private message and must not be lost.
			return telegramAPIReply(out, telegramAPIPage("200000000", "9", "new low ID after eight days", "200000001"))
		case 3:
			return telegramAPIReply(out, telegrambot.Page{Verification: telegrambot.Verification{BotID: "123456", IsBot: true}, Events: []telegrambot.Event{}, NextOffset: "200000001"})
		default:
			return errors.New("automatic extra page/retry forbidden")
		}
	})
	openFixture := func() *handler {
		api, err := NewHandler(cfg)
		if err != nil {
			t.Fatal(err)
		}
		h := api.(*handler)
		h.closeTelegramBot()
		h.telegram, err = newTelegramConnection(telegramAPIConfig(), caller)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = h.Close() })
		return h
	}
	receive := func(h *handler, wantImported int, wantNext string) {
		t.Helper()
		w := telegramAPIRequest(h, http.MethodPost, telegramBotPrefix+"/sync", `{"consent_receive":true}`, true)
		var result store.TelegramBotCommitResult
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Imported != wantImported || result.NextOffset != wantNext || result.AIQueued != 0 || result.SourceKind != "telegram_bot" {
			t.Fatalf("idle receive failed: HTTP=%d result=%+v", w.Code, result)
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(w.Body.Bytes(), &fields) != nil || len(fields) != 10 {
			t.Fatal("internal epoch/revision leaked into public DTO")
		}
	}
	h := openFixture()
	telegramAPIVerify(t, h)
	receive(h, 2, "900000001")
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	// Artificially age only this closed synthetic fixture. The new connection
	// must make its decision from durable state, not an in-memory idle timer.
	uriPath := filepath.ToSlash(cfg.DatabasePath)
	if runtime.GOOS == "windows" {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	query := url.Values{}
	query.Set("_pragma_key", "x'"+cfg.DatabaseKey+"'")
	u.RawQuery = query.Encode()
	db, err := sql.Open("social-sqlcipher", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	aged, err := db.Exec(`UPDATE telegram_bot_cursors SET last_event_at=? WHERE workspace_id='local-default' AND bot_id='123456'`, time.Now().UTC().Add(-8*24*time.Hour).Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	if count, err := aged.RowsAffected(); err != nil || count != 1 {
		t.Fatal("synthetic idle fixture did not contain exactly one cursor")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	h = openFixture()
	telegramAPIVerify(t, h)
	receive(h, 1, "200000001")
	beforeEmpty, err := h.inbox.LoadTelegramBotCursor(context.Background(), "123456")
	if err != nil || beforeEmpty.Epoch != 1 || beforeEmpty.Revision != 2 || beforeEmpty.LastEventAt == "" {
		t.Fatal("new low cursor/epoch was not committed")
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	h = openFixture()
	telegramAPIVerify(t, h)
	receive(h, 0, "200000001")
	afterEmpty, err := h.inbox.LoadTelegramBotCursor(context.Background(), "123456")
	if err != nil || afterEmpty.LastEventAt != beforeEmpty.LastEventAt || afterEmpty.Epoch != 1 || afterEmpty.Revision != 3 || polls != 3 {
		t.Fatal("fresh restart/empty page extended lease, lost cursor or retried")
	}
	conversations, err := h.inbox.Conversations(store.Filter{Platform: "telegram"})
	if err != nil || len(conversations.Items) != 1 {
		t.Fatal("idle reset changed conversation identity")
	}
	messages, err := h.inbox.SearchMessages(conversations.Items[0].ID, "", "", 20)
	if err != nil || len(messages.Items) != 3 {
		t.Fatal("old/new epoch messages were lost or duplicated")
	}
	for _, message := range messages.Items {
		if message.Source != "telegram_bot" {
			t.Fatal("epoch reset lost authoritative message provenance")
		}
	}
}

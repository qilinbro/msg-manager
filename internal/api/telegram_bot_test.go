package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qilinbro/msg-manager/internal/config"
	"github.com/qilinbro/msg-manager/internal/connector"
	"github.com/qilinbro/msg-manager/internal/connector/nonebotbridge"
	"github.com/qilinbro/msg-manager/internal/connector/telegrambot"
	"github.com/qilinbro/msg-manager/internal/domain"
	"github.com/qilinbro/msg-manager/internal/store"
)

const telegramAPITestToken = "synthetic-telegram-local-authentication-token"

type telegramAPICaller func(context.Context, string, any, any) error

func (f telegramAPICaller) Call(ctx context.Context, action string, in, out any) error {
	return f(ctx, action, in, out)
}
func telegramAPIConfig() telegrambot.Config {
	return telegrambot.Config{Token: "123456:" + strings.Repeat("A", 35), ExpectedBotID: "123456", AllowedPrivateChatIDs: []string{"42"}, Limit: 20}
}
func telegramAPIPage(update, messageID, body, next string) telegrambot.Page {
	return telegrambot.Page{Verification: telegrambot.Verification{BotID: "123456", IsBot: true}, Events: []telegrambot.Event{{UpdateID: update, Type: "message", Message: &telegrambot.Message{ChatID: "42", MessageID: messageID, SenderID: "42", ChatType: "private", Body: body, Date: 1767225600}}}, NextOffset: next}
}
func telegramAPIReply(out, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}
func telegramAPISuccess(_ context.Context, action string, _ any, out any) error {
	if action == "telegram.probe" {
		return telegramAPIReply(out, telegrambot.Verification{BotID: "123456", IsBot: true})
	}
	if action == "telegram.poll" {
		return telegramAPIReply(out, telegramAPIPage("10", "7", "  original @bot 原文\n ", "11"))
	}
	return errors.New("unexpected action")
}
func newTelegramAPIFixture(t *testing.T, withStore bool, caller nonebotbridge.Caller) *handler {
	t.Helper()
	cfg := config.Config{Addr: config.DefaultAddr, APIToken: telegramAPITestToken}
	if withStore {
		cfg.DatabasePath = filepath.Join(t.TempDir(), "synthetic-telegram.db")
		cfg.DatabaseKey = strings.Repeat("ab", 32)
	}
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
func telegramAPIRequest(h http.Handler, method, path, body string, authenticated bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:17890"+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if authenticated {
		r.Header.Set("Authorization", "Bearer "+telegramAPITestToken)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func telegramAPIError(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	var response ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != status || response.Error.Code != code {
		t.Fatalf("HTTP=%d code=%s, want %d %s (decode=%v)", w.Code, response.Error.Code, status, code, err)
	}
	if strings.Contains(w.Body.String(), telegramAPIConfig().Token) || strings.Contains(w.Body.String(), "PRIVATE_DIAGNOSTIC_MARKER") {
		t.Fatal("unsafe diagnostics escaped to HTTP")
	}
}
func telegramAPIStatus(t *testing.T, h http.Handler) TelegramBotStatus {
	t.Helper()
	w := telegramAPIRequest(h, http.MethodGet, telegramBotPrefix+"/status", "", true)
	var status TelegramBotStatus
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &status) != nil {
		t.Fatalf("status failed: HTTP %d", w.Code)
	}
	if !status.ManualReceiveOnly || status.BackgroundReceiving || status.SendingEnabled || status.HistoryImport {
		t.Fatal("status advertised automatic receiving, sending, or history")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(w.Body.Bytes(), &fields) != nil || len(fields) != 13 {
		t.Fatal("status response changed its minimal public shape")
	}
	if _, ok := fields["bot_id"]; ok {
		t.Fatal("bot identity exposed")
	}
	if _, ok := fields["allowed_private_chat_ids"]; ok {
		t.Fatal("peer allowlist exposed")
	}
	return status
}
func telegramAPIVerify(t *testing.T, h http.Handler) {
	t.Helper()
	w := telegramAPIRequest(h, http.MethodPost, telegramBotPrefix+"/verify", "{}", true)
	var status TelegramBotStatus
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &status) != nil || !status.IdentityVerified || status.State != "identity_verified" {
		t.Fatalf("manual verification failed: HTTP %d", w.Code)
	}
}
func telegramAPIAssertNoMessages(t *testing.T, h *handler) {
	t.Helper()
	cursor, err := h.inbox.LoadTelegramBotCursor(context.Background(), "123456")
	if err != nil || cursor.NextOffset != "0" {
		t.Fatal("failed/canceled request advanced cursor")
	}
	page, err := h.inbox.Conversations(store.Filter{})
	if err != nil || len(page.Items) != 0 {
		t.Fatal("failed/canceled request saved candidate messages")
	}
}
func telegramAPIWait(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("bounded lifecycle operation did not finish")
	}
}

func TestTelegramBotAPIZeroConfigurationAndMissingRuntimeAreHonestAndOffline(t *testing.T) {
	for _, configured := range []bool{false, true} {
		t.Run(map[bool]string{false: "unconfigured", true: "runtime-missing"}[configured], func(t *testing.T) {
			cfg := config.Config{Addr: config.DefaultAddr, APIToken: telegramAPITestToken}
			if configured {
				cfg.TelegramBot = telegramAPIConfig()
			}
			api, err := NewHandler(cfg)
			if err != nil {
				t.Fatal(err)
			}
			h := api.(*handler)
			defer h.Close()
			status := telegramAPIStatus(t, h)
			wantState, wantCode := "not_configured", "TELEGRAM_BOT_NOT_CONFIGURED"
			if configured {
				wantState = "runtime_not_configured"
				wantCode = "TELEGRAM_BOT_RUNTIME_NOT_CONFIGURED"
			}
			if status.Configured != configured || status.RuntimeConfigured || status.IdentityVerified || status.State != wantState || status.CheckedAt != "" || status.LastSyncAt != "" || status.LastResult != nil {
				t.Fatalf("unconfigured status=%+v", status)
			}
			telegramAPIError(t, telegramAPIRequest(h, http.MethodPost, telegramBotPrefix+"/verify", "{}", true), 503, wantCode)
			telegramAPIError(t, telegramAPIRequest(h, http.MethodPost, telegramBotPrefix+"/sync", `{"consent_receive":true}`, true), 503, "STORAGE_DISABLED")
		})
	}
}

func TestTelegramBotAPIManualVerifiedPageAtomicResultAndRealInboxSource(t *testing.T) {
	var calls atomic.Int32
	h := newTelegramAPIFixture(t, true, telegramAPICaller(func(ctx context.Context, action string, in, out any) error {
		calls.Add(1)
		return telegramAPISuccess(ctx, action, in, out)
	}))
	for i := 0; i < 10; i++ {
		status := telegramAPIStatus(t, h)
		if status.State != "configured" || status.IdentityVerified || status.LastResult != nil {
			t.Fatal("status performed verification or receiving")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("status made IPC requests")
	}
	telegramAPIError(t, telegramAPIRequest(h, "POST", telegramBotPrefix+"/sync", `{"consent_receive":true}`, true), 409, "TELEGRAM_BOT_VERIFICATION_REQUIRED")
	if calls.Load() != 0 {
		t.Fatal("unverified sync made IPC request")
	}
	telegramAPIVerify(t, h)
	if calls.Load() != 1 {
		t.Fatal("verify called more than one bridge action")
	}
	w := telegramAPIRequest(h, "POST", telegramBotPrefix+"/sync", `{"consent_receive":true}`, true)
	var result store.TelegramBotCommitResult
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Events != 1 || result.Imported != 1 || result.NextOffset != "11" || result.SourceKind != "telegram_bot" || result.AIQueued != 0 {
		t.Fatalf("manual page response failed: HTTP %d result=%+v", w.Code, result)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(w.Body.Bytes(), &fields) != nil || len(fields) != 10 {
		t.Fatal("sync response is wrapped or contains raw platform data")
	}
	status := telegramAPIStatus(t, h)
	if !status.IdentityVerified || status.LastSyncAt == "" || status.CheckedAt == "" || status.LastResult == nil || *status.LastResult != result {
		t.Fatal("last successful atomic page not represented")
	}
	if calls.Load() != 2 {
		t.Fatal("manual sync or later status started automatic requests")
	}
	w = telegramAPIRequest(h, "GET", "/api/v1/inbox/connections", "", true)
	var connections ListResponse[store.Connection]
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &connections) != nil || len(connections.Items) != 1 || connections.Items[0].Source != "telegram_bot" || connections.Items[0].Platform != "telegram" {
		t.Fatal("real inbox connection lost Bot API provenance")
	}
	w = telegramAPIRequest(h, "GET", "/api/v1/conversations?platform=telegram", "", true)
	var conversations store.ConversationPage
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &conversations) != nil || len(conversations.Items) != 1 {
		t.Fatal("manual page missing from inbox")
	}
	w = telegramAPIRequest(h, "GET", "/api/v1/conversations/"+conversations.Items[0].ID+"/messages", "", true)
	var messages store.MessagePage
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &messages) != nil || len(messages.Items) != 1 || messages.Items[0].Body != "  original @bot 原文\n " || messages.Items[0].Source != "telegram_bot" {
		t.Fatal("real read API trimmed body or relabeled it as Desktop import")
	}
	w = telegramAPIRequest(h, "GET", "/api/v1/connections", "", true)
	var catalog ListResponse[connector.Descriptor]
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &catalog) != nil {
		t.Fatal("connection catalog failed")
	}
	found := false
	for _, item := range catalog.Items {
		if item.ID == "telegram-bot" {
			found = true
			if item.Status != "identity_verified" || !item.Capabilities.ReceiveText || item.Capabilities.SendText || item.Capabilities.HistoryImport || !strings.Contains(item.Description, "NoneBot") {
				t.Fatal("catalog misrepresented manual receive-only connector")
			}
		}
	}
	if !found {
		t.Fatal("dedicated Bot connector absent from catalog")
	}
}

func TestTelegramBotAPIStrictConsentMethodsAuthScopeAndModelBoundaryBeforeIPC(t *testing.T) {
	var calls atomic.Int32
	h := newTelegramAPIFixture(t, true, telegramAPICaller(func(ctx context.Context, action string, in, out any) error {
		calls.Add(1)
		return telegramAPISuccess(ctx, action, in, out)
	}))
	for _, resource := range []string{"verify", "sync"} {
		bodies := []string{"", `null`, `[]`, `{"consent_receive":null}`, `{"consent_receive":false}`, `{"Consent_Receive":true}`, `{"consent_receive":true,"consent_receive":true}`, `{"consent_receive":true,"consent\u005freceive":true}`, `{"consent_receive":true,"workspace_id":"local-default"}`, `{"consent_receive":true,"ai_organize":true}`, `{"consent_receive":true,"token":"PRIVATE_DIAGNOSTIC_MARKER"}`, `{} {}`, "{\"bad\":\"\xff\"}"}
		if resource == "sync" {
			bodies = append(bodies, "{}")
		}
		for _, body := range bodies {
			telegramAPIError(t, telegramAPIRequest(h, "POST", telegramBotPrefix+"/"+resource, body, true), 400, "TELEGRAM_BOT_INVALID_REQUEST")
		}
		telegramAPIError(t, telegramAPIRequest(h, "POST", telegramBotPrefix+"/"+resource, strings.Repeat(" ", 1025), true), 413, "TELEGRAM_BOT_PAYLOAD_TOO_LARGE")
		telegramAPIError(t, telegramAPIRequest(h, "GET", telegramBotPrefix+"/"+resource, "", true), 405, "METHOD_NOT_ALLOWED")
	}
	telegramAPIError(t, telegramAPIRequest(h, "POST", telegramBotPrefix+"/verify?limit=1", "{}", true), 400, "TELEGRAM_BOT_INVALID_REQUEST")
	telegramAPIError(t, telegramAPIRequest(h, "POST", telegramBotPrefix+"/verify?workspace_id=%zz", "{}", true), 400, "TELEGRAM_BOT_INVALID_REQUEST")
	telegramAPIError(t, telegramAPIRequest(h, "POST", telegramBotPrefix+"/verify?workspace_id=foreign", "{}", true), 403, "SCOPE_DENIED")
	telegramAPIError(t, telegramAPIRequest(h, "GET", telegramBotPrefix+"/status", "", false), 401, "UNAUTHORIZED")
	telegramAPIError(t, telegramAPIRequest(h, "POST", telegramBotPrefix+"/status", "{}", true), 405, "METHOD_NOT_ALLOWED")
	telegramAPIError(t, telegramAPIRequest(h, "POST", telegramBotPrefix+"/monitor", "{}", true), 404, "NOT_FOUND")
	for name, modify := range map[string]func(*http.Request){
		"foreign-origin":    func(r *http.Request) { r.Header.Set("Origin", "https://attacker.invalid") },
		"foreign-workspace": func(r *http.Request) { r.Header.Set("X-Workspace-ID", "foreign") },
		"assistant": func(r *http.Request) {
			*r = *r.WithContext(context.WithValue(r.Context(), assistantDispatchKey{}, true))
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "http://127.0.0.1:17890"+telegramBotPrefix+"/verify", strings.NewReader("{}"))
			r.Header.Set("Authorization", "Bearer "+telegramAPITestToken)
			r.Header.Set("Content-Type", "application/json")
			modify(r)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 403 {
				t.Fatalf("unsafe request HTTP=%d", w.Code)
			}
		})
	}
	r := httptest.NewRequest("POST", "http://127.0.0.1:17890"+telegramBotPrefix+"/verify", strings.NewReader("{}"))
	r.Header.Set("Authorization", "Bearer "+telegramAPITestToken)
	r.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	telegramAPIError(t, w, 400, "TELEGRAM_BOT_INVALID_REQUEST")
	// The dedicated handler also defends against bypassing root authentication.
	r = httptest.NewRequest("POST", "http://127.0.0.1:17890"+telegramBotPrefix+"/verify", strings.NewReader("{}"))
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.serveTelegramBot(w, r, "synthetic-trace")
	telegramAPIError(t, w, 403, "FORBIDDEN_SCOPE")
	if calls.Load() != 0 {
		t.Fatal("invalid/untrusted request reached bridge")
	}
	telegramAPIAssertNoMessages(t, h)
}

func TestTelegramBotAPIFailuresAreWhitelistedSanitizedAndNeverAcknowledge(t *testing.T) {
	var failure error
	h := newTelegramAPIFixture(t, true, telegramAPICaller(func(context.Context, string, any, any) error { return failure }))
	for _, test := range []struct {
		kind   nonebotbridge.Kind
		status int
	}{
		{nonebotbridge.AuthFailed, 503}, {nonebotbridge.RateLimited, 429}, {nonebotbridge.Unreachable, 503},
		{nonebotbridge.ProtocolError, 503}, {nonebotbridge.IdentityMismatch, 503}, {nonebotbridge.WebhookPresent, 503},
		{nonebotbridge.Canceled, 408}, {nonebotbridge.PermissionDenied, 503}, {nonebotbridge.PlatformRejected, 503},
		{nonebotbridge.Unavailable, 503}, {nonebotbridge.InvalidConfig, 503},
	} {
		t.Run(string(test.kind), func(t *testing.T) {
			failure = &nonebotbridge.Error{Kind: test.kind, RetryAfter: 2 * time.Second, SendState: "PRIVATE_DIAGNOSTIC_MARKER", PlatformCode: 123456}
			w := telegramAPIRequest(h, "POST", telegramBotPrefix+"/verify", "{}", true)
			telegramAPIError(t, w, test.status, "TELEGRAM_BOT_"+strings.ToUpper(string(test.kind)))
			if test.kind == nonebotbridge.RateLimited && w.Header().Get("Retry-After") != "2" {
				t.Fatal("safe bounded retry hint absent")
			}
			status := telegramAPIStatus(t, h)
			if status.IdentityVerified || status.State != string(test.kind) {
				t.Fatal("failure left verification current")
			}
			telegramAPIAssertNoMessages(t, h)
		})
	}
	failure = errors.New("PRIVATE_DIAGNOSTIC_MARKER " + telegramAPIConfig().Token)
	telegramAPIError(t, telegramAPIRequest(h, "POST", telegramBotPrefix+"/verify", "{}", true), 503, "TELEGRAM_BOT_PROTOCOL_ERROR")
}

func TestTelegramBotAPIPageValidationFailureAndStorageDisabledDoNotCommit(t *testing.T) {
	for _, kind := range []string{"identity", "webhook", "scope", "watermark", "malformed"} {
		t.Run(kind, func(t *testing.T) {
			h := newTelegramAPIFixture(t, true, telegramAPICaller(func(ctx context.Context, action string, in, out any) error {
				if action == "telegram.probe" {
					return telegramAPISuccess(ctx, action, in, out)
				}
				page := telegramAPIPage("10", "7", "PRIVATE_DIAGNOSTIC_MARKER", "11")
				switch kind {
				case "identity":
					page.BotID = "99"
				case "webhook":
					page.WebhookPresent = true
				case "scope":
					page.Events[0].Message.ChatID = "99"
				case "watermark":
					page.NextOffset = "999"
				case "malformed":
					return json.Unmarshal([]byte(`{"bot_id":"123456","is_bot":true}`), out)
				}
				return telegramAPIReply(out, page)
			}))
			telegramAPIVerify(t, h)
			w := telegramAPIRequest(h, "POST", telegramBotPrefix+"/sync", `{"consent_receive":true}`, true)
			code := "TELEGRAM_BOT_PROTOCOL_ERROR"
			if kind == "identity" {
				code = "TELEGRAM_BOT_IDENTITY_MISMATCH"
			}
			if kind == "webhook" {
				code = "TELEGRAM_BOT_WEBHOOK_PRESENT"
			}
			telegramAPIError(t, w, 503, code)
			telegramAPIAssertNoMessages(t, h)
			if telegramAPIStatus(t, h).IdentityVerified {
				t.Fatal("invalid poll kept old verification")
			}
		})
	}
	var calls atomic.Int32
	h := newTelegramAPIFixture(t, false, telegramAPICaller(func(ctx context.Context, action string, in, out any) error {
		calls.Add(1)
		return telegramAPISuccess(ctx, action, in, out)
	}))
	telegramAPIVerify(t, h)
	telegramAPIError(t, telegramAPIRequest(h, "POST", telegramBotPrefix+"/sync", `{"consent_receive":true}`, true), 503, "STORAGE_DISABLED")
	if calls.Load() != 1 {
		t.Fatal("storage-disabled sync made IPC request")
	}
}

func TestTelegramBotAPIUnsupportedAndEmptyPagesNeedSeparateExplicitConsent(t *testing.T) {
	var offsets []string
	h := newTelegramAPIFixture(t, true, telegramAPICaller(func(ctx context.Context, action string, in, out any) error {
		if action == "telegram.probe" {
			return telegramAPISuccess(ctx, action, in, out)
		}
		raw, _ := json.Marshal(in)
		var request struct {
			Offset string `json:"offset"`
		}
		_ = json.Unmarshal(raw, &request)
		offsets = append(offsets, request.Offset)
		page := telegrambot.Page{Verification: telegrambot.Verification{BotID: "123456", IsBot: true}, Events: []telegrambot.Event{}, NextOffset: "11"}
		if request.Offset == "0" {
			page.Events = append(page.Events, telegrambot.Event{UpdateID: "10", Type: "unknown", Reason: "unsupported_event"})
		}
		return telegramAPIReply(out, page)
	}))
	telegramAPIVerify(t, h)
	w := telegramAPIRequest(h, "POST", telegramBotPrefix+"/sync", `{"consent_receive":true}`, true)
	var first store.TelegramBotCommitResult
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &first) != nil || first.Events != 1 || first.Imported != 0 || first.Unsupported != 1 || !first.Partial || first.NextOffset != "11" || first.AIQueued != 0 {
		t.Fatal("unsupported-only page was not honestly counted")
	}
	telegramAPIError(t, telegramAPIRequest(h, "POST", telegramBotPrefix+"/sync", `{}`, true), 400, "TELEGRAM_BOT_INVALID_REQUEST")
	w = telegramAPIRequest(h, "POST", telegramBotPrefix+"/sync", `{"consent_receive":true}`, true)
	var empty store.TelegramBotCommitResult
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &empty) != nil || empty.Events != 0 || empty.Imported != 0 || empty.Partial || empty.NextOffset != "11" || empty.AIQueued != 0 {
		t.Fatal("empty page changed acknowledgement or implied full history")
	}
	if !reflect.DeepEqual(offsets, []string{"0", "11"}) {
		t.Fatal("cursor was not commit-owned or another page fetched automatically")
	}
	conversations, err := h.inbox.Conversations(store.Filter{})
	if err != nil || len(conversations.Items) != 0 {
		t.Fatal("unsupported-only page created conversations")
	}
}

func TestTelegramBotAPIMessageConflictAndUnavailableStoreAreSanitized(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		t.Run(map[bool]string{false: "conflict", true: "store-unavailable"}[unavailable], func(t *testing.T) {
			h := newTelegramAPIFixture(t, true, telegramAPICaller(func(ctx context.Context, action string, in, out any) error {
				if action == "telegram.probe" {
					return telegramAPISuccess(ctx, action, in, out)
				}
				return telegramAPIReply(out, telegramAPIPage("11", "7", "PRIVATE_DIAGNOSTIC_MARKER conflict", "12"))
			}))
			_, err := h.inbox.CommitTelegramBot(context.Background(), "123456", []string{"42"}, store.TelegramBotCursor{NextOffset: "0"}, "0", telegramAPIPage("10", "7", "original", "11"))
			if err != nil {
				t.Fatal(err)
			}
			telegramAPIVerify(t, h)
			if unavailable {
				_ = h.inbox.Close()
			}
			w := telegramAPIRequest(h, "POST", telegramBotPrefix+"/sync", `{"consent_receive":true}`, true)
			if unavailable {
				telegramAPIError(t, w, 503, "TELEGRAM_BOT_STORE_UNAVAILABLE")
			} else {
				telegramAPIError(t, w, 409, "TELEGRAM_BOT_CONFLICT")
				cursor, err := h.inbox.LoadTelegramBotCursor(context.Background(), "123456")
				if err != nil || cursor.NextOffset != "11" {
					t.Fatal("conflicting page acknowledged")
				}
			}
			if telegramAPIStatus(t, h).IdentityVerified {
				t.Fatal("storage failure left verification current")
			}
		})
	}
}

func TestTelegramBotAPICursorCASRejectsStaleCandidateAfterConcurrentCommit(t *testing.T) {
	var h *handler
	h = newTelegramAPIFixture(t, true, telegramAPICaller(func(ctx context.Context, action string, in, out any) error {
		if action == "telegram.probe" {
			return telegramAPISuccess(ctx, action, in, out)
		}
		_, err := h.inbox.CommitTelegramBot(ctx, "123456", []string{"42"}, store.TelegramBotCursor{NextOffset: "0"}, "0", telegramAPIPage("10", "7", "concurrent winner", "11"))
		if err != nil {
			return err
		}
		return telegramAPIReply(out, telegramAPIPage("11", "8", "PRIVATE_DIAGNOSTIC_MARKER stale candidate", "12"))
	}))
	telegramAPIVerify(t, h)
	telegramAPIError(t, telegramAPIRequest(h, "POST", telegramBotPrefix+"/sync", `{"consent_receive":true}`, true), 409, "TELEGRAM_BOT_CURSOR_CHANGED")
	cursor, err := h.inbox.LoadTelegramBotCursor(context.Background(), "123456")
	if err != nil || cursor.NextOffset != "11" {
		t.Fatal("stale page overwrote winning cursor")
	}
	conversations, err := h.inbox.Conversations(store.Filter{})
	if err != nil || len(conversations.Items) != 1 {
		t.Fatal("winning page absent")
	}
	messages, err := h.inbox.SearchMessages(conversations.Items[0].ID, "", "", 20)
	if err != nil || len(messages.Items) != 1 || messages.Items[0].Body != "concurrent winner" {
		t.Fatal("stale candidate partially committed")
	}
}

func TestTelegramBotAPIReverifyRevokesBlockedReceiveBeforeReturningBusy(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	var polls atomic.Int32
	h := newTelegramAPIFixture(t, true, telegramAPICaller(func(ctx context.Context, action string, in, out any) error {
		if action == "telegram.probe" {
			return telegramAPISuccess(ctx, action, in, out)
		}
		polls.Add(1)
		close(entered)
		<-release // Deliberately ignore ctx: the Go boundary must still fence it.
		return telegramAPIReply(out, telegramAPIPage("10", "7", "PRIVATE_DIAGNOSTIC_MARKER canceled candidate", "11"))
	}))
	telegramAPIVerify(t, h)
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		result <- telegramAPIRequest(h, "POST", telegramBotPrefix+"/sync", `{"consent_receive":true}`, true)
	}()
	telegramAPIWait(t, entered)
	telegramAPIError(t, telegramAPIRequest(h, "POST", telegramBotPrefix+"/sync", `{"consent_receive":true}`, true), 409, "TELEGRAM_BOT_BUSY")
	telegramAPIError(t, telegramAPIRequest(h, "POST", telegramBotPrefix+"/verify", "{}", true), 409, "TELEGRAM_BOT_BUSY")
	status := telegramAPIStatus(t, h)
	if status.IdentityVerified || status.State != "canceled" {
		t.Fatal("reverify did not revoke older candidate")
	}
	close(release)
	select {
	case w := <-result:
		telegramAPIError(t, w, 408, "TELEGRAM_BOT_CANCELED")
	case <-time.After(5 * time.Second):
		t.Fatal("revoked receive did not finish")
	}
	telegramAPIAssertNoMessages(t, h)
	telegramAPIError(t, telegramAPIRequest(h, "POST", telegramBotPrefix+"/sync", `{"consent_receive":true}`, true), 409, "TELEGRAM_BOT_VERIFICATION_REQUIRED")
	telegramAPIVerify(t, h)
	if polls.Load() != 1 {
		t.Fatal("busy/reverification started extra poll")
	}
}

func TestTelegramBotAPIRequestCancellationAndCloseJoinActiveIPC(t *testing.T) {
	for _, byClose := range []bool{false, true} {
		t.Run(map[bool]string{false: "request-cancel", true: "close"}[byClose], func(t *testing.T) {
			entered, workerExited := make(chan struct{}), make(chan struct{})
			h := newTelegramAPIFixture(t, true, telegramAPICaller(func(ctx context.Context, action string, in, out any) error {
				if action == "telegram.probe" {
					return telegramAPISuccess(ctx, action, in, out)
				}
				close(entered)
				<-ctx.Done()
				close(workerExited)
				return telegramAPIReply(out, telegramAPIPage("10", "7", "PRIVATE_DIAGNOSTIC_MARKER canceled", "11"))
			}))
			telegramAPIVerify(t, h)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := httptest.NewRequest("POST", "http://127.0.0.1:17890"+telegramBotPrefix+"/sync", strings.NewReader(`{"consent_receive":true}`)).WithContext(ctx)
			r.Header.Set("Authorization", "Bearer "+telegramAPITestToken)
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { defer close(done); h.ServeHTTP(w, r) }()
			telegramAPIWait(t, entered)
			if byClose {
				closed := make(chan struct{})
				go func() { h.closeTelegramBot(); close(closed) }()
				telegramAPIWait(t, closed)
			} else {
				cancel()
			}
			telegramAPIWait(t, workerExited)
			telegramAPIWait(t, done)
			telegramAPIError(t, w, 408, "TELEGRAM_BOT_CANCELED")
			telegramAPIAssertNoMessages(t, h)
			status := telegramAPIStatus(t, h)
			if status.IdentityVerified || byClose && status.State != "closed" || !byClose && status.State != "canceled" {
				t.Fatal("canceled work resurrected verification")
			}
			if byClose {
				telegramAPIError(t, telegramAPIRequest(h, "POST", telegramBotPrefix+"/verify", "{}", true), 503, "TELEGRAM_BOT_CLOSED")
			}
		})
	}
}

func TestTelegramBotAPICloseCancelsBeforeWaitingForLifecycleFence(t *testing.T) {
	h := newTelegramAPIFixture(t, true, telegramAPICaller(telegramAPISuccess))
	closed := make(chan struct{})
	func() {
		h.telegram.mu.Lock()
		defer h.telegram.mu.Unlock()
		go func() { h.closeTelegramBot(); close(closed) }()
		telegramAPIWait(t, h.telegram.root.Done())
	}()
	telegramAPIWait(t, closed)
	if status := telegramAPIStatus(t, h); status.State != "closed" || status.IdentityVerified {
		t.Fatal("close lifecycle fence failed")
	}
}

func TestTelegramBotAPIHandlerCloseRevokesReceiveBeforeOtherComponentJoins(t *testing.T) {
	entered, workerExited := make(chan struct{}), make(chan struct{})
	h := newTelegramAPIFixture(t, true, telegramAPICaller(func(ctx context.Context, action string, in, out any) error {
		if action == "telegram.probe" {
			return telegramAPISuccess(ctx, action, in, out)
		}
		close(entered)
		<-ctx.Done()
		close(workerExited)
		return telegramAPIReply(out, telegramAPIPage("10", "7", "synthetic shutdown candidate", "11"))
	}))
	telegramAPIVerify(t, h)
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		result <- telegramAPIRequest(h, "POST", telegramBotPrefix+"/sync", `{"consent_receive":true}`, true)
	}()
	telegramAPIWait(t, entered)
	// Keep the first existing shutdown join blocked. Telegram cancellation must
	// not depend on this unrelated operation voluntarily completing first.
	h.feishu.wg.Add(1)
	released := false
	defer func() {
		if !released {
			h.feishu.wg.Done()
		}
	}()
	closed := make(chan struct{})
	go func() { _ = h.Close(); close(closed) }()
	telegramAPIWait(t, h.feishu.ctx.Done())
	telegramAPIWait(t, h.telegram.root.Done())
	telegramAPIWait(t, workerExited)
	select {
	case w := <-result:
		telegramAPIError(t, w, 408, "TELEGRAM_BOT_CANCELED")
	case <-time.After(5 * time.Second):
		t.Fatal("receive was not canceled while another shutdown join was blocked")
	}
	telegramAPIAssertNoMessages(t, h)
	h.feishu.wg.Done()
	released = true
	telegramAPIWait(t, closed)
}

func TestTelegramBotAPIPathAndInternalScopeAreExact(t *testing.T) {
	if telegramBotPath("/api/v1/connections/telegram-bot-evil/verify") || !telegramBotPath(telegramBotPrefix+"/verify") || telegramBotMutation("GET", telegramBotPrefix+"/verify") || telegramBotMutation("POST", telegramBotPrefix+"/monitor") {
		t.Fatal("route helpers widened manual boundary")
	}
	h := newTelegramAPIFixture(t, true, telegramAPICaller(telegramAPISuccess))
	r := httptest.NewRequest("POST", "http://127.0.0.1:17890"+telegramBotPrefix+"/verify?workspace_id="+domain.LocalWorkspaceID, strings.NewReader("{}"))
	r = r.WithContext(withLocalPrincipal(r.Context()))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.serveTelegramBot(w, r, "synthetic-trace")
	if w.Code != 200 {
		t.Fatal("canonical local workspace claim rejected")
	}
	if !reflect.DeepEqual(h.telegram.client.AllowedPrivateChatIDs(), []string{"42"}) {
		t.Fatal("request changed configured allowlist")
	}
}

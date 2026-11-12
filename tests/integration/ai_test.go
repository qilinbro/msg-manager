package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qilinbro/msg-manager/internal/agent"
	"github.com/qilinbro/msg-manager/internal/api"
	"github.com/qilinbro/msg-manager/internal/config"
	"github.com/qilinbro/msg-manager/internal/store"
)

type modelFixture struct {
	calls                 atomic.Int32
	invalid               atomic.Bool
	forbiddenConversation string
	started               chan struct{}
	release               chan struct{}
}

func (f *modelFixture) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		if r.Method != "POST" || r.URL.Path != "/v1/chat/completions" {
			t.Error("wrong model transport route")
		}
		var req struct {
			Messages       []struct{ Role, Content string }
			Tools          any
			ResponseFormat struct {
				Type string `json:"type"`
			} `json:"response_format"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil || len(req.Messages) != 2 || req.Tools != nil || req.ResponseFormat.Type != "json_object" {
			t.Error("unsafe model request shape")
			w.WriteHeader(400)
			return
		}
		var input agent.Input
		if json.Unmarshal([]byte(req.Messages[1].Content), &input) != nil || len(input.Messages) == 0 {
			t.Error("missing scoped evidence")
			w.WriteHeader(400)
			return
		}
		if strings.Contains(req.Messages[1].Content, f.forbiddenConversation) && f.forbiddenConversation != "" {
			t.Error("another conversation leaked to model")
		}
		output := agent.Output{Intent: "information_request", Summary: "对方询问资料进展。", Draft: "我看到了，稍后核对资料再回复你。", RiskFlags: []string{}, EvidenceIDs: []string{input.Messages[0].ID}, MemoryCandidates: []agent.MemoryCandidate{{Content: "对方正在关注资料进展。", EvidenceIDs: []string{input.Messages[0].ID}}}}
		if f.invalid.Load() {
			output.EvidenceIDs = []string{"cross-conversation-evidence"}
		}
		if f.started != nil {
			close(f.started)
			select {
			case <-f.release:
			case <-r.Context().Done():
				return
			case <-time.After(5 * time.Second):
				t.Error("synthetic model release timed out")
				return
			}
		}
		encoded, _ := json.Marshal(output)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": string(encoded)}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 24, "completion_tokens": 16}})
	}
}

func newAIHandler(t *testing.T, baseURL string, limit int) http.Handler {
	t.Helper()
	return newAIHandlerWithPath(t, baseURL, "synthetic-json", limit, filepath.Join(t.TempDir(), "ai.db"))
}

func newAIHandlerWithPath(t *testing.T, baseURL, model string, limit int, path string) http.Handler {
	t.Helper()
	h, err := api.NewHandler(config.Config{Addr: config.DefaultAddr, APIToken: token, DatabasePath: path, DatabaseKey: strings.Repeat("cd", 32), AI: agent.Config{BaseURL: baseURL, Model: model}, AIDailyLimit: limit})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.(interface{ Close() error }).Close() })
	return h
}

func TestAIInFlightChangesAndCancelDoNotSave(t *testing.T) {
	for _, change := range []string{"message", "pause", "cancel"} {
		t.Run(change, func(t *testing.T) {
			fixture := &modelFixture{started: make(chan struct{}), release: make(chan struct{})}
			model := httptest.NewServer(fixture.handler(t))
			defer model.Close()
			defer close(fixture.release)
			h := newAIHandler(t, model.URL+"/v1", 10)
			id := importAIConversation(t, h, "account", "peer", "原始合成消息")
			path := "/api/v1/conversations/" + id
			preview := readAIContext(t, h, id)
			body, _ := json.Marshal(map[string]any{"context_hash": preview.ContextHash, "confirmed": true})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := httptest.NewRequest("POST", "http://"+config.DefaultAddr+path+"/analyze", strings.NewReader(string(body))).WithContext(ctx)
			r.Header.Set("Authorization", "Bearer "+token)
			r.Header.Set("Content-Type", "application/json")
			completed := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				completed <- w
			}()
			select {
			case <-fixture.started:
			case <-time.After(2 * time.Second):
				t.Fatal("model request did not start")
			}
			checkError(t, inboxRequest(h, "POST", path+"/analyze", map[string]any{"context_hash": preview.ContextHash, "confirmed": true}), 503, "AI_BUSY")
			switch change {
			case "message":
				batch := store.ImportRequest{Platform: "qq", AccountID: "account", Messages: []store.ImportMessage{{ExternalID: "new", PeerID: "peer", Direction: "incoming", Body: "迟到的新消息", OccurredAt: "2025-12-31T00:00:00Z"}}}
				if w := inboxRequest(h, "POST", "/api/v1/imports/messages", batch); w.Code != 200 {
					t.Fatal(w.Body.String())
				}
			case "pause":
				if w := inboxRequest(h, "PATCH", path+"/ai-settings", map[string]any{"mode": "paused"}); w.Code != 200 {
					t.Fatal(w.Body.String())
				}
			case "cancel":
				cancel()
			}
			if change != "cancel" {
				fixture.release <- struct{}{}
			}
			select {
			case w := <-completed:
				if change == "cancel" {
					checkError(t, w, 504, "AI_TIMEOUT")
				} else {
					checkError(t, w, 409, "STALE_CONTEXT")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("model request did not finish")
			}
			w := inboxRequest(h, "GET", path+"/analysis", nil)
			if !strings.Contains(w.Body.String(), `"analysis":null`) || !strings.Contains(w.Body.String(), `"memories":[]`) || fixture.calls.Load() != 1 {
				t.Fatal("stale/cancelled analysis saved or busy request reached provider", w.Body.String())
			}
		})
	}
}

func TestAIConfirmationBindsDestinationAndModel(t *testing.T) {
	for _, change := range []string{"destination", "model"} {
		t.Run(change, func(t *testing.T) {
			fixture := &modelFixture{}
			model := httptest.NewServer(fixture.handler(t))
			defer model.Close()
			otherFixture := &modelFixture{}
			other := httptest.NewServer(otherFixture.handler(t))
			defer other.Close()
			dbPath := filepath.Join(t.TempDir(), "restart.db")
			h := newAIHandlerWithPath(t, model.URL+"/v1", "synthetic-json", 10, dbPath)
			id := importAIConversation(t, h, "account", "peer", "确认目标模型的合成消息")
			path := "/api/v1/conversations/" + id
			preview := readAIContext(t, h, id)
			w := inboxRequest(h, "POST", path+"/analyze", map[string]any{"context_hash": preview.ContextHash, "confirmed": true})
			var record struct{ ID string }
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &record) != nil || record.ID == "" {
				t.Fatal(w.Body.String())
			}
			if err := h.(interface{ Close() error }).Close(); err != nil {
				t.Fatal(err)
			}
			baseURL, modelID := model.URL+"/v1", "synthetic-json"
			if change == "destination" {
				baseURL = other.URL + "/v1"
			} else {
				modelID = "different-synthetic-model"
			}
			h = newAIHandlerWithPath(t, baseURL, modelID, 10, dbPath)
			newPreview := readAIContext(t, h, id)
			if preview.ContextHash == newPreview.ContextHash {
				t.Fatal("changed model configuration preserved old confirmation")
			}
			checkError(t, inboxRequest(h, "POST", path+"/analyze", map[string]any{"context_hash": preview.ContextHash, "confirmed": true}), 409, "STALE_CONTEXT")
			checkError(t, inboxRequest(h, "PATCH", path+"/draft", map[string]any{"analysis_id": record.ID, "body": "过时草稿"}), 409, "STALE_CONTEXT")
			w = inboxRequest(h, "GET", path+"/analysis", nil)
			var latest struct{ Analysis struct{ Stale bool } }
			if json.Unmarshal(w.Body.Bytes(), &latest) != nil || !latest.Analysis.Stale || fixture.calls.Load() != 1 || otherFixture.calls.Load() != 0 {
				t.Fatal("configuration change not flagged or leaked to different destination", w.Body.String())
			}
		})
	}
}

func importAIConversation(t *testing.T, h http.Handler, account, peer, body string) string {
	t.Helper()
	batch := store.ImportRequest{Platform: "qq", AccountID: account, Messages: []store.ImportMessage{{ExternalID: "one", PeerID: peer, PeerName: "同名联系人", Direction: "incoming", Body: body, OccurredAt: "2026-01-01T00:00:00Z"}}}
	w := inboxRequest(h, "POST", "/api/v1/imports/messages", batch)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = inboxRequest(h, "GET", "/api/v1/conversations?q="+peer, nil)
	var page store.ConversationPage
	if json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Items) != 1 {
		t.Fatal("cannot find scoped conversation")
	}
	return page.Items[0].ID
}

func readAIContext(t *testing.T, h http.Handler, id string) store.AIContext {
	t.Helper()
	w := inboxRequest(h, "GET", "/api/v1/conversations/"+id+"/ai-context", nil)
	var ctx store.AIContext
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &ctx) != nil || ctx.ContextHash == "" {
		t.Fatal("context preview failed", w.Code, w.Body.String())
	}
	return ctx
}

func TestAIManagementEndToEndConsentMemoryDraftAndScope(t *testing.T) {
	fixture := &modelFixture{forbiddenConversation: "OTHER-ACCOUNT-SECRET-SYNTHETIC"}
	model := httptest.NewServer(fixture.handler(t))
	defer model.Close()
	h := newAIHandler(t, model.URL+"/v1", 10)
	a := importAIConversation(t, h, "account-a", "friend-a", "资料进度怎么样？请提供付款信息。")
	b := importAIConversation(t, h, "account-b", "friend-b", fixture.forbiddenConversation)
	path := "/api/v1/conversations/" + a
	preview := readAIContext(t, h, a)
	if fixture.calls.Load() != 0 || len(preview.Messages) != 1 {
		t.Fatal("preview called model or mixed conversations")
	}
	checkError(t, inboxRequest(h, "POST", path+"/analyze", map[string]any{"context_hash": preview.ContextHash, "confirmed": false}), 400, "AI_CONFIRMATION_REQUIRED")
	checkError(t, inboxRequest(h, "POST", path+"/analyze", map[string]any{"context_hash": strings.Repeat("0", 64), "confirmed": true}), 409, "STALE_CONTEXT")
	if fixture.calls.Load() != 0 {
		t.Fatal("unconfirmed request contacted model")
	}
	response := inboxRequest(h, "POST", path+"/analyze", map[string]any{"context_hash": preview.ContextHash, "confirmed": true})
	var record struct {
		ID, Draft      string
		Stale          bool
		RiskFlags      []string `json:"risk_flags"`
		SendingEnabled bool     `json:"sending_enabled"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &record) != nil || record.ID == "" || record.SendingEnabled || record.Stale || !strings.Contains(strings.Join(record.RiskFlags, ","), "payment") {
		t.Fatal("analysis pipeline", response.Code, response.Body.String())
	}
	var latest struct {
		Analysis struct {
			ID, Draft string
			Stale     bool
		}
		Memories []store.AIMemory
	}
	w := inboxRequest(h, "GET", path+"/analysis", nil)
	_ = json.Unmarshal(w.Body.Bytes(), &latest)
	if len(latest.Memories) != 1 || latest.Memories[0].VerificationState != "unverified" {
		t.Fatal("model promoted its own memory")
	}
	if ctx := readAIContext(t, h, a); len(ctx.Memories) != 0 {
		t.Fatal("unverified memory sent into subsequent context")
	}
	edit := inboxRequest(h, "PATCH", path+"/draft", map[string]any{"analysis_id": record.ID, "body": "这是我本人核对后的本地草稿。"})
	if edit.Code != 200 {
		t.Fatal(edit.Body.String())
	}
	w = inboxRequest(h, "GET", path+"/analysis", nil)
	_ = json.Unmarshal(w.Body.Bytes(), &latest)
	if latest.Analysis.Draft != "这是我本人核对后的本地草稿。" {
		t.Fatal("draft edit not persistent")
	}
	memoryID := latest.Memories[0].ID
	checkError(t, inboxRequest(h, "PATCH", "/api/v1/conversations/"+b+"/memories/"+memoryID, map[string]any{"verification_state": "confirmed"}), 404, "CONVERSATION_NOT_FOUND")
	if w = inboxRequest(h, "PATCH", path+"/memories/"+memoryID, map[string]any{"verification_state": "confirmed"}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	confirmed := readAIContext(t, h, a)
	if len(confirmed.Memories) != 1 || confirmed.ContextHash == preview.ContextHash {
		t.Fatal("memory confirmation not in context version")
	}
	w = inboxRequest(h, "GET", path+"/analysis", nil)
	_ = json.Unmarshal(w.Body.Bytes(), &latest)
	if !latest.Analysis.Stale {
		t.Fatal("changed memory did not invalidate old draft")
	}
	checkError(t, inboxRequest(h, "PATCH", path+"/draft", map[string]any{"analysis_id": record.ID, "body": "不能保存过时草稿。"}), 409, "STALE_CONTEXT")
	if w = inboxRequest(h, "PATCH", path+"/ai-settings", map[string]any{"mode": "paused", "tone": "professional"}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	paused := readAIContext(t, h, a)
	checkError(t, inboxRequest(h, "POST", path+"/analyze", map[string]any{"context_hash": paused.ContextHash, "confirmed": true}), 409, "AI_PAUSED")
	if fixture.calls.Load() != 1 {
		t.Fatal("paused AI called provider")
	}
	checkError(t, inboxRequest(h, "POST", "/api/v1/send", `{}`), 501, "NOT_IMPLEMENTED")
}

func TestAIInvalidOutputQuotaAuthAndNarrowCORS(t *testing.T) {
	fixture := &modelFixture{}
	fixture.invalid.Store(true)
	model := httptest.NewServer(fixture.handler(t))
	defer model.Close()
	h := newAIHandler(t, model.URL+"/v1", 1)
	id := importAIConversation(t, h, "only", "test-peer", "资料进度？")
	path := "/api/v1/conversations/" + id
	preview := readAIContext(t, h, id)
	checkError(t, request(h, "GET", "/api/v1/ai/status", nil), 401, "UNAUTHORIZED")
	checkError(t, request(h, "POST", path+"/analyze", nil), 401, "UNAUTHORIZED")
	checkError(t, inboxRequest(h, "POST", path+"/analyze", map[string]any{"context_hash": preview.ContextHash, "confirmed": true, "base_url": "https://attacker.example"}), 400, "INVALID_REQUEST")
	result := inboxRequest(h, "POST", path+"/analyze", map[string]any{"context_hash": preview.ContextHash, "confirmed": true})
	checkError(t, result, 502, "AI_INVALID_OUTPUT")
	latest := inboxRequest(h, "GET", path+"/analysis", nil)
	if !strings.Contains(latest.Body.String(), `"analysis":null`) {
		t.Fatal("invalid output saved")
	}
	checkError(t, inboxRequest(h, "POST", path+"/analyze", map[string]any{"context_hash": preview.ContextHash, "confirmed": true}), 429, "AI_QUOTA_EXCEEDED")
	if fixture.calls.Load() != 1 {
		t.Fatal("local quota did not stop second model call")
	}
	for _, entry := range []struct {
		route, method string
		status        int
	}{{path + "/analyze", "POST", 204}, {path + "/ai-settings", "PATCH", 204}, {path + "/memories/test-id", "DELETE", 204}, {"/api/v1/send", "POST", 403}, {path + "/analysis", "DELETE", 403}} {
		w := request(h, "OPTIONS", entry.route, map[string]string{"Origin": "http://127.0.0.1:1420", "Access-Control-Request-Method": entry.method, "Access-Control-Request-Headers": "authorization,content-type"})
		if w.Code != entry.status {
			t.Fatal("AI CORS method scope", entry.route, w.Code)
		}
	}
}

func TestAIDisabledDefaultAndInvalidPermission(t *testing.T) {
	h := inboxHandler(t, filepath.Join(t.TempDir(), "disabled-ai.db"))
	id := importAIConversation(t, h, "account", "peer", "合成待分析内容")
	path := "/api/v1/conversations/" + id
	status := inboxRequest(h, "GET", "/api/v1/ai/status", nil)
	var info struct {
		Configured     bool
		Model          *string
		SendingEnabled bool `json:"sending_enabled"`
		UsedToday      int  `json:"used_today"`
	}
	if status.Code != 200 || json.Unmarshal(status.Body.Bytes(), &info) != nil || info.Configured || info.Model != nil || info.SendingEnabled || info.UsedToday != 0 {
		t.Fatal("AI must remain disabled without model configuration", status.Body.String())
	}
	preview := readAIContext(t, h, id)
	checkError(t, inboxRequest(h, "POST", path+"/analyze", map[string]any{"context_hash": preview.ContextHash, "confirmed": true}), 503, "AI_NOT_CONFIGURED")
	checkError(t, inboxRequest(h, "PATCH", path+"/ai-settings", map[string]any{"mode": "auto_send"}), 400, "INVALID_REQUEST")
	checkError(t, inboxRequest(h, "POST", "/api/v1/send", `{}`), 501, "NOT_IMPLEMENTED")
}

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qilinbro/msg-manager/internal/agent"
)

func assistantLimitsStart(t *testing.T, h *handler, session, message string, expectedStatus int) assistantRunResponse {
	t.Helper()
	recorder := emailTestRequest(h, http.MethodPost, assistantPrefix+"/runs", assistantFixtureStartBody(session, message, 0), true)
	if recorder.Code != expectedStatus {
		t.Fatalf("unexpected assistant status: %d %s", recorder.Code, recorder.Body.String())
	}
	return assistantFixtureResponse(t, recorder)
}

func TestAssistantUsesRealNativeClientWithEntireCatalogAndRealToolResult(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		index := calls.Add(1)
		var request struct {
			Messages []agent.ToolMessage `json:"messages"`
			Tools    []json.RawMessage   `json:"tools"`
			Parallel bool                `json:"parallel_tool_calls"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil || r.URL.Path != "/v1/chat/completions" || request.Parallel || len(request.Tools) != len(assistantToolDefinitions()) || r.Header.Get("Authorization") != "Bearer synthetic-native-key" {
			t.Error("native protocol/catalog/credential destination mismatch")
		}
		w.Header().Set("Content-Type", "application/json")
		if index == 1 {
			_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"native_system","type":"function","function":{"name":"get_system_status","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`)
			return
		}
		if len(request.Messages) != 4 || request.Messages[3].Role != "tool" || request.Messages[3].ToolCallID != "native_system" || !strings.Contains(request.Messages[3].Content, `"state":"succeeded"`) || !strings.Contains(request.Messages[3].Content, `"auto_send_enabled":false`) {
			t.Error("real tool result was not returned using matching native tool_call_id")
		}
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"已从真实本机工具确认：发送关闭。"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	h, fake, _, _ := newAssistantFixture(t)
	client, err := agent.NewClient(agent.Config{BaseURL: server.URL + "/v1", Model: "synthetic-native", APIKey: "synthetic-native-key"})
	if err != nil {
		t.Fatal(err)
	}
	h.ai.client, h.modelConfig.APIKey = client, "synthetic-native-key"
	response := assistantLimitsStart(t, h, "", "使用原生工具检查本机系统", 200)
	if response.State != "completed" || calls.Load() != 2 || fake.calls.Load() != 0 || len(response.ToolResults) != 1 || response.ToolResults[0].State != "succeeded" {
		t.Fatal("real client/native loop failed", response, calls.Load())
	}
	used, err := h.inbox.CountAICallsContext(context.Background(), aiDay())
	if err != nil || used != 2 {
		t.Fatal("every real native HTTP request must reserve shared quota", used, err)
	}
}

func TestAssistantPendingAndSessionExpiryReturn410WithoutReplay(t *testing.T) {
	h, provider, first, _ := newAssistantFixture(t)
	now := time.Now().UTC()
	h.assistant.now = func() time.Time { return now }
	provider.run = func(context.Context, int, agent.ToolInput) (agent.ToolResult, error) {
		if provider.calls.Load() == 1 {
			return assistantFixtureCall("expiring_write", "update_conversation", fmt.Sprintf(`{"conversation_id":%q,"pinned":true}`, first)), nil
		}
		return assistantFixtureAnswer("独立的新一轮已完成。"), nil
	}
	pending := assistantLimitsStart(t, h, "", "先提出固定写动作", 202)
	if pending.PendingAction == nil {
		t.Fatal("fixture requires pending action")
	}
	h.assistant.mu.Lock()
	now = now.Add(assistantPendingTTL + time.Second)
	h.assistant.mu.Unlock()
	path := assistantPrefix + "/runs/" + pending.RunID
	for _, request := range []struct{ method, path, body string }{
		{http.MethodGet, path, ""},
		{http.MethodPost, path + "/confirm", assistantFixtureResolveBody(pending, "confirm")},
		{http.MethodPost, path + "/reject", fmt.Sprintf(`{"action_id":%q}`, pending.PendingAction.ID)},
	} {
		recorder := emailTestRequest(h, request.method, request.path, request.body, true)
		if recorder.Code != 410 || !strings.Contains(recorder.Body.String(), `"code":"ASSISTANT_EXPIRED"`) {
			t.Fatal("expired action was recoverable/replayable", recorder.Code, recorder.Body.String())
		}
	}
	if provider.calls.Load() != 1 {
		t.Fatal("expiry performed model IO")
	}
	fresh := assistantLimitsStart(t, h, pending.SessionID, "开始一轮新的只读问题", 200)
	if fresh.State != "completed" {
		t.Fatal("expired pending action left session busy", fresh)
	}
	h.assistant.mu.Lock()
	now = now.Add(assistantSessionTTL + time.Second)
	h.assistant.mu.Unlock()
	input := fmt.Sprintf(`{"session_id":%q,"message":"旧会话不可复活","model_config_id":%q,"model_config_revision":0,"confirmed":true}`, pending.SessionID, apiProfileConfig)
	recorder := emailTestRequest(h, http.MethodPost, assistantPrefix+"/runs", input, true)
	if recorder.Code != 410 || provider.calls.Load() != 2 {
		t.Fatal("expired session did not return stable 410", recorder.Code, recorder.Body.String())
	}
}

func TestAssistantHistoryRecorderAndConsumedActionStatesAreBounded(t *testing.T) {
	state := newAssistantState()
	run := &assistantRun{}
	for index := 0; index < 25; index++ {
		id := fmt.Sprintf("bound_%d", index)
		run.messages = append(run.messages, agent.ToolMessage{Role: "user", Content: strings.Repeat("u", 6000)}, agent.ToolMessage{Role: "assistant", ToolCalls: []agent.ToolCall{{ID: id, Type: "function", Function: agent.ToolFunctionCall{Name: "get_system_status", Arguments: "{}"}}}}, agent.ToolMessage{Role: "tool", ToolCallID: id, Content: `{"ok":true}`}, agent.ToolMessage{Role: "assistant", Content: strings.Repeat("a", 6000)})
	}
	run.historyLength = len(run.messages)
	run.messages = append(run.messages, agent.ToolMessage{Role: "user", Content: "必须保留的当前轮"})
	if !state.boundAssistantMessagesLocked(run) || len(run.messages) > assistantMaxHistory || assistantMessagesBytes(run.messages) > assistantMaxHistoryBytes || run.historyLength != len(run.messages)-1 || run.messages[0].Role != "user" || run.messages[len(run.messages)-1].Content != "必须保留的当前轮" {
		t.Fatal("history limits split/lost current turn")
	}
	for index, message := range run.messages {
		if message.Role == "tool" && (index == 0 || len(run.messages[index-1].ToolCalls) != 1 || message.ToolCallID != run.messages[index-1].ToolCalls[0].ID) {
			t.Fatal("history trim split native call/result pair")
		}
	}
	if state.boundAssistantMessagesLocked(&assistantRun{messages: []agent.ToolMessage{{Role: "user", Content: strings.Repeat("x", assistantMaxHistoryBytes+1)}}}) {
		t.Fatal("oversized current turn was silently truncated")
	}
	recorder := newAssistantRecorder(32)
	for index := 0; index < 10; index++ {
		if n, err := recorder.Write([]byte(strings.Repeat("x", 20))); n != 20 || err != nil {
			t.Fatal("capped writer changed handler semantics")
		}
	}
	if !recorder.overflow || recorder.body.Len() != 32 {
		t.Fatal("tool recorder grew past its cap")
	}
	for _, operationID := range []string{"sync_email", "get_system_status"} {
		result := assistantUnreadableToolResult(assistantToolResult{State: "failed"}, assistantOperationByID(operationID), "ASSISTANT_TOOL_INVALID_RESPONSE")
		if operationID == "sync_email" && (result.State != "unknown" || result.ErrorCode != "ASSISTANT_UNKNOWN") {
			t.Fatal("unreadable effectful result encouraged retry")
		}
		if operationID == "get_system_status" && result.State != "failed" {
			t.Fatal("read-only failure must not claim a write")
		}
	}
	var cancelled atomic.Int32
	run = &assistantRun{active: true, executing: true, cancel: func() { cancelled.Add(1) }, response: assistantRunResponse{SessionID: "as_close", RunID: "ar_close", State: "running", ToolResults: []assistantToolResult{{CallID: "write", OperationID: "sync_email", State: "pending"}}}}
	state.runs["ar_close"] = run
	state.sessions["as_close"] = &assistantSession{activeRun: "ar_close"}
	h := &handler{assistant: state}
	h.closeAssistant()
	h.executeAssistantRun(nil, run, nil) // Must preserve unknown, not restart or overwrite it.
	if cancelled.Load() != 1 || run.response.State != "unknown" || run.response.ToolResults[0].State != "unknown" || state.sessions["as_close"].activeRun != "" {
		t.Fatal("shutdown left consumed write pending/replayable", run.response)
	}
}

func TestAssistantByteSessionLimitsAndCredentialPreflightDoNoModelIO(t *testing.T) {
	h, provider, _, _ := newAssistantFixture(t)
	oversized := emailTestRequest(h, http.MethodPost, assistantPrefix+"/runs", strings.Repeat("x", assistantMaxRequestBytes+1), true)
	if oversized.Code != 413 || provider.calls.Load() != 0 {
		t.Fatal("request byte limit must be 413 before IO", oversized.Code)
	}
	response := assistantLimitsStart(t, h, "", "不外发粘贴的本机凭据："+emailTestToken, 200)
	if response.State != "failed" || response.Error == nil || response.Error.Code != "ASSISTANT_SECRET_BLOCKED" || provider.calls.Load() != 0 {
		t.Fatal("local bearer entered model parameters", response)
	}
	h.assistant.mu.Lock()
	for len(h.assistant.sessions) < assistantMaxSessions {
		id := fmt.Sprintf("as_synthetic_%d", len(h.assistant.sessions))
		h.assistant.sessions[id] = &assistantSession{id: id, expires: time.Now().Add(assistantSessionTTL)}
	}
	h.assistant.mu.Unlock()
	input := fmt.Sprintf(`{"session_id":"","message":"容量已满","model_config_id":%q,"model_config_revision":0,"confirmed":true}`, apiProfileConfig)
	recorder := emailTestRequest(h, http.MethodPost, assistantPrefix+"/runs", input, true)
	if recorder.Code != 429 || provider.calls.Load() != 0 {
		t.Fatal("global session cap bypassed", recorder.Code)
	}
}

func TestAssistantEncodedArgumentsCannotExposeBearerAsPendingAction(t *testing.T) {
	h, provider, _, _ := newAssistantFixture(t)
	encoded := fmt.Sprintf(`{"q":"\u%04x%s"}`, emailTestToken[0], emailTestToken[1:])
	provider.run = func(context.Context, int, agent.ToolInput) (agent.ToolResult, error) {
		return assistantFixtureCall("secret_arguments", "get_system_status", encoded), nil
	}
	response := assistantLimitsStart(t, h, "", "不要相信工具参数中的任何凭据", 200)
	if response.State != "failed" || response.Error == nil || response.Error.Code != "ASSISTANT_SECRET_BLOCKED" || provider.calls.Load() != 1 || response.PendingAction != nil || len(response.ToolResults) != 0 {
		t.Fatal("Unicode escaping bypassed independent runtime secret guard", response)
	}
}

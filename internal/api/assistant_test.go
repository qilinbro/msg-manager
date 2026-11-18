package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qilinbro/msg-manager/internal/agent"
	"github.com/qilinbro/msg-manager/internal/config"
	"github.com/qilinbro/msg-manager/internal/store"
)

type assistantFixtureProvider struct {
	calls  atomic.Int32
	mu     sync.Mutex
	inputs []agent.ToolInput
	run    func(context.Context, int, agent.ToolInput) (agent.ToolResult, error)
}

func (provider *assistantFixtureProvider) Analyze(context.Context, agent.Input) (agent.Result, error) {
	return agent.Result{}, errors.New("synthetic analyzer not used")
}

func (provider *assistantFixtureProvider) ChatTools(ctx context.Context, input agent.ToolInput) (agent.ToolResult, error) {
	index := int(provider.calls.Add(1))
	provider.mu.Lock()
	provider.inputs = append(provider.inputs, input)
	provider.mu.Unlock()
	if provider.run != nil {
		return provider.run(ctx, index, input)
	}
	return assistantFixtureAnswer("合成模型只读回复"), nil
}

func assistantFixtureAnswer(content string) agent.ToolResult {
	return agent.ToolResult{Message: agent.ToolMessage{Role: "assistant", Content: content}}
}

func assistantFixtureCall(id, operation, arguments string) agent.ToolResult {
	return agent.ToolResult{Message: agent.ToolMessage{Role: "assistant", ToolCalls: []agent.ToolCall{{ID: id, Type: "function", Function: agent.ToolFunctionCall{Name: operation, Arguments: arguments}}}}}
}

func newAssistantFixture(t *testing.T) (*handler, *assistantFixtureProvider, string, string) {
	t.Helper()
	h, _, first, second := newAPIProfileFixture(t)
	provider := &assistantFixtureProvider{}
	h.ai.client = provider
	h.ai.dailyLimit = 100
	return h, provider, first, second
}

func assistantFixtureStartBody(session, message string, revision int64) string {
	raw, _ := json.Marshal(map[string]any{"session_id": session, "message": message, "model_config_id": apiProfileConfig, "model_config_revision": revision, "confirmed": true})
	return string(raw)
}

func assistantFixtureStart(t *testing.T, h *handler, session string) assistantRunResponse {
	t.Helper()
	w := emailTestRequest(h, "POST", assistantPrefix+"/runs", assistantFixtureStartBody(session, "仅使用合成资料测试助手", 0), true)
	return assistantFixtureResponse(t, w)
}

func assistantFixtureResponse(t *testing.T, recorder *httptest.ResponseRecorder) assistantRunResponse {
	t.Helper()
	var response assistantRunResponse
	if recorder.Code != 200 && recorder.Code != 202 || json.Unmarshal(recorder.Body.Bytes(), &response) != nil || response.RunID == "" {
		t.Fatalf("assistant request failed: %d %s", recorder.Code, recorder.Body.String())
	}
	return response
}

func assistantFixtureResolveBody(response assistantRunResponse, action string) string {
	input := map[string]any{"action_id": response.PendingAction.ID}
	if action == "confirm" {
		input["model_config_id"], input["model_config_revision"], input["confirmed"] = response.ModelConfigID, response.ModelConfigRevision, true
	}
	raw, _ := json.Marshal(input)
	return string(raw)
}

func TestAssistantNativeToolResultReallyContinuesToModel(t *testing.T) {
	h, provider, first, _ := newAssistantFixture(t)
	provider.run = func(_ context.Context, index int, input agent.ToolInput) (agent.ToolResult, error) {
		if len(input.Tools) < 40 || input.Messages[0].Role != "system" {
			t.Fatal("missing native catalog/system prompt")
		}
		if index == 1 {
			return assistantFixtureCall("read_messages", "get_conversation_messages", `{"conversation_id":"`+first+`","limit":2}`), nil
		}
		last := input.Messages[len(input.Messages)-1]
		if index != 2 || last.Role != "tool" || last.ToolCallID != "read_messages" || !strings.Contains(last.Content, "合成工作资料") || strings.Contains(last.Content, emailTestToken) {
			t.Fatal("genuine tool result did not return to provider", last)
		}
		return assistantFixtureAnswer("根据实际工具结果概括合成工作资料。"), nil
	}
	response := assistantFixtureStart(t, h, "")
	if response.State != "completed" || provider.calls.Load() != 2 || len(response.ToolResults) != 1 || response.ToolResults[0].State != "succeeded" || response.ToolResults[0].HTTPStatus == nil || *response.ToolResults[0].HTTPStatus != 200 {
		t.Fatalf("native continuation failed: %+v", response)
	}
	used, err := h.inbox.CountAICallsContext(context.Background(), aiDay())
	if err != nil || used != 2 {
		t.Fatal("each native model round must reserve the shared persistent quota", used, err)
	}
	before := provider.calls.Load()
	recovered := emailTestRequest(h, "GET", assistantPrefix+"/runs/"+response.RunID, "", true)
	if recovered.Code != 200 || provider.calls.Load() != before {
		t.Fatal("read-only recovery performed model IO")
	}
}

func TestAssistantConfirmationImmutableSingleUseAndRealContinuation(t *testing.T) {
	h, provider, _, _ := newAssistantFixture(t)
	mailbox := &stubMailbox{batch: validMailBatch(), entered: make(chan struct{}, 1), release: make(chan struct{})}
	bindStubEmail(h, mailbox)
	provider.run = func(_ context.Context, index int, input agent.ToolInput) (agent.ToolResult, error) {
		if index == 1 {
			return assistantFixtureCall("sync_once", "sync_email", `{}`), nil
		}
		last := input.Messages[len(input.Messages)-1]
		if last.Role != "tool" || last.ToolCallID != "sync_once" || !strings.Contains(last.Content, `"inserted":1`) {
			t.Error("confirmed result was not continued to model", last)
		}
		return assistantFixtureAnswer("一次合成邮件导入成功。"), nil
	}
	pending := assistantFixtureStart(t, h, "")
	if pending.State != "awaiting_confirmation" || pending.PendingAction == nil || mailbox.calls.Load() != 0 || provider.calls.Load() != 1 {
		t.Fatal("effectful operation ran before confirmation", pending)
	}
	path := assistantPrefix + "/runs/" + pending.RunID + "/confirm"
	body := assistantFixtureResolveBody(pending, "confirm")
	// A confirmation cannot add/replace parameters, even with a valid action ID.
	injected := strings.TrimSuffix(body, "}") + `,"arguments":{"folder":"Other"}}`
	if w := emailTestRequest(h, "POST", path, injected, true); w.Code != 400 || mailbox.calls.Load() != 0 {
		t.Fatal("confirmation accepted parameter overrides", w.Code)
	}
	finished := make(chan *httptest.ResponseRecorder, 1)
	go func() { finished <- emailTestRequest(h, "POST", path, body, true) }()
	select {
	case <-mailbox.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("confirmed dispatch deadlocked (aiMu/model gate must not cross ServeHTTP)")
	}
	if w := emailTestRequest(h, "POST", path, body, true); w.Code != 409 || !strings.Contains(w.Body.String(), "ASSISTANT_ACTION_CONSUMED") {
		t.Fatal("parallel confirmation was not atomically consumed", w.Code, w.Body.String())
	}
	if mailbox.calls.Load() != 1 {
		t.Fatal("same action executed more than once")
	}
	running := emailTestRequest(h, "GET", assistantPrefix+"/runs/"+pending.RunID, "", true)
	if response := assistantFixtureResponse(t, running); response.State != "running" {
		t.Fatal("recovery failed while operation was running", response)
	}
	close(mailbox.release)
	var final assistantRunResponse
	select {
	case recorder := <-finished:
		final = assistantFixtureResponse(t, recorder)
	case <-time.After(5 * time.Second):
		t.Fatal("confirmed continuation did not finish")
	}
	if final.State != "completed" || provider.calls.Load() != 2 || mailbox.calls.Load() != 1 || final.PendingAction != nil || len(final.ToolResults) != 1 || final.ToolResults[0].State != "succeeded" {
		t.Fatal("confirmed operation did not produce a real terminal result", final)
	}
	if replay := emailTestRequest(h, "POST", path, body, true); replay.Code != 409 || mailbox.calls.Load() != 1 {
		t.Fatal("completed action was replayed")
	}
}

func TestAssistantRejectDoesNoBusinessOrModelIO(t *testing.T) {
	h, provider, _, _ := newAssistantFixture(t)
	mailbox := &stubMailbox{batch: validMailBatch()}
	bindStubEmail(h, mailbox)
	provider.run = func(context.Context, int, agent.ToolInput) (agent.ToolResult, error) {
		return assistantFixtureCall("reject_sync", "sync_email", `{}`), nil
	}
	pending := assistantFixtureStart(t, h, "")
	w := emailTestRequest(h, "POST", assistantPrefix+"/runs/"+pending.RunID+"/reject", assistantFixtureResolveBody(pending, "reject"), true)
	rejected := assistantFixtureResponse(t, w)
	if rejected.State != "rejected" || rejected.PendingAction != nil || rejected.ToolResults[0].State != "rejected" || mailbox.calls.Load() != 0 || provider.calls.Load() != 1 {
		t.Fatal("reject performed IO or retained pending authority", rejected)
	}
	if w := emailTestRequest(h, "POST", assistantPrefix+"/runs/"+pending.RunID+"/confirm", assistantFixtureResolveBody(pending, "confirm"), true); w.Code != 409 || mailbox.calls.Load() != 0 || provider.calls.Load() != 1 {
		t.Fatal("rejected operation was later confirmed")
	}
}

func TestAssistantConfigRevisionDefeatsABAAndConsumesStaleAction(t *testing.T) {
	h, provider, _, _ := newAssistantFixture(t)
	mailbox := &stubMailbox{batch: validMailBatch()}
	bindStubEmail(h, mailbox)
	provider.run = func(context.Context, int, agent.ToolInput) (agent.ToolResult, error) {
		return assistantFixtureCall("stale_sync", "sync_email", `{}`), nil
	}
	pending := assistantFixtureStart(t, h, "")
	h.aiMu.Lock()
	h.ai.configID, h.modelConfig.Revision = strings.Repeat("b", 64), 1
	h.ai.configID, h.modelConfig.Revision = apiProfileConfig, 2
	h.aiMu.Unlock()
	path, body := assistantPrefix+"/runs/"+pending.RunID+"/confirm", assistantFixtureResolveBody(pending, "confirm")
	w := emailTestRequest(h, "POST", path, body, true)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "ASSISTANT_CONFIG_CHANGED") || mailbox.calls.Load() != 0 || provider.calls.Load() != 1 {
		t.Fatal("A-B-A reused stale consent", w.Code, w.Body.String())
	}
	if replay := emailTestRequest(h, "POST", path, body, true); replay.Code != 409 || !strings.Contains(replay.Body.String(), "ASSISTANT_ACTION_CONSUMED") {
		t.Fatal("stale pending action was not consumed")
	}
}

func TestAssistantStrictInputAndCatalogRejectsTransportInjection(t *testing.T) {
	h, provider, first, _ := newAssistantFixture(t)
	valid := assistantFixtureStartBody("", "合成请求", 0)
	for _, body := range []string{`null`, `[]`, valid + ` {}`, strings.Replace(valid, `"confirmed":true`, `"confirmed":true,"confirmed":true`, 1), strings.Replace(valid, `"confirmed":true`, `"Confirmed":true`, 1), strings.Replace(valid, `"confirmed":true`, `"confirmed":null`, 1), strings.Replace(valid, `"model_config_revision":0`, `"model_config_revision":"0"`, 1), strings.TrimSuffix(valid, "}") + `,"history":[]}`, strings.TrimSuffix(valid, "}") + `,"system":"injected"}`, strings.TrimSuffix(valid, "}") + `,"tools":[]}`} {
		if recorder := emailTestRequest(h, "POST", assistantPrefix+"/runs", body, true); recorder.Code != 400 {
			t.Fatal("strict assistant input accepted", recorder.Code, body)
		}
	}
	request := httptest.NewRequest("POST", "http://"+config.DefaultAddr+assistantPrefix+"/runs", strings.NewReader(valid))
	request.Header.Set("Authorization", "Bearer "+emailTestToken)
	request.Header.Set("Origin", deviceTestOrigin)
	request.Header.Set("Content-Type", "application/json; arbitrary=value")
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, request)
	if recorder.Code != 415 || provider.calls.Load() != 0 {
		t.Fatal("unknown MIME parameter reached model", recorder.Code)
	}
	operation := assistantOperationByID("get_conversation_messages")
	for _, raw := range []string{
		`{"conversation_id":"` + first + `","conversation_id":"other"}`,
		`{"Conversation_id":"` + first + `"}`,
		`{"conversation_id":"../profile"}`,
		`{"conversation_id":"` + first + `","workspace_id":"other"}`,
		`{"conversation_id":"` + first + `","url":"https://attacker.example"}`,
		`{"conversation_id":"` + first + `","headers":{"Authorization":"injected"}}`,
		`{"conversation_id":"` + first + `","method":"DELETE"}`,
		`{"conversation_id":"` + first + `","sql":"SELECT *"}`,
		`{"conversation_id":"` + first + `","limit":null}`,
		`{"conversation_id":"` + first + `","limit":1.0}`,
	} {
		if _, err := prepareAssistantOperation(operation, raw, apiProfileConfig, 0); err == nil {
			t.Fatal("trusted catalog accepted injection/coercion", raw)
		}
	}
	for _, name := range []string{"update_model_config", "register_device", "create_device_session", "events", "approve_action", "send_message"} {
		if assistantOperationByID(name) != nil {
			t.Fatal("manual/unimplemented/credential capability exposed to model", name)
		}
	}
	for _, operation := range assistantOperations {
		if operation.Method != "GET" && operation.schema != nil && operation.Availability != "confirmation_required" {
			t.Fatal("effectful operation lacks confirmation", operation.ID)
		}
		if strings.HasPrefix(operation.ID, "verify_") && operation.Availability != "confirmation_required" {
			t.Fatal("effectful GET verify bypasses confirmation", operation.ID)
		}
	}
}

func TestAssistantDigestOperationInjectsFrozenModelRevision(t *testing.T) {
	for _, test := range []struct{ name, arguments string }{{"run_digest", `{}`}, {"update_digest_schedule", `{"enabled":true,"local_time":"09:00","timezone":"Asia/Shanghai","expected_revision":3}`}} {
		prepared, err := prepareAssistantOperation(assistantOperationByID(test.name), test.arguments, apiProfileConfig, 7)
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		if json.Unmarshal(prepared.Body, &body) != nil || body["model_config_id"] != apiProfileConfig || body["model_config_revision"] != float64(7) || body["confirmed"] != true {
			t.Fatal("digest operation did not inject server-owned current consent", string(prepared.Body))
		}
		injected := strings.TrimSuffix(test.arguments, "}") + `,"model_config_revision":0}`
		if test.arguments == `{}` {
			injected = `{"model_config_revision":0}`
		}
		if _, err := prepareAssistantOperation(assistantOperationByID(test.name), injected, apiProfileConfig, 7); err == nil {
			t.Fatal("model selected digest binding")
		}
	}
}

func TestAssistantCatalogExactlyCoversExistingOpenAPIWithoutRecursiveTools(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if json.Unmarshal(raw, &spec) != nil {
		t.Fatal("OpenAPI JSON is not valid")
	}
	catalog := map[string]int{}
	for _, operation := range assistantOperations {
		catalog[operation.Method+" "+operation.Path]++
	}
	count, controls := 0, 0
	for path, item := range spec.Paths {
		for method := range item {
			switch method {
			case "get", "post", "patch", "delete", "put", "head", "options":
			default:
				continue
			}
			if assistantPath(path) {
				controls++
				continue
			}
			key := strings.ToUpper(method) + " " + path
			count++
			if catalog[key] != 1 {
				t.Errorf("business API missing or duplicated in honest operation inventory: %s count=%d", key, catalog[key])
			}
			delete(catalog, key)
		}
	}
	if len(catalog) != 0 || count != 72 || controls != 5 {
		t.Fatalf("inventory drift: business=%d assistant=%d unexpected=%v", count, controls, catalog)
	}
	definitions := map[string]bool{}
	for _, definition := range assistantToolDefinitions() {
		definitions[definition.Name] = true
	}
	human, unavailable := 0, 0
	for _, operation := range assistantOperations {
		switch operation.Availability {
		case "human_only":
			human++
			if definitions[operation.ID] {
				t.Fatal("human-only operation exposed", operation.ID)
			}
		case "not_implemented":
			unavailable++
			if definitions[operation.ID] {
				t.Fatal("unimplemented operation exposed", operation.ID)
			}
		}
	}
	if human != 7 || unavailable != 13 || len(definitions) != 52 {
		t.Fatal("credential/unimplemented/executable inventory drift", human, unavailable, len(definitions))
	}
	if definitions["verify_qq_onebot"] || assistantOperationByID("verify_qq_onebot") != nil {
		t.Fatal("retired OneBot verification became a model-executable tool")
	}
}

func TestAssistantPausedConversationsNeverEnterModelAndCursorSurvivesFiltering(t *testing.T) {
	h, provider, first, _ := newAssistantFixture(t)
	const marker = "SYNTHETIC_PAUSED_BODY_NEVER_OUTBOUND"
	_, err := h.inbox.Import(store.ImportRequest{Platform: "qq", AccountID: "synthetic-profile-account", AccountName: "合成测试账号", Messages: []store.ImportMessage{{ExternalID: "paused-marker", PeerID: "peer-one", PeerName: marker, Body: marker, Direction: "incoming", OccurredAt: "2026-11-16T00:00:00Z"}}})
	if err != nil || h.inbox.UpdateAISettings(first, "paused", "natural") != nil {
		t.Fatal("paused fixture failed", err)
	}
	human := emailTestRequest(h, "GET", "/api/v1/conversations/"+first+"/messages", "", true)
	if human.Code != 200 || !strings.Contains(human.Body.String(), marker) {
		t.Fatal("assistant privacy accidentally changed human REST")
	}
	page, err := h.inbox.Conversations(store.Filter{Limit: 1})
	if err != nil || page.NextCursor == nil {
		t.Fatal("fixture needs a nonempty pagination cursor", err)
	}
	provider.run = func(_ context.Context, index int, input agent.ToolInput) (agent.ToolResult, error) {
		encoded, _ := json.Marshal(input)
		if strings.Contains(string(encoded), marker) {
			t.Error("paused raw marker reached model")
		}
		if index == 1 {
			return assistantFixtureCall("list_filtered", "list_conversations", `{"limit":1}`), nil
		}
		last := input.Messages[len(input.Messages)-1]
		var result assistantToolResult
		var data struct {
			Items      []any   `json:"items"`
			NextCursor *string `json:"next_cursor"`
			Privacy    struct {
				Filtered int `json:"filtered_paused"`
			} `json:"assistant_privacy"`
		}
		if json.Unmarshal([]byte(last.Content), &result) != nil || json.Unmarshal(result.Data, &data) != nil || len(data.Items) != 0 || data.Privacy.Filtered != 1 || data.NextCursor == nil || *data.NextCursor != *page.NextCursor {
			t.Error("paused filtering broke page cursor/explanation", last.Content)
		}
		return assistantFixtureAnswer("已过滤暂停来源，可使用原游标翻页。"), nil
	}
	response := assistantFixtureStart(t, h, "")
	if response.State != "completed" || provider.calls.Load() != 2 {
		t.Fatal("filtered listing failed", response)
	}
}

func TestAssistantPausedScopedReadAndCrossProfileLinksFailClosed(t *testing.T) {
	for _, operation := range []string{"get_conversation_messages", "get_ai_context", "get_analysis", "get_ai_settings", "get_profile", "get_profile_links"} {
		t.Run(operation, func(t *testing.T) {
			h, provider, first, second := newAssistantFixture(t)
			target := first
			if operation == "get_profile_links" {
				target = second
			}
			if err := h.inbox.UpdateAISettings(target, "paused", "natural"); err != nil {
				t.Fatal(err)
			}
			provider.run = func(context.Context, int, agent.ToolInput) (agent.ToolResult, error) {
				return assistantFixtureCall("private_read", operation, `{"conversation_id":"`+first+`"}`), nil
			}
			response := assistantFixtureStart(t, h, "")
			if response.State != "failed" || response.Error == nil || response.Error.Code != "ASSISTANT_AI_PAUSED" || provider.calls.Load() != 1 || len(response.ToolResults) != 1 || string(response.ToolResults[0].Data) != "null" {
				t.Fatal("paused scope reached a business/model response", response)
			}
		})
	}
}

func TestAssistantHistoricalProvenanceRecheckedAfterPauseBeforeNextUserTurn(t *testing.T) {
	h, provider, first, _ := newAssistantFixture(t)
	const marker = "SYNTHETIC_HISTORY_SOURCE_SECRET"
	_, err := h.inbox.Import(store.ImportRequest{Platform: "qq", AccountID: "synthetic-profile-account", Messages: []store.ImportMessage{{ExternalID: "history-marker", PeerID: "peer-one", Body: marker, Direction: "incoming", OccurredAt: "2026-11-16T00:00:00Z"}}})
	if err != nil {
		t.Fatal(err)
	}
	provider.run = func(_ context.Context, index int, input agent.ToolInput) (agent.ToolResult, error) {
		if index == 1 {
			return assistantFixtureCall("read_history", "get_conversation_messages", `{"conversation_id":"`+first+`"}`), nil
		}
		if index == 2 && !strings.Contains(input.Messages[len(input.Messages)-1].Content, marker) {
			t.Error("fixture did not acquire sensitive history")
		}
		if index > 2 {
			encoded, _ := json.Marshal(input)
			if strings.Contains(string(encoded), marker) {
				t.Error("newly paused historical body was resent")
			}
		}
		return assistantFixtureAnswer("合成资料已读取。"), nil
	}
	firstRun := assistantFixtureStart(t, h, "")
	if firstRun.State != "completed" || provider.calls.Load() != 2 {
		t.Fatal(firstRun)
	}
	if err := h.inbox.UpdateAISettings(first, "paused", "natural"); err != nil {
		t.Fatal(err)
	}
	blocked := assistantFixtureStart(t, h, firstRun.SessionID)
	if blocked.State != "failed" || blocked.Error == nil || blocked.Error.Code != "ASSISTANT_AI_PAUSED" || provider.calls.Load() != 2 {
		t.Fatal("paused historical source was not checked before provider IO", blocked)
	}
	h.assistant.mu.Lock()
	session := h.assistant.sessions[firstRun.SessionID]
	remaining := len(session.history)
	refs := len(session.references.IDs)
	h.assistant.mu.Unlock()
	if remaining != 0 || refs != 0 {
		t.Fatal("sensitive session history was not invalidated", remaining, refs)
	}
	// A fresh clean turn may continue, but cannot regain the discarded body.
	clean := assistantFixtureStart(t, h, firstRun.SessionID)
	if clean.State != "completed" || provider.calls.Load() != 3 {
		t.Fatal("clean turn retained invalid historical provenance", clean)
	}
}

func TestAssistantHistoryRecheckedBetweenToolIterationsAndGlobalDigestBlocked(t *testing.T) {
	h, provider, first, _ := newAssistantFixture(t)
	provider.run = func(_ context.Context, index int, input agent.ToolInput) (agent.ToolResult, error) {
		switch index {
		case 1:
			return assistantFixtureCall("first_read", "get_conversation_messages", `{"conversation_id":"`+first+`"}`), nil
		case 2:
			if err := h.inbox.UpdateAISettings(first, "paused", "natural"); err != nil {
				t.Error(err)
			}
			return assistantFixtureCall("safe_status", "get_system_status", `{}`), nil
		default:
			t.Error("paused history crossed the next native model iteration")
		}
		return assistantFixtureAnswer("should not run"), nil
	}
	response := assistantFixtureStart(t, h, "")
	if response.State != "failed" || response.Error == nil || response.Error.Code != "ASSISTANT_AI_PAUSED" || provider.calls.Load() != 2 {
		t.Fatal("tool-round pause guard failed", response)
	}
	provider.run = func(context.Context, int, agent.ToolInput) (agent.ToolResult, error) {
		return assistantFixtureCall("read_digest", "get_latest_digest", `{}`), nil
	}
	digest := assistantFixtureStart(t, h, "")
	if digest.State != "failed" || digest.Error == nil || digest.Error.Code != "ASSISTANT_AI_PAUSED" || provider.calls.Load() != 3 || string(digest.ToolResults[0].Data) != "null" {
		t.Fatal("digest summary may contain hidden paused provenance", digest)
	}
}

func TestAssistantExplicitConfirmedPauseIsOnlyMutationExceptionAndStopsModel(t *testing.T) {
	h, provider, first, _ := newAssistantFixture(t)
	provider.run = func(context.Context, int, agent.ToolInput) (agent.ToolResult, error) {
		return assistantFixtureCall("pause_explicit", "update_ai_settings", `{"conversation_id":"`+first+`","mode":"paused","tone":"natural"}`), nil
	}
	pending := assistantFixtureStart(t, h, "")
	if pending.PendingAction == nil {
		t.Fatal(pending)
	}
	response := assistantFixtureResponse(t, emailTestRequest(h, "POST", assistantPrefix+"/runs/"+pending.RunID+"/confirm", assistantFixtureResolveBody(pending, "confirm"), true))
	settings, err := h.inbox.AISettingsContext(context.Background(), first)
	if response.State != "completed" || settings.Mode != "paused" || err != nil || provider.calls.Load() != 1 || response.ToolResults[0].State != "succeeded" {
		t.Fatal("explicit pause did not complete without sending newly paused history", response, settings, err)
	}
}

func TestAssistantSharedQuotaConsumesProviderFailuresAndBoundsNativeRounds(t *testing.T) {
	t.Run("failed-provider-consumes", func(t *testing.T) {
		h, provider, _, _ := newAssistantFixture(t)
		h.ai.dailyLimit = 1
		provider.run = func(context.Context, int, agent.ToolInput) (agent.ToolResult, error) {
			return agent.ToolResult{}, errors.New("synthetic upstream failure contains secret text")
		}
		first := assistantFixtureStart(t, h, "")
		second := assistantFixtureStart(t, h, "")
		used, err := h.inbox.CountAICallsContext(context.Background(), aiDay())
		if first.State != "failed" || second.Error == nil || second.Error.Code != "AI_QUOTA_EXCEEDED" || provider.calls.Load() != 1 || used != 1 || err != nil {
			t.Fatal("failed provider IO escaped persistent quota", first, second, used, err)
		}
		raw, _ := json.Marshal(first)
		if strings.Contains(string(raw), "synthetic upstream failure") {
			t.Fatal("raw provider error leaked")
		}
	})
	t.Run("six-round-bound", func(t *testing.T) {
		h, provider, _, _ := newAssistantFixture(t)
		provider.run = func(_ context.Context, index int, _ agent.ToolInput) (agent.ToolResult, error) {
			return assistantFixtureCall(fmt.Sprintf("status_%d", index), "get_system_status", `{}`), nil
		}
		response := assistantFixtureStart(t, h, "")
		if response.State != "failed" || response.Error == nil || response.Error.Code != "ASSISTANT_LIMIT" || provider.calls.Load() != 6 || len(response.ToolResults) != 6 {
			t.Fatal("native loop did not stop at six actual model calls", response, provider.calls.Load())
		}
	})
}

func TestAssistantDeviceOwnershipStableRenewalRevocationAndHandlerIsolation(t *testing.T) {
	h, provider, _, _ := newAssistantFixture(t)
	device := pairSyntheticLocalDevice(t, h, emailTestToken)
	token := openSyntheticDeviceSession(t, h, device)
	start := localDeviceRequest(h, http.MethodPost, assistantPrefix+"/runs", assistantFixtureStartBody("", "设备会话测试", 0), token, deviceTestOrigin)
	response := assistantFixtureResponse(t, start)
	renewed := openSyntheticDeviceSession(t, h, device)
	path := assistantPrefix + "/runs/" + response.RunID
	if got := localDeviceRequest(h, http.MethodGet, path, "", renewed, deviceTestOrigin); got.Code != 200 {
		t.Fatal("same stable device lost ownership on bearer renewal", got.Code, got.Body.String())
	}
	other := pairSyntheticLocalDevice(t, h, emailTestToken)
	otherToken := openSyntheticDeviceSession(t, h, other)
	for _, credential := range []string{otherToken, emailTestToken} {
		if got := localDeviceRequest(h, http.MethodGet, path, "", credential, deviceTestOrigin); got.Code != 404 {
			t.Fatal("another principal accessed device-owned run", got.Code)
		}
	}
	if got := localDeviceRequest(h, http.MethodPost, assistantPrefix+"/runs", assistantFixtureStartBody(response.SessionID, "越权会话", 0), otherToken, deviceTestOrigin); got.Code != 404 {
		t.Fatal("another device appended to session", got.Code)
	}
	if got := localDeviceRequest(h, http.MethodDelete, localDevicesPath+"/"+device.id, "", emailTestToken, deviceTestOrigin); got.Code != http.StatusNoContent {
		t.Fatal("synthetic revocation failed", got.Code)
	}
	if got := localDeviceRequest(h, http.MethodGet, path, "", renewed, deviceTestOrigin); got.Code != 401 {
		t.Fatal("revoked device recovered session", got.Code)
	}
	if provider.calls.Load() != 1 {
		t.Fatal("ownership/recovery checks performed model IO")
	}
	otherHandler, _, _, _ := newAssistantFixture(t)
	if got := emailTestRequest(otherHandler, "GET", path, "", true); got.Code != 404 {
		t.Fatal("handler-local state leaked to another service instance", got.Code)
	}
}

func TestAssistantDeviceRevokedBetweenModelAndToolCannotDispatch(t *testing.T) {
	h, provider, _, _ := newAssistantFixture(t)
	device := pairSyntheticLocalDevice(t, h, emailTestToken)
	token := openSyntheticDeviceSession(t, h, device)
	provider.run = func(context.Context, int, agent.ToolInput) (agent.ToolResult, error) {
		recorder := localDeviceRequest(h, http.MethodDelete, localDevicesPath+"/"+device.id, "", emailTestToken, deviceTestOrigin)
		if recorder.Code != http.StatusNoContent {
			t.Error("synthetic revocation failed", recorder.Code)
		}
		return assistantFixtureCall("revoked_status", "get_system_status", `{}`), nil
	}
	response := assistantFixtureResponse(t, localDeviceRequest(h, http.MethodPost, assistantPrefix+"/runs", assistantFixtureStartBody("", "撤销后不得继续", 0), token, deviceTestOrigin))
	if response.State != "failed" || response.Error == nil || response.Error.Code != "UNAUTHORIZED" && response.Error.Code != "DEVICE_UNAUTHORIZED" || provider.calls.Load() != 1 || len(response.ToolResults) != 1 || string(response.ToolResults[0].Data) != "null" {
		t.Fatal("revocation was not rechecked at dispatcher", response)
	}
}

func TestAssistantCancelledEffectfulDispatchIsUnknownAndNeverReplayed(t *testing.T) {
	for _, remaining := range []int{-1, 0} {
		t.Run(fmt.Sprintf("remaining_result_bytes_%d", remaining), func(t *testing.T) {
			h, provider, _, _ := newAssistantFixture(t)
			mailbox := &stubMailbox{batch: validMailBatch(), entered: make(chan struct{}, 1), release: make(chan struct{})}
			bindStubEmail(h, mailbox)
			provider.run = func(context.Context, int, agent.ToolInput) (agent.ToolResult, error) {
				return assistantFixtureCall("cancel_sync", "sync_email", `{}`), nil
			}
			pending := assistantFixtureStart(t, h, "")
			if remaining >= 0 {
				h.assistant.mu.Lock()
				h.assistant.runs[pending.RunID].resultBytes = assistantMaxResultBytes - remaining
				h.assistant.mu.Unlock()
			}
			path, body := assistantPrefix+"/runs/"+pending.RunID+"/confirm", assistantFixtureResolveBody(pending, "confirm")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			request := httptest.NewRequest("POST", "http://"+config.DefaultAddr+path, strings.NewReader(body)).WithContext(ctx)
			request.Header.Set("Authorization", "Bearer "+emailTestToken)
			request.Header.Set("Origin", deviceTestOrigin)
			request.Header.Set("Content-Type", "application/json")
			finished := make(chan *httptest.ResponseRecorder, 1)
			go func() { recorder := httptest.NewRecorder(); h.ServeHTTP(recorder, request); finished <- recorder }()
			select {
			case <-mailbox.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("effectful fixture not entered")
			}
			cancel()
			select {
			case recorder := <-finished:
				response := assistantFixtureResponse(t, recorder)
				if response.State != "unknown" || response.Error == nil || response.Error.Code != "ASSISTANT_UNKNOWN" || response.ToolResults[0].State != "unknown" {
					t.Fatal("canceled effectful operation was presented as retryable", response)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("canceled dispatch did not return")
			}
			if replay := emailTestRequest(h, "POST", path, body, true); replay.Code != 409 || mailbox.calls.Load() != 1 || provider.calls.Load() != 1 {
				t.Fatal("unknown operation replayed")
			}
			recovered := assistantFixtureResponse(t, emailTestRequest(h, "GET", assistantPrefix+"/runs/"+pending.RunID, "", true))
			if recovered.State != "unknown" || mailbox.calls.Load() != 1 {
				t.Fatal("unknown recovery performed IO")
			}
		})
	}
}

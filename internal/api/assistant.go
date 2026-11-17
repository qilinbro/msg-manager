package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/qilinbro/msg-manager/internal/agent"
)

const (
	assistantMaxRequestBytes = 16 << 10
	assistantMaxSessions     = 64
	assistantMaxRuns         = 16
	assistantMaxHistory      = 64
	assistantMaxHistoryBytes = 256 << 10
	assistantMaxResultBytes  = 256 << 10
	assistantMaxModelCalls   = 6
	assistantMaxToolCalls    = 12
	assistantSessionTTL      = 30 * time.Minute
	assistantPendingTTL      = 5 * time.Minute
	assistantExecutionLimit  = 120 * time.Second
	assistantPrefix          = "/api/v1/assistant"
)

type assistantRunError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type assistantToolResult struct {
	CallID      string          `json:"call_id"`
	OperationID string          `json:"operation_id"`
	State       string          `json:"state"`
	HTTPStatus  *int            `json:"http_status"`
	Data        json.RawMessage `json:"data"`
	ErrorCode   string          `json:"error_code"`
}

type assistantPendingAction struct {
	ID          string          `json:"id"`
	OperationID string          `json:"operation_id"`
	Method      string          `json:"method"`
	Path        string          `json:"path"`
	Arguments   json.RawMessage `json:"arguments"`
	Effects     []string        `json:"effects"`
	ExpiresAt   string          `json:"expires_at"`
}

type assistantRunResponse struct {
	SessionID           string                  `json:"session_id"`
	RunID               string                  `json:"run_id"`
	State               string                  `json:"state"`
	AssistantMessage    string                  `json:"assistant_message"`
	ToolResults         []assistantToolResult   `json:"tool_results"`
	PendingAction       *assistantPendingAction `json:"pending_action"`
	ModelConfigID       string                  `json:"model_config_id"`
	ModelConfigRevision int64                   `json:"model_config_revision"`
	ExpiresAt           string                  `json:"expires_at"`
	Error               *assistantRunError      `json:"error"`
}

type assistantPrivacyRefs struct {
	IDs    map[string]bool
	Global bool
}

func newAssistantPrivacyRefs() assistantPrivacyRefs {
	return assistantPrivacyRefs{IDs: map[string]bool{}}
}

func (references *assistantPrivacyRefs) merge(other assistantPrivacyRefs) {
	for id := range other.IDs {
		references.IDs[id] = true
	}
	references.Global = references.Global || other.Global
}

func (references assistantPrivacyRefs) clone() assistantPrivacyRefs {
	out := newAssistantPrivacyRefs()
	out.merge(references)
	return out
}

type assistantSession struct {
	id         string
	owner      string
	expires    time.Time
	history    []agent.ToolMessage
	references assistantPrivacyRefs
	runIDs     []string
	activeRun  string
}

type assistantPending struct {
	prepared assistantPreparedOperation
	callID   string
	expires  time.Time
}

type assistantRun struct {
	response      assistantRunResponse
	owner         string
	expires       time.Time
	messages      []agent.ToolMessage
	historyLength int
	references    assistantPrivacyRefs
	pending       *assistantPending
	lastActionID  string
	active        bool
	executing     bool
	cancel        context.CancelFunc
	spent         time.Duration
	modelCalls    int
	toolCalls     int
	resultBytes   int
}

type assistantTombstone struct {
	owner   string
	expires time.Time
}

type assistantState struct {
	mu       sync.Mutex
	sessions map[string]*assistantSession
	runs     map[string]*assistantRun
	expired  map[string]assistantTombstone
	closed   bool
	now      func() time.Time
}

func newAssistantState() *assistantState {
	return &assistantState{sessions: map[string]*assistantSession{}, runs: map[string]*assistantRun{}, expired: map[string]assistantTombstone{}, now: time.Now}
}

func assistantOwner(r *http.Request) string {
	workspace := authenticatedWorkspace(r.Context())
	if workspace == "" {
		return ""
	}
	device, _ := r.Context().Value(devicePrincipalKey{}).(string)
	if device == "" {
		return workspace + ":master"
	}
	return workspace + ":device:" + device
}

func assistantRandomID(prefix string) (string, error) {
	var value [20]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(value[:]), nil
}

func assistantPath(path string) bool {
	return path == assistantPrefix || strings.HasPrefix(path, assistantPrefix+"/")
}

func assistantRunPath(path string) (id, action string, ok bool) {
	parts := strings.Split(strings.TrimPrefix(path, assistantPrefix+"/runs/"), "/")
	if !strings.HasPrefix(path, assistantPrefix+"/runs/") || len(parts) < 1 || len(parts) > 2 || !aiResourceID(parts[0]) {
		return "", "", false
	}
	if len(parts) == 2 {
		if parts[1] != "confirm" && parts[1] != "reject" {
			return "", "", false
		}
		action = parts[1]
	}
	return parts[0], action, true
}

func assistantMutation(method, path string) bool {
	if method != http.MethodPost {
		return false
	}
	if path == assistantPrefix+"/runs" {
		return true
	}
	_, action, ok := assistantRunPath(path)
	return ok && action != ""
}

func (h *handler) serveAssistant(w http.ResponseWriter, r *http.Request, traceID string) {
	if r.URL.RawQuery != "" {
		writeError(w, 400, "INVALID_REQUEST", "助手接口不接受查询参数。", traceID)
		return
	}
	if h.assistant == nil {
		writeError(w, 503, "ASSISTANT_UNAVAILABLE", "助手尚未启动。", traceID)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == assistantPrefix+"/status":
		h.assistantStatus(w, r, traceID)
	case r.Method == http.MethodPost && r.URL.Path == assistantPrefix+"/runs":
		h.startAssistantRun(w, r, traceID)
	default:
		id, action, ok := assistantRunPath(r.URL.Path)
		if !ok || action == "" && r.Method != http.MethodGet || action != "" && r.Method != http.MethodPost {
			writeError(w, 404, "NOT_FOUND", "助手操作不存在。", traceID)
			return
		}
		if action == "" {
			h.recoverAssistantRun(w, r, id, traceID)
		} else {
			h.resolveAssistantRun(w, r, id, action, traceID)
		}
	}
}

func (h *handler) assistantStatus(w http.ResponseWriter, r *http.Request, traceID string) {
	h.aiMu.RLock()
	config := h.exposeModelConfig()
	model, host, external := "", "", false
	if h.ai != nil {
		model, host, external = h.ai.model, h.ai.endpointHost, h.ai.external
	}
	h.aiMu.RUnlock()
	used := 0
	if h.inbox != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		var err error
		used, err = h.inbox.CountAICallsContext(ctx, aiDay())
		if err != nil {
			writeError(w, 503, "ASSISTANT_UNAVAILABLE", "无法读取模型额度。", traceID)
			return
		}
	}
	operations := make([]assistantOperationEntry, 0, len(assistantOperations))
	for _, operation := range assistantOperations {
		operations = append(operations, operation.assistantOperationEntry)
	}
	h.assistant.mu.Lock()
	h.assistant.pruneLocked()
	h.assistant.mu.Unlock()
	writeJSON(w, 200, struct {
		Configured          bool                      `json:"configured"`
		Model               string                    `json:"model"`
		EndpointHost        string                    `json:"endpoint_host"`
		External            bool                      `json:"external"`
		ModelConfigID       string                    `json:"model_config_id"`
		ModelConfigRevision int64                     `json:"model_config_revision"`
		DailyLimit          int                       `json:"daily_limit"`
		UsedToday           int                       `json:"used_today"`
		SendingEnabled      bool                      `json:"sending_enabled"`
		SessionTTLSeconds   int                       `json:"session_ttl_seconds"`
		Operations          []assistantOperationEntry `json:"operations"`
	}{config.Configured, model, host, external, config.ModelConfigID, config.Revision, config.DailyLimit, used, false, int(assistantSessionTTL.Seconds()), operations})
}

func (h *handler) startAssistantRun(w http.ResponseWriter, r *http.Request, traceID string) {
	schema := assistantObject(map[string]*assistantSchema{"session_id": assistantString(0, 128), "message": assistantText(1, 8000), "model_config_id": assistantHashSchema(), "model_config_revision": assistantInteger(0, 9007199254740991), "confirmed": assistantBoolean()}, "session_id", "message", "model_config_id", "model_config_revision", "confirmed")
	input, ok := readAssistantJSON(w, r, schema, traceID)
	if !ok {
		return
	}
	if input["confirmed"] != true {
		writeError(w, 400, "ASSISTANT_CONSENT_REQUIRED", "请先确认将本轮问题、历史与所需工具数据发往当前模型。", traceID)
		return
	}
	configID, revision := input["model_config_id"].(string), assistantNumber(input["model_config_revision"])
	if status, code := h.assistantConfigStatus(configID, revision); code != "" {
		writeAssistantFailure(w, status, code, traceID)
		return
	}
	if h.inbox == nil {
		writeError(w, 503, "STORAGE_DISABLED", "助手需要加密存储以持久保留共享模型额度。", traceID)
		return
	}
	owner := assistantOwner(r)
	if owner == "" {
		writeAssistantFailure(w, 401, "UNAUTHORIZED", traceID)
		return
	}
	state := h.assistant
	state.mu.Lock()
	state.pruneLocked()
	if state.closed {
		state.mu.Unlock()
		writeAssistantFailure(w, 503, "ASSISTANT_UNAVAILABLE", traceID)
		return
	}
	sessionID := input["session_id"].(string)
	var session *assistantSession
	if sessionID == "" {
		if len(state.sessions) >= assistantMaxSessions {
			state.mu.Unlock()
			writeAssistantFailure(w, 429, "ASSISTANT_LIMIT", traceID)
			return
		}
		var err error
		sessionID, err = assistantRandomID("as_")
		if err != nil {
			state.mu.Unlock()
			writeAssistantFailure(w, 503, "ASSISTANT_UNAVAILABLE", traceID)
			return
		}
		session = &assistantSession{id: sessionID, owner: owner, references: newAssistantPrivacyRefs()}
		state.sessions[sessionID] = session
	} else {
		session = state.sessions[sessionID]
		if session == nil || session.owner != owner {
			status, code := 404, "ASSISTANT_NOT_FOUND"
			if expired, found := state.expired[sessionID]; found && expired.owner == owner && state.now().Before(expired.expires) {
				status, code = 410, "ASSISTANT_EXPIRED"
			}
			state.mu.Unlock()
			writeAssistantFailure(w, status, code, traceID)
			return
		}
	}
	if session.activeRun != "" {
		state.mu.Unlock()
		writeAssistantFailure(w, 409, "ASSISTANT_BUSY", traceID)
		return
	}
	id, err := assistantRandomID("ar_")
	if err != nil {
		state.mu.Unlock()
		writeAssistantFailure(w, 503, "ASSISTANT_UNAVAILABLE", traceID)
		return
	}
	now := state.now().UTC()
	expires := now.Add(assistantSessionTTL)
	for len(session.runIDs) >= assistantMaxRuns {
		oldest := session.runIDs[0]
		session.runIDs = session.runIDs[1:]
		state.expireRunLocked(oldest)
	}
	run := &assistantRun{owner: owner, expires: expires, active: true, references: session.references.clone(), messages: append([]agent.ToolMessage(nil), session.history...), historyLength: len(session.history)}
	run.messages = append(run.messages, agent.ToolMessage{Role: "user", Content: input["message"].(string)})
	run.response = assistantRunResponse{SessionID: sessionID, RunID: id, State: "running", ToolResults: []assistantToolResult{}, ModelConfigID: configID, ModelConfigRevision: revision, ExpiresAt: expires.Format(time.RFC3339)}
	session.expires, session.activeRun = expires, id
	session.runIDs = append(session.runIDs, id)
	state.runs[id] = run
	state.mu.Unlock()
	h.executeAssistantRun(r, run, nil)
	h.writeAssistantRun(w, run)
}

func (h *handler) recoverAssistantRun(w http.ResponseWriter, r *http.Request, id, traceID string) {
	state := h.assistant
	state.mu.Lock()
	run, status, code := state.lookupRunLocked(id, assistantOwner(r))
	if code == "" {
		payload := cloneAssistantResponse(run.response)
		state.mu.Unlock()
		writeAssistantResponse(w, payload)
		return
	}
	state.mu.Unlock()
	writeAssistantFailure(w, status, code, traceID)
}

func (h *handler) resolveAssistantRun(w http.ResponseWriter, r *http.Request, id, action, traceID string) {
	fields := map[string]*assistantSchema{"action_id": assistantIDSchema()}
	required := []string{"action_id"}
	if action == "confirm" {
		fields["model_config_id"], fields["model_config_revision"], fields["confirmed"] = assistantHashSchema(), assistantInteger(0, 9007199254740991), assistantBoolean()
		required = append(required, "model_config_id", "model_config_revision", "confirmed")
	}
	input, ok := readAssistantJSON(w, r, assistantObject(fields, required...), traceID)
	if !ok {
		return
	}
	if action == "confirm" && input["confirmed"] != true {
		writeError(w, 400, "ASSISTANT_CONSENT_REQUIRED", "请逐项核对本次固定操作后明确确认。", traceID)
		return
	}
	state := h.assistant
	state.mu.Lock()
	run, status, code := state.lookupRunLocked(id, assistantOwner(r))
	if code != "" {
		state.mu.Unlock()
		writeAssistantFailure(w, status, code, traceID)
		return
	}
	if run.pending == nil || run.response.PendingAction == nil || run.response.PendingAction.ID != input["action_id"] || run.active || run.response.State != "awaiting_confirmation" {
		state.mu.Unlock()
		writeAssistantFailure(w, 409, "ASSISTANT_ACTION_CONSUMED", traceID)
		return
	}
	if !state.now().Before(run.pending.expires) {
		state.failRunLocked(run, "failed", "ASSISTANT_EXPIRED")
		state.mu.Unlock()
		writeAssistantFailure(w, 410, "ASSISTANT_EXPIRED", traceID)
		return
	}
	if action == "reject" {
		pending := run.pending
		run.lastActionID = run.response.PendingAction.ID
		run.pending, run.response.PendingAction = nil, nil
		result := assistantToolResult{CallID: pending.callID, OperationID: pending.prepared.OperationID, State: "rejected", ErrorCode: "ASSISTANT_REJECTED"}
		state.addToolResultLocked(run, result)
		encoded, _ := json.Marshal(result)
		run.messages = append(run.messages, agent.ToolMessage{Role: "tool", ToolCallID: pending.callID, Content: string(encoded)}, agent.ToolMessage{Role: "assistant", Content: "已按你的选择拒绝该操作，未执行，也未继续调用模型。"})
		run.response.AssistantMessage = "已拒绝该操作；没有执行业务操作，也没有继续调用模型。"
		state.finishRunLocked(run, "rejected", true)
		payload := cloneAssistantResponse(run.response)
		state.mu.Unlock()
		writeAssistantResponse(w, payload)
		return
	}
	if input["model_config_id"] != run.response.ModelConfigID || assistantNumber(input["model_config_revision"]) != run.response.ModelConfigRevision {
		state.mu.Unlock()
		writeAssistantFailure(w, 409, "ASSISTANT_CONFIG_CHANGED", traceID)
		return
	}
	// Consume before any model/business IO. No client argument can alter the
	// prepared method, path, query, body, target IDs or original consent binding.
	pending := run.pending
	run.lastActionID = run.response.PendingAction.ID
	run.pending, run.response.PendingAction = nil, nil
	run.active, run.response.State = true, "running"
	state.mu.Unlock()
	if status, code = h.assistantConfigStatus(run.response.ModelConfigID, run.response.ModelConfigRevision); code != "" {
		state.mu.Lock()
		state.failRunLocked(run, "failed", code)
		state.mu.Unlock()
		writeAssistantFailure(w, status, code, traceID)
		return
	}
	h.executeAssistantRun(r, run, pending)
	h.writeAssistantRun(w, run)
}

func cloneAssistantResponse(response assistantRunResponse) assistantRunResponse {
	// A snapshot prevents recovery requests racing with an in-flight tool/model
	// update. All state-owned byte slices/maps remain private after unlocking.
	encoded, _ := json.Marshal(response)
	var cloned assistantRunResponse
	_ = json.Unmarshal(encoded, &cloned)
	return cloned
}

func (h *handler) writeAssistantRun(w http.ResponseWriter, run *assistantRun) {
	h.assistant.mu.Lock()
	response := cloneAssistantResponse(run.response)
	h.assistant.mu.Unlock()
	writeAssistantResponse(w, response)
}

func writeAssistantResponse(w http.ResponseWriter, response assistantRunResponse) {
	status := http.StatusOK
	if response.State == "awaiting_confirmation" || response.State == "running" {
		status = http.StatusAccepted
	}
	writeJSON(w, status, response)
}

func assistantErrorMessage(code string) string {
	switch code {
	case "ASSISTANT_CONFIG_CHANGED":
		return "模型配置或版本已变化；旧同意和待确认操作已失效，请刷新并重新开始。"
	case "ASSISTANT_ACTION_CONSUMED":
		return "该操作已被处理或正在执行，不能重复确认；请只读恢复运行结果。"
	case "ASSISTANT_NOT_FOUND":
		return "该助手会话不存在或不属于当前设备。"
	case "ASSISTANT_EXPIRED":
		return "助手会话或待确认操作已过期，请重新开始；不要自动重试写操作。"
	case "ASSISTANT_LIMIT":
		return "助手已达到会话、轮次、大小或执行时间上限，请新建会话或稍后再试。"
	case "ASSISTANT_BUSY":
		return "该会话已有运行或待确认操作，请先处理现有操作。"
	case "ASSISTANT_AI_PAUSED":
		return "相关会话的AI处理已暂停；本次数据未继续提供给模型，请使用新的无敏感历史会话。"
	case "AI_QUOTA_EXCEEDED":
		return "今日共享模型调用额度已用尽；失败的模型请求也计入额度。"
	case "ASSISTANT_MODEL_UNAVAILABLE":
		return "当前模型未配置原生工具调用，或模型响应/连接不可用；未将非结构化文字当作操作执行。"
	case "ASSISTANT_AUTH_LOST", "UNAUTHORIZED", "DEVICE_UNAUTHORIZED":
		return "当前设备授权已失效，未继续调用模型或执行业务操作。"
	case "ASSISTANT_UNKNOWN":
		return "操作执行期间中断，结果不确定；请人工只读核实，助手不会自动重试。"
	case "ASSISTANT_REJECTED":
		return "已拒绝该操作，未执行业务或继续调用模型。"
	default:
		return "助手运行未能继续；请检查状态，勿自动重试可能已执行的操作。"
	}
}

func writeAssistantFailure(w http.ResponseWriter, status int, code, traceID string) {
	writeError(w, status, code, assistantErrorMessage(code), traceID)
}

func (state *assistantState) lookupRunLocked(id, owner string) (*assistantRun, int, string) {
	if run := state.runs[id]; run != nil && run.owner == owner {
		if run.response.Error != nil && run.response.Error.Code == "ASSISTANT_EXPIRED" {
			return nil, 410, "ASSISTANT_EXPIRED"
		}
		if run.pending != nil && !state.now().Before(run.pending.expires) {
			state.failRunLocked(run, "failed", "ASSISTANT_EXPIRED")
			return nil, 410, "ASSISTANT_EXPIRED"
		}
		if !state.now().Before(run.expires) {
			if run.cancel != nil {
				run.cancel()
			}
			if run.executing {
				state.failRunLocked(run, "unknown", "ASSISTANT_UNKNOWN")
			} else {
				state.failRunLocked(run, "failed", "ASSISTANT_EXPIRED")
			}
			return nil, 410, "ASSISTANT_EXPIRED"
		}
		return run, 0, ""
	}
	if expired, ok := state.expired[id]; ok && expired.owner == owner && state.now().Before(expired.expires) {
		return nil, 410, "ASSISTANT_EXPIRED"
	}
	return nil, 404, "ASSISTANT_NOT_FOUND"
}

func (state *assistantState) expireRunLocked(id string) {
	if run := state.runs[id]; run != nil {
		if run.active {
			return
		}
		state.addTombstoneLocked(id, run.owner)
		delete(state.runs, id)
	}
}

func (state *assistantState) addTombstoneLocked(id, owner string) {
	state.expired[id] = assistantTombstone{owner: owner, expires: state.now().Add(assistantSessionTTL)}
	for len(state.expired) > assistantMaxSessions*assistantMaxRuns {
		var oldestID string
		var oldest time.Time
		for key, value := range state.expired {
			if oldestID == "" || value.expires.Before(oldest) {
				oldestID, oldest = key, value.expires
			}
		}
		delete(state.expired, oldestID)
	}
}

func (state *assistantState) pruneLocked() {
	now := state.now()
	for id, tombstone := range state.expired {
		if !now.Before(tombstone.expires) {
			delete(state.expired, id)
		}
	}
	for id, session := range state.sessions {
		active := state.runs[session.activeRun]
		if active != nil && !active.active && active.pending != nil && !now.Before(active.pending.expires) {
			state.failRunLocked(active, "failed", "ASSISTANT_EXPIRED")
		}
		if !now.Before(session.expires) && (active == nil || !active.active) {
			for _, runID := range session.runIDs {
				state.expireRunLocked(runID)
			}
			state.addTombstoneLocked(id, session.owner)
			delete(state.sessions, id)
		}
	}
}

func (state *assistantState) addToolResultLocked(run *assistantRun, result assistantToolResult) {
	for index := range run.response.ToolResults {
		if run.response.ToolResults[index].CallID == result.CallID {
			run.response.ToolResults[index] = result
			return
		}
	}
	run.response.ToolResults = append(run.response.ToolResults, result)
}

func (state *assistantState) finishRunLocked(run *assistantRun, status string, retainHistory bool) {
	run.response.State, run.active, run.executing = status, false, false
	run.pending, run.response.PendingAction = nil, nil
	if session := state.sessions[run.response.SessionID]; session != nil && session.activeRun == run.response.RunID {
		session.activeRun = ""
		if retainHistory && run.historyLength <= len(run.messages) {
			session.history = append(session.history, run.messages[run.historyLength:]...)
			session.references.merge(run.references)
			for len(session.history) > assistantMaxHistory || assistantMessagesBytes(session.history) > assistantMaxHistoryBytes {
				cut := 1
				for cut < len(session.history) && session.history[cut].Role != "user" {
					cut++
				}
				session.history = append([]agent.ToolMessage(nil), session.history[cut:]...)
			}
		}
	}
	run.messages = nil
}

func (state *assistantState) failRunLocked(run *assistantRun, status, code string) {
	if run.pending != nil {
		state.addToolResultLocked(run, assistantToolResult{CallID: run.pending.callID, OperationID: run.pending.prepared.OperationID, State: "failed", ErrorCode: code})
	}
	// A consumed action has no pending pointer while IO is in flight. Its
	// visible result must still become failed/unknown, never remain "pending"
	// after expiry, stale consent, cancellation or handler shutdown.
	for index := range run.response.ToolResults {
		if run.response.ToolResults[index].State == "pending" {
			run.response.ToolResults[index].State = status
			run.response.ToolResults[index].ErrorCode = code
		}
	}
	run.response.Error = &assistantRunError{Code: code, Message: assistantErrorMessage(code)}
	if code == "ASSISTANT_AI_PAUSED" {
		if session := state.sessions[run.response.SessionID]; session != nil {
			session.history = nil
			session.references = newAssistantPrivacyRefs()
		}
	}
	state.finishRunLocked(run, status, false)
}

func assistantMessagesBytes(messages []agent.ToolMessage) int {
	encoded, _ := json.Marshal(messages)
	return len(encoded)
}

func (h *handler) closeAssistant() {
	if h.assistant == nil {
		return
	}
	state := h.assistant
	state.mu.Lock()
	defer state.mu.Unlock()
	state.closed = true
	for _, run := range state.runs {
		if run.cancel != nil {
			run.cancel()
		}
		if run.active || run.pending != nil {
			if run.executing {
				state.failRunLocked(run, "unknown", "ASSISTANT_UNKNOWN")
			} else {
				state.failRunLocked(run, "failed", "ASSISTANT_UNAVAILABLE")
			}
		}
	}
}

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/qilinbro/msg-manager/internal/agent"
	"github.com/qilinbro/msg-manager/internal/store"
)

type assistantDispatchKey struct{}

type assistantDispatchBinding struct {
	configID        string
	revision        int64
	owner           string
	feishuWindowEnd string
}

type assistantRecorder struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	limit    int
	overflow bool
}

func newAssistantRecorder(limit int) *assistantRecorder {
	return &assistantRecorder{header: make(http.Header), limit: limit}
}

func (recorder *assistantRecorder) Header() http.Header { return recorder.header }
func (recorder *assistantRecorder) WriteHeader(status int) {
	if recorder.status == 0 {
		recorder.status = status
	}
}
func (recorder *assistantRecorder) Write(data []byte) (int, error) {
	if recorder.status == 0 {
		recorder.status = http.StatusOK
	}
	remaining := recorder.limit - recorder.body.Len()
	if len(data) > remaining {
		recorder.overflow = true
		_, _ = recorder.body.Write(data[:remaining])
	} else {
		_, _ = recorder.body.Write(data)
	}
	// Discard excess output instead of allocating an unbounded recorder. The
	// business handler can finish, but its truncated output is never forwarded.
	return len(data), nil
}

func (h *handler) assistantConfigStatusLocked(id string, revision int64) (int, string) {
	if h.ai == nil || h.ai.client == nil {
		return http.StatusServiceUnavailable, "ASSISTANT_MODEL_UNAVAILABLE"
	}
	if h.ai.configID != id || h.modelConfig.Revision != revision {
		return http.StatusConflict, "ASSISTANT_CONFIG_CHANGED"
	}
	if _, ok := h.ai.client.(agent.ToolProvider); !ok {
		return http.StatusServiceUnavailable, "ASSISTANT_MODEL_UNAVAILABLE"
	}
	return 0, ""
}

func (h *handler) assistantConfigStatus(id string, revision int64) (int, string) {
	h.aiMu.RLock()
	defer h.aiMu.RUnlock()
	return h.assistantConfigStatusLocked(id, revision)
}

// Called only after the existing router acquires its business aiMu read lock.
// Ordinary human REST is unaffected. This closes the check-to-dispatch config
// race without holding aiMu (or the model gate) across recursive ServeHTTP.
func (h *handler) assistantDispatchAllowed(w http.ResponseWriter, r *http.Request, traceID string) bool {
	binding, internal := r.Context().Value(assistantDispatchKey{}).(assistantDispatchBinding)
	if !internal {
		return true
	}
	if assistantOwner(r) != binding.owner {
		writeAssistantFailure(w, 403, "ASSISTANT_AUTH_LOST", traceID)
		return false
	}
	if status, code := h.assistantConfigStatusLocked(binding.configID, binding.revision); code != "" {
		writeAssistantFailure(w, status, code, traceID)
		return false
	}
	return true
}

func (h *handler) reauthenticateAssistant(ctx context.Context, original *http.Request, owner string) (int, string) {
	// Never persist bearer strings in a session/run. Use only the currently
	// executing HTTP request's credential and re-evaluate current device grants.
	request := original.Clone(context.WithValue(ctx, devicePrincipalKey{}, ""))
	recorder := newAssistantRecorder(4096)
	authenticated, ok := h.authenticateRequest(recorder, request, "assistant-auth")
	if !ok {
		code := "UNAUTHORIZED"
		if bytes.Contains(recorder.body.Bytes(), []byte(`"DEVICE_UNAUTHORIZED"`)) {
			code = "DEVICE_UNAUTHORIZED"
		}
		return http.StatusUnauthorized, code
	}
	if assistantOwner(authenticated) != owner || !validScopeClaims(authenticated, authenticatedWorkspace(authenticated.Context())) {
		return http.StatusForbidden, "ASSISTANT_AUTH_LOST"
	}
	return 0, ""
}

func assistantGloballySensitive(operation *assistantOperation, prepared assistantPreparedOperation) bool {
	// Feishu filters bounded inputs and persists exact source revisions itself.
	// Unrelated paused conversations must not become a blanket schedule grant
	// blocker, nor may this exception bypass any report's frozen provenance.
	if assistantFeishuOperation(operation.ID) {
		return false
	}
	if len(prepared.ConversationIDs) == 0 && operation.Availability == "confirmation_required" {
		// Global sync/import/monitoring cannot safely infer every affected
		// conversation from a peer string. Fail closed if any source is paused.
		return true
	}
	switch operation.ID {
	case "get_inbox_status", "get_inbox_connections", "get_email_status", "get_wechat_status", "get_wechat_sessions", "get_qq_milky_status", "get_latest_digest", "get_profile_links", "create_profile_link", "delete_profile_link":
		return true
	}
	return false
}

func (h *handler) checkAssistantPrivacy(ctx context.Context, references assistantPrivacyRefs) string {
	if references.Invalid {
		return "ASSISTANT_SOURCE_CHANGED"
	}
	if len(references.IDs) == 0 && !references.Global && len(references.Bindings) == 0 && len(references.Revisions) == 0 {
		return ""
	}
	if h.inbox == nil {
		return "ASSISTANT_TOOL_UNAVAILABLE"
	}
	if references.Global {
		paused, err := h.inbox.HasPausedConversationsContext(ctx)
		if err != nil {
			return "ASSISTANT_TOOL_UNAVAILABLE"
		}
		if paused {
			return "ASSISTANT_AI_PAUSED"
		}
	}
	for id := range references.IDs {
		settings, err := h.inbox.AISettingsContext(ctx, id)
		if err != nil {
			return "ASSISTANT_TOOL_UNAVAILABLE"
		}
		if settings.Mode == "paused" {
			return "ASSISTANT_AI_PAUSED"
		}
	}
	refs := make([]store.FeishuSourceRef, 0, len(references.Revisions))
	for id, revision := range references.Revisions {
		refs = append(refs, store.FeishuSourceRef{ConversationID: id, SettingsRevision: revision})
	}
	if len(refs) > 0 {
		if err := h.inbox.CheckFeishuSourceRefs(ctx, refs); err != nil {
			return "ASSISTANT_SOURCE_CHANGED"
		}
	}
	for binding := range references.Bindings {
		if err := h.validateFeishuBinding(ctx, binding, false); err != nil {
			return "ASSISTANT_SOURCE_CHANGED"
		}
	}
	return ""
}

func (h *handler) dispatchAssistantTool(ctx context.Context, original *http.Request, run *assistantRun, callID string, prepared assistantPreparedOperation) (assistantToolResult, assistantPrivacyRefs) {
	result := assistantToolResult{CallID: callID, OperationID: prepared.OperationID, State: "failed"}
	references := newAssistantPrivacyRefs()
	operation := assistantOperationByID(prepared.OperationID)
	if operation == nil || operation.Method != prepared.Method {
		result.ErrorCode = "ASSISTANT_TOOL_INVALID"
		return result, references
	}
	if status, code := h.reauthenticateAssistant(ctx, original, run.owner); code != "" {
		result.HTTPStatus, result.ErrorCode = &status, code
		return result, references
	}
	if status, code := h.assistantConfigStatus(run.response.ModelConfigID, run.response.ModelConfigRevision); code != "" {
		result.HTTPStatus, result.ErrorCode = &status, code
		return result, references
	}
	for _, id := range prepared.ConversationIDs {
		references.IDs[id] = true
	}
	references.Global = assistantGloballySensitive(operation, prepared)
	privacyChecks := references
	if operation.ID == "update_ai_settings" {
		// Only this independently human-confirmed operation may pause/resume a
		// paused conversation. Other reads/writes cannot auto-unpause it.
		privacyChecks = newAssistantPrivacyRefs()
	}
	h.assistant.mu.Lock()
	prior := run.references.clone()
	h.assistant.mu.Unlock()
	prior.merge(privacyChecks)
	unlock, code := h.assistantPrivacyBarrier(ctx, run.response.ModelConfigID, run.response.ModelConfigRevision, prior)
	if code != "" {
		status := http.StatusConflict
		result.HTTPStatus, result.ErrorCode = &status, code
		return result, references
	}
	unlock() // Never recursively ServeHTTP while holding aiMu/dispatch leases.
	if ctx.Err() != nil {
		result.ErrorCode = "ASSISTANT_TIMEOUT"
		return result, references
	}
	binding := assistantDispatchBinding{configID: run.response.ModelConfigID, revision: run.response.ModelConfigRevision, owner: run.owner, feishuWindowEnd: prepared.FeishuWindowEnd}
	ctx = context.WithValue(ctx, assistantDispatchKey{}, binding)
	request, err := http.NewRequestWithContext(ctx, prepared.Method, "http://"+original.Host+prepared.Path, bytes.NewReader(prepared.Body))
	if err != nil {
		result.ErrorCode = "ASSISTANT_TOOL_INVALID"
		return result, references
	}
	request.URL.RawQuery = prepared.Query
	request.Host = original.Host
	for _, field := range []string{"Authorization", "Origin", "Sec-Fetch-Site"} {
		for _, value := range original.Header.Values(field) {
			request.Header.Add(field, value)
		}
	}
	if len(prepared.Body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := newAssistantRecorder(agent.MaxToolResponseBytes)
	// No assistant-state mutex, aiMu or model gate is held here. The existing
	// router re-runs Host/Origin/auth/scope and all business-specific checks.
	h.ServeHTTP(recorder, request)
	status := recorder.status
	if status == 0 {
		status = http.StatusOK
	}
	result.HTTPStatus = &status
	if ctx.Err() != nil && operation.Availability == "confirmation_required" {
		result.State, result.ErrorCode = "unknown", "ASSISTANT_UNKNOWN"
		return result, references
	}
	if status >= 400 {
		result.ErrorCode = "ASSISTANT_TOOL_REJECTED"
		if status >= 500 {
			result.ErrorCode = "ASSISTANT_TOOL_UNAVAILABLE"
			if operation.Availability == "confirmation_required" {
				result.State, result.ErrorCode = "unknown", "ASSISTANT_UNKNOWN"
			}
		}
		for _, code := range []string{"ASSISTANT_CONFIG_CHANGED", "ASSISTANT_AUTH_LOST", "UNAUTHORIZED", "DEVICE_UNAUTHORIZED"} {
			if bytes.Contains(recorder.body.Bytes(), []byte(`"`+code+`"`)) {
				result.State, result.ErrorCode = "failed", code
				break
			}
		}
		// Raw business errors (including upstream connector text) never become
		// a tool result. A fixed code + status provides enough recovery context.
		return result, references
	}
	if recorder.overflow {
		result.ErrorCode = "ASSISTANT_TOOL_RESPONSE_LIMIT"
		if operation.Availability == "confirmation_required" {
			result.State, result.ErrorCode = "unknown", "ASSISTANT_UNKNOWN"
		}
		return result, references
	}
	value, err := assistantDecode(recorder.body.Bytes(), agent.MaxToolResponseBytes)
	if err != nil {
		return assistantUnreadableToolResult(result, operation, "ASSISTANT_TOOL_INVALID_RESPONSE"), references
	}
	if operation.ID == "list_conversations" {
		if code := h.filterAssistantConversations(ctx, value, &references); code != "" {
			result.ErrorCode = code
			return result, references
		}
	} else {
		assistantCollectConversationIDs(value, references.IDs)
	}
	assistantCollectFeishuRefs(value, &references)
	desiredState, desiredCode := "succeeded", ""
	if assistantFeishuOperation(operation.ID) {
		if state, code := assistantFeishuResultState(value); state != "" {
			desiredState, desiredCode = state, code
		}
	}
	privacyAfter := references
	if operation.ID == "update_ai_settings" {
		privacyAfter = newAssistantPrivacyRefs()
	}
	unlock, code = h.assistantPrivacyBarrier(ctx, run.response.ModelConfigID, run.response.ModelConfigRevision, privacyAfter)
	if code != "" {
		result.ErrorCode = code
		if operation.Availability == "confirmation_required" {
			result.State = "succeeded"
			if status == http.StatusAccepted {
				result.State, result.ErrorCode = "unknown", "ASSISTANT_UNKNOWN"
			}
		}
		return result, references
	}
	defer unlock()
	bearer := strings.TrimPrefix(original.Header.Get("Authorization"), "Bearer ")
	secrets, secretErr := h.assistantSecretValues(ctx, h.modelConfig.APIKey, bearer)
	if secretErr != nil || assistantValueContainsSecret(value, secrets...) {
		return assistantUnreadableToolResult(result, operation, "ASSISTANT_SECRET_BLOCKED"), references
	}
	data, err := json.Marshal(value)
	if err != nil || len(data) > agent.MaxToolResponseBytes {
		return assistantUnreadableToolResult(result, operation, "ASSISTANT_TOOL_RESPONSE_LIMIT"), references
	}
	result.State, result.Data, result.ErrorCode = desiredState, data, desiredCode
	return result, references
}

// A response that cannot be safely decoded/exposed is never evidence that an
// effectful operation did not happen. Stop the run instead of offering a retry.
func assistantUnreadableToolResult(result assistantToolResult, operation *assistantOperation, code string) assistantToolResult {
	result.Data, result.ErrorCode = nil, code
	if operation.Availability == "confirmation_required" {
		result.State, result.ErrorCode = "unknown", "ASSISTANT_UNKNOWN"
	}
	return result
}

func (h *handler) filterAssistantConversations(ctx context.Context, value any, references *assistantPrivacyRefs) string {
	page, ok := value.(map[string]any)
	if !ok || h.inbox == nil {
		return "ASSISTANT_TOOL_INVALID_RESPONSE"
	}
	items, ok := page["items"].([]any)
	if !ok {
		return "ASSISTANT_TOOL_INVALID_RESPONSE"
	}
	kept := make([]any, 0, len(items))
	filtered := 0
	for _, value := range items {
		item, ok := value.(map[string]any)
		if !ok {
			return "ASSISTANT_TOOL_INVALID_RESPONSE"
		}
		id, ok := item["id"].(string)
		if !ok || !aiResourceID(id) {
			return "ASSISTANT_TOOL_INVALID_RESPONSE"
		}
		settings, err := h.inbox.AISettingsContext(ctx, id)
		if err != nil {
			return "ASSISTANT_TOOL_UNAVAILABLE"
		}
		if settings.Mode == "paused" {
			filtered++
			continue
		}
		references.IDs[id] = true
		kept = append(kept, item)
	}
	page["items"] = kept
	page["assistant_privacy"] = map[string]any{"filtered_paused": filtered, "note": "已过滤暂停AI的会话；保留原分页游标，空页不表示没有更多会话。"}
	return ""
}

func assistantCollectConversationIDs(value any, ids map[string]bool) {
	switch typed := value.(type) {
	case map[string]any:
		for name, item := range typed {
			if name == "conversation_id" || name == "target_conversation_id" {
				if id, ok := item.(string); ok && aiResourceID(id) {
					ids[id] = true
				}
			}
			if name == "conversation_ids" {
				if values, ok := item.([]any); ok {
					for _, value := range values {
						if id, ok := value.(string); ok && aiResourceID(id) {
							ids[id] = true
						}
					}
				}
			}
			assistantCollectConversationIDs(item, ids)
		}
	case []any:
		for _, item := range typed {
			assistantCollectConversationIDs(item, ids)
		}
	}
}

func assistantValueContainsSecret(value any, secrets ...string) bool {
	switch typed := value.(type) {
	case map[string]any:
		for name, item := range typed {
			switch strings.ToLower(name) {
			case "api_key", "access_token", "tenant_access_token", "app_secret", "client_secret", "recipient_open_id", "authorization", "password", "database_key", "private_key":
				return true
			}
			if assistantValueContainsSecret(item, secrets...) {
				return true
			}
			// Native function arguments are themselves an encoded JSON object.
			// Inspect decoded values too: Unicode escaping must not hide a key
			// or bearer before it is canonicalized into a pending action.
			if name == "arguments" {
				if raw, ok := item.(string); ok {
					if decoded, err := assistantDecode([]byte(raw), agent.MaxToolArgumentBytes); err == nil && assistantValueContainsSecret(decoded, secrets...) {
						return true
					}
				}
			}
		}
	case []any:
		for _, item := range typed {
			if assistantValueContainsSecret(item, secrets...) {
				return true
			}
		}
	case string:
		for _, secret := range secrets {
			if secret != "" && strings.Contains(typed, secret) {
				return true
			}
		}
	}
	return false
}

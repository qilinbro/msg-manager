package api

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/qilinbro/msg-manager/internal/agent"
	"github.com/qilinbro/msg-manager/internal/store"
)

// Never hold this lease while recursively entering ServeHTTP: business handlers
// acquire their own locks. It is used for final checks before IO and cached output.
func (h *handler) assistantPrivacyBarrier(ctx context.Context, configID string, revision int64, references assistantPrivacyRefs) (func(), string) {
	if err := lockFeishu(ctx, &h.aiMu, false); err != nil {
		return nil, "ASSISTANT_TIMEOUT"
	}
	if err := lockFeishu(ctx, &h.feishuDispatchMu, false); err != nil {
		h.aiMu.RUnlock()
		return nil, "ASSISTANT_TIMEOUT"
	}
	unlock := func() { h.feishuDispatchMu.RUnlock(); h.aiMu.RUnlock() }
	if _, code := h.assistantConfigStatusLocked(configID, revision); code != "" {
		unlock()
		return nil, code
	}
	if code := h.checkAssistantPrivacy(ctx, references); code != "" {
		unlock()
		return nil, code
	}
	return unlock, ""
}

func assistantFeishuOperation(id string) bool {
	switch id {
	case "get_feishu_status", "get_feishu_schedule", "update_feishu_schedule", "generate_feishu_preview", "get_latest_feishu_report", "get_feishu_report", "send_feishu_report", "get_feishu_delivery":
		return true
	}
	return false
}

// The report carries all actual model-input sources, not merely excerpt IDs.
// Conflicting frozen revisions/bindings poison a history instead of silently
// replacing the old revision and resurrecting it after pause -> resume or A->B->A.
func assistantCollectFeishuRefs(value any, references *assistantPrivacyRefs) {
	switch typed := value.(type) {
	case map[string]any:
		if _, exists := typed["source_refs"]; exists {
			raw, err := json.Marshal(typed)
			var report store.FeishuReport
			if err != nil || json.Unmarshal(raw, &report) != nil || !report.FeishuBinding.Valid() || len(report.SourceRefs) > 4 {
				references.Invalid = true
				return
			}
			incoming := newAssistantPrivacyRefs()
			incoming.Bindings[report.FeishuBinding] = true
			seen := map[string]bool{}
			for _, ref := range report.SourceRefs {
				if !aiResourceID(ref.ConversationID) || ref.SettingsRevision < 0 || seen[ref.ConversationID] {
					incoming.Invalid = true
					continue
				}
				seen[ref.ConversationID] = true
				incoming.IDs[ref.ConversationID] = true
				incoming.Revisions[ref.ConversationID] = ref.SettingsRevision
			}
			references.merge(incoming)
		}
		for _, item := range typed {
			assistantCollectFeishuRefs(item, references)
		}
	case []any:
		for _, item := range typed {
			assistantCollectFeishuRefs(item, references)
		}
	}
}

func (h *handler) assistantSecretValues(ctx context.Context, modelKey, bearer string) ([]string, error) {
	secrets := []string{modelKey, bearer}
	if h.inbox == nil {
		return secrets, nil
	}
	cfg, err := h.inbox.LoadFeishuConfig(ctx)
	if err != nil {
		return nil, err
	}
	return append(secrets, cfg.AppSecret, cfg.RecipientOpenID), nil
}

// Only the server constructs Feishu mutation bodies. The model chooses no
// recipient, credentials, text, URL, window, content hash or consent binding.
// The complete immutable send report is displayed to the human in arguments,
// while only its frozen hash/binding are sent to the business endpoint.
func (h *handler) freezeAssistantFeishuPending(ctx context.Context, run *assistantRun, prepared assistantPreparedOperation, arguments map[string]any, bearer string) (assistantPreparedOperation, assistantPrivacyRefs, []string, string) {
	references := newAssistantPrivacyRefs()
	effects := []string{}
	switch prepared.OperationID {
	case "update_feishu_schedule", "generate_feishu_preview", "send_feishu_report":
	default:
		return prepared, references, effects, ""
	}
	h.assistant.mu.Lock()
	prior := run.references.clone()
	h.assistant.mu.Unlock()
	unlock, code := h.assistantPrivacyBarrier(ctx, run.response.ModelConfigID, run.response.ModelConfigRevision, prior)
	if code != "" {
		return prepared, references, effects, code
	}
	defer unlock()
	if h.inbox == nil || h.feishu == nil {
		return prepared, references, effects, "ASSISTANT_TOOL_UNAVAILABLE"
	}
	status, err := h.exposeFeishuStatus(ctx)
	if err != nil {
		return prepared, references, effects, "ASSISTANT_TOOL_UNAVAILABLE"
	}
	body := map[string]any{"confirmed": true}
	if len(prepared.Body) > 0 {
		value, e := assistantDecode(prepared.Body, agent.MaxToolArgumentBytes+1024)
		if e != nil {
			return prepared, references, effects, "ASSISTANT_TOOL_INVALID"
		}
		body = value.(map[string]any)
	}
	binding := store.FeishuBinding{ModelConfigID: run.response.ModelConfigID, ModelConfigRevision: run.response.ModelConfigRevision, ConfigRevision: status.ConfigRevision, TargetBindingID: status.TargetBindingID}
	if prepared.OperationID == "send_feishu_report" {
		id, _ := arguments["report_id"].(string)
		report, e := h.readFeishuReportUnderLease(ctx, id, false)
		if e != nil || report == nil || report.State != "succeeded" || report.Text == "" || report.ContentHash != store.FeishuContentHash(*report) {
			return prepared, references, effects, "ASSISTANT_CONTEXT_STALE"
		}
		end, e := time.Parse(time.RFC3339Nano, report.WindowEnd)
		if e != nil || !h.feishu.now().Before(end.Add(12*time.Hour)) {
			return prepared, references, effects, "ASSISTANT_CONTEXT_STALE"
		}
		// A consumed ledger is read-only recovery, never another send confirmation.
		if report.Delivery != nil {
			return prepared, references, effects, "ASSISTANT_ACTION_CONSUMED"
		}
		binding = report.FeishuBinding
		body["content_hash"] = report.ContentHash
		arguments["content_hash"] = report.ContentHash
		arguments["report_preview"] = report
		raw, _ := json.Marshal(report)
		value, _ := assistantDecode(raw, agent.MaxToolResponseBytes)
		assistantCollectFeishuRefs(value, &references)
		effects = append(effects, "仅向人工设置的本人私聊尝试发送这一条完整冻结文本；接收者归属尚未验证。", "202只表示受理，不代表送达。投递台账任意状态均禁止再次尝试。")
	} else if prepared.OperationID == "update_feishu_schedule" {
		enabled, _ := arguments["enabled"].(bool)
		if assistantNumber(arguments["expected_revision"]) != status.Schedule.Revision {
			return prepared, references, effects, "ASSISTANT_CONTEXT_STALE"
		}
		if !enabled {
			binding = store.FeishuBinding{}
		}
		effects = append(effects, "固定Asia/Shanghai 08:00/20:00，相邻十二小时窗口；开启只从下一未来时点开始。无重试、无离线追补。", "停止仅取消后续处理，不能保证撤回在途请求或平台已接受的消息。")
	} else {
		start, end := store.FeishuWindow(h.feishu.now())
		arguments["window_start"], arguments["window_end"] = store.FeishuTime(start), store.FeishuTime(end)
		prepared.FeishuWindowEnd = store.FeishuTime(end)
		effects = append(effects, "只生成本次最近已结束窗口的有界报告，不发送；任意已存在报告均不重算。", "事项是服务器基于原文提取的待人工核对候选，不是模型草稿或承诺已完成事项。")
	}
	if prepared.OperationID != "update_feishu_schedule" || arguments["enabled"] == true {
		if !status.Configured || !binding.Valid() {
			return prepared, references, effects, "ASSISTANT_TOOL_UNAVAILABLE"
		}
		if err = h.validateFeishuBinding(ctx, binding, true); err != nil {
			return prepared, references, effects, "ASSISTANT_CONTEXT_STALE"
		}
	}
	if prepared.OperationID != "update_feishu_schedule" || arguments["enabled"] == true {
		body["model_config_id"], body["model_config_revision"], body["config_revision"], body["target_binding_id"] = binding.ModelConfigID, binding.ModelConfigRevision, binding.ConfigRevision, binding.TargetBindingID
		arguments["model_config_id"], arguments["model_config_revision"], arguments["config_revision"], arguments["target_binding_id"] = binding.ModelConfigID, binding.ModelConfigRevision, binding.ConfigRevision, binding.TargetBindingID
		arguments["target_label_masked"] = status.TargetLabelMasked
		arguments["recipient_ownership_verified"] = false
		arguments["consent_binding"] = map[string]any{"model_config_id": run.response.ModelConfigID, "model_config_revision": run.response.ModelConfigRevision, "config_revision": status.ConfigRevision, "target_binding_id": status.TargetBindingID}
	} else {
		// The generic mutation preparer may already have attached model keys.
		// Revocation deliberately has no model/target consent binding on the wire.
		for _, key := range feishuBindingFields {
			delete(body, key)
			delete(arguments, key)
		}
	}
	if binding.Valid() {
		references.Bindings[binding] = true
	}
	rawArguments, err := json.Marshal(arguments)
	if err != nil {
		return prepared, references, effects, "ASSISTANT_TOOL_INVALID"
	}
	argumentValue, err := assistantDecode(rawArguments, agent.MaxToolResponseBytes)
	if err != nil {
		return prepared, references, effects, "ASSISTANT_TOOL_RESPONSE_LIMIT"
	}
	secrets, err := h.assistantSecretValues(ctx, h.modelConfig.APIKey, bearer)
	if err != nil {
		return prepared, references, effects, "ASSISTANT_TOOL_UNAVAILABLE"
	}
	if assistantValueContainsSecret(argumentValue, secrets...) {
		return prepared, references, effects, "ASSISTANT_SECRET_BLOCKED"
	}
	prepared.Body, err = json.Marshal(body)
	if err != nil || len(prepared.Body) > 8192 {
		return prepared, references, effects, "ASSISTANT_TOOL_INVALID"
	}
	return prepared, references, effects, ""
}

func assistantFeishuResultState(value any) (string, string) {
	object, ok := value.(map[string]any)
	if !ok {
		return "", ""
	}
	for _, name := range []string{"report", "delivery"} {
		item, ok := object[name].(map[string]any)
		if !ok {
			continue
		}
		state, _ := item["state"].(string)
		code, _ := item["error_code"].(string)
		switch state {
		case "running":
			return "pending", ""
		case "failed", "unknown":
			return state, code
		case "succeeded":
			return "succeeded", ""
		}
	}
	return "", ""
}

func assistantFeishuPendingMessage(id, state string) string {
	if state == "pending" {
		if strings.Contains(id, "delivery") || id == "send_feishu_report" {
			return "飞书投递台账显示仍在处理中：受理不代表已送达。请稍后只读查询投递状态；不会自动重发。"
		}
		return "飞书报告仍在处理中：受理不代表生成成功，更未发送。请稍后只读查询报告；不会自动重算或自动执行下一项操作。"
	}
	return "飞书本窗口处理未成功，或结果不确定。请只读核实报告/投递台账并人工处理；不会自动重算、补发或重试。"
}

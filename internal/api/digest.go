package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/qilinbro/msg-manager/internal/agent"
	"github.com/qilinbro/msg-manager/internal/store"
)

var errDigestBusy = errors.New("digest model gate is busy")

func digestRead(path string) bool {
	return path == "/api/v1/digest/schedule" || path == "/api/v1/digest/latest"
}
func digestMutation(method, path string) bool {
	return method == http.MethodPatch && path == "/api/v1/digest/schedule" || method == http.MethodPost && path == "/api/v1/digest/run"
}

func digestTimestamp() *string {
	value := time.Now().UTC().Format(time.RFC3339Nano)
	return &value
}

// Consent fields use exact spelling: case aliases and null must never grant
// authorization. The shared decoder bounds depth and rejects duplicate keys.
func readDigestJSON(w http.ResponseWriter, r *http.Request, target any, traceID string, required []string, optional ...string) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var fields map[string]json.RawMessage
	if !readInboxJSON(w, r, &fields, traceID) {
		return false
	}
	keys := make(map[string]bool, len(required)+len(optional))
	for _, key := range required {
		if _, present := fields[key]; !present {
			writeError(w, 400, "INVALID_JSON", "日报参数缺少必填字段。", traceID)
			return false
		}
		keys[key] = true
	}
	for _, key := range optional {
		keys[key] = true
	}
	for key, value := range fields {
		if !keys[key] || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			writeError(w, 400, "INVALID_JSON", "日报参数包含未支持的字段或空值。", traceID)
			return false
		}
	}
	raw, err := json.Marshal(fields)
	if err != nil || json.Unmarshal(raw, target) != nil {
		writeError(w, 400, "INVALID_JSON", "日报参数格式无效。", traceID)
		return false
	}
	return true
}

func (h *handler) serveDigest(w http.ResponseWriter, r *http.Request, traceID string) {
	if h.inbox == nil {
		writeError(w, 503, "STORAGE_DISABLED", "摘要需要本地加密消息库。", traceID)
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, 400, "INVALID_QUERY", "摘要接口不接受查询参数。", traceID)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	switch {
	case r.URL.Path == "/api/v1/digest/schedule" && r.Method == http.MethodGet:
		schedule, err := h.inbox.LoadDigestSchedule(ctx)
		if err != nil {
			writeDigestError(w, err, traceID)
			return
		}
		writeJSON(w, 200, schedule)
	case r.URL.Path == "/api/v1/digest/schedule" && r.Method == http.MethodPatch:
		var input struct {
			Enabled             bool   `json:"enabled"`
			LocalTime           string `json:"local_time"`
			Timezone            string `json:"timezone"`
			ExpectedRevision    int64  `json:"expected_revision"`
			Confirmed           bool   `json:"confirmed"`
			ModelConfigID       string `json:"model_config_id"`
			ModelConfigRevision *int64 `json:"model_config_revision"`
		}
		if !readDigestJSON(w, r, &input, traceID, []string{"enabled", "local_time", "timezone", "expected_revision", "confirmed"}, "model_config_id", "model_config_revision") {
			return
		}
		if !input.Confirmed {
			writeError(w, 400, "DIGEST_CONFIRMATION_REQUIRED", "请核对摘要计划及模型数据使用范围后明确确认。", traceID)
			return
		}
		revision := int64(0)
		if input.Enabled {
			if h.ai.client == nil {
				writeError(w, 503, "AI_NOT_CONFIGURED", "请先配置模型，再同意每日发送摘要所需的消息文本。", traceID)
				return
			}
			if input.ModelConfigID != h.ai.configID || input.ModelConfigRevision == nil || *input.ModelConfigRevision != h.modelConfig.Revision {
				writeError(w, 409, "DIGEST_MODEL_CHANGED", "模型设置已变化，请重新核对目标并同意每日数据使用。", traceID)
				return
			}
			revision = *input.ModelConfigRevision
		} else {
			input.ModelConfigID = ""
		}
		saved, err := h.inbox.SaveDigestSchedule(ctx, store.DigestSchedule{Enabled: input.Enabled, LocalTime: strings.TrimSpace(input.LocalTime), Timezone: input.Timezone, ModelConfigID: input.ModelConfigID, ModelConfigRevision: revision}, input.ExpectedRevision)
		if err != nil {
			if errors.Is(err, store.ErrStaleContext) {
				writeError(w, 409, "DIGEST_STALE", "摘要计划已变化，请刷新后核对。", traceID)
			} else if errors.Is(err, store.ErrInvalidInput) {
				writeError(w, 400, "DIGEST_INVALID", "请选择有效时间及上海时区。", traceID)
			} else {
				writeDigestError(w, err, traceID)
			}
			return
		}
		writeJSON(w, 200, saved)
	case r.URL.Path == "/api/v1/digest/latest" && r.Method == http.MethodGet:
		report, err := h.inbox.LatestDigestReport(ctx)
		if err != nil {
			writeDigestError(w, err, traceID)
			return
		}
		writeJSON(w, 200, map[string]any{"report": report, "sending_enabled": false})
	case r.URL.Path == "/api/v1/digest/run" && r.Method == http.MethodPost:
		var input struct {
			Confirmed           bool   `json:"confirmed"`
			ModelConfigID       string `json:"model_config_id"`
			ModelConfigRevision *int64 `json:"model_config_revision"`
		}
		if !readDigestJSON(w, r, &input, traceID, []string{"confirmed", "model_config_id", "model_config_revision"}) {
			return
		}
		if !input.Confirmed {
			writeError(w, 400, "DIGEST_CONFIRMATION_REQUIRED", "请明确同意将未暂停会话的摘要所需文本发送至当前模型。", traceID)
			return
		}
		if h.ai.client == nil {
			writeError(w, 503, "AI_NOT_CONFIGURED", "请先配置模型，再手动生成摘要。", traceID)
			return
		}
		if input.ModelConfigID != h.ai.configID || input.ModelConfigRevision == nil || *input.ModelConfigRevision != h.modelConfig.Revision {
			writeError(w, 409, "DIGEST_MODEL_CHANGED", "模型设置已变化，请刷新目标并重新确认。", traceID)
			return
		}
		report, err := h.generateDigest(ctx, true)
		if err != nil {
			writeDigestError(w, err, traceID)
			return
		}
		writeJSON(w, 200, map[string]any{"report": report, "sending_enabled": false})
	default:
		writeError(w, 405, "METHOD_NOT_ALLOWED", "摘要接口方法无效。", traceID)
	}
}

func digestError(err error) (string, int, string) {
	switch {
	case errors.Is(err, store.ErrQuotaExceeded):
		return "AI_QUOTA_EXCEEDED", 429, "今日模型调用额度已用尽，请先查看已保存的摘要状态。"
	case errors.Is(err, errDigestBusy):
		return "AI_BUSY", 409, "模型正在处理其他请求；本次未开始，请稍后核对摘要状态。"
	case errors.Is(err, context.DeadlineExceeded):
		return "DIGEST_TIMEOUT", 504, "摘要生成超时，请刷新最近摘要；不要盲目重放请求。"
	case errors.Is(err, context.Canceled):
		return "DIGEST_INTERRUPTED", 408, "摘要处理已中断，请读取最近摘要核对结果。"
	}
	var modelErr *agent.Error
	if errors.As(err, &modelErr) {
		switch modelErr.Kind {
		case agent.AuthFailed:
			return "AI_AUTH_FAILED", 503, "模型认证失败，请人工检查模型设置。"
		case agent.QuotaExceeded:
			return "AI_QUOTA_EXCEEDED", 429, "模型服务额度或频率受限，请先查看摘要状态。"
		case agent.Timeout:
			return "AI_TIMEOUT", 504, "模型响应超时，请读取摘要状态，不要盲目重试。"
		case agent.InvalidOutput:
			return "AI_INVALID_OUTPUT", 502, "模型结果或证据无效，本次摘要未成功。"
		case agent.InvalidInput:
			return "DIGEST_INPUT_INVALID", 422, "摘要消息输入无法通过校验，本次未成功。"
		}
	}
	return "DIGEST_UNAVAILABLE", 503, "摘要暂时不可用，请查看模型设置、加密存储及最近摘要状态。"
}

func writeDigestError(w http.ResponseWriter, err error, traceID string) {
	code, status, message := digestError(err)
	writeError(w, status, code, message, traceID)
}

func digestEvidence(messages []store.DigestMessage) []string {
	out := make([]string, 0, len(messages))
	for _, message := range messages {
		out = append(out, message.Message.ID)
	}
	return out
}

func digestRelevant(message store.DigestMessage) int {
	body := strings.ToLower(message.Message.Body)
	score := 0
	if message.Unread {
		score += 3
	}
	for _, marker := range []string{"?", "？", "请", "需要", "截止", "明天", "今天", "周", "¥", "钱", "付款", "合同", "会议", "安排", "提醒", "@"} {
		if strings.Contains(body, strings.ToLower(marker)) {
			score += 2
		}
	}
	if message.Message.Direction == "incoming" {
		score++
	}
	return score
}

// Both callers hold aiMu.RLock. The common gate is held only while generating
// this report, never while the assistant recursively dispatches a tool.
func (h *handler) generateDigest(ctx context.Context, force bool) (report *store.DigestReport, err error) {
	select {
	case h.ai.gate <- struct{}{}:
		defer func() { <-h.ai.gate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return nil, errDigestBusy
	}
	now := time.Now().UTC()
	day := now.In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006-01-02")
	claimed, err := h.inbox.ClaimDigestRun(ctx, day, force)
	if err != nil {
		return nil, err
	}
	if !claimed {
		return h.inbox.DigestReportForDay(ctx, day)
	}
	report = &store.DigestReport{ID: store.DigestReportID(day), Day: day, State: "running", Reminders: []store.DigestReminder{}, EvidenceIDs: []string{}}
	attempted := false
	defer func() {
		if err == nil {
			return
		}
		report.State = "failed"
		var modelErr *agent.Error
		if attempted && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.As(err, &modelErr) && (modelErr.Kind == agent.Timeout || modelErr.Kind == agent.Unavailable)) {
			report.State = "unknown"
		}
		report.ErrorCode, _, _ = digestError(err)
		report.GeneratedAt = digestTimestamp()
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// Terminal persistence is safe to attempt; no model or business mutation
		// is retried. An unavailable store may still leave the durable running row.
		_ = h.inbox.CompleteDigestReport(cleanup, *report)
	}()
	items, err := h.inbox.LoadDigestMessages(ctx, now.Add(-24*time.Hour).Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), 500)
	if err != nil {
		return report, err
	}
	byConversation := map[string][]store.DigestMessage{}
	for _, item := range items {
		if digestRelevant(item) >= 3 && len(byConversation[item.ConversationID]) < 20 {
			byConversation[item.ConversationID] = append(byConversation[item.ConversationID], item)
		}
	}
	ids := make([]string, 0, len(byConversation))
	for id := range byConversation {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	summaries := []string{}
	for _, conversationID := range ids {
		settings, settingsErr := h.inbox.AISettingsContext(ctx, conversationID)
		if settingsErr != nil {
			return report, settingsErr
		}
		if settings.Mode == "paused" {
			continue
		}
		messages := byConversation[conversationID]
		input := agent.Input{ConversationID: conversationID, Tone: settings.Tone, Messages: []agent.Evidence{}, Memories: []agent.Memory{}}
		for {
			input.Messages = input.Messages[:0]
			for i := len(messages) - 1; i >= 0; i-- {
				input.Messages = append(input.Messages, messages[i].Message)
			}
			raw, marshalErr := json.Marshal(input)
			if marshalErr != nil {
				return report, &agent.Error{Kind: agent.InvalidInput}
			}
			if len(raw) <= agent.MaxInputBytes || len(messages) <= 1 {
				break
			}
			messages = messages[:len(messages)-1]
		}
		if err := agent.ValidateInput(input); err != nil {
			return report, err
		}
		if _, err := h.inbox.ReserveAICallContext(ctx, aiDay(), conversationID, h.ai.dailyLimit); err != nil {
			return report, err
		}
		if err := ctx.Err(); err != nil {
			return report, err
		}
		attempted = true
		result, callErr := h.ai.client.Analyze(ctx, input)
		if ctx.Err() != nil {
			return report, ctx.Err()
		}
		if callErr != nil {
			return report, callErr
		}
		if err := agent.ValidateOutput(input, result.Output); err != nil {
			return report, err
		}
		settings, settingsErr = h.inbox.AISettingsContext(ctx, conversationID)
		if settingsErr != nil {
			return report, settingsErr
		}
		if settings.Mode == "paused" {
			continue
		}
		title := messages[0].Title
		summaries = append(summaries, title+"："+result.Output.Summary)
		flags := mergeAIRisks(input, result.Output.RiskFlags)
		priority := "normal"
		if len(flags) > 0 {
			priority = "high"
		}
		if len(flags) > 0 || len(messages) > 5 {
			report.Reminders = append(report.Reminders, store.DigestReminder{ConversationID: conversationID, Title: title, Platform: messages[0].Platform, ConversationKind: messages[0].ConversationKind, Priority: priority, Reason: result.Output.Summary, EvidenceIDs: result.Output.EvidenceIDs})
		}
		report.EvidenceIDs = append(report.EvidenceIDs, digestEvidence(messages)...)
		report.ConversationCount++
	}
	report.State = "succeeded"
	report.Summary = strings.Join(summaries, "\n")
	if len(summaries) == 0 {
		report.Summary = "过去24小时没有达到相关性阈值的未暂停会话消息。"
	}
	report.GeneratedAt = digestTimestamp()
	if err := h.inbox.CompleteDigestReport(ctx, *report); err != nil {
		return report, err
	}
	return report, nil
}

func (h *handler) maybeDailyDigest(ctx context.Context) {
	if h.inbox == nil || h.ai.client == nil {
		return
	}
	schedule, err := h.inbox.LoadDigestSchedule(ctx)
	if err != nil || !schedule.Enabled || schedule.ModelConfigID != h.ai.configID || schedule.ModelConfigRevision != h.modelConfig.Revision {
		return
	}
	now := time.Now().In(time.FixedZone("Asia/Shanghai", 8*60*60))
	day := now.Format("2006-01-02")
	if schedule.LastRunDay == day || now.Format("15:04") < schedule.LocalTime {
		return
	}
	runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	_, _ = h.generateDigest(runCtx, false)
}

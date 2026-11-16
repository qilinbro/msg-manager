package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/qilinbro/msg-manager/internal/agent"
	"github.com/qilinbro/msg-manager/internal/store"
)

const digestSchedulePath = "/api/v1/digest/schedule"

type digestReportEnvelope struct {
	Report *store.DigestReport `json:"report"`
}

func digestMutation(method, path string) bool {
	return (method == http.MethodPatch && path == digestSchedulePath) || (method == http.MethodPost && path == "/api/v1/digest/run")
}

func digestRead(path string) bool {
	return path == digestSchedulePath || path == "/api/v1/digest/latest"
}

func (h *handler) serveDigest(w http.ResponseWriter, r *http.Request, traceID string) {
	if h.inbox == nil {
		writeError(w, http.StatusServiceUnavailable, "STORAGE_DISABLED", "请先配置本地加密消息存储。", traceID)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	switch {
	case r.URL.Path == digestSchedulePath && r.Method == http.MethodGet:
		schedule, err := h.inbox.LoadDigestSchedule(ctx)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "DIGEST_UNAVAILABLE", "无法读取每日摘要设置。", traceID)
			return
		}
		writeJSON(w, http.StatusOK, schedule)
	case r.URL.Path == digestSchedulePath && r.Method == http.MethodPatch:
		var input struct {
			Enabled          bool   `json:"enabled"`
			LocalTime        string `json:"local_time"`
			Timezone         string `json:"timezone"`
			ExpectedRevision int64  `json:"expected_revision"`
			Confirmed        bool   `json:"confirmed"`
		}
		if !readInboxJSON(w, r, &input, traceID) {
			return
		}
		if !input.Confirmed || input.Timezone == "" {
			writeError(w, http.StatusBadRequest, "DIGEST_CONFIRMATION_REQUIRED", "请核对摘要时间和提醒范围后保存。", traceID)
			return
		}
		saved, err := h.inbox.SaveDigestSchedule(ctx, store.DigestSchedule{Enabled: input.Enabled, LocalTime: strings.TrimSpace(input.LocalTime), Timezone: input.Timezone}, input.ExpectedRevision)
		if err != nil {
			if errors.Is(err, store.ErrStaleContext) {
				writeError(w, http.StatusConflict, "DIGEST_STALE", "摘要设置已变化，请刷新后再保存。", traceID)
			} else {
				writeError(w, http.StatusBadRequest, "DIGEST_INVALID", "摘要时间必须是24小时制的 HH:MM。", traceID)
			}
			return
		}
		writeJSON(w, http.StatusOK, saved)
	case r.URL.Path == "/api/v1/digest/latest" && r.Method == http.MethodGet:
		report, err := h.inbox.LatestDigestReport(ctx)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "DIGEST_UNAVAILABLE", "无法读取最近摘要。", traceID)
			return
		}
		if report == nil {
			writeJSON(w, http.StatusOK, digestReportEnvelope{})
			return
		}
		writeJSON(w, http.StatusOK, digestReportEnvelope{Report: report})
	case r.URL.Path == "/api/v1/digest/run" && r.Method == http.MethodPost:
		if !h.modelConfigured() {
			writeError(w, http.StatusServiceUnavailable, "AI_NOT_CONFIGURED", "请先配置模型，再生成每日摘要。", traceID)
			return
		}
		report, err := h.generateDigest(ctx, true)
		if err != nil {
			writeDigestError(w, err, traceID)
			return
		}
		writeJSON(w, http.StatusOK, report)
	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "摘要接口不存在。", traceID)
	}
}

func (h *handler) modelConfigured() bool {
	return h.ai != nil && h.ai.client != nil
}

func writeDigestError(w http.ResponseWriter, err error, traceID string) {
	code, status, message := "DIGEST_UNAVAILABLE", http.StatusServiceUnavailable, "摘要暂时无法生成，请查看设置和后台状态。"
	if errors.Is(err, store.ErrQuotaExceeded) {
		code, message = "AI_QUOTA_EXCEEDED", "模型额度已用尽，摘要会在额度恢复后重试。"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		code, message = "DIGEST_TIMEOUT", "摘要生成超时，请刷新最近摘要状态。"
	}
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

func (h *handler) generateDigest(ctx context.Context, force bool) (*store.DigestReport, error) {
	day := time.Now().In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006-01-02")
	claimed, err := h.inbox.ClaimDigestRun(ctx, day, force)
	if err != nil {
		return nil, err
	}
	if !claimed {
		return h.inbox.LatestDigestReport(ctx)
	}
	start := time.Now().In(time.FixedZone("Asia/Shanghai", 8*60*60)).Add(-24 * time.Hour).Format(time.RFC3339)
	end := time.Now().In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format(time.RFC3339)
	items, err := h.inbox.LoadDigestMessages(ctx, start, end, 500)
	if err != nil {
		return nil, err
	}
	byConversation := map[string][]store.DigestMessage{}
	for _, item := range items {
		if digestRelevant(item) < 3 {
			continue
		}
		if len(byConversation[item.ConversationID]) < 20 {
			byConversation[item.ConversationID] = append(byConversation[item.ConversationID], item)
		}
	}
	report := &store.DigestReport{ID: "digest-report-" + day, Day: day, State: "succeeded", Reminders: []store.DigestReminder{}, EvidenceIDs: []string{}}
	var summaries []string
	for conversationID, messages := range byConversation {
		if len(messages) == 0 {
			continue
		}
		input := agent.Input{ConversationID: conversationID, Tone: "natural", Messages: make([]agent.Evidence, 0, len(messages)), Memories: []agent.Memory{}}
		for _, item := range messages {
			input.Messages = append(input.Messages, item.Message)
		}
		if err := agent.ValidateInput(input); err != nil {
			continue
		}
		result, callErr := h.ai.client.(agent.Provider).Analyze(ctx, input)
		if callErr != nil {
			continue
		}
		if result.Output.Summary != "" {
			title := messages[0].Title
			summaries = append(summaries, title+"："+result.Output.Summary)
		}
		priority := "normal"
		if len(result.Output.RiskFlags) > 0 {
			priority = "high"
		}
		if len(result.Output.RiskFlags) > 0 || len(messages) > 5 {
			report.Reminders = append(report.Reminders, store.DigestReminder{ConversationID: conversationID, Title: messages[0].Title, Platform: messages[0].Platform, ConversationKind: messages[0].ConversationKind, Priority: priority, Reason: result.Output.Summary, EvidenceIDs: result.Output.EvidenceIDs})
		}
		report.EvidenceIDs = append(report.EvidenceIDs, digestEvidence(messages)...)
		report.ConversationCount++
	}
	if len(summaries) == 0 {
		report.Summary = "过去24小时没有达到相关性阈值的已入库消息。"
	} else {
		report.Summary = strings.Join(summaries, "\n")
	}
	report.GeneratedAt = ptrString(time.Now().UTC().Format(time.RFC3339Nano))
	if err := h.inbox.CompleteDigestReport(ctx, *report); err != nil {
		return nil, err
	}
	return report, nil
}

func (h *handler) maybeDailyDigest(ctx context.Context) {
	if h.inbox == nil || !h.modelConfigured() {
		return
	}
	schedule, err := h.inbox.LoadDigestSchedule(ctx)
	if err != nil || !schedule.Enabled || schedule.Timezone != "Asia/Shanghai" {
		return
	}
	now := time.Now().In(time.FixedZone("Asia/Shanghai", 8*60*60))
	if now.Format("15:04") < schedule.LocalTime {
		return
	}
	_, _ = h.generateDigest(ctx, false)
}

func ptrString(value string) *string { return &value }

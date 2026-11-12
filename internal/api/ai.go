package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/qilinbro/msg-manager/internal/agent"
	"github.com/qilinbro/msg-manager/internal/store"
)

type aiAnalyzer interface {
	Analyze(context.Context, agent.Input) (agent.Result, error)
}
type aiConnection struct {
	client       aiAnalyzer
	gate         chan struct{}
	model        string
	provider     string
	endpointHost string
	configID     string
	external     bool
	dailyLimit   int
}

func newAIConnection(cfg agent.Config, limit int) (*aiConnection, error) {
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 1000 {
		return nil, errors.New("AI daily request limit is invalid")
	}
	c := &aiConnection{gate: make(chan struct{}, 1), dailyLimit: limit}
	if cfg.BaseURL == "" && cfg.APIKey == "" && cfg.Model == "" {
		return c, nil
	}
	client, err := agent.NewClient(cfg)
	if err != nil {
		return nil, errors.New("AI configuration is invalid; check the locally configured base URL, model and API key")
	}
	u, _ := url.Parse(cfg.BaseURL)
	c.client = client
	c.model = cfg.Model
	c.endpointHost = u.Host
	// Bind confirmation to the destination and model, never to the secret key.
	configBytes, _ := json.Marshal([]string{u.String(), cfg.Model})
	fingerprint := sha256.Sum256(configBytes)
	c.configID = hex.EncodeToString(fingerprint[:])
	c.provider = "兼容模型服务"
	if u.Hostname() == "api.deepseek.com" {
		c.provider = "DeepSeek"
	}
	ip := net.ParseIP(u.Hostname())
	c.external = ip == nil || !ip.IsLoopback()
	return c, nil
}

func aiPath(path string) (conversation, resource, id string, ok bool) {
	if path == "/api/v1/ai/status" {
		return "", "status", "", true
	}
	parts := strings.Split(path, "/")
	if len(parts) < 6 || parts[1] != "api" || parts[2] != "v1" || parts[3] != "conversations" || !aiResourceID(parts[4]) {
		return "", "", "", false
	}
	if len(parts) == 6 {
		switch parts[5] {
		case "ai-context", "analysis", "analyze", "draft", "ai-settings":
			return parts[4], parts[5], "", true
		}
	}
	if len(parts) == 7 && parts[5] == "memories" && aiResourceID(parts[6]) {
		return parts[4], "memory", parts[6], true
	}
	return "", "", "", false
}

func aiResourceID(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, ch := range value {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_') {
			return false
		}
	}
	return true
}

func aiMutation(method, path string) bool {
	_, resource, _, ok := aiPath(path)
	if !ok {
		return false
	}
	return method == http.MethodPost && resource == "analyze" || method == http.MethodPatch && (resource == "draft" || resource == "ai-settings" || resource == "memory") || method == http.MethodDelete && resource == "memory"
}

func aiRead(path string) bool {
	_, resource, _, ok := aiPath(path)
	return ok && (resource == "status" || resource == "ai-context" || resource == "analysis" || resource == "ai-settings")
}

type aiRecord struct {
	ID             string      `json:"id"`
	ConversationID string      `json:"conversation_id"`
	ContextHash    string      `json:"context_hash"`
	Model          string      `json:"model"`
	CreatedAt      string      `json:"created_at"`
	UpdatedAt      string      `json:"updated_at"`
	DraftEdited    bool        `json:"draft_edited"`
	Stale          bool        `json:"stale"`
	Intent         string      `json:"intent"`
	Summary        string      `json:"summary"`
	Draft          string      `json:"draft"`
	RiskFlags      []string    `json:"risk_flags"`
	EvidenceIDs    []string    `json:"evidence_ids"`
	Usage          agent.Usage `json:"usage"`
	SendingEnabled bool        `json:"sending_enabled"`
}

func (c *aiConnection) exposeRecord(record *store.AIAnalysis) *aiRecord {
	if record == nil {
		return nil
	}
	return &aiRecord{ID: record.ID, ConversationID: record.ConversationID, ContextHash: aiConsentHash(record.ContextHash, record.ConfigID), Model: record.Model, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt, DraftEdited: record.DraftEdited, Stale: record.Stale || record.ConfigID != c.configID, Intent: record.Intent, Summary: record.Summary, Draft: record.Draft, RiskFlags: record.RiskFlags, EvidenceIDs: record.EvidenceIDs, Usage: agent.Usage{PromptTokens: record.PromptTokens, CompletionTokens: record.CompletionTokens}, SendingEnabled: false}
}

func aiConsentHash(contextHash, configID string) string {
	value, _ := json.Marshal([]string{contextHash, configID})
	hash := sha256.Sum256(value)
	return hex.EncodeToString(hash[:])
}

func aiDay() string {
	return time.Now().In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006-01-02")
}

func (h *handler) serveAI(w http.ResponseWriter, r *http.Request, traceID string) {
	conversation, resource, id, ok := aiPath(r.URL.Path)
	if !ok {
		writeError(w, 404, "NOT_FOUND", "AI接口不存在。", traceID)
		return
	}
	if resource == "status" && r.Method == http.MethodGet {
		used := 0
		if h.inbox != nil {
			var err error
			used, err = h.inbox.CountAICalls(aiDay())
			if err != nil {
				writeAIError(w, err, traceID)
				return
			}
		}
		var provider, model, host *string
		if h.ai.client != nil {
			provider = &h.ai.provider
			model = &h.ai.model
			host = &h.ai.endpointHost
		}
		writeJSON(w, 200, struct {
			Configured     bool    `json:"configured"`
			Provider       *string `json:"provider"`
			Model          *string `json:"model"`
			EndpointHost   *string `json:"endpoint_host"`
			SendingEnabled bool    `json:"sending_enabled"`
			DailyLimit     int     `json:"daily_limit"`
			UsedToday      int     `json:"used_today"`
		}{h.ai.client != nil, provider, model, host, false, h.ai.dailyLimit, used})
		return
	}
	if h.inbox == nil {
		writeError(w, 503, "STORAGE_DISABLED", "请先启用本地加密消息存储。", traceID)
		return
	}
	if resource == "ai-context" && r.Method == http.MethodGet {
		data, err := h.inbox.AIContext(conversation)
		if err != nil {
			writeAIError(w, err, traceID)
			return
		}
		var model, host *string
		if h.ai.client != nil {
			model = &h.ai.model
			host = &h.ai.endpointHost
		}
		writeJSON(w, 200, struct {
			ContextHash  string            `json:"context_hash"`
			Model        *string           `json:"model"`
			EndpointHost *string           `json:"endpoint_host"`
			External     bool              `json:"external"`
			MessageCount int               `json:"message_count"`
			MemoryCount  int               `json:"memory_count"`
			Truncated    bool              `json:"truncated"`
			Messages     []store.AIMessage `json:"messages"`
			Memories     []store.AIMemory  `json:"memories"`
			Mode         string            `json:"mode"`
			Tone         string            `json:"tone"`
		}{aiConsentHash(data.ContextHash, h.ai.configID), model, host, h.ai.external, len(data.Messages), len(data.Memories), data.Truncated, data.Messages, data.Memories, data.Settings.Mode, data.Settings.Tone})
		return
	}
	if resource == "analysis" && r.Method == http.MethodGet {
		analysis, err := h.inbox.LastAIAnalysis(conversation)
		if err != nil {
			writeAIError(w, err, traceID)
			return
		}
		memories, err := h.inbox.ListAIMemories(conversation)
		if err != nil {
			writeAIError(w, err, traceID)
			return
		}
		writeJSON(w, 200, struct {
			Analysis *aiRecord        `json:"analysis"`
			Memories []store.AIMemory `json:"memories"`
		}{h.ai.exposeRecord(analysis), memories})
		return
	}
	if resource == "ai-settings" {
		if r.Method == http.MethodPatch {
			var input struct {
				Mode string `json:"mode"`
				Tone string `json:"tone"`
			}
			if !readInboxJSON(w, r, &input, traceID) {
				return
			}
			if err := h.inbox.UpdateAISettings(conversation, input.Mode, input.Tone); err != nil {
				writeAIError(w, err, traceID)
				return
			}
		}
		settings, err := h.inbox.AISettings(conversation)
		if err != nil {
			writeAIError(w, err, traceID)
			return
		}
		writeJSON(w, 200, settings)
		return
	}
	if resource == "memory" {
		var err error
		if r.Method == http.MethodDelete {
			err = h.inbox.DeleteAIMemory(conversation, id)
		} else {
			var input struct {
				State string `json:"verification_state"`
			}
			if !readInboxJSON(w, r, &input, traceID) {
				return
			}
			err = h.inbox.UpdateAIMemory(conversation, id, input.State)
		}
		if err != nil {
			writeAIError(w, err, traceID)
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	if resource == "draft" {
		var input struct {
			AnalysisID string `json:"analysis_id"`
			Body       string `json:"body"`
		}
		if !readInboxJSON(w, r, &input, traceID) {
			return
		}
		if err := h.inbox.UpdateAIDraft(conversation, input.AnalysisID, input.Body, h.ai.configID); err != nil {
			writeAIError(w, err, traceID)
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	if resource == "analyze" {
		h.analyzeConversation(w, r, conversation, traceID)
		return
	}
	writeError(w, 404, "NOT_FOUND", "AI接口不存在。", traceID)
}

func (h *handler) analyzeConversation(w http.ResponseWriter, r *http.Request, conversation, traceID string) {
	var input struct {
		ContextHash string `json:"context_hash"`
		Confirmed   bool   `json:"confirmed"`
	}
	if !readInboxJSON(w, r, &input, traceID) {
		return
	}
	if !input.Confirmed || len(input.ContextHash) != 64 {
		writeError(w, 400, "AI_CONFIRMATION_REQUIRED", "请先核对本次会话内容并确认分析。", traceID)
		return
	}
	if h.ai.client == nil {
		writeError(w, 503, "AI_NOT_CONFIGURED", "请先在本机后台配置模型服务。", traceID)
		return
	}
	select {
	case h.ai.gate <- struct{}{}:
		defer func() { <-h.ai.gate }()
	default:
		writeError(w, 503, "AI_BUSY", "已有AI分析正在进行，请稍后查看结果。", traceID)
		return
	}
	data, err := h.inbox.AIContext(conversation)
	if err != nil {
		writeAIError(w, err, traceID)
		return
	}
	if data.Settings.Mode == "paused" {
		writeError(w, 409, "AI_PAUSED", "该会话的AI处理已暂停。", traceID)
		return
	}
	if aiConsentHash(data.ContextHash, h.ai.configID) != input.ContextHash {
		writeAIError(w, store.ErrStaleContext, traceID)
		return
	}
	modelInput := agent.Input{ConversationID: conversation, Tone: data.Settings.Tone, Messages: make([]agent.Evidence, 0, len(data.Messages)), Memories: make([]agent.Memory, 0, len(data.Memories))}
	for _, m := range data.Messages {
		modelInput.Messages = append(modelInput.Messages, agent.Evidence{ID: m.ID, Body: m.Body, Direction: m.Direction, OccurredAt: m.OccurredAt})
	}
	for _, m := range data.Memories {
		modelInput.Memories = append(modelInput.Memories, agent.Memory{ID: m.ID, Content: m.Content, VerificationState: m.VerificationState, EvidenceIDs: m.EvidenceIDs})
	}
	if err := agent.ValidateInput(modelInput); err != nil {
		writeError(w, 400, "AI_CONTEXT_INVALID", "当前会话没有可分析内容，或超出模型输入限制。", traceID)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 28*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		writeError(w, 504, "AI_TIMEOUT", "分析已中断，尚未调用模型。", traceID)
		return
	}
	if _, err = h.inbox.ReserveAICall(aiDay(), conversation, h.ai.dailyLimit); err != nil {
		writeAIError(w, err, traceID)
		return
	}
	result, err := h.ai.client.Analyze(ctx, modelInput)
	if err != nil {
		writeAIError(w, err, traceID)
		return
	}
	if agent.ValidateOutput(modelInput, result.Output) != nil {
		writeError(w, 502, "AI_INVALID_OUTPUT", "模型结果格式或证据范围无效，本次没有保存草稿。", traceID)
		return
	}
	result.Output.RiskFlags = mergeAIRisks(modelInput, result.Output.RiskFlags)
	if ctx.Err() != nil {
		writeError(w, 504, "AI_TIMEOUT", "分析等待已结束，请刷新确认结果后再试。", traceID)
		return
	}
	saved := store.AIResult{Intent: result.Output.Intent, Summary: result.Output.Summary, Draft: result.Output.Draft, Model: h.ai.model, ConfigID: h.ai.configID, RiskFlags: result.Output.RiskFlags, EvidenceIDs: result.Output.EvidenceIDs, MemoryCandidates: []store.AICandidate{}, PromptTokens: result.Usage.PromptTokens, CompletionTokens: result.Usage.CompletionTokens}
	for _, candidate := range result.Output.MemoryCandidates {
		saved.MemoryCandidates = append(saved.MemoryCandidates, store.AICandidate{Content: candidate.Content, EvidenceIDs: candidate.EvidenceIDs})
	}
	record, err := h.inbox.SaveAIAnalysis(conversation, data.ContextHash, saved)
	if err != nil {
		writeAIError(w, err, traceID)
		return
	}
	writeJSON(w, 200, h.ai.exposeRecord(&record))
}

func mergeAIRisks(input agent.Input, modelFlags []string) []string {
	flags := map[string]bool{}
	for _, flag := range modelFlags {
		flags[flag] = true
	}
	for _, message := range input.Messages {
		text := strings.ToLower(message.Body)
		for _, rule := range []struct {
			flag  string
			words []string
		}{{"payment", []string{"转账", "付款", "借款", "借钱", "收款", "payment", "bank transfer", "loan"}}, {"commitment", []string{"合同", "签约", "保证", "承诺", "contract", "commitment", "promise"}}, {"sensitive", []string{"密码", "身份证", "验证码", "password", "verification code", "passport"}}, {"emotional_conflict", []string{"分手", "绝交", "恨你", "break up", "hate you"}}, {"uncertain", []string{"忽略规则", "忽略之前", "泄露", "ignore previous", "ignore instructions", "system prompt"}}} {
			for _, word := range rule.words {
				if strings.Contains(text, word) {
					flags[rule.flag] = true
					break
				}
			}
		}
	}
	result := make([]string, 0, len(flags))
	for flag := range flags {
		result = append(result, flag)
	}
	sort.Strings(result)
	return result
}

func writeAIError(w http.ResponseWriter, err error, traceID string) {
	if errors.Is(err, store.ErrStaleContext) {
		writeError(w, 409, "STALE_CONTEXT", "会话、记忆或规则已变化，请重新预览后分析。", traceID)
		return
	}
	if errors.Is(err, store.ErrQuotaExceeded) {
		writeError(w, 429, "AI_QUOTA_EXCEEDED", "今日AI请求次数已达到本地限额。", traceID)
		return
	}
	var providerError *agent.Error
	if errors.As(err, &providerError) {
		switch string(providerError.Kind) {
		case "auth_failed":
			writeError(w, 503, "AI_AUTH_FAILED", "模型认证失败，请检查本机密钥。", traceID)
		case "quota_exceeded":
			writeError(w, 429, "AI_QUOTA_EXCEEDED", "模型服务额度或请求频率已达到限制。", traceID)
		case "timeout":
			writeError(w, 504, "AI_TIMEOUT", "模型响应超时，请先刷新结果后再试。", traceID)
		case "invalid_output":
			writeError(w, 502, "AI_INVALID_OUTPUT", "模型结果格式或证据无效，本次未保存。", traceID)
		default:
			writeError(w, 503, "AI_UNAVAILABLE", "模型服务暂时不可用，请稍后重试。", traceID)
		}
		return
	}
	writeInboxError(w, err, traceID)
}

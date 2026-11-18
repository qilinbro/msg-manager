package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/qilinbro/msg-manager/internal/agent"
	"github.com/qilinbro/msg-manager/internal/store"
)

const modelConfigPath = "/api/v1/ai/model-config"

type modelConfigResponse struct {
	BaseURL        string `json:"base_url"`
	Model          string `json:"model"`
	DailyLimit     int    `json:"daily_limit"`
	KeyConfigured  bool   `json:"key_configured"`
	Revision       int64  `json:"revision"`
	Configured     bool   `json:"configured"`
	ModelConfigID  string `json:"model_config_id"`
	SendingEnabled bool   `json:"sending_enabled"`
}

func (h *handler) exposeModelConfig() modelConfigResponse {
	return modelConfigResponse{BaseURL: h.modelConfig.BaseURL, Model: h.modelConfig.Model, DailyLimit: h.ai.dailyLimit, KeyConfigured: h.modelConfig.APIKey != "", Revision: h.modelConfig.Revision, Configured: h.ai.client != nil, ModelConfigID: h.ai.configID, SendingEnabled: false}
}

func (h *handler) restoreModelConfig() error {
	if h.inbox == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	saved, err := h.inbox.LoadModelConfig(ctx)
	if err != nil {
		return err
	}
	if saved == nil {
		return nil
	}
	client, err := newAIConnection(agent.Config{BaseURL: saved.BaseURL, Model: saved.Model, APIKey: saved.APIKey}, saved.DailyLimit)
	if err != nil {
		return store.ErrUnavailable
	}
	h.ai = client
	h.modelConfig = *saved
	return nil
}

func (h *handler) serveModelConfig(w http.ResponseWriter, r *http.Request, traceID string) {
	if r.URL.RawQuery != "" {
		writeError(w, 400, "AI_CONFIG_INVALID", "模型设置不接受地址查询参数。", traceID)
		return
	}
	if r.Method == http.MethodGet {
		h.aiMu.RLock()
		defer h.aiMu.RUnlock()
		writeJSON(w, 200, h.exposeModelConfig())
		return
	}
	if r.Method != http.MethodPatch {
		writeError(w, 405, "METHOD_NOT_ALLOWED", "请选择读取或保存模型设置。", traceID)
		return
	}
	if h.inbox == nil {
		writeError(w, 503, "STORAGE_DISABLED", "请先启用本地加密消息库，再保存模型设置。", traceID)
		return
	}
	var input struct {
		ExpectedRevision *int64 `json:"expected_revision"`
		BaseURL          string `json:"base_url"`
		Model            string `json:"model"`
		APIKey           string `json:"api_key"`
		DailyLimit       int    `json:"daily_limit"`
		Confirmed        bool   `json:"confirmed"`
	}
	if !readInboxJSON(w, r, &input, traceID) {
		return
	}
	if input.ExpectedRevision == nil || *input.ExpectedRevision < 0 || !input.Confirmed {
		writeError(w, 400, "AI_CONFIG_CONFIRMATION_REQUIRED", "请核对模型地址、名称和额度后明确确认保存。", traceID)
		return
	}
	if input.DailyLimit < 1 || input.DailyLimit > 1000 {
		writeError(w, 400, "AI_CONFIG_INVALID", "每日调用上限须在1至1000之间。", traceID)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	// Cancel before aiMu: accepted Feishu analyses hold an immutable model
	// snapshot. The dispatch writer below also drains already-started sends.
	h.feishu.cancelOperations()
	// All in-flight manual/profile requests finish under a read lock. Switching
	// waits for their immutable configuration snapshot, never mixes two models.
	for !h.aiMu.TryLock() {
		select {
		case <-ctx.Done():
			writeError(w, 503, "AI_CONFIG_BUSY", "当前模型仍在处理，请稍后保存；输入已保留。", traceID)
			return
		case <-time.After(5 * time.Millisecond):
		}
	}
	defer h.aiMu.Unlock()
	if *input.ExpectedRevision != h.modelConfig.Revision {
		writeError(w, 409, "AI_CONFIG_STALE", "模型设置已被更新，输入已保留，请刷新后核对。", traceID)
		return
	}
	key := input.APIKey
	// Retain a blank key only for the exact same URL. A changed destination
	// must pass the complete transport/model validation without the old key;
	// only explicit-port numeric-loopback HTTP can be configured keylessly.
	if key == "" && input.BaseURL == h.modelConfig.BaseURL {
		key = h.modelConfig.APIKey
	}
	next, err := newAIConnection(agent.Config{BaseURL: input.BaseURL, Model: input.Model, APIKey: key}, input.DailyLimit)
	if err != nil || next.client == nil {
		if key == "" && input.BaseURL != h.modelConfig.BaseURL && h.modelConfig.APIKey != "" {
			writeError(w, 400, "AI_CONFIG_KEY_REQUIRED", "更换服务地址时请重新填写对应密钥。", traceID)
			return
		}
		writeError(w, 400, "AI_CONFIG_INVALID", "请检查模型地址、名称、密钥与每日额度；公网服务需HTTPS。", traceID)
		return
	}
	if h.profiles != nil {
		h.profiles.lifecycle.Lock()
		closed := h.profiles.closed
		h.profiles.lifecycle.Unlock()
		if closed {
			writeError(w, 503, "AI_CONFIG_BUSY", "后台服务正在停止，请稍后保存。", traceID)
			return
		}
	}
	if err := lockFeishu(ctx, &h.feishuDispatchMu, true); err != nil {
		writeError(w, 503, "AI_CONFIG_BUSY", "飞书操作正在停止；本次模型设置未保存。", traceID)
		return
	}
	defer h.feishuDispatchMu.Unlock()
	saved, err := h.inbox.SaveModelConfig(ctx, store.ModelConfig{BaseURL: input.BaseURL, Model: input.Model, APIKey: key, DailyLimit: next.dailyLimit}, *input.ExpectedRevision, h.ai.configID)
	if err != nil {
		if errors.Is(err, store.ErrStaleContext) {
			writeError(w, 409, "AI_CONFIG_STALE", "模型设置已被更新，请刷新后核对。", traceID)
		} else {
			writeError(w, 503, "AI_CONFIG_UNAVAILABLE", "设置未保存，请检查加密存储后重试。", traceID)
		}
		return
	}
	h.ai = next
	h.modelConfig = saved
	writeJSON(w, 200, h.exposeModelConfig())
}

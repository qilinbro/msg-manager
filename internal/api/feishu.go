package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/qilinbro/msg-manager/internal/agent"
	"github.com/qilinbro/msg-manager/internal/connector/feishu"
	"github.com/qilinbro/msg-manager/internal/store"
)

const feishuPrefix = "/api/v1/feishu"

var errFeishuBusy = errors.New("feishu model gate busy")
var errFeishuModelChanged = errors.New("feishu model binding changed")

type feishuStatus struct {
	Transport                  string                `json:"transport"`
	RuntimeConfigured          bool                  `json:"runtime_configured"`
	Configured                 bool                  `json:"configured"`
	KeyConfigured              bool                  `json:"key_configured"`
	AppID                      string                `json:"app_id"`
	ConfigRevision             int64                 `json:"config_revision"`
	TargetBindingID            string                `json:"target_binding_id"`
	TargetLabelMasked          string                `json:"target_label_masked"`
	RecipientOpenIDMasked      string                `json:"recipient_open_id_masked"`
	RecipientOwnershipVerified bool                  `json:"recipient_ownership_verified"`
	Schedule                   store.FeishuSchedule  `json:"schedule"`
	LatestReportID             string                `json:"latest_report_id"`
	LatestDelivery             *store.FeishuDelivery `json:"latest_delivery"`
	Capabilities               struct {
		PrivateSelfOnly bool `json:"private_self_only"`
		AutomaticRetry  bool `json:"automatic_retry"`
	} `json:"capabilities"`
	Timezone string   `json:"timezone"`
	Slots    []string `json:"slots"`
}

func feishuPath(path string) bool {
	return path == feishuPrefix || strings.HasPrefix(path, feishuPrefix+"/")
}
func feishuRecordID(value, prefix string) bool {
	if len(value) != len(prefix)+32 || !strings.HasPrefix(value, prefix) {
		return false
	}
	for _, r := range value[len(prefix):] {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
func feishuRoute(path string) (resource, id string) {
	if !strings.HasPrefix(path, feishuPrefix+"/") {
		return "", ""
	}
	suffix := strings.TrimPrefix(path, feishuPrefix+"/")
	switch suffix {
	case "status", "config", "schedule", "preview", "reports/latest":
		return suffix, ""
	}
	parts := strings.Split(suffix, "/")
	if len(parts) == 2 && parts[0] == "reports" && feishuRecordID(parts[1], "fr_") {
		return "report", parts[1]
	}
	if len(parts) == 3 && parts[0] == "reports" && parts[2] == "send" && feishuRecordID(parts[1], "fr_") {
		return "send", parts[1]
	}
	if len(parts) == 2 && parts[0] == "deliveries" && feishuRecordID(parts[1], "fd_") {
		return "delivery", parts[1]
	}
	return "", ""
}
func feishuMutation(method, path string) bool {
	resource, _ := feishuRoute(path)
	return resource == "config" && (method == http.MethodPatch || method == http.MethodDelete) || resource == "schedule" && method == http.MethodPatch || (resource == "preview" || resource == "send") && method == http.MethodPost
}
func feishuMethodAllowed(method, resource string) bool {
	if resource == "config" {
		return method == http.MethodPatch || method == http.MethodDelete
	}
	if resource == "schedule" {
		return method == http.MethodGet || method == http.MethodPatch
	}
	if resource == "preview" || resource == "send" {
		return method == http.MethodPost
	}
	return resource != "" && method == http.MethodGet
}

// This decoder additionally requires canonical case-sensitive field spelling,
// non-null values, bounded bodies, and the shared duplicate/depth/trailing guard.
func readFeishuJSON(w http.ResponseWriter, r *http.Request, target any, trace string, required []string, optional ...string) (map[string]json.RawMessage, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	var fields map[string]json.RawMessage
	if !readInboxJSON(w, r, &fields, trace) {
		return nil, false
	}
	allowed := map[string]bool{}
	for _, key := range required {
		allowed[key] = true
		if _, ok := fields[key]; !ok {
			writeFeishuError(w, store.ErrInvalidInput, trace)
			return nil, false
		}
	}
	for _, key := range optional {
		allowed[key] = true
	}
	for key, value := range fields {
		if !allowed[key] || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			writeFeishuError(w, store.ErrInvalidInput, trace)
			return nil, false
		}
	}
	raw, _ := json.Marshal(fields)
	if json.Unmarshal(raw, target) != nil {
		writeFeishuError(w, store.ErrInvalidInput, trace)
		return nil, false
	}
	return fields, true
}

var feishuBindingFields = []string{"model_config_id", "model_config_revision", "config_revision", "target_binding_id"}

func lockFeishu(ctx context.Context, mu *sync.RWMutex, write bool) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if write {
			if mu.TryLock() {
				return nil
			}
		} else if mu.TryRLock() {
			return nil
		}
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
func (h *handler) validateFeishuBinding(ctx context.Context, b store.FeishuBinding, needModel bool) error {
	if !b.Valid() {
		return store.ErrInvalidInput
	}
	if h.ai == nil || h.ai.configID != b.ModelConfigID || h.modelConfig.Revision != b.ModelConfigRevision {
		return errFeishuModelChanged
	}
	if needModel && h.ai.client == nil {
		return &agent.Error{Kind: agent.NotConfigured}
	}
	return h.inbox.CheckFeishuBinding(ctx, b)
}
func (h *handler) exposeFeishuStatus(ctx context.Context) (feishuStatus, error) {
	var out feishuStatus
	c, err := h.inbox.LoadFeishuConfig(ctx)
	if err != nil {
		return out, err
	}
	schedule, err := h.inbox.LoadFeishuSchedule(ctx)
	if err != nil {
		return out, err
	}
	latest, err := h.inbox.LatestFeishuReport(ctx)
	if err != nil {
		return out, err
	}
	out.Transport = h.feishu.transport
	out.RuntimeConfigured = h.feishu.available()
	out.Configured = c.Configured()
	out.KeyConfigured = c.AppSecret != ""
	out.AppID = c.AppID
	out.ConfigRevision = c.Revision
	out.TargetBindingID = c.TargetBindingID()
	if c.RecipientOpenID != "" {
		out.RecipientOpenIDMasked = "ou_***" + c.RecipientOpenID[max(3, len(c.RecipientOpenID)-4):]
		out.TargetLabelMasked = "本人私聊（" + out.RecipientOpenIDMasked + "；归属尚未验证）"
	}
	out.Schedule = schedule
	out.Timezone = store.FeishuTimezone
	out.Slots = []string{"08:00", "20:00"}
	out.Capabilities.PrivateSelfOnly = true
	if latest != nil {
		out.LatestReportID = latest.ID
		out.LatestDelivery = latest.Delivery
	}
	return out, nil
}
func (h *handler) readFeishuReport(ctx context.Context, id string, latest bool) (*store.FeishuReport, error) {
	if err := lockFeishu(ctx, &h.feishuDispatchMu, false); err != nil {
		return nil, err
	}
	defer h.feishuDispatchMu.RUnlock()
	return h.readFeishuReportUnderLease(ctx, id, latest)
}

// Caller holds aiMu then the dispatch read lease through use of the returned text.
func (h *handler) readFeishuReportUnderLease(ctx context.Context, id string, latest bool) (*store.FeishuReport, error) {
	var report *store.FeishuReport
	var err error
	if latest {
		report, err = h.inbox.LatestFeishuReport(ctx)
	} else {
		report, err = h.inbox.FeishuReportByID(ctx, id)
	}
	if err != nil || report == nil {
		return report, err
	}
	if err = h.validateFeishuBinding(ctx, report.FeishuBinding, false); err != nil {
		return nil, err
	}
	// Never return stale derived text, including after pause -> resume. Minimal
	// receipt metadata remains available from /deliveries without exposing content.
	if err = h.inbox.CheckFeishuSourceRefs(ctx, report.SourceRefs); err != nil {
		return nil, err
	}
	return report, nil
}
func (h *handler) serveFeishu(w http.ResponseWriter, r *http.Request, trace string) {
	resource, id := feishuRoute(r.URL.Path)
	if resource == "" {
		writeError(w, 404, "NOT_FOUND", "未找到飞书接口。", trace)
		return
	}
	if !feishuMethodAllowed(r.Method, resource) {
		writeError(w, 405, "METHOD_NOT_ALLOWED", "该飞书接口不支持此方法。", trace)
		return
	}
	if r.URL.RawQuery != "" {
		writeFeishuError(w, store.ErrInvalidInput, trace)
		return
	}
	if h.inbox == nil || h.feishu == nil {
		writeError(w, 503, "STORAGE_DISABLED", "请先启用本地加密消息库。", trace)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := lockFeishu(ctx, &h.aiMu, false); err != nil {
		writeFeishuError(w, err, trace)
		return
	}
	defer h.aiMu.RUnlock()
	if !h.assistantDispatchAllowed(w, r, trace) {
		return
	}
	switch resource {
	case "status":
		if err := lockFeishu(ctx, &h.feishuDispatchMu, false); err != nil {
			writeFeishuError(w, err, trace)
			return
		}
		defer h.feishuDispatchMu.RUnlock()
		status, err := h.exposeFeishuStatus(ctx)
		if err != nil {
			writeFeishuError(w, err, trace)
			return
		}
		writeJSON(w, 200, status)
	case "config":
		h.serveFeishuConfig(w, r, trace, ctx)
	case "schedule":
		if r.Method == http.MethodGet {
			schedule, err := h.inbox.LoadFeishuSchedule(ctx)
			if err != nil {
				writeFeishuError(w, err, trace)
				return
			}
			writeJSON(w, 200, schedule)
			return
		}
		var input struct {
			Enabled          bool  `json:"enabled"`
			ExpectedRevision int64 `json:"expected_revision"`
			Confirmed        bool  `json:"confirmed"`
			store.FeishuBinding
		}
		fields, ok := readFeishuJSON(w, r, &input, trace, []string{"enabled", "expected_revision", "confirmed"}, feishuBindingFields...)
		if !ok {
			return
		}
		if !input.Confirmed {
			writeError(w, 400, "FEISHU_CONFIRMATION_REQUIRED", "请独立确认固定时段、模型、本人接收目标及摘要外发风险。", trace)
			return
		}
		for _, field := range feishuBindingFields {
			_, present := fields[field]
			// Disable is an unbound revocation: even zero-valued binding keys
			// are forbidden, rather than silently accepted and normalized.
			if present != input.Enabled {
				writeFeishuError(w, store.ErrInvalidInput, trace)
				return
			}
		}
		if input.Enabled {
			if err := h.validateFeishuBinding(ctx, input.FeishuBinding, true); err != nil {
				writeFeishuError(w, err, trace)
				return
			}
		}
		// Revoking a schedule cancels a running scheduled generation/send before the
		// writer barrier acknowledges. It does not claim to recall accepted messages.
		h.feishu.cancelOperations()
		if err := lockFeishu(ctx, &h.feishuDispatchMu, true); err != nil {
			writeFeishuError(w, err, trace)
			return
		}
		defer h.feishuDispatchMu.Unlock()
		// Claimants register under a dispatch read lease and may have missed the
		// first cancellation snapshot. No new claimant can enter this writer lease.
		h.feishu.cancelOperations()
		schedule, err := h.inbox.SaveFeishuSchedule(ctx, store.FeishuSchedule{Enabled: input.Enabled, FeishuBinding: input.FeishuBinding}, input.ExpectedRevision, h.feishu.now(), assistantOwner(r))
		if err != nil {
			writeFeishuError(w, err, trace)
			return
		}
		writeJSON(w, 200, schedule)
	case "preview":
		var input struct {
			Confirmed bool `json:"confirmed"`
			store.FeishuBinding
		}
		required := append([]string{"confirmed"}, feishuBindingFields...)
		if _, ok := readFeishuJSON(w, r, &input, trace, required); !ok {
			return
		}
		if !input.Confirmed {
			writeError(w, 400, "FEISHU_CONFIRMATION_REQUIRED", "请确认将有界、未暂停会话消息交给当前模型生成本次摘要。", trace)
			return
		}
		if err := h.validateFeishuBinding(ctx, input.FeishuBinding, true); err != nil {
			writeFeishuError(w, err, trace)
			return
		}
		report, claimed, err := h.startFeishuPreview(ctx, input.FeishuBinding, nil, r.Header.Get("Authorization"))
		if err != nil {
			writeFeishuError(w, err, trace)
			return
		}
		// Recheck and keep the read lease through serialization: a pause/config
		// acknowledgement must not be followed by exposing stale derived text.
		if err := lockFeishu(ctx, &h.feishuDispatchMu, false); err != nil {
			writeFeishuError(w, err, trace)
			return
		}
		defer h.feishuDispatchMu.RUnlock()
		if err := h.validateFeishuBinding(ctx, report.FeishuBinding, false); err != nil {
			writeFeishuError(w, err, trace)
			return
		}
		if err := h.inbox.CheckFeishuSourceRefs(ctx, report.SourceRefs); err != nil {
			writeFeishuError(w, err, trace)
			return
		}
		status := 200
		if claimed {
			status = 202
		}
		writeJSON(w, status, struct {
			Report *store.FeishuReport `json:"report"`
		}{report})
	case "reports/latest", "report":
		if err := lockFeishu(ctx, &h.feishuDispatchMu, false); err != nil {
			writeFeishuError(w, err, trace)
			return
		}
		defer h.feishuDispatchMu.RUnlock()
		report, err := h.readFeishuReportUnderLease(ctx, id, resource == "reports/latest")
		if err != nil {
			writeFeishuError(w, err, trace)
			return
		}
		if report == nil && resource == "report" {
			writeFeishuError(w, store.ErrNotFound, trace)
			return
		}
		writeJSON(w, 200, struct {
			Report *store.FeishuReport `json:"report"`
		}{report})
	case "send":
		var input struct {
			Confirmed   bool   `json:"confirmed"`
			ContentHash string `json:"content_hash"`
			store.FeishuBinding
		}
		required := append([]string{"confirmed", "content_hash"}, feishuBindingFields...)
		if _, ok := readFeishuJSON(w, r, &input, trace, required); !ok {
			return
		}
		if !input.Confirmed {
			writeError(w, 400, "FEISHU_CONFIRMATION_REQUIRED", "请核对完整冻结正文、本人目标与本次发送风险，再独立确认。", trace)
			return
		}
		delivery, claimed, err := h.startFeishuSend(ctx, id, input.ContentHash, input.FeishuBinding, false)
		if err != nil {
			writeFeishuError(w, err, trace)
			return
		}
		status := 200
		if claimed {
			status = 202
		}
		writeJSON(w, status, struct {
			Delivery *store.FeishuDelivery `json:"delivery"`
		}{delivery})
	case "delivery":
		delivery, err := h.inbox.FeishuDeliveryByID(ctx, id)
		if err != nil {
			writeFeishuError(w, err, trace)
			return
		}
		if delivery == nil {
			writeFeishuError(w, store.ErrNotFound, trace)
			return
		}
		writeJSON(w, 200, struct {
			Delivery *store.FeishuDelivery `json:"delivery"`
		}{delivery})
	}
}
func (h *handler) serveFeishuConfig(w http.ResponseWriter, r *http.Request, trace string, ctx context.Context) {
	// These paths deliberately have no executable assistant schema. Even a forged
	// recursive dispatch cannot mutate credentials using an Agent confirmation.
	if r.Context().Value(assistantDispatchKey{}) != nil {
		writeError(w, 403, "ASSISTANT_HUMAN_ONLY", "飞书凭据只能在专用设置表单由本人保存或清除。", trace)
		return
	}
	var input struct {
		ExpectedRevision int64  `json:"expected_revision"`
		AppID            string `json:"app_id"`
		AppSecret        string `json:"app_secret"`
		RetainSecret     bool   `json:"retain_secret"`
		RecipientOpenID  string `json:"recipient_open_id"`
		Confirmed        bool   `json:"confirmed"`
	}
	required := []string{"expected_revision", "confirmed"}
	if r.Method == http.MethodPatch {
		required = []string{"expected_revision", "app_id", "app_secret", "retain_secret", "recipient_open_id", "confirmed"}
	}
	if _, ok := readFeishuJSON(w, r, &input, trace, required); !ok {
		return
	}
	if !input.Confirmed {
		writeError(w, 400, "FEISHU_CONFIRMATION_REQUIRED", "请本人在专用表单核对凭据与接收者，明确确认保存或清除。", trace)
		return
	}
	h.feishu.cancelOperations()
	if err := lockFeishu(ctx, &h.feishuDispatchMu, true); err != nil {
		writeFeishuError(w, err, trace)
		return
	}
	defer h.feishuDispatchMu.Unlock()
	var err error
	if r.Method == http.MethodDelete {
		err = h.inbox.DeleteFeishuConfig(ctx, input.ExpectedRevision)
	} else {
		_, err = h.inbox.SaveFeishuConfig(ctx, store.FeishuConfig{AppID: input.AppID, AppSecret: input.AppSecret, RecipientOpenID: input.RecipientOpenID}, input.ExpectedRevision, input.RetainSecret)
	}
	if err != nil {
		writeFeishuError(w, err, trace)
		return
	}
	status, err := h.exposeFeishuStatus(ctx)
	if err != nil {
		writeFeishuError(w, err, trace)
		return
	}
	writeJSON(w, 200, status)
}
func feishuError(err error) (status int, code, message string) {
	switch {
	case errors.Is(err, store.ErrInvalidInput):
		return 400, "FEISHU_INVALID_INPUT", "参数无效；请核对当前版本、完整必填字段和固定窗口。"
	case errors.Is(err, store.ErrNotFound):
		return 404, "FEISHU_NOT_FOUND", "未找到摘要或发送回执。"
	case errors.Is(err, store.ErrFeishuSourceChanged):
		return 409, "FEISHU_SOURCE_CHANGED", "来源会话已暂停、被删除或隐私设置版本已变；旧摘要不可再次读取或外发。"
	case errors.Is(err, store.ErrFeishuConfigChanged):
		return 409, "FEISHU_CONFIG_CHANGED", "飞书配置或本人接收目标已变化；请重新核对授权。"
	case errors.Is(err, store.ErrFeishuExpired):
		return 409, "FEISHU_EXPIRED", "本次固定窗口摘要已过期；不会补发，请等待下个时段。"
	case errors.Is(err, store.ErrStaleContext):
		return 409, "FEISHU_STALE", "版本、正文哈希或授权状态已变化；请只读取状态核实，不要盲目重放。"
	case errors.Is(err, errFeishuModelChanged):
		return 409, "FEISHU_MODEL_CHANGED", "模型或其配置版本已变化；需要重新独立确认。"
	case errors.Is(err, errFeishuBusy):
		return 409, "FEISHU_BUSY", "共享AI通道正在使用；未占用本窗口，也未扣减额度。"
	case errors.Is(err, store.ErrQuotaExceeded):
		return 429, "AI_QUOTA_EXCEEDED", "今日共享AI额度已用尽；本窗口不会自动重算。"
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		return 503, "FEISHU_CANCELLED", "操作已中断；受理后只读取报告或回执，禁止自动重放。"
	}
	var modelErr *agent.Error
	if errors.As(err, &modelErr) {
		if modelErr.Kind == agent.NotConfigured {
			return 503, "AI_NOT_CONFIGURED", "请先由本人配置模型。"
		}
		return 503, "FEISHU_MODEL_FAILED", "摘要模型调用未完成或输出无效；不会自动重算。"
	}
	var platformErr *feishu.Error
	if errors.As(err, &platformErr) {
		return 503, platformErr.Error(), "平台未确认成功；请查看持久回执，不要自动重发。"
	}
	return 503, "FEISHU_UNAVAILABLE", "飞书服务或加密存储暂不可用；受理后的操作请只读取状态核实。"
}
func writeFeishuError(w http.ResponseWriter, err error, trace string) {
	status, code, message := feishuError(err)
	writeError(w, status, code, message, trace)
}

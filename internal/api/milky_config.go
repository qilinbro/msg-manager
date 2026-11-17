package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qilinbro/msg-manager/internal/connector/adapter"
	"github.com/qilinbro/msg-manager/internal/connector/qqmilky"
	"github.com/qilinbro/msg-manager/internal/store"
)

const milkyConfigPath = "/api/v1/connections/qq-milky/config"

type milkyConfigResponse struct {
	URL             string `json:"url"`
	AccountID       string `json:"account_id"`
	HistoryLimit    int    `json:"history_limit"`
	TokenConfigured bool   `json:"token_configured"`
	Revision        int64  `json:"revision"`
	Configured      bool   `json:"configured"`
	SendingEnabled  bool   `json:"sending_enabled"`
	Source          string `json:"source"`
}

type milkyConfigInput struct {
	URL              *string `json:"url"`
	AccountID        *string `json:"account_id"`
	AccessToken      *string `json:"access_token"`
	HistoryLimit     *int    `json:"history_limit"`
	ExpectedRevision *int64  `json:"expected_revision"`
	Confirmed        *bool   `json:"confirmed,omitempty"`
}

func milkyConfigRoute(path string) bool {
	return path == milkyConfigPath || path == milkyConfigPath+"/test"
}
func milkyConfigMutation(method, path string) bool {
	return method == http.MethodPatch && path == milkyConfigPath || method == http.MethodPost && path == milkyConfigPath+"/test"
}

func canonicalMilkyURL(value string) string {
	parsed, _ := url.Parse(value) // Only called after qqmilky.NewClient validation.
	host := parsed.Hostname()
	if host == "localhost" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, parsed.Port())
}

func milkyStoreConfig(cfg qqmilky.Config) store.QQMilkyConfig {
	limit := cfg.HistoryLimit
	if limit == 0 {
		limit = qqmilky.DefaultHistoryLimit
	}
	return store.QQMilkyConfig{URL: canonicalMilkyURL(cfg.URL), AccountID: cfg.ExpectedSelfID, AccessToken: cfg.AccessToken, HistoryLimit: limit}
}

func milkyClientConfig(cfg store.QQMilkyConfig) qqmilky.Config {
	return qqmilky.Config{Endpoint: adapter.Endpoint{URL: cfg.URL, ExpectedSelfID: cfg.AccountID, AccessToken: cfg.AccessToken}, HistoryLimit: cfg.HistoryLimit}
}

func (h *handler) restoreMilkyConfig(fallback qqmilky.Config) error {
	if h.inbox != nil {
		saved, err := h.inbox.LoadQQMilkyConfig(context.Background())
		if err != nil {
			return err
		}
		if saved != nil {
			next, err := newMilkyConnection(milkyClientConfig(*saved))
			if err != nil {
				return store.ErrUnavailable
			}
			next.configuration, next.configSource = *saved, "saved"
			h.milky = next
			return nil
		}
	}
	next, err := newMilkyConnection(fallback)
	if err != nil {
		return err
	}
	h.milky = next
	return nil
}

// The caller holds configMu (read or write). No secret-valued struct is ever
// marshaled as a response, and this cache read never performs provider I/O.
func (h *handler) exposeMilkyConfig() milkyConfigResponse {
	c := h.milky
	return milkyConfigResponse{URL: c.configuration.URL, AccountID: c.configuration.AccountID,
		HistoryLimit: c.configuration.HistoryLimit, TokenConfigured: c.configuration.AccessToken != "",
		Revision: c.configuration.Revision, Configured: c.client != nil, Source: c.configSource, SendingEnabled: false}
}

func readMilkyConfigInput(w http.ResponseWriter, r *http.Request, save bool, traceID string) (milkyConfigInput, bool) {
	var input milkyConfigInput
	invalid := func() (milkyConfigInput, bool) {
		writeError(w, 400, "MILKY_CONFIG_INVALID", "请检查本机地址、本人 QQ、访问令牌与历史条数。", traceID)
		return input, false
	}
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" || params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8") {
		return invalid()
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, (16<<10)+1))
	if len(body) > 16<<10 {
		writeError(w, 413, "PAYLOAD_TOO_LARGE", "连接设置最多 16 KiB，请缩短填写内容。", traceID)
		return input, false
	}
	if err != nil || !utf8.Valid(body) || !json.Valid(body) || !uniqueJSON(body) {
		return invalid()
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields == nil {
		return invalid()
	}
	allowed := map[string]bool{"url": true, "account_id": true, "access_token": true, "history_limit": true, "expected_revision": true}
	if save {
		allowed["confirmed"] = true
	}
	if len(fields) != len(allowed) {
		return invalid()
	}
	for key, value := range fields {
		if !allowed[key] || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return invalid()
		}
	}
	if json.Unmarshal(body, &input) != nil || input.URL == nil || input.AccountID == nil || input.AccessToken == nil || input.HistoryLimit == nil || input.ExpectedRevision == nil {
		return invalid()
	}
	if *input.ExpectedRevision < 0 || *input.ExpectedRevision >= 9007199254740991 {
		return invalid()
	}
	if save && (input.Confirmed == nil || !*input.Confirmed) {
		writeError(w, 400, "MILKY_CONFIG_CONFIRMATION_REQUIRED", "请核对连接信息并明确确认保存；保存将暂停现有后台接收，须重新授权。", traceID)
		return input, false
	}
	return input, true
}

// Caller holds configMu. Empty token retains the current secret only for the
// exact same canonical destination and account; it can never be redirected.
func (h *handler) candidateMilkyConfig(input milkyConfigInput) (store.QQMilkyConfig, *qqmilky.Client, string) {
	current := h.milky.configuration
	if *input.ExpectedRevision != current.Revision {
		return store.QQMilkyConfig{}, nil, "MILKY_CONFIG_STALE"
	}
	next := store.QQMilkyConfig{URL: *input.URL, AccountID: *input.AccountID, AccessToken: *input.AccessToken, HistoryLimit: *input.HistoryLimit}
	if !validMilkyPeer(next.AccountID) || len(next.AccountID) < 2 || next.HistoryLimit < 1 || next.HistoryLimit > qqmilky.MaxHistoryLimit {
		return store.QQMilkyConfig{}, nil, "MILKY_CONFIG_INVALID"
	}
	// Validate the address separately with a non-secret placeholder. A malformed
	// or changed address never inherits the saved provider token.
	validation := next
	validation.AccessToken = "validation-placeholder"
	if _, err := qqmilky.NewClient(milkyClientConfig(validation)); err != nil {
		return store.QQMilkyConfig{}, nil, "MILKY_CONFIG_INVALID"
	}
	next.URL = canonicalMilkyURL(next.URL)
	if next.AccessToken == "" {
		if next.URL != current.URL || next.AccountID != current.AccountID || current.AccessToken == "" {
			return store.QQMilkyConfig{}, nil, "MILKY_CONFIG_TOKEN_REQUIRED"
		}
		next.AccessToken = current.AccessToken
	}
	client, err := qqmilky.NewClient(milkyClientConfig(next))
	if err != nil {
		return store.QQMilkyConfig{}, nil, "MILKY_CONFIG_INVALID"
	}
	return next, client, ""
}

func writeMilkyConfigFailure(w http.ResponseWriter, code, traceID string) {
	status, message := 400, "请检查本机 Milky 地址、本人 QQ、访问令牌和历史条数。"
	switch code {
	case "MILKY_CONFIG_STALE":
		status, message = 409, "连接信息已更新，请刷新配置后核对；当前输入已保留。"
	case "MILKY_CONFIG_TOKEN_REQUIRED":
		message = "首次连接或更换地址/QQ 时，请重新填写对应协议端的访问令牌。"
	}
	writeError(w, status, code, message, traceID)
}

func (h *handler) serveMilkyConfig(w http.ResponseWriter, r *http.Request, traceID string) {
	if r.URL.RawQuery != "" {
		writeMilkyConfigFailure(w, "MILKY_CONFIG_INVALID", traceID)
		return
	}
	c := h.milky
	if r.URL.Path == milkyConfigPath && r.Method == http.MethodGet {
		c.configMu.RLock()
		defer c.configMu.RUnlock()
		writeJSON(w, 200, h.exposeMilkyConfig())
		return
	}
	if !milkyConfigMutation(r.Method, r.URL.Path) {
		writeError(w, 405, "METHOD_NOT_ALLOWED", "请选择读取、测试或保存 Milky 连接。", traceID)
		return
	}
	save := r.Method == http.MethodPatch
	input, ok := readMilkyConfigInput(w, r, save, traceID)
	if !ok {
		return
	}
	if !save {
		c.configMu.RLock()
		defer c.configMu.RUnlock()
		_, client, failure := h.candidateMilkyConfig(input)
		if failure != "" {
			writeMilkyConfigFailure(w, failure, traceID)
			return
		}
		select {
		case c.gate <- struct{}{}:
			defer func() { <-c.gate }()
		default:
			writeError(w, 503, "MILKY_BUSY", "已有 Milky 操作正在进行，请稍后测试。", traceID)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		verification, err := client.Probe(ctx)
		if err != nil {
			state := milkyFailureState(err)
			writeError(w, 503, "MILKY_"+strings.ToUpper(state), milkyFailureMessage(state), traceID)
			return
		}
		writeJSON(w, 200, MilkyVerification{ConnectionID: "qq-milky", State: "identity_verified", AccountID: verification.AccountID,
			Nickname: verification.Nickname, ImplName: verification.ImplName, ImplVersion: verification.ImplVersion,
			MilkyVersion: verification.MilkyVersion, CheckedAt: time.Now().UTC().Format(time.RFC3339), ReceivingEnabled: false, SendingEnabled: false})
		return
	}
	if h.inbox == nil {
		writeError(w, 503, "MILKY_CONFIG_STORAGE_DISABLED", "请先启用本地加密消息库，再保存 Milky 连接。", traceID)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
	defer cancel()
	for !c.configMu.TryLock() {
		select {
		case <-ctx.Done():
			writeError(w, 503, "MILKY_CONFIG_BUSY", "Milky 仍在处理请求，请稍后保存；输入已保留。", traceID)
			return
		case <-time.After(5 * time.Millisecond):
		}
	}
	defer c.configMu.Unlock()
	next, client, failure := h.candidateMilkyConfig(input)
	if failure != "" {
		writeMilkyConfigFailure(w, failure, traceID)
		return
	}
	c.monitorMu.Lock()
	defer c.monitorMu.Unlock()
	if c.closed {
		writeError(w, 503, "MILKY_UNAVAILABLE", "后台服务正在停止，请稍后保存。", traceID)
		return
	}
	old := c.persisted
	wasRunning := c.cancel != nil
	h.stopMilkyMonitorLocked()
	saved, err := h.inbox.SaveQQMilkyConfig(ctx, next, *input.ExpectedRevision)
	if err != nil {
		if wasRunning {
			h.startMilkyMonitorLocked(old)
		}
		if errors.Is(err, store.ErrStaleContext) {
			writeMilkyConfigFailure(w, "MILKY_CONFIG_STALE", traceID)
		} else {
			writeError(w, 503, "MILKY_CONFIG_UNAVAILABLE", "连接设置未保存，请检查加密存储后重试。", traceID)
		}
		return
	}
	c.client, c.configuration, c.configSource = client, saved, "saved"
	c.persisted = store.QQMilkyMonitorSettings{PeerIDs: []string{}}
	c.mu.Lock()
	c.status = MilkyStatus{Configured: true, State: "configured", AccountID: saved.AccountID, MilkyVersion: qqmilky.DefaultMilkyVersion,
		ReadOnly: true, Gaps: []milkyGap{}, Monitor: MilkyMonitor{State: "disabled", PeerIDs: []string{}, ReadOnly: true}}
	c.mu.Unlock()
	writeJSON(w, 200, h.exposeMilkyConfig())
}

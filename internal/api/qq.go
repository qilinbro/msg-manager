package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/qilinbro/msg-manager/internal/connector"
	"github.com/qilinbro/msg-manager/internal/connector/onebot"
)

type qqProber interface {
	Probe(context.Context) (onebot.LoginInfo, error)
}

type qqConnection struct {
	client qqProber
	gate   chan struct{}
	mu     sync.RWMutex
	state  string
}

func newQQConnection(cfg onebot.Config) (*qqConnection, error) {
	q := &qqConnection{state: "not_configured", gate: make(chan struct{}, 1)}
	if cfg.URL == "" && cfg.AccessToken == "" && cfg.ExpectedSelfID == "" {
		return q, nil
	}
	client, err := onebot.NewClient(cfg)
	if err != nil {
		// Never surface provider addresses or access tokens in startup logs.
		return nil, errors.New("QQ_ONEBOT configuration is invalid; check the local URL, access token and expected account ID")
	}
	q.client = client
	q.state = "configured"
	return q, nil
}

func (h *handler) connections() []connector.Descriptor {
	items := connector.Planned()
	h.qq.mu.RLock()
	state := h.qq.state
	h.qq.mu.RUnlock()
	for i := range items {
		if items[i].ID == "qq-onebot" {
			items[i].Status = state
			if state == "identity_verified" {
				items[i].Description = "上次检测已核验 QQ 账号；这不是持续在线状态。消息入库、持续接收和发送仍未启用。"
			}
		}
	}
	h.email.mu.RLock()
	emailConfigured := h.email.status.Configured
	h.email.mu.RUnlock()
	for i := range items {
		if items[i].ID == "email-imap" && emailConfigured {
			items[i].Status = "configured"
			items[i].Capabilities.ReceiveText = h.inbox != nil
		}
		if items[i].ID == "wechat-db" {
			h.wechat.mu.RLock()
			items[i].Status = h.wechat.status.State
			items[i].Capabilities.ReceiveText = h.wechat.monitor.Enabled && h.wechat.reader != nil && h.inbox != nil
			h.wechat.mu.RUnlock()
			items[i].Capabilities.HistoryImport = h.wechat.reader != nil && h.inbox != nil
		}
	}
	return items
}

type QQVerification struct {
	ConnectionID     string `json:"connection_id"`
	State            string `json:"state"`
	AccountID        string `json:"account_id"`
	Nickname         string `json:"nickname"`
	Online           bool   `json:"online"`
	CheckedAt        string `json:"checked_at"`
	ReceivingEnabled bool   `json:"receiving_enabled"`
	SendingEnabled   bool   `json:"sending_enabled"`
}

func (h *handler) verifyQQ(w http.ResponseWriter, r *http.Request, traceID string) {
	q := h.qq
	if q.client == nil {
		writeError(w, http.StatusServiceUnavailable, "QQ_NOT_CONFIGURED", "请先在后台配置 OneBot 地址、访问令牌与预期 QQ 账号，再重启服务。", traceID)
		return
	}
	// The destination always comes from local operator configuration. Request
	// query strings and bodies never select a target or provide credentials.
	select {
	case q.gate <- struct{}{}:
		defer func() { <-q.gate }()
	default:
		writeError(w, http.StatusServiceUnavailable, "QQ_BUSY", "QQ 账号检测正在进行，请稍后重试。", traceID)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	info, err := q.client.Probe(ctx)
	state := "identity_verified"
	if err != nil {
		state = qqFailureState(err)
	}
	q.mu.Lock()
	q.state = state
	q.mu.Unlock()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "QQ_"+strings.ToUpper(state), qqFailureMessage(state), traceID)
		return
	}
	writeJSON(w, http.StatusOK, QQVerification{
		ConnectionID: "qq-onebot", State: state, AccountID: info.UserID,
		Nickname: info.Nickname, Online: info.Online,
		CheckedAt:        time.Now().UTC().Format(time.RFC3339),
		ReceivingEnabled: false, SendingEnabled: false,
	})
}

func qqFailureState(err error) string {
	// Map only typed, stable kinds, never upstream response text or addresses.
	var failure *onebot.Error
	if errors.As(err, &failure) {
		switch string(failure.Kind) {
		case "not_configured", "unreachable", "auth_failed", "identity_mismatch", "logged_out", "protocol_error":
			return string(failure.Kind)
		}
	}
	return "unreachable"
}

func qqFailureMessage(state string) string {
	switch state {
	case "auth_failed":
		return "OneBot 认证失败，请检查服务端访问令牌。"
	case "identity_mismatch":
		return "OneBot 登录账号与配置的预期 QQ 不一致，未通过核验。"
	case "logged_out":
		return "OneBot 服务可访问，但 QQ 尚未登录或未就绪。"
	case "protocol_error":
		return "OneBot 返回格式或协议不匹配，请确认使用 v11 接口。"
	default:
		return "暂时无法核验 QQ，请检查本机 OneBot 地址与服务状态。"
	}
}

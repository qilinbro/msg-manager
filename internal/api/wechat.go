package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/qilinbro/msg-manager/internal/connector/wechatdb"
	"github.com/qilinbro/msg-manager/internal/store"
)

type wechatReader interface {
	Probe(context.Context) (wechatdb.Info, error)
	Sessions(context.Context) ([]wechatdb.Session, error)
	Read(context.Context, string) (wechatdb.Batch, error)
}

type wechatSessionsReader interface {
	SessionsWithInfo(context.Context) (wechatdb.Info, []wechatdb.Session, error)
}

type wechatConnection struct {
	reader        wechatReader
	gate          chan struct{}
	mu            sync.RWMutex
	status        WechatStatus
	accountID     string
	monitorMu     sync.Mutex
	monitor       WechatMonitorStatus
	cancelMonitor context.CancelFunc
	monitorDone   chan struct{}
	closed        bool
	monitorWait   func(context.Context, time.Duration) bool
}

type WechatReadResult struct {
	store.ImportResult
	Skipped        int    `json:"skipped"`
	Limited        bool   `json:"limited"`
	ReadAt         string `json:"read_at"`
	ReadOnly       bool   `json:"read_only"`
	SendingEnabled bool   `json:"sending_enabled"`
}

type WechatStatus struct {
	Configured     bool                `json:"configured"`
	State          string              `json:"state"`
	AccountID      *string             `json:"account_id"`
	ReadOnly       bool                `json:"read_only"`
	LastReadAt     *string             `json:"last_read_at"`
	LastResult     *WechatReadResult   `json:"last_result"`
	SendingEnabled bool                `json:"sending_enabled"`
	Monitor        WechatMonitorStatus `json:"monitor"`
}

func newWechatConnection(cfg wechatdb.Config) (*wechatConnection, error) {
	c := &wechatConnection{gate: make(chan struct{}, 1), status: WechatStatus{State: "not_configured", ReadOnly: true}, monitor: defaultWechatMonitor()}
	if cfg.Directory == "" && cfg.AccountID == "" && cfg.Key == "" && cfg.Limit == 0 {
		return c, nil
	}
	reader, err := wechatdb.NewReader(cfg)
	if err != nil {
		return nil, errors.New("WECHAT_DB configuration is invalid; configure the local account directory, account ID and database key")
	}
	c.reader, c.accountID = reader, cfg.AccountID
	account := cfg.AccountID
	c.status = WechatStatus{Configured: true, State: "ready", AccountID: &account, ReadOnly: true}
	return c, nil
}

// Status reads cached metadata only. Merely opening Settings never reads chats.
func (h *handler) wechatStatus(w http.ResponseWriter) {
	h.wechat.mu.RLock()
	status := h.wechat.status
	status.Monitor = h.wechat.monitorSnapshotLocked()
	h.wechat.mu.RUnlock()
	writeJSON(w, http.StatusOK, status)
}

func (h *handler) beginWechat(w http.ResponseWriter, traceID string) bool {
	c := h.wechat
	if c.reader == nil {
		writeError(w, 503, "WECHAT_NOT_CONFIGURED", "请先按本机接入说明配置微信数据库；密钥只在后台保存。", traceID)
		return false
	}
	select {
	case c.gate <- struct{}{}:
		c.mu.Lock()
		c.status.State = "reading"
		c.mu.Unlock()
		return true
	default:
		writeError(w, 503, "WECHAT_BUSY", "微信数据库正在读取，请稍后更新状态。", traceID)
		return false
	}
}

func (h *handler) failWechat(w http.ResponseWriter, err error, traceID string) {
	state, message := "unavailable", "微信数据库暂时无法读取，请检查本机配置和文件访问权限。"
	code := ""
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		code, message = "WECHAT_TIMEOUT", "微信数据库读取超时；请稍后重试。本次读取没有完成，不表示密钥或文件权限错误。"
	case errors.Is(err, wechatdb.ErrInvalidKey):
		state, message = "invalid_key", "微信数据库密钥或加密格式不匹配；本轮未保存。"
	case errors.Is(err, wechatdb.ErrUnsupportedSchema):
		state, message = "unsupported_schema", "当前微信数据库结构或消息格式尚未支持；本轮未保存。"
	case errors.Is(err, wechatdb.ErrNotFound):
		state, message = "not_found", "未找到所选私聊，请重新读取会话列表。"
	case errors.Is(err, wechatdb.ErrChanged):
		state, message = "changed", "读取期间数据库发生变化；本轮未保存，请稍后重试。"
	case errors.Is(err, wechatdb.ErrInvalidConfig):
		state, message = "unavailable", "微信本机配置不完整或路径不符合要求。"
	}
	h.wechat.mu.Lock()
	h.wechat.status.State = state
	h.wechat.mu.Unlock()
	if code == "" {
		code = "WECHAT_" + strings.ToUpper(state)
	}
	writeError(w, 503, code, message, traceID)
}

// A real reader can return identity and sessions from one validated operation.
// The fallback keeps older adapters subject to the original identity checks.
func (h *handler) validatedWechatSessions(ctx context.Context) ([]wechatdb.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var info wechatdb.Info
	var items []wechatdb.Session
	var err error
	if reader, ok := h.wechat.reader.(wechatSessionsReader); ok {
		info, items, err = reader.SessionsWithInfo(ctx)
	} else {
		info, err = h.wechat.reader.Probe(ctx)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err == nil && (info.AccountID != h.wechat.accountID || !info.ReadOnly) {
			err = wechatdb.ErrUnsupportedSchema
		}
		if err != nil {
			return nil, err
		}
		items, err = h.wechat.reader.Sessions(ctx)
	}
	// Source adapters may collapse a row-iteration timeout to unavailable.
	// Preserve the context's precise reason before classifying source errors.
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	if info.AccountID != h.wechat.accountID || !info.ReadOnly {
		return nil, wechatdb.ErrUnsupportedSchema
	}
	return items, nil
}

func (h *handler) wechatSessions(w http.ResponseWriter, r *http.Request, traceID string) {
	if r.URL.RawQuery != "" {
		writeError(w, 400, "INVALID_INPUT", "会话读取不接受路径、密钥或账号参数。", traceID)
		return
	}
	if !h.beginWechat(w, traceID) {
		return
	}
	defer func() { <-h.wechat.gate }()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	items, err := h.validatedWechatSessions(ctx)
	if err != nil {
		h.failWechat(w, err, traceID)
		return
	}
	if ctx.Err() != nil {
		h.failWechat(w, ctx.Err(), traceID)
		return
	}
	if items == nil {
		items = []wechatdb.Session{}
	}
	h.wechat.mu.Lock()
	h.wechat.status.State = "ready"
	h.wechat.mu.Unlock()
	writeJSON(w, 200, struct {
		Items          []wechatdb.Session `json:"items"`
		ReadOnly       bool               `json:"read_only"`
		SendingEnabled bool               `json:"sending_enabled"`
	}{items, true, false})
}

func (h *handler) readWechat(w http.ResponseWriter, r *http.Request, traceID string) {
	var input struct {
		PeerID string `json:"peer_id"`
	}
	if !readInboxJSON(w, r, &input, traceID) {
		return
	}
	if r.URL.RawQuery != "" || input.PeerID == "" || len(input.PeerID) > 128 || strings.TrimSpace(input.PeerID) != input.PeerID || strings.ContainsAny(input.PeerID, "\x00\r\n/\\") {
		writeError(w, 400, "INVALID_INPUT", "请选择本机已配置账号中的一个私聊。", traceID)
		return
	}
	if h.inbox == nil {
		writeError(w, 503, "STORAGE_DISABLED", "请先启用本产品的加密消息存储。", traceID)
		return
	}
	if !h.beginWechat(w, traceID) {
		return
	}
	defer func() { <-h.wechat.gate }()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	response, err := h.readWechatPeer(ctx, input.PeerID)
	if err != nil {
		var importFailure wechatImportFailure
		if errors.As(err, &importFailure) {
			h.wechat.mu.Lock()
			h.wechat.status.State = "unavailable"
			h.wechat.mu.Unlock()
			writeInboxError(w, importFailure.err, traceID)
		} else {
			h.failWechat(w, err, traceID)
		}
		return
	}
	writeJSON(w, 200, response)
}

type wechatImportFailure struct{ err error }

func (e wechatImportFailure) Error() string { return "WeChat import failed" }
func (e wechatImportFailure) Unwrap() error { return e.err }

// Caller owns the connector gate. Both foreground and background reads use the
// same account checks and context-aware atomic import.
func (h *handler) readWechatPeer(ctx context.Context, peerID string) (WechatReadResult, error) {
	if err := ctx.Err(); err != nil {
		return WechatReadResult{}, err
	}
	batch, err := h.wechat.reader.Read(ctx, peerID)
	if ctx.Err() != nil {
		return WechatReadResult{}, ctx.Err()
	}
	if err == nil && (batch.Info.AccountID != h.wechat.accountID || !batch.Info.ReadOnly || batch.PeerID != peerID || len(batch.Messages) > 500) {
		err = wechatdb.ErrUnsupportedSchema
	}
	if err != nil {
		return WechatReadResult{}, err
	}
	if ctx.Err() != nil {
		return WechatReadResult{}, ctx.Err()
	}
	req := store.ImportRequest{Platform: "weixin", AccountID: h.wechat.accountID, AccountName: h.wechat.accountID}
	for _, item := range batch.Messages {
		req.Messages = append(req.Messages, store.ImportMessage{ExternalID: item.ExternalID, PeerID: batch.PeerID, PeerName: mailLabel(batch.PeerName, 128), Direction: item.Direction, Body: item.Body, OccurredAt: item.OccurredAt})
	}
	result := store.ImportResult{}
	if len(req.Messages) > 0 {
		result, err = h.inbox.ImportWeChatContext(ctx, req)
		if err != nil {
			if ctx.Err() != nil {
				return WechatReadResult{}, ctx.Err()
			}
			return WechatReadResult{}, wechatImportFailure{err}
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	response := WechatReadResult{ImportResult: result, Skipped: batch.Skipped, Limited: batch.Limited, ReadAt: now, ReadOnly: true}
	h.wechat.mu.Lock()
	h.wechat.status.State, h.wechat.status.LastReadAt, h.wechat.status.LastResult = "read", &now, &response
	h.wechat.mu.Unlock()
	return response, nil
}

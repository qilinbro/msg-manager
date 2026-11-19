package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/qilinbro/msg-manager/internal/connector"
	"github.com/qilinbro/msg-manager/internal/connector/nonebotbridge"
	"github.com/qilinbro/msg-manager/internal/connector/telegrambot"
	"github.com/qilinbro/msg-manager/internal/domain"
	"github.com/qilinbro/msg-manager/internal/store"
)

const telegramBotPrefix = "/api/v1/connections/telegram-bot"

var (
	errTelegramBotBusy                 = errors.New("telegram manual operation busy")
	errTelegramBotClosed               = errors.New("telegram manual connector closed")
	errTelegramBotNotConfigured        = errors.New("telegram bot not configured")
	errTelegramBotRuntimeNotConfigured = errors.New("telegram runtime not configured")
	errTelegramBotVerificationRequired = errors.New("telegram fresh manual verification required")
)

type TelegramBotStatus struct {
	ConnectionID            string                         `json:"connection_id"`
	RuntimeConfigured       bool                           `json:"runtime_configured"`
	Configured              bool                           `json:"configured"`
	IdentityVerified        bool                           `json:"identity_verified"`
	AllowedPrivateChatCount int                            `json:"allowed_private_chat_count"`
	State                   string                         `json:"state"`
	CheckedAt               string                         `json:"checked_at"`
	LastSyncAt              string                         `json:"last_sync_at"`
	ManualReceiveOnly       bool                           `json:"manual_receive_only"`
	BackgroundReceiving     bool                           `json:"background_receiving"`
	SendingEnabled          bool                           `json:"sending_enabled"`
	HistoryImport           bool                           `json:"history_import"`
	LastResult              *store.TelegramBotCommitResult `json:"last_result"`
}

type telegramBotOperation struct {
	ctx      context.Context
	cancel   context.CancelFunc
	stopRoot func() bool
	done     chan struct{}
	epoch    uint64
}

type telegramConnection struct {
	client     *telegrambot.Client
	gate       chan struct{}
	root       context.Context
	rootCancel context.CancelFunc
	mu         sync.RWMutex
	status     TelegramBotStatus
	epoch      uint64
	closed     bool
	// Cancel is separately accessible while mu fences a store transaction. A
	// close or reverify can cancel a store mutex waiter, not wait behind it.
	opMu   sync.Mutex
	active *telegramBotOperation
}

func newTelegramConnection(cfg telegrambot.Config, caller nonebotbridge.Caller) (*telegramConnection, error) {
	client, err := telegrambot.New(cfg, caller)
	if err != nil {
		return nil, err
	}
	root, cancel := context.WithCancel(context.Background())
	c := &telegramConnection{client: client, gate: make(chan struct{}, 1), root: root, rootCancel: cancel}
	c.status = TelegramBotStatus{ConnectionID: "telegram-bot", Configured: client.Configured(),
		RuntimeConfigured: client.RuntimeConfigured(), AllowedPrivateChatCount: len(client.AllowedPrivateChatIDs()),
		ManualReceiveOnly: true, State: "not_configured"}
	if c.status.Configured {
		c.status.State = "configured"
		if !c.status.RuntimeConfigured {
			c.status.State = "runtime_not_configured"
		}
	}
	return c, nil
}

func telegramBotPath(path string) bool {
	return path == telegramBotPrefix || strings.HasPrefix(path, telegramBotPrefix+"/")
}
func telegramBotMutation(method, path string) bool {
	return method == http.MethodPost && (path == telegramBotPrefix+"/verify" || path == telegramBotPrefix+"/sync")
}

func (c *telegramConnection) snapshot() TelegramBotStatus {
	if c == nil {
		return TelegramBotStatus{ConnectionID: "telegram-bot", State: "not_configured", ManualReceiveOnly: true}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	status := c.status
	if status.LastResult != nil {
		result := *status.LastResult
		status.LastResult = &result
	}
	return status
}

func (c *telegramConnection) cancelActive() {
	if c == nil {
		return
	}
	c.opMu.Lock()
	defer c.opMu.Unlock()
	if c.active != nil {
		c.active.cancel()
	}
}

func (c *telegramConnection) begin(ctx context.Context, verify bool) (*telegramBotOperation, error) {
	if c == nil {
		return nil, errTelegramBotNotConfigured
	}
	// An explicit reverify revokes any old candidate even if it receives BUSY.
	// Cancellation occurs outside mu so a blocked SQL transaction is interruptible.
	if verify {
		c.cancelActive()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.root.Err() != nil {
		return nil, errTelegramBotClosed
	}
	if !c.status.Configured {
		return nil, errTelegramBotNotConfigured
	}
	if !c.status.RuntimeConfigured {
		return nil, errTelegramBotRuntimeNotConfigured
	}
	if verify {
		c.epoch++
		c.status.IdentityVerified = false
	}
	select {
	case c.gate <- struct{}{}:
	default:
		if verify {
			// Cover a new operation registered between the optimistic cancel
			// above and acquiring the lifecycle fence.
			c.cancelActive()
			c.status.State = "canceled"
		}
		return nil, errTelegramBotBusy
	}
	if !verify && !c.status.IdentityVerified {
		<-c.gate
		return nil, errTelegramBotVerificationRequired
	}
	operationCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	op := &telegramBotOperation{ctx: operationCtx, cancel: cancel, done: make(chan struct{}), epoch: c.epoch}
	op.stopRoot = context.AfterFunc(c.root, cancel)
	c.opMu.Lock()
	c.active = op
	c.opMu.Unlock()
	c.status.State = "receiving"
	if verify {
		c.status.State = "verifying"
	}
	return op, nil
}

func (c *telegramConnection) finish(op *telegramBotOperation) {
	op.cancel()
	op.stopRoot()
	c.opMu.Lock()
	if c.active == op {
		c.active = nil
	}
	close(op.done)
	c.opMu.Unlock()
	<-c.gate
}

func (c *telegramConnection) currentLocked(op *telegramBotOperation) bool {
	return !c.closed && c.epoch == op.epoch && c.root.Err() == nil && op.ctx.Err() == nil
}

func telegramBotFailureState(err error) string {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "canceled"
	}
	if errors.Is(err, store.ErrInvalidInput) {
		return "protocol_error"
	}
	switch kind := nonebotbridge.KindOf(err); kind {
	case nonebotbridge.AuthFailed, nonebotbridge.RateLimited, nonebotbridge.Unreachable,
		nonebotbridge.ProtocolError, nonebotbridge.IdentityMismatch, nonebotbridge.WebhookPresent,
		nonebotbridge.Canceled, nonebotbridge.PermissionDenied, nonebotbridge.PlatformRejected,
		nonebotbridge.Unavailable, nonebotbridge.InvalidConfig:
		return string(kind)
	default:
		return "unavailable"
	}
}

func (c *telegramConnection) failed(op *telegramBotOperation, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.epoch != op.epoch {
		return
	}
	c.status.IdentityVerified = false
	c.status.State = telegramBotFailureState(err)
}

func (h *handler) closeTelegramBot() {
	c := h.telegram
	if c == nil {
		return
	}
	// Cancel first: mu may be held across the final atomic store transaction.
	c.rootCancel()
	c.mu.Lock()
	c.closed = true
	c.epoch++
	c.status.IdentityVerified = false
	c.status.State = "closed"
	c.mu.Unlock()
	c.opMu.Lock()
	var done <-chan struct{}
	if c.active != nil {
		c.active.cancel()
		done = c.active.done
	}
	c.opMu.Unlock()
	if done != nil {
		<-done
	}
}

func (h *handler) serveTelegramBot(w http.ResponseWriter, r *http.Request, trace string) {
	// Repeat the scope boundary: neither a client-supplied identity nor an
	// internal call lacking the authenticated local principal may select a bot.
	if authenticatedWorkspace(r.Context()) != domain.LocalWorkspaceID || !validScopeClaims(r, domain.LocalWorkspaceID) {
		writeError(w, http.StatusForbidden, "FORBIDDEN_SCOPE", "不能访问其他工作区的 Telegram 接收配置。", trace)
		return
	}
	if r.Context().Value(assistantDispatchKey{}) != nil {
		writeError(w, http.StatusForbidden, "TELEGRAM_BOT_HUMAN_REQUIRED", "Telegram 机器人仅接受人工操作，不开放模型工具调用。", trace)
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeTelegramBotInvalid(w, trace)
		return
	}
	for key := range query {
		if key != "workspace_id" {
			writeTelegramBotInvalid(w, trace)
			return
		}
	}
	resource := strings.TrimPrefix(r.URL.Path, telegramBotPrefix+"/")
	if resource != "status" && resource != "verify" && resource != "sync" {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "接口不存在。", trace)
		return
	}
	method := http.MethodPost
	if resource == "status" {
		method = http.MethodGet
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "此接口不支持该请求方法。", trace)
		return
	}
	if resource == "status" {
		writeJSON(w, http.StatusOK, h.telegram.snapshot())
		return
	}
	if !readTelegramBotConsent(w, r, resource == "sync", trace) {
		return
	}
	if resource == "verify" {
		h.verifyTelegramBot(w, r, trace)
		return
	}
	h.syncTelegramBot(w, r, trace)
}

func writeTelegramBotInvalid(w http.ResponseWriter, trace string) {
	writeError(w, http.StatusBadRequest, "TELEGRAM_BOT_INVALID_REQUEST", "请仅提交本次操作要求的明确授权字段。", trace)
}

func readTelegramBotConsent(w http.ResponseWriter, r *http.Request, receive bool, trace string) bool {
	contentType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" || params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8") {
		writeTelegramBotInvalid(w, trace)
		return false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1025))
	if err != nil {
		writeTelegramBotInvalid(w, trace)
		return false
	}
	if len(body) > 1024 {
		writeError(w, http.StatusRequestEntityTooLarge, "TELEGRAM_BOT_PAYLOAD_TOO_LARGE", "操作授权请求过大。", trace)
		return false
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' || !utf8.Valid(body) || !uniqueJSON(body) {
		writeTelegramBotInvalid(w, trace)
		return false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields == nil {
		writeTelegramBotInvalid(w, trace)
		return false
	}
	if !receive && len(fields) == 0 {
		return true
	}
	if receive && len(fields) == 1 && bytes.Equal(bytes.TrimSpace(fields["consent_receive"]), []byte("true")) {
		return true
	}
	writeTelegramBotInvalid(w, trace)
	return false
}

func (h *handler) verifyTelegramBot(w http.ResponseWriter, r *http.Request, trace string) {
	c := h.telegram
	op, err := c.begin(r.Context(), true)
	if err != nil {
		writeTelegramBotError(w, err, trace)
		return
	}
	defer c.finish(op)
	_, err = c.client.Probe(op.ctx)
	if err != nil {
		c.failed(op, err)
		writeTelegramBotError(w, err, trace)
		return
	}
	c.mu.Lock()
	if !c.currentLocked(op) {
		c.mu.Unlock()
		c.failed(op, context.Canceled)
		writeTelegramBotError(w, context.Canceled, trace)
		return
	}
	c.status.IdentityVerified = true
	c.status.State = "identity_verified"
	c.status.CheckedAt = time.Now().UTC().Format(time.RFC3339Nano)
	c.mu.Unlock()
	writeJSON(w, http.StatusOK, c.snapshot())
}

func (h *handler) syncTelegramBot(w http.ResponseWriter, r *http.Request, trace string) {
	if h.inbox == nil {
		writeError(w, http.StatusServiceUnavailable, "STORAGE_DISABLED", "请先在本地服务中配置加密消息存储。", trace)
		return
	}
	c := h.telegram
	op, err := c.begin(r.Context(), false)
	if err != nil {
		writeTelegramBotError(w, err, trace)
		return
	}
	defer c.finish(op)
	cursor, err := h.inbox.LoadTelegramBotCursor(op.ctx, c.client.BotID())
	if err != nil {
		c.failed(op, err)
		writeTelegramBotError(w, err, trace)
		return
	}
	// A stale acknowledgement offset cannot cross Telegram's idle-ID reset.
	// The store owns the lease/epoch; this remains exactly one consented page.
	requestOffset := cursor.PollOffset(time.Now())
	page, err := c.client.Poll(op.ctx, requestOffset)
	if err != nil {
		c.failed(op, err)
		writeTelegramBotError(w, err, trace)
		return
	}
	// The lifecycle fence and store cursor CAS cover the entire final commit.
	// Root/reverify cancellation remains accessible without acquiring this mu.
	c.mu.Lock()
	if !c.currentLocked(op) {
		c.mu.Unlock()
		c.failed(op, context.Canceled)
		writeTelegramBotError(w, context.Canceled, trace)
		return
	}
	result, err := h.inbox.CommitTelegramBot(op.ctx, c.client.BotID(), c.client.AllowedPrivateChatIDs(), cursor, requestOffset, page)
	if err != nil {
		c.status.IdentityVerified = false
		c.status.State = telegramBotFailureState(err)
		c.mu.Unlock()
		writeTelegramBotError(w, err, trace)
		return
	}
	c.status.IdentityVerified = true
	c.status.State = "identity_verified"
	c.status.CheckedAt = time.Now().UTC().Format(time.RFC3339Nano)
	c.status.LastSyncAt = c.status.CheckedAt
	c.status.LastResult = &result
	c.mu.Unlock()
	writeJSON(w, http.StatusOK, result)
}

func writeTelegramBotError(w http.ResponseWriter, err error, trace string) {
	status, code, message := http.StatusServiceUnavailable, "TELEGRAM_BOT_STORE_UNAVAILABLE", "暂时无法访问加密消息存储，本页未提交，请稍后手动重试。"
	switch {
	case errors.Is(err, errTelegramBotNotConfigured):
		code = "TELEGRAM_BOT_NOT_CONFIGURED"
		message = "请先在服务端配置机器人身份与私聊授权名单。"
	case errors.Is(err, errTelegramBotRuntimeNotConfigured):
		code = "TELEGRAM_BOT_RUNTIME_NOT_CONFIGURED"
		message = "请先在服务端配置受限的 NoneBot Python 运行时。"
	case errors.Is(err, errTelegramBotClosed):
		code = "TELEGRAM_BOT_CLOSED"
		message = "Telegram 手动接收连接已关闭。"
	case errors.Is(err, errTelegramBotBusy):
		status = http.StatusConflict
		code = "TELEGRAM_BOT_BUSY"
		message = "已有 Telegram 人工操作进行中，请完成后再试。"
	case errors.Is(err, errTelegramBotVerificationRequired):
		status = http.StatusConflict
		code = "TELEGRAM_BOT_VERIFICATION_REQUIRED"
		message = "请先手动核验机器人身份与 webhook 状态。"
	case errors.Is(err, store.ErrTelegramCursorChanged):
		status = http.StatusConflict
		code = "TELEGRAM_BOT_CURSOR_CHANGED"
		message = "接收游标已改变，本页未提交，请重新核验并手动接收。"
	case errors.Is(err, store.ErrConflict):
		status = http.StatusConflict
		code = "TELEGRAM_BOT_CONFLICT"
		message = "同一 Telegram 消息标识存在不同内容，本页未提交。"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		status = http.StatusRequestTimeout
		code = "TELEGRAM_BOT_CANCELED"
		message = "本次操作已取消，未确认的页面不会保存。"
	case errors.Is(err, store.ErrInvalidInput):
		code = "TELEGRAM_BOT_PROTOCOL_ERROR"
		message = "Telegram 返回内容不符合受限接收协议，本页未提交。"
	default:
		var failure *nonebotbridge.Error
		if errors.As(err, &failure) {
			kind := nonebotbridge.KindOf(err)
			code = "TELEGRAM_BOT_" + strings.ToUpper(string(kind))
			message = "Telegram 核验或接收失败，本次不会自动重试，请检查服务端配置。"
			switch kind {
			case nonebotbridge.Canceled:
				status = http.StatusRequestTimeout
			case nonebotbridge.RateLimited:
				status = http.StatusTooManyRequests
				if failure.RetryAfter > 0 && failure.RetryAfter <= 24*time.Hour {
					w.Header().Set("Retry-After", strconv.Itoa(int((failure.RetryAfter+time.Second-1)/time.Second)))
				}
			case nonebotbridge.NotConfigured:
				code = "TELEGRAM_BOT_RUNTIME_NOT_CONFIGURED"
			case nonebotbridge.InvalidConfig, nonebotbridge.Unreachable, nonebotbridge.AuthFailed, nonebotbridge.ProtocolError,
				nonebotbridge.IdentityMismatch, nonebotbridge.WebhookPresent, nonebotbridge.PermissionDenied,
				nonebotbridge.PlatformRejected, nonebotbridge.Unavailable:
			default:
				code = "TELEGRAM_BOT_UNAVAILABLE"
			}
		}
	}
	writeError(w, status, code, message, trace)
}

func (h *handler) applyTelegramBot(items []connector.Descriptor) {
	status := h.telegram.snapshot()
	for i := range items {
		if items[i].ID != "telegram-bot" {
			continue
		}
		items[i].Status = status.State
		items[i].ImplementationStatus = "manual_receive_ready"
		items[i].Capabilities = connector.Capabilities{ReceiveText: status.Configured && status.RuntimeConfigured && status.IdentityVerified && h.inbox != nil}
		items[i].Description = "真实复用 NoneBot Telegram 适配器；仅人工同意后读取机器人可见的授权私聊纯文本单页，不是个人账号历史；后台接收、发送和自动 AI 均关闭。"
	}
}

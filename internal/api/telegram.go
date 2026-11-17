package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/qilinbro/msg-manager/internal/connector"
	"github.com/qilinbro/msg-manager/internal/connector/telegramuser"
	"github.com/qilinbro/msg-manager/internal/store"
)

const telegramPath = "/api/v1/connections/telegram-user"

type TelegramStatus struct {
	Configured             bool   `json:"configured"`
	HelperAvailable        bool   `json:"helper_available"`
	RequiresInitialization bool   `json:"requires_initialization"`
	State                  string `json:"state"`
	AccountID              string `json:"account_id"`
	Name                   string `json:"name"`
	ReadOnly               bool   `json:"read_only"`
	SendingEnabled         bool   `json:"sending_enabled"`
	CheckedAt              string `json:"checked_at"`
	LastSyncAt             string `json:"last_sync_at"`
	LastError              string `json:"last_error"`
}
type TelegramSyncResult struct {
	Inserted       int    `json:"inserted"`
	Duplicates     int    `json:"duplicates"`
	Conversations  int    `json:"conversations"`
	Skipped        int    `json:"skipped"`
	Limited        bool   `json:"limited"`
	SourceKind     string `json:"source_kind"`
	ReadOnly       bool   `json:"read_only"`
	SendingEnabled bool   `json:"sending_enabled"`
}

type telegramReader interface {
	Metadata() telegramuser.Metadata
	Probe(context.Context) (telegramuser.Identity, error)
	Dialogs(context.Context) (telegramuser.Dialogs, error)
	Read(context.Context, string, int) (telegramuser.Batch, error)
}
type telegramConnection struct {
	client telegramReader
	gate   chan struct{}
	mu     sync.RWMutex
	status TelegramStatus
	peers  map[string]bool
}

func newTelegramConnection(cfg telegramuser.Config) (*telegramConnection, error) {
	client, err := telegramuser.NewClient(cfg)
	if err != nil {
		return nil, errors.New("Telegram local helper configuration is invalid")
	}
	return &telegramConnection{client: client, gate: make(chan struct{}, 1), status: TelegramStatus{State: "not_configured", ReadOnly: true, RequiresInitialization: true}, peers: map[string]bool{}}, nil
}
func telegramMutation(method, path string) bool {
	return method == http.MethodPost && (path == telegramPath+"/verify" || path == telegramPath+"/sync")
}
func telegramRead(path string) bool {
	return path == telegramPath+"/status" || path == telegramPath+"/dialogs"
}

// snapshot only inspects fixed file metadata. Viewing Settings never decrypts a
// session, starts a helper, contacts Telegram or automatically retries a read.
func (c *telegramConnection) snapshot() TelegramStatus {
	metadata := c.client.Metadata()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.status.Configured, c.status.HelperAvailable = metadata.Configured, metadata.HelperAvailable
	c.status.RequiresInitialization = !metadata.Configured
	if !metadata.Configured {
		c.status.State = "requires_initialization"
		c.status.AccountID = ""
		c.status.Name = ""
		c.peers = map[string]bool{}
	} else if !metadata.HelperAvailable {
		c.status.State = "helper_unavailable"
		c.peers = map[string]bool{}
	} else if c.status.State == "not_configured" || c.status.State == "requires_initialization" || c.status.State == "helper_unavailable" {
		c.status.State = "configured"
	}
	return c.status
}
func (h *handler) applyTelegram(items []connector.Descriptor) {
	status := h.telegram.snapshot()
	for i := range items {
		if items[i].ID == "telegram-user" {
			items[i].Status = status.State
			items[i].Capabilities.HistoryImport = status.Configured && status.HelperAvailable && h.inbox != nil
		}
	}
}

func (h *handler) serveTelegram(w http.ResponseWriter, r *http.Request, traceID string) {
	if r.URL.RawQuery != "" {
		writeError(w, 400, "INVALID_INPUT", "此操作不接受 URL 查询参数。", traceID)
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == telegramPath+"/status" {
		writeJSON(w, 200, h.telegram.snapshot())
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == telegramPath+"/dialogs" {
		h.telegramDialogs(w, r, traceID)
		return
	}
	if r.Method == http.MethodPost && r.URL.Path == telegramPath+"/verify" {
		// Verify accepts an empty body only; credentials and targets never travel
		// through this API. The sole login flow is the local interactive helper.
		body, err := io.ReadAll(io.LimitReader(r.Body, 2))
		if err != nil || len(body) != 0 {
			writeError(w, 400, "INVALID_INPUT", "账号核验不接受配置或登录参数，请在本机初始化。", traceID)
			return
		}
		h.verifyTelegram(w, r, traceID)
		return
	}
	if r.Method == http.MethodPost && r.URL.Path == telegramPath+"/sync" {
		h.syncTelegram(w, r, traceID)
		return
	}
	writeError(w, 404, "NOT_FOUND", "Telegram 接口不存在。", traceID)
}
func (h *handler) telegramReady(w http.ResponseWriter, traceID string, verified bool) bool {
	s := h.telegram.snapshot()
	if !s.HelperAvailable {
		h.telegramError(w, &telegramuser.Error{Code: "helper_unavailable"}, traceID)
		return false
	}
	if !s.Configured {
		h.telegramError(w, &telegramuser.Error{Code: "requires_initialization"}, traceID)
		return false
	}
	if verified && s.State != "identity_verified" {
		writeError(w, 503, "TELEGRAM_NOT_VERIFIED", "请先核验本机已登录的 Telegram 本人账号。", traceID)
		return false
	}
	return true
}
func (h *handler) telegramGate(w http.ResponseWriter, traceID string) bool {
	select {
	case h.telegram.gate <- struct{}{}:
		return true
	default:
		writeError(w, 503, "TELEGRAM_BUSY", "Telegram 读取正在进行，请稍后重试。", traceID)
		return false
	}
}
func (h *handler) verifyTelegram(w http.ResponseWriter, r *http.Request, traceID string) {
	if !h.telegramReady(w, traceID, false) || !h.telegramGate(w, traceID) {
		return
	}
	defer func() { <-h.telegram.gate }()
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	identity, err := h.telegram.client.Probe(ctx)
	if err == nil && !telegramuser.ValidIdentity(identity) {
		err = &telegramuser.Error{Code: "protocol_error"}
	}
	if err != nil {
		h.telegramError(w, err, traceID)
		return
	}
	h.telegram.mu.Lock()
	h.telegram.status.State = "identity_verified"
	h.telegram.status.AccountID = identity.AccountID
	h.telegram.status.Name = identity.Name
	h.telegram.status.CheckedAt = time.Now().UTC().Format(time.RFC3339)
	h.telegram.status.LastError = ""
	h.telegram.peers = map[string]bool{}
	h.telegram.mu.Unlock()
	writeJSON(w, 200, h.telegram.snapshot())
}
func (h *handler) telegramDialogs(w http.ResponseWriter, r *http.Request, traceID string) {
	if !h.telegramReady(w, traceID, true) || !h.telegramGate(w, traceID) {
		return
	}
	defer func() { <-h.telegram.gate }()
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	result, err := h.telegram.client.Dialogs(ctx)
	h.telegram.mu.RLock()
	expected := h.telegram.status.AccountID
	h.telegram.mu.RUnlock()
	if err == nil && (!telegramuser.ValidIdentity(result.Identity) || result.AccountID != expected) {
		err = &telegramuser.Error{Code: "identity_mismatch"}
	}
	if err == nil && (result.Items == nil || len(result.Items) > telegramuser.MaxDialogs) {
		err = &telegramuser.Error{Code: "protocol_error"}
	}
	peers := map[string]bool{}
	if err == nil {
		for _, item := range result.Items {
			if !telegramuser.ValidID(item.PeerID) || item.PeerID == expected || peers[item.PeerID] {
				err = &telegramuser.Error{Code: "protocol_error"}
				break
			}
			peers[item.PeerID] = true
		}
	}
	if err != nil {
		h.telegramError(w, err, traceID)
		return
	}
	h.telegram.mu.Lock()
	h.telegram.peers = peers
	h.telegram.status.LastError = ""
	h.telegram.mu.Unlock()
	writeJSON(w, 200, result)
}
func (h *handler) syncTelegram(w http.ResponseWriter, r *http.Request, traceID string) {
	var input struct {
		PeerID            string `json:"peer_id"`
		ExpectedAccountID string `json:"expected_account_id"`
		Limit             *int   `json:"limit"`
	}
	if !readInboxJSON(w, r, &input, traceID) {
		return
	}
	if !telegramuser.ValidID(input.PeerID) || !telegramuser.ValidID(input.ExpectedAccountID) || input.Limit == nil || *input.Limit < 1 || *input.Limit > telegramuser.MaxMessages {
		writeError(w, 400, "INVALID_INPUT", "请选择一位私聊联系人和 1 到 100 条的读取数量。", traceID)
		return
	}
	if h.inbox == nil {
		writeError(w, 503, "TELEGRAM_STORAGE_DISABLED", "请先启用本机加密消息库。", traceID)
		return
	}
	if !h.telegramReady(w, traceID, true) || !h.telegramGate(w, traceID) {
		return
	}
	defer func() { <-h.telegram.gate }()
	h.telegram.mu.RLock()
	selected := h.telegram.peers[input.PeerID]
	expected := h.telegram.status.AccountID
	h.telegram.mu.RUnlock()
	if input.ExpectedAccountID != expected {
		// Another window may have explicitly verified a replacement account.
		// Reject this stale selection without revoking that window's valid state.
		writeError(w, http.StatusConflict, "TELEGRAM_IDENTITY_MISMATCH", "私聊列表所属账号已变化，请重新核验并选择联系人。本次未读取或导入消息。", traceID)
		return
	}
	if !selected {
		writeError(w, 400, "TELEGRAM_PEER_NOT_SELECTED", "请先获取最近私聊列表，再明确选择一位联系人。", traceID)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	batch, err := h.telegram.client.Read(ctx, input.PeerID, *input.Limit)
	if err == nil && batch.AccountID != expected {
		err = &telegramuser.Error{Code: "identity_mismatch"}
	}
	if err == nil && !telegramuser.ValidBatch(batch, input.PeerID, *input.Limit) {
		err = &telegramuser.Error{Code: "protocol_error"}
	}
	if err != nil {
		h.telegramError(w, err, traceID)
		return
	}
	result := TelegramSyncResult{Skipped: batch.Skipped, Limited: batch.Limited, SourceKind: "telegram_user", ReadOnly: true}
	if len(batch.Messages) > 0 {
		messages := make([]store.ImportMessage, 0, len(batch.Messages))
		for _, m := range batch.Messages {
			messages = append(messages, store.ImportMessage{ExternalID: m.ExternalID, PeerID: batch.PeerID, PeerName: batch.PeerName, ConversationKind: "private", Body: m.Body, Direction: m.Direction, OccurredAt: m.OccurredAt})
		}
		saved, err := h.inbox.ImportTelegramContext(ctx, store.ImportRequest{Platform: "telegram", AccountID: expected, AccountName: "Telegram个人账号(在线只读)", Messages: messages})
		if err != nil {
			writeInboxError(w, err, traceID)
			return
		}
		result.Inserted, result.Duplicates, result.Conversations = saved.Inserted, saved.Duplicates, saved.Conversations
	}
	h.telegram.mu.Lock()
	h.telegram.status.LastSyncAt = time.Now().UTC().Format(time.RFC3339)
	h.telegram.status.LastError = ""
	h.telegram.mu.Unlock()
	writeJSON(w, 200, result)
}
func (h *handler) telegramError(w http.ResponseWriter, err error, traceID string) {
	code := "unavailable"
	retry := 0
	var failure *telegramuser.Error
	if errors.As(err, &failure) {
		code = failure.Code
		retry = failure.RetryAfter
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		code = "timeout"
	}
	message := "暂时无法读取 Telegram，请检查本机初始化与网络后手动重试。"
	switch code {
	case "requires_initialization", "not_configured":
		message = "请先运行本机 Telegram 初始化，在终端填写自己的 API 信息并登录。"
	case "helper_unavailable", "unsupported_platform":
		message = "Telegram 本机读取组件尚未就绪，请先完成 Windows 初始化。"
	case "config_invalid":
		message = "本机 Telegram 加密配置无法读取，请用同一 Windows 用户检查或重新初始化。"
	case "logged_out", "auth_failed":
		message = "Telegram 登录已失效或 API 配置不正确，请在本机重新初始化。"
	case "identity_mismatch":
		message = "当前 Telegram 账号与核验账号不一致，读取已停止，请重新核验。"
	case "flood_wait":
		message = "Telegram 要求稍后再试；系统不会自动重试，请等待后手动操作。"
	case "peer_not_found":
		message = "该联系人不在最近私聊范围，请刷新列表后重新选择。"
	case "protocol_error":
		message = "本机 Telegram 读取结果不符合契约，本批未保存。"
	case "timeout":
		message = "Telegram 读取超时，本次未自动重试。"
	case "network_error":
		message = "暂时无法连接 Telegram，请检查本机网络及初始化时设置的代理。"
	default:
		code = "unavailable"
	}
	if retry > 0 && retry <= 7*86400 {
		w.Header().Set("Retry-After", strconv.Itoa(retry))
	}
	h.telegram.mu.Lock()
	h.telegram.status.State = code
	h.telegram.status.LastError = code
	h.telegram.peers = map[string]bool{}
	h.telegram.mu.Unlock()
	writeError(w, http.StatusServiceUnavailable, "TELEGRAM_"+strings.ToUpper(code), message, traceID)
}

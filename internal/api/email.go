package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/qilinbro/msg-manager/internal/connector/mailbox"
	"github.com/qilinbro/msg-manager/internal/store"
)

type mailboxFetcher interface {
	Fetch(context.Context) (mailbox.Batch, error)
}

type emailConnection struct {
	client    mailboxFetcher
	gate      chan struct{}
	mu        sync.RWMutex
	status    EmailStatus
	accountID string
}

type EmailResult struct {
	store.ImportResult
	Skipped        int    `json:"skipped"`
	Limited        bool   `json:"limited"`
	SyncedAt       string `json:"synced_at"`
	SendingEnabled bool   `json:"sending_enabled"`
}

type EmailStatus struct {
	Configured     bool         `json:"configured"`
	State          string       `json:"state"`
	Account        *string      `json:"account"`
	Folder         *string      `json:"folder"`
	LastSyncedAt   *string      `json:"last_synced_at"`
	LastResult     *EmailResult `json:"last_result"`
	SendingEnabled bool         `json:"sending_enabled"`
}

func newEmailConnection(cfg mailbox.Config) (*emailConnection, error) {
	c := &emailConnection{gate: make(chan struct{}, 1), status: EmailStatus{State: "not_configured"}}
	if cfg.Host == "" && cfg.Username == "" && cfg.Password == "" && cfg.Folder == "" && cfg.Port == 0 && cfg.Limit == 0 {
		return c, nil
	}
	if cfg.Port == 0 {
		cfg.Port = 993
	}
	if cfg.Folder == "" {
		cfg.Folder = "INBOX"
	}
	if cfg.Limit == 0 {
		cfg.Limit = 50
	}
	if strings.EqualFold(cfg.Folder, "INBOX") {
		cfg.Folder = "INBOX"
	}
	client, err := mailbox.NewClient(cfg)
	if err != nil {
		return nil, errors.New("EMAIL_IMAP configuration is invalid; check the local host, username, application password and TLS port")
	}
	c.client = client
	hash := sha256.Sum256([]byte(strings.ToLower(cfg.Host) + ":" + strconv.Itoa(cfg.Port) + "\x00" + cfg.Username))
	c.accountID = "imap-" + hex.EncodeToString(hash[:])
	c.status = EmailStatus{Configured: true, State: "ready", Account: &cfg.Username, Folder: &cfg.Folder}
	return c, nil
}

func (h *handler) emailStatus(w http.ResponseWriter) {
	h.email.mu.RLock()
	status := h.email.status
	h.email.mu.RUnlock()
	writeJSON(w, http.StatusOK, status)
}

func (h *handler) syncEmail(w http.ResponseWriter, r *http.Request, traceID string) {
	var input struct{}
	if !readInboxJSON(w, r, &input, traceID) {
		return
	}
	if h.inbox == nil {
		writeError(w, 503, "STORAGE_DISABLED", "请先启用本地加密消息存储。", traceID)
		return
	}
	c := h.email
	if c.client == nil {
		writeError(w, 503, "EMAIL_NOT_CONFIGURED", "请先在本机后台配置邮箱，再重启服务。", traceID)
		return
	}
	select {
	case c.gate <- struct{}{}:
		defer func() { <-c.gate }()
	default:
		writeError(w, 503, "EMAIL_BUSY", "邮箱同步正在进行，请稍后查看结果。", traceID)
		return
	}
	c.mu.Lock()
	c.status.State = "syncing"
	account := *c.status.Account
	folder := *c.status.Folder
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 28*time.Second)
	defer cancel()
	batch, err := c.client.Fetch(ctx)
	if err != nil {
		state := emailFailureState(err)
		c.mu.Lock()
		c.status.State = state
		c.mu.Unlock()
		writeError(w, 503, "EMAIL_"+strings.ToUpper(state), emailFailureMessage(state), traceID)
		return
	}
	// Identity, folder and UID generation are tied to the server's fixed config,
	// not to request fields, sender-controlled Message-ID or message sequence.
	if batch.Info.AccountID != account || batch.Info.Folder != folder || !batch.Info.ReadOnly || batch.Info.UIDValidity == 0 {
		c.mu.Lock()
		c.status.State = "protocol_error"
		c.mu.Unlock()
		writeError(w, 503, "EMAIL_PROTOCOL_ERROR", "邮箱返回的账号或只读状态不匹配，本轮未保存。", traceID)
		return
	}
	req := store.ImportRequest{Platform: "email", AccountID: c.accountID, AccountName: mailLabel(account, 128)}
	skipped := batch.Skipped
	for _, item := range batch.Mails {
		if item.UID == 0 {
			skipped++
			continue
		}
		identity := folder + "\x00" + strconv.FormatUint(uint64(batch.Info.UIDValidity), 10) + "\x00" + strconv.FormatUint(uint64(item.UID), 10)
		key := sha256.Sum256([]byte(identity))
		externalID := hex.EncodeToString(key[:])
		title := mailLabel(item.Subject, 128)
		if title == "" {
			title = "（无主题）"
		}
		seen := item.Seen
		body := item.Body
		if body == "" {
			body = "（无可显示的纯文本正文）"
		}
		to := item.To
		if to == nil {
			to = []string{}
		}
		req.Messages = append(req.Messages, store.ImportMessage{ExternalID: externalID, PeerID: externalID, PeerName: title, Direction: "incoming", Body: body, OccurredAt: item.OccurredAt, Read: &seen, Email: &store.EmailHeaders{Subject: mailLabel(item.Subject, 1024), FromName: mailLabel(item.FromName, 128), FromAddress: item.FromAddress, To: to, Folder: folder}})
	}
	if ctx.Err() != nil {
		c.mu.Lock()
		c.status.State = "unreachable"
		c.mu.Unlock()
		writeError(w, 503, "EMAIL_UNREACHABLE", "同步已中断，尚未开始本地保存。", traceID)
		return
	}
	result := store.ImportResult{}
	if len(req.Messages) > 0 {
		result, err = h.inbox.ImportIMAP(req)
		if err != nil {
			c.mu.Lock()
			c.status.State = "protocol_error"
			c.mu.Unlock()
			writeInboxError(w, err, traceID)
			return
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	response := EmailResult{ImportResult: result, Skipped: skipped, Limited: batch.Truncated, SyncedAt: now, SendingEnabled: false}
	c.mu.Lock()
	c.status.State = "synced"
	c.status.LastSyncedAt = &now
	c.status.LastResult = &response
	c.mu.Unlock()
	writeJSON(w, http.StatusOK, response)
}

func mailLabel(value string, limit int) string {
	value = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value))
	runes := []rune(value)
	if len(runes) > limit {
		value = string(runes[:limit])
	}
	return value
}

func emailFailureState(err error) string {
	var failure *mailbox.Error
	if errors.As(err, &failure) {
		switch string(failure.Kind) {
		case "auth_failed":
			return "auth_failed"
		case "protocol_error", "mailbox_failed", "tls_failed", "address_denied", "invalid_config", "not_configured":
			return "protocol_error"
		}
	}
	return "unreachable"
}

func emailFailureMessage(state string) string {
	switch state {
	case "auth_failed":
		return "邮箱认证失败，请检查用户名、IMAP服务及本机授权码。"
	case "protocol_error":
		return "邮箱的加密连接、只读状态或返回格式不符合要求，本轮未完成同步。"
	default:
		return "暂时无法访问邮箱，请检查网络和本机邮箱配置。"
	}
}

package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/qilinbro/msg-manager/internal/connector/mailbox"
)

type EmailMonitorStatus struct {
	Enabled         bool    `json:"enabled"`
	IntervalSeconds int     `json:"interval_seconds"`
	State           string  `json:"state"`
	LastSuccessAt   *string `json:"last_success_at"`
	LastErrorCode   *string `json:"last_error_code"`
	ReadOnly        bool    `json:"read_only"`
	SendingEnabled  bool    `json:"sending_enabled"`
	BaselineMode    string  `json:"baseline_mode"`
	Skipped         int     `json:"skipped"`
	MissingUIDCount int     `json:"missing_uid_count"`
	LastGapReason   *string `json:"last_gap_reason"`
	Limited         bool    `json:"limited"`
}
type emailCheckpoint struct {
	Enabled         bool                 `json:"enabled"`
	IntervalSeconds int                  `json:"interval_seconds"`
	Cursor          mailbox.Cursor       `json:"cursor"`
	LastSuccessAt   *string              `json:"last_success_at"`
	LastErrorCode   *string              `json:"last_error_code"`
	Skipped         int                  `json:"skipped"`
	MissingUIDCount int                  `json:"missing_uid_count"`
	Gaps            []mailbox.SkippedUID `json:"gaps"`
}
type incrementalMailbox interface {
	Probe(context.Context) (mailbox.Info, error)
	FetchAfter(context.Context, mailbox.Cursor) (mailbox.Delta, error)
}

func newEmailMonitorStatus() EmailMonitorStatus {
	return EmailMonitorStatus{IntervalSeconds: 60, State: "disabled", ReadOnly: true, BaselineMode: "future_only"}
}
func (c *emailConnection) monitorBinding() string {
	if c.status.Folder == nil || c.accountID == "" {
		return ""
	}
	hash := sha256.Sum256([]byte(c.accountID + "\x00" + *c.status.Folder))
	return hex.EncodeToString(hash[:])
}
func (c *emailConnection) applyEmailCheckpoint(p emailCheckpoint, state string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checkpoint = p
	c.monitor = EmailMonitorStatus{Enabled: p.Enabled, IntervalSeconds: p.IntervalSeconds, State: state, LastSuccessAt: p.LastSuccessAt, LastErrorCode: p.LastErrorCode, ReadOnly: true, BaselineMode: "future_only", Skipped: p.Skipped, MissingUIDCount: p.MissingUIDCount}
	if len(p.Gaps) > 0 {
		reason := p.Gaps[len(p.Gaps)-1].Reason
		c.monitor.LastGapReason = &reason
	}
}
func (h *handler) saveEmailCheckpoint(ctx context.Context, p emailCheckpoint) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return h.inbox.SaveReceiveMonitor(ctx, "email", h.email.monitorBinding(), raw)
}
func validEmailCheckpoint(p emailCheckpoint) bool {
	if p.IntervalSeconds < 30 || p.IntervalSeconds > 900 || p.Skipped < 0 || p.MissingUIDCount < 0 || len(p.Gaps) > 10 || (p.Cursor.NextUID == 0) != (p.Cursor.UIDValidity == 0) {
		return false
	}
	if p.Enabled && p.Cursor.NextUID == 0 {
		return false
	}
	for _, v := range []*string{p.LastSuccessAt} {
		if v != nil {
			if _, err := time.Parse(time.RFC3339Nano, *v); err != nil {
				return false
			}
		}
	}
	if p.LastErrorCode != nil && !validEmailMonitorCode(*p.LastErrorCode) {
		return false
	}
	for _, gap := range p.Gaps {
		if !validSkipReason(gap.Reason) {
			return false
		}
	}
	return true
}
func validSkipReason(reason string) bool {
	return reason == "oversized" || reason == "vanished" || reason == "missing_body" || reason == "unsupported_message" || reason == "uidvalidity_changed"
}
func validEmailMonitorCode(code string) bool {
	switch code {
	case "EMAIL_AUTH_FAILED", "EMAIL_PROTOCOL_ERROR", "EMAIL_UNREACHABLE", "EMAIL_UIDVALIDITY_CHANGED", "EMAIL_IDENTITY_MISMATCH", "EMAIL_STATE_SAVE_FAILED", "EMAIL_STORAGE_ERROR":
		return true
	}
	return false
}
func (h *handler) restoreEmailMonitor() error {
	c := h.email
	if c == nil || c.client == nil || h.inbox == nil {
		return nil
	}
	if _, ok := c.client.(incrementalMailbox); !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	raw, err := h.inbox.LoadReceiveMonitor(ctx, "email", c.monitorBinding())
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	var p emailCheckpoint
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if !uniqueJSON(raw) || decoder.Decode(&p) != nil || !validEmailCheckpoint(p) {
		return errors.New("encrypted email receive settings are invalid")
	}
	c.monitorMu.Lock()
	defer c.monitorMu.Unlock()
	state := "disabled"
	if p.Enabled {
		state = "starting"
	} else if p.LastErrorCode != nil {
		state = "needs_reenable"
	}
	c.applyEmailCheckpoint(p, state)
	if p.Enabled {
		h.startEmailMonitorLocked(p)
	}
	return nil
}
func (h *handler) closeEmailMonitor() {
	if h.email == nil {
		return
	}
	c := h.email
	c.monitorMu.Lock()
	defer c.monitorMu.Unlock()
	c.closed = true
	h.stopEmailMonitorLocked()
}
func (h *handler) stopEmailMonitorLocked() {
	c := h.email
	if c.cancel != nil {
		c.cancel()
		if c.done != nil {
			<-c.done
		}
	}
	c.cancel, c.done = nil, nil
}
func (h *handler) startEmailMonitorLocked(p emailCheckpoint) {
	c := h.email
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	c.cancel, c.done = cancel, done
	go func() { defer close(done); h.runEmailMonitor(ctx, p) }()
}
func (h *handler) serveEmailMonitor(w http.ResponseWriter, r *http.Request, traceID string) {
	c := h.email
	if r.URL.RawQuery != "" {
		writeError(w, 400, "INVALID_INPUT", "邮箱后台接收不接受查询参数。", traceID)
		return
	}
	if r.Method == http.MethodGet {
		c.mu.RLock()
		status := c.monitor
		c.mu.RUnlock()
		if c.client == nil {
			status.State = "not_configured"
		} else if h.inbox == nil {
			status.State = "storage_disabled"
		}
		writeJSON(w, 200, status)
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, 405, "METHOD_NOT_ALLOWED", "该接口仅接受读取状态或明确修改接收设置。", traceID)
		return
	}
	var input struct {
		Enabled         *bool `json:"enabled"`
		IntervalSeconds *int  `json:"interval_seconds"`
	}
	if !readInboxJSON(w, r, &input, traceID) {
		return
	}
	interval := 60
	if input.IntervalSeconds != nil {
		interval = *input.IntervalSeconds
	}
	if input.Enabled == nil || interval < 30 || interval > 900 {
		writeError(w, 400, "INVALID_INPUT", "请明确设置是否开启，读取间隔须为30到900秒。", traceID)
		return
	}
	reader, ok := c.client.(incrementalMailbox)
	if !ok || c.client == nil {
		writeError(w, 503, "EMAIL_NOT_CONFIGURED", "请先在本机配置只读邮箱。", traceID)
		return
	}
	if h.inbox == nil {
		writeError(w, 503, "STORAGE_DISABLED", "请先启用本机加密消息库。", traceID)
		return
	}
	c.monitorMu.Lock()
	defer c.monitorMu.Unlock()
	if c.closed {
		writeError(w, 503, "EMAIL_UNAVAILABLE", "后台正在停止。", traceID)
		return
	}
	h.stopEmailMonitorLocked()
	c.mu.RLock()
	previous := c.checkpoint
	c.mu.RUnlock()
	if previous.IntervalSeconds == 0 {
		previous.IntervalSeconds = 60
	}
	if input.IntervalSeconds == nil {
		interval = previous.IntervalSeconds
	}
	next := previous
	next.Enabled = *input.Enabled
	next.IntervalSeconds = interval
	if next.Enabled {
		next.LastErrorCode = nil
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if next.Enabled && (next.Cursor.NextUID == 0 || previous.LastErrorCode != nil && *previous.LastErrorCode == "EMAIL_UIDVALIDITY_CHANGED") {
		select {
		case c.gate <- struct{}{}:
			defer func() { <-c.gate }()
		case <-ctx.Done():
			writeError(w, 503, "EMAIL_BUSY", "邮箱正在读取，请稍后重试。", traceID)
			h.resumeEmailOnFailure(previous)
			return
		}
		info, err := reader.Probe(ctx)
		if err != nil || !h.validEmailInfo(info) || info.UIDNext == 0 {
			h.resumeEmailOnFailure(previous)
			writeError(w, 503, "EMAIL_BASELINE_FAILED", "无法建立只读邮箱的新消息基线，请检查本机配置与网络。", traceID)
			return
		}
		next.Cursor = mailbox.Cursor{UIDValidity: info.UIDValidity, NextUID: info.UIDNext}
	}
	if err := h.saveEmailCheckpoint(ctx, next); err != nil {
		h.resumeEmailOnFailure(previous)
		writeInboxError(w, err, traceID)
		return
	}
	state := "disabled"
	if next.Enabled {
		state = "starting"
	}
	c.applyEmailCheckpoint(next, state)
	if next.Enabled {
		h.startEmailMonitorLocked(next)
	}
	c.mu.RLock()
	status := c.monitor
	c.mu.RUnlock()
	writeJSON(w, 200, status)
}
func (h *handler) resumeEmailOnFailure(p emailCheckpoint) {
	if p.Enabled && !h.email.closed {
		h.startEmailMonitorLocked(p)
	}
}
func (h *handler) validEmailInfo(info mailbox.Info) bool {
	c := h.email
	return c.status.Account != nil && c.status.Folder != nil && info.AccountID == *c.status.Account && info.Folder == *c.status.Folder && info.ReadOnly && info.UIDValidity != 0
}
func (h *handler) runEmailMonitor(ctx context.Context, p emailCheckpoint) {
	// Both a fresh future-only baseline and restored watermarks poll at once,
	// but only UIDs at/above the saved next UID are eligible for body reads.
	for {
		if !h.emailMonitorTick(ctx, &p) {
			return
		}
		timer := time.NewTimer(time.Duration(p.IntervalSeconds) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
func (h *handler) emailMonitorTick(ctx context.Context, p *emailCheckpoint) bool {
	c := h.email
	reader, ok := c.client.(incrementalMailbox)
	if !ok {
		return false
	}
	select {
	case c.gate <- struct{}{}:
		defer func() { <-c.gate }()
	case <-ctx.Done():
		return false
	}
	for page := 0; page < 3; page++ {
		if ctx.Err() != nil {
			return false
		}
		batchCtx, cancel := context.WithTimeout(ctx, 28*time.Second)
		delta, err := reader.FetchAfter(batchCtx, p.Cursor)
		if err == nil && (!h.validEmailInfo(delta.Info) || delta.Info.UIDValidity != p.Cursor.UIDValidity) {
			err = &mailbox.Error{Kind: mailbox.ProtocolError}
		}
		if err == nil && !validEmailDelta(delta, p.Cursor) {
			err = &mailbox.Error{Kind: mailbox.ProtocolError}
		}
		if err != nil {
			cancel()
			if ctx.Err() != nil {
				return false
			}
			return h.recordEmailMonitorFailure(ctx, p, err)
		}
		req, _ := emailImportBatch(c.accountID, *c.status.Account, *c.status.Folder, delta.Batch)
		if len(req.Messages) > 0 {
			_, err = h.inbox.ImportIMAPContext(batchCtx, req)
		}
		if err != nil {
			cancel()
			if ctx.Err() != nil {
				return false
			}
			return h.recordEmailMonitorCode(ctx, p, "EMAIL_STORAGE_ERROR", false)
		}
		now := time.Now().UTC().Format(time.RFC3339)
		next := *p
		next.Cursor = delta.Next
		next.LastSuccessAt = &now
		next.LastErrorCode = nil
		next.Skipped += len(delta.SkippedUIDs)
		next.MissingUIDCount += delta.MissingUIDCount
		next.Gaps = append(append([]mailbox.SkippedUID{}, p.Gaps...), delta.SkippedUIDs...)
		if len(next.Gaps) > 10 {
			next.Gaps = next.Gaps[len(next.Gaps)-10:]
		}
		if err = h.saveEmailCheckpoint(batchCtx, next); err != nil {
			cancel()
			if ctx.Err() != nil {
				return false
			}
			return h.recordEmailMonitorCode(ctx, p, "EMAIL_STATE_SAVE_FAILED", true)
		}
		cancel()
		*p = next
		c.applyEmailCheckpoint(next, "running")
		c.mu.Lock()
		c.monitor.Limited = delta.Truncated
		c.mu.Unlock()
		if !delta.Truncated {
			break
		}
	}
	return true
}
func validEmailDelta(d mailbox.Delta, cursor mailbox.Cursor) bool {
	if d.Next.UIDValidity != cursor.UIDValidity || d.Next.NextUID < cursor.NextUID || d.Next.NextUID > d.Info.UIDNext || d.Info.UIDNext == 0 || len(d.Mails)+len(d.SkippedUIDs) > 100 || d.Skipped != len(d.SkippedUIDs) || d.MissingUIDCount < 0 || d.MissingUIDCount > mailbox.UIDWindow {
		return false
	}
	if d.Next.NextUID == cursor.NextUID {
		return len(d.Mails) == 0 && len(d.SkippedUIDs) == 0 && d.MissingUIDCount == 0 && d.Next.NextUID == d.Info.UIDNext && !d.Truncated
	}
	if uint64(d.Next.NextUID)-uint64(cursor.NextUID) > mailbox.UIDWindow {
		return false
	}
	seen := map[uint32]bool{}
	for _, mail := range d.Mails {
		if mail.UID < cursor.NextUID || mail.UID >= d.Next.NextUID || seen[mail.UID] {
			return false
		}
		seen[mail.UID] = true
	}
	for _, gap := range d.SkippedUIDs {
		if gap.UID < cursor.NextUID || gap.UID >= d.Next.NextUID || seen[gap.UID] || !validSkipReason(gap.Reason) || gap.Reason == "uidvalidity_changed" {
			return false
		}
		seen[gap.UID] = true
	}
	return int(d.Next.NextUID-cursor.NextUID) == len(seen)+d.MissingUIDCount && d.Truncated == (d.Next.NextUID < d.Info.UIDNext)
}
func (h *handler) recordEmailMonitorFailure(ctx context.Context, p *emailCheckpoint, err error) bool {
	var failure *mailbox.Error
	if errors.As(err, &failure) && failure.Kind == mailbox.UIDValidityChanged {
		p.Gaps = append(append([]mailbox.SkippedUID{}, p.Gaps...), mailbox.SkippedUID{Reason: "uidvalidity_changed"})
		if len(p.Gaps) > 10 {
			p.Gaps = p.Gaps[len(p.Gaps)-10:]
		}
		return h.recordEmailMonitorCode(ctx, p, "EMAIL_UIDVALIDITY_CHANGED", true)
	}
	code := "EMAIL_" + strings.ToUpper(emailFailureState(err))
	terminal := code == "EMAIL_AUTH_FAILED" || code == "EMAIL_PROTOCOL_ERROR"
	return h.recordEmailMonitorCode(ctx, p, code, terminal)
}
func (h *handler) recordEmailMonitorCode(ctx context.Context, p *emailCheckpoint, code string, terminal bool) bool {
	previous := *p
	next := *p
	next.LastErrorCode = &code
	if terminal {
		next.Enabled = false
	}
	state := "error"
	if terminal {
		state = "needs_reenable"
	}
	saveCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := h.saveEmailCheckpoint(saveCtx, next); err != nil {
		// Leave durable authorization/cursor untouched. The in-memory worker
		// stops; a restart may safely replay from its last committed checkpoint.
		code = "EMAIL_STATE_SAVE_FAILED"
		previous.LastErrorCode = &code
		previous.Enabled = false
		h.email.applyEmailCheckpoint(previous, "needs_reenable")
		return false
	}
	*p = next
	h.email.applyEmailCheckpoint(next, state)
	return !terminal
}

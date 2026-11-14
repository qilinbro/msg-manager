package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/qilinbro/msg-manager/internal/connector/wechatdb"
	"github.com/qilinbro/msg-manager/internal/store"
)

// This is a bounded recent-window poller, not a durable job queue or a history
// cursor. The explicit per-account allowlist is persisted in encrypted storage.
type WechatMonitorStatus struct {
	Enabled         bool              `json:"enabled"`
	PeerIDs         []string          `json:"peer_ids"`
	IntervalSeconds int               `json:"interval_seconds"`
	State           string            `json:"state"`
	LastCycleAt     *string           `json:"last_cycle_at"`
	LastErrorCode   *string           `json:"last_error_code"`
	LastResult      *WechatReadResult `json:"last_result"`
	ReadOnly        bool              `json:"read_only"`
	SendingEnabled  bool              `json:"sending_enabled"`
}

func defaultWechatMonitor() WechatMonitorStatus {
	return WechatMonitorStatus{PeerIDs: []string{}, IntervalSeconds: 30, State: "disabled", ReadOnly: true}
}

// mu is held by the caller. Copy mutable slices before exposing metadata.
func (c *wechatConnection) monitorSnapshotLocked() WechatMonitorStatus {
	status := c.monitor
	status.PeerIDs = append([]string{}, status.PeerIDs...)
	if c.reader == nil {
		status.State = "not_configured"
	}
	return status
}

func (h *handler) restoreWechatMonitor() error {
	c := h.wechat
	if c.reader == nil || h.inbox == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	settings, err := h.inbox.LoadWechatMonitor(ctx, c.accountID)
	if err != nil {
		return err
	}
	c.monitorMu.Lock()
	defer c.monitorMu.Unlock()
	h.applyWechatMonitorLocked(settings)
	return nil
}

// monitorMu serializes configuration changes, restart, and shutdown. Workers
// never acquire it, so cancellation and join cannot deadlock with a cycle.
func (h *handler) stopWechatMonitorLocked() {
	c := h.wechat
	if c.cancelMonitor != nil {
		c.cancelMonitor()
		<-c.monitorDone
		c.cancelMonitor, c.monitorDone = nil, nil
	}
}

func (h *handler) closeWechatMonitor() {
	c := h.wechat
	if c == nil {
		return
	}
	c.monitorMu.Lock()
	defer c.monitorMu.Unlock()
	c.closed = true
	h.stopWechatMonitorLocked()
}

func (h *handler) applyWechatMonitorLocked(settings store.WechatMonitorSettings) {
	c := h.wechat
	c.mu.Lock()
	c.monitor = defaultWechatMonitor()
	c.monitor.Enabled, c.monitor.IntervalSeconds = settings.Enabled, settings.IntervalSeconds
	c.monitor.PeerIDs = append([]string{}, settings.PeerIDs...)
	if settings.Enabled {
		c.monitor.State = "waiting"
	}
	c.mu.Unlock()
	if settings.Enabled {
		// No request context: closing or navigating away from the UI must not
		// cancel a previously accepted background subscription.
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		c.cancelMonitor, c.monitorDone = cancel, done
		go func() {
			defer close(done)
			h.runWechatMonitor(ctx, settings)
		}()
	}
}

func (h *handler) configureWechatMonitor(w http.ResponseWriter, r *http.Request, traceID string) {
	var input struct {
		Enabled         *bool     `json:"enabled"`
		PeerIDs         *[]string `json:"peer_ids"`
		IntervalSeconds *int      `json:"interval_seconds"`
	}
	if !readInboxJSON(w, r, &input, traceID) {
		return
	}
	if r.URL.RawQuery != "" || input.Enabled == nil || input.PeerIDs == nil || input.IntervalSeconds == nil {
		writeError(w, 400, "INVALID_INPUT", "请明确指定后台读取开关、私聊列表和10至300秒的间隔。", traceID)
		return
	}
	c := h.wechat
	settings := store.WechatMonitorSettings{Enabled: *input.Enabled, PeerIDs: *input.PeerIDs, IntervalSeconds: *input.IntervalSeconds}
	if c.reader == nil {
		writeError(w, 503, "WECHAT_NOT_CONFIGURED", "请先在本机配置微信数据库。", traceID)
		return
	}
	if h.inbox == nil {
		writeError(w, 503, "STORAGE_DISABLED", "请先启用本产品的加密消息存储。", traceID)
		return
	}
	if err := store.ValidateWechatMonitorSettings(c.accountID, settings); err != nil {
		writeError(w, 400, "INVALID_INPUT", "请选择最多20个不同的私聊，并设置10至300秒的读取间隔。", traceID)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := c.lockWechatMonitor(ctx); err != nil {
		writeError(w, 503, "WECHAT_UNAVAILABLE", "后台读取设置请求已取消或超时。", traceID)
		return
	}
	defer c.monitorMu.Unlock()
	if c.closed {
		writeError(w, 503, "WECHAT_UNAVAILABLE", "后台服务正在停止。", traceID)
		return
	}
	if ctx.Err() != nil {
		writeError(w, 503, "WECHAT_UNAVAILABLE", "后台读取设置请求已取消或超时。", traceID)
		return
	}
	if settings.Enabled {
		if err := h.validateWechatMonitorPeers(ctx, settings.PeerIDs); err != nil {
			if errors.Is(err, store.ErrInvalidInput) {
				writeError(w, 400, "INVALID_INPUT", "后台读取只能选择当前本机账号中可读取的私聊。", traceID)
			} else {
				h.failWechat(w, err, traceID)
			}
			return
		}
	}
	c.mu.RLock()
	old := store.WechatMonitorSettings{Enabled: c.monitor.Enabled, PeerIDs: append([]string{}, c.monitor.PeerIDs...), IntervalSeconds: c.monitor.IntervalSeconds}
	c.mu.RUnlock()
	// Join before changing durable settings. In particular, a successful pause
	// response guarantees no canceled worker can save another batch afterwards.
	h.stopWechatMonitorLocked()
	if err := h.inbox.SaveWechatMonitor(ctx, c.accountID, settings); err != nil {
		// The update was not accepted; keep the prior durable authorization.
		h.applyWechatMonitorLocked(old)
		writeInboxError(w, err, traceID)
		return
	}
	h.applyWechatMonitorLocked(settings)
	c.mu.RLock()
	status := c.monitorSnapshotLocked()
	c.mu.RUnlock()
	writeJSON(w, 200, status)
}

func (c *wechatConnection) lockWechatMonitor(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if c.monitorMu.TryLock() {
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

func (h *handler) validateWechatMonitorPeers(ctx context.Context, peers []string) error {
	c := h.wechat
	select {
	case c.gate <- struct{}{}:
		defer func() { <-c.gate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	sessions, err := h.validatedWechatSessions(ctx)
	if err != nil {
		return err
	}
	available := make(map[string]bool, len(sessions))
	for _, session := range sessions {
		available[session.PeerID] = true
	}
	for _, peer := range peers {
		if !available[peer] {
			return store.ErrInvalidInput
		}
	}
	return ctx.Err()
}

func (h *handler) runWechatMonitor(ctx context.Context, settings store.WechatMonitorSettings) {
	c := h.wechat
	for ctx.Err() == nil {
		c.mu.Lock()
		c.monitor.State = "reading"
		c.mu.Unlock()
		result, err := h.wechatMonitorCycle(ctx, settings.PeerIDs)
		if ctx.Err() != nil {
			return
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		result.ReadAt = now
		c.mu.Lock()
		c.monitor.State, c.monitor.LastCycleAt = "waiting", &now
		c.monitor.LastResult, c.monitor.LastErrorCode = &result, nil
		if err != nil {
			code := wechatMonitorErrorCode(err)
			c.monitor.State, c.monitor.LastErrorCode = "error", &code
		}
		c.mu.Unlock()
		if !c.waitWechatMonitor(ctx, time.Duration(settings.IntervalSeconds)*time.Second) {
			return
		}
	}
}

func (c *wechatConnection) waitWechatMonitor(ctx context.Context, interval time.Duration) bool {
	if c.monitorWait != nil {
		return c.monitorWait(ctx, interval)
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (h *handler) wechatMonitorCycle(ctx context.Context, peers []string) (WechatReadResult, error) {
	total := WechatReadResult{ReadOnly: true}
	var firstError error
	for _, peer := range peers {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		result, err := h.wechatMonitorReadPeer(ctx, peer)
		if err != nil {
			if firstError == nil {
				firstError = err
			}
			continue
		}
		total.Inserted += result.Inserted
		total.Duplicates += result.Duplicates
		total.Conversations += result.Conversations
		total.Skipped += result.Skipped
		total.Limited = total.Limited || result.Limited
	}
	return total, firstError
}

func (h *handler) wechatMonitorReadPeer(parent context.Context, peer string) (WechatReadResult, error) {
	c := h.wechat
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	select {
	case c.gate <- struct{}{}:
		defer func() { <-c.gate }()
	case <-ctx.Done():
		return WechatReadResult{}, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return WechatReadResult{}, err
	}
	c.mu.Lock()
	c.status.State = "reading"
	c.mu.Unlock()
	result, err := h.readWechatPeer(ctx, peer)
	if err != nil {
		c.mu.Lock()
		if parent.Err() != nil {
			// An operator pause/shutdown is not a source database failure.
			c.status.State = "ready"
			if c.status.LastResult != nil {
				c.status.State = "read"
			}
		} else {
			c.status.State = "unavailable"
		}
		c.mu.Unlock()
	}
	return result, err
}

func wechatMonitorErrorCode(err error) string {
	switch {
	case errors.Is(err, wechatdb.ErrInvalidKey):
		return "WECHAT_INVALID_KEY"
	case errors.Is(err, wechatdb.ErrUnsupportedSchema):
		return "WECHAT_UNSUPPORTED_SCHEMA"
	case errors.Is(err, wechatdb.ErrChanged):
		return "WECHAT_CHANGED"
	case errors.Is(err, wechatdb.ErrNotFound):
		return "WECHAT_NOT_FOUND"
	case errors.Is(err, store.ErrConflict):
		return "IMPORT_CONFLICT"
	case errors.Is(err, context.DeadlineExceeded):
		return "WECHAT_TIMEOUT"
	default:
		return "WECHAT_UNAVAILABLE"
	}
}

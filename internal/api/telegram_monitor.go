package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/qilinbro/msg-manager/internal/connector/telegramuser"
	"github.com/qilinbro/msg-manager/internal/store"
)

const telegramMonitorPath = telegramPath + "/monitor"

type TelegramMonitorStatus struct {
	Enabled           bool     `json:"enabled"`
	IntervalSeconds   int      `json:"interval_seconds"`
	PeerIDs           []string `json:"peer_ids"`
	ExpectedAccountID string   `json:"expected_account_id,omitempty"`
	State             string   `json:"state"`
	LastSuccessAt     *string  `json:"last_success_at"`
	LastErrorCode     *string  `json:"last_error_code"`
	SkippedCount      int      `json:"skipped_count"`
	RetryNotBefore    *string  `json:"retry_not_before"`
	ReadOnly          bool     `json:"read_only"`
	SendingEnabled    bool     `json:"sending_enabled"`
}

// The single encrypted local slot deliberately replaces the prior account's
// subscription. Switching A -> B -> A cannot resurrect a hidden A allowlist.
type telegramMonitorRecord struct {
	Version           int               `json:"version"`
	Enabled           bool              `json:"enabled"`
	IntervalSeconds   int               `json:"interval_seconds"`
	PeerIDs           []string          `json:"peer_ids"`
	ExpectedAccountID string            `json:"expected_account_id"`
	Cursors           map[string]string `json:"cursors"`
	LastSuccessAt     *string           `json:"last_success_at"`
	SkippedCount      int               `json:"skipped_count"`
	RetryNotBefore    *string           `json:"retry_not_before"`
}

type telegramIncrementalReader interface {
	Baseline(context.Context, string, string) (telegramuser.IncrementalBatch, error)
	Scan(context.Context, string, string, string) (telegramuser.IncrementalBatch, error)
}

func defaultTelegramMonitorRecord() telegramMonitorRecord {
	return telegramMonitorRecord{Version: 1, IntervalSeconds: 60, PeerIDs: []string{}, Cursors: map[string]string{}}
}

func cloneTelegramMonitor(v telegramMonitorRecord) telegramMonitorRecord {
	v.PeerIDs = append([]string{}, v.PeerIDs...)
	cursors := map[string]string{}
	for peer, cursor := range v.Cursors {
		cursors[peer] = cursor
	}
	v.Cursors = cursors
	return v
}

func validTelegramMonitor(v telegramMonitorRecord) bool {
	if v.Version != 1 || v.IntervalSeconds < 30 || v.IntervalSeconds > 900 || v.PeerIDs == nil || len(v.PeerIDs) > 10 || v.Cursors == nil || len(v.Cursors) > len(v.PeerIDs) || v.Enabled && len(v.PeerIDs) == 0 || v.SkippedCount < 0 {
		return false
	}
	if (v.Enabled || len(v.PeerIDs) > 0) && !telegramuser.ValidID(v.ExpectedAccountID) || v.ExpectedAccountID != "" && !telegramuser.ValidID(v.ExpectedAccountID) {
		return false
	}
	seen := map[string]bool{}
	for _, peer := range v.PeerIDs {
		if !telegramuser.ValidID(peer) || peer == v.ExpectedAccountID || seen[peer] {
			return false
		}
		seen[peer] = true
	}
	for peer, cursor := range v.Cursors {
		if !seen[peer] || !telegramuser.ValidCursor(cursor) {
			return false
		}
	}
	if v.LastSuccessAt != nil {
		if _, err := time.Parse(time.RFC3339Nano, *v.LastSuccessAt); err != nil {
			return false
		}
	}
	if v.RetryNotBefore != nil {
		if _, err := time.Parse(time.RFC3339Nano, *v.RetryNotBefore); err != nil {
			return false
		}
	}
	return true
}

func (c *telegramConnection) monitorSnapshot() TelegramMonitorStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v := c.monitor
	if v.IntervalSeconds == 0 {
		v = TelegramMonitorStatus{IntervalSeconds: 60, State: "disabled", ReadOnly: true}
	}
	v.PeerIDs = append([]string{}, v.PeerIDs...)
	return v
}

func (c *telegramConnection) publishTelegramMonitor(record telegramMonitorRecord, state string, code *string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.monitorRecord = cloneTelegramMonitor(record)
	c.monitor = TelegramMonitorStatus{Enabled: record.Enabled, IntervalSeconds: record.IntervalSeconds, PeerIDs: append([]string{}, record.PeerIDs...), ExpectedAccountID: record.ExpectedAccountID, State: state, LastSuccessAt: record.LastSuccessAt, LastErrorCode: code, SkippedCount: record.SkippedCount, RetryNotBefore: record.RetryNotBefore, ReadOnly: true}
}

func (h *handler) restoreTelegramMonitor() error {
	if h.telegram == nil || h.inbox == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	raw, err := h.inbox.LoadReceiveMonitor(ctx, "telegram", "local")
	if err != nil {
		return err
	}
	record := defaultTelegramMonitorRecord()
	if len(raw) > 0 {
		if len(raw) > 16384 || !uniqueJSON(raw) {
			return store.ErrUnavailable
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		record = telegramMonitorRecord{}
		if decoder.Decode(&record) != nil || decoder.Decode(new(any)) != io.EOF || !validTelegramMonitor(record) {
			return store.ErrUnavailable
		}
	}
	c := h.telegram
	if err := c.lockTelegramMonitor(ctx); err != nil {
		return err
	}
	defer c.monitorMu.Unlock()
	h.startTelegramMonitorLocked(record)
	return nil
}

func (h *handler) stopTelegramMonitorLocked() {
	c := h.telegram
	if c.monitorCancel != nil {
		c.monitorCancel()
		<-c.monitorDone
		c.monitorCancel = nil
		c.monitorDone = nil
	}
}

func (h *handler) closeTelegramMonitor() {
	if h.telegram == nil {
		return
	}
	c := h.telegram
	c.monitorMu.Lock()
	defer c.monitorMu.Unlock()
	c.monitorClosed = true
	h.stopTelegramMonitorLocked()
	c.mu.Lock()
	c.monitor.State = "stopped"
	c.mu.Unlock()
}

func (h *handler) startTelegramMonitorLocked(record telegramMonitorRecord) {
	c := h.telegram
	state := "disabled"
	if record.Enabled {
		state = "waiting"
	}
	c.publishTelegramMonitor(record, state, nil)
	if !record.Enabled || c.monitorClosed {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	c.monitorCancel, c.monitorDone = cancel, done
	go func() { defer close(done); h.runTelegramMonitor(ctx, cloneTelegramMonitor(record)) }()
}

func (h *handler) saveTelegramMonitor(ctx context.Context, record telegramMonitorRecord) error {
	if !validTelegramMonitor(record) || h.inbox == nil {
		return store.ErrInvalidInput
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return store.ErrInvalidInput
	}
	return h.inbox.SaveReceiveMonitor(ctx, "telegram", "local", raw)
}

func (h *handler) serveTelegramMonitor(w http.ResponseWriter, r *http.Request, traceID string) {
	if r.URL.RawQuery != "" {
		writeError(w, 400, "INVALID_INPUT", "后台接收不接受URL查询参数。", traceID)
		return
	}
	if r.Method == http.MethodGet {
		writeJSON(w, 200, h.telegram.monitorSnapshot())
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, 405, "METHOD_NOT_ALLOWED", "此接口只接受GET或POST。", traceID)
		return
	}
	var input struct {
		Enabled  *bool           `json:"enabled"`
		Interval json.RawMessage `json:"interval_seconds"`
		Peers    json.RawMessage `json:"peer_ids"`
		Expected json.RawMessage `json:"expected_account_id"`
	}
	if !readInboxJSON(w, r, &input, traceID) {
		return
	}
	if input.Enabled == nil {
		writeError(w, 400, "INVALID_INPUT", "请明确开启或暂停后台接收。", traceID)
		return
	}
	c := h.telegram
	if err := c.lockTelegramMonitor(r.Context()); err != nil {
		writeError(w, 503, "TELEGRAM_TIMEOUT", "后台接收设置已取消。", traceID)
		return
	}
	defer c.monitorMu.Unlock()
	if c.monitorClosed || h.inbox == nil {
		writeError(w, 503, "TELEGRAM_STORAGE_DISABLED", "后台或加密消息库不可用。", traceID)
		return
	}
	c.mu.RLock()
	record := cloneTelegramMonitor(c.monitorRecord)
	verified := c.status.State == "identity_verified"
	account := c.status.AccountID
	visible := map[string]bool{}
	for peer, allowed := range c.peers {
		visible[peer] = allowed
	}
	c.mu.RUnlock()
	if record.Version == 0 {
		record = defaultTelegramMonitorRecord()
	}
	next := cloneTelegramMonitor(record)
	next.Enabled = *input.Enabled
	if *input.Enabled {
		next.IntervalSeconds = 60
	}
	if len(input.Interval) > 0 {
		if bytes.Equal(input.Interval, []byte("null")) || json.Unmarshal(input.Interval, &next.IntervalSeconds) != nil {
			writeError(w, 400, "INVALID_INPUT", "读取周期必须是30至900秒。", traceID)
			return
		}
	}
	if len(input.Peers) > 0 {
		if json.Unmarshal(input.Peers, &next.PeerIDs) != nil || next.PeerIDs == nil {
			writeError(w, 400, "INVALID_INPUT", "请明确选择私聊名单。", traceID)
			return
		}
	}
	if len(input.Expected) > 0 {
		if json.Unmarshal(input.Expected, &next.ExpectedAccountID) != nil || !telegramuser.ValidID(next.ExpectedAccountID) {
			writeError(w, 400, "INVALID_INPUT", "请指定已经核验的本人账号。", traceID)
			return
		}
	}
	if *input.Enabled && (len(input.Peers) == 0 || len(input.Expected) == 0) {
		writeError(w, 400, "INVALID_INPUT", "开启前请明确指定本人账号和1至10个私聊。", traceID)
		return
	}
	if !*input.Enabled && next.ExpectedAccountID != record.ExpectedAccountID {
		writeError(w, 400, "INVALID_INPUT", "切换本人账号前请先核验，再明确开启新的接收名单。", traceID)
		return
	}
	if next.ExpectedAccountID != record.ExpectedAccountID {
		next.Cursors = map[string]string{}
		next.LastSuccessAt = nil
		next.SkippedCount = 0
		next.RetryNotBefore = nil
	}
	keep := map[string]bool{}
	for _, peer := range next.PeerIDs {
		keep[peer] = true
	}
	for peer := range next.Cursors {
		if !keep[peer] {
			delete(next.Cursors, peer)
		}
	}
	if !validTelegramMonitor(next) {
		writeError(w, 400, "INVALID_INPUT", "请选择最多10个不同私聊，并设置30至900秒周期。", traceID)
		return
	}
	if *input.Enabled {
		if !verified || account != next.ExpectedAccountID {
			writeError(w, 409, "TELEGRAM_NOT_VERIFIED", "请重新核验本人账号并获取私聊列表。", traceID)
			return
		}
		for _, peer := range next.PeerIDs {
			if !visible[peer] {
				writeError(w, 400, "TELEGRAM_PEER_NOT_SELECTED", "请先获取本人账号的最近私聊列表，再选择联系人。", traceID)
				return
			}
		}
		if _, ok := c.client.(telegramIncrementalReader); !ok {
			writeError(w, 503, "TELEGRAM_HELPER_UNAVAILABLE", "当前读取组件不支持后台接收。", traceID)
			return
		}
	}
	sort.Strings(next.PeerIDs)
	// Join first, then merge the latest committed cursors. An HTTP pause cannot
	// be followed by a late write from the retired worker.
	h.stopTelegramMonitorLocked()
	c.mu.RLock()
	latest := cloneTelegramMonitor(c.monitorRecord)
	c.mu.RUnlock()
	if latest.Version == 1 && latest.ExpectedAccountID == next.ExpectedAccountID {
		for peer, cursor := range latest.Cursors {
			if keep[peer] {
				next.Cursors[peer] = cursor
			}
		}
		next.LastSuccessAt = latest.LastSuccessAt
		next.SkippedCount = latest.SkippedCount
		next.RetryNotBefore = latest.RetryNotBefore
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := h.saveTelegramMonitor(ctx, next); err != nil {
		if latest.Version == 0 {
			latest = record
		}
		h.startTelegramMonitorLocked(latest)
		writeInboxError(w, err, traceID)
		return
	}
	h.startTelegramMonitorLocked(next)
	writeJSON(w, 200, c.monitorSnapshot())
}

func telegramMonitorFailure(err error) (string, time.Duration, bool) {
	code := "TELEGRAM_UNAVAILABLE"
	delay := time.Duration(0)
	blocked := false
	var failure *telegramuser.Error
	if errors.As(err, &failure) {
		switch failure.Code {
		case "flood_wait":
			code = "TELEGRAM_FLOOD_WAIT"
			if failure.RetryAfter > 0 && failure.RetryAfter <= 7*86400 {
				delay = time.Duration(failure.RetryAfter) * time.Second
			}
		case "identity_mismatch", "logged_out", "auth_failed", "config_invalid", "requires_initialization", "helper_unavailable", "unsupported_platform":
			code = "TELEGRAM_" + strings.ToUpper(failure.Code)
			blocked = true
		case "peer_not_found", "protocol_error", "network_error", "timeout":
			code = "TELEGRAM_" + strings.ToUpper(failure.Code)
		}
	}
	if errors.Is(err, store.ErrUnavailable) || errors.Is(err, store.ErrConflict) {
		code = "STORE_UNAVAILABLE"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		code = "TELEGRAM_TIMEOUT"
	}
	return code, delay, blocked
}

func (c *telegramConnection) lockTelegramMonitor(ctx context.Context) error {
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

func (h *handler) runTelegramMonitor(ctx context.Context, record telegramMonitorRecord) {
	c := h.telegram
	reader, ok := c.client.(telegramIncrementalReader)
	if !ok {
		code := "TELEGRAM_HELPER_UNAVAILABLE"
		c.publishTelegramMonitor(record, "blocked", &code)
		return
	}
	for ctx.Err() == nil {
		// Preserve the server's flood deadline across pause, settings and restart.
		if record.RetryNotBefore != nil {
			deadline, _ := time.Parse(time.RFC3339Nano, *record.RetryNotBefore)
			if delay := time.Until(deadline); delay > 0 {
				code := "TELEGRAM_FLOOD_WAIT"
				c.publishTelegramMonitor(record, "flood_wait", &code)
				if !c.waitTelegramMonitor(ctx, delay) {
					return
				}
				continue
			}
			record.RetryNotBefore = nil
		}
		var firstError error
		limited := false
		for _, peer := range record.PeerIDs {
			state := "reading"
			if _, set := record.Cursors[peer]; !set {
				state = "baselining"
			}
			c.publishTelegramMonitor(record, state, nil)
			next, more, err := h.telegramMonitorPeer(ctx, reader, record, peer)
			// A completed commit remains the latest cursor even if pause arrives
			// immediately afterwards; the controller joins before saving settings.
			if err == nil {
				record = next
				c.publishTelegramMonitor(record, state, nil)
			}
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				if firstError == nil {
					firstError = err
				}
				_, _, blocked := telegramMonitorFailure(err)
				var failure *telegramuser.Error
				if blocked || errors.As(err, &failure) && failure.Code == "flood_wait" {
					firstError = err
					break
				}
				continue
			}
			limited = limited || more
		}
		state := "waiting"
		if limited {
			state = "catching_up"
		}
		var code *string
		wait := time.Duration(record.IntervalSeconds) * time.Second
		if firstError != nil {
			value, delay, blocked := telegramMonitorFailure(firstError)
			code = &value
			state = "error"
			if delay > wait {
				wait = delay
			}
			if value == "TELEGRAM_FLOOD_WAIT" {
				state = "flood_wait"
				deadline := time.Now().UTC().Add(wait).Format(time.RFC3339Nano)
				record.RetryNotBefore = &deadline
				saveCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				saveErr := h.saveTelegramMonitor(saveCtx, record)
				cancel()
				if saveErr != nil {
					failure := "STORE_UNAVAILABLE"
					c.publishTelegramMonitor(record, "blocked", &failure)
					return
				}
			}
			if blocked {
				c.publishTelegramMonitor(record, "blocked", code)
				return
			}
		}
		c.publishTelegramMonitor(record, state, code)
		if !c.waitTelegramMonitor(ctx, wait) {
			return
		}
	}
}

func (c *telegramConnection) waitTelegramMonitor(ctx context.Context, wait time.Duration) bool {
	if c.monitorWait != nil {
		return c.monitorWait(ctx, wait)
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (h *handler) telegramMonitorPeer(parent context.Context, reader telegramIncrementalReader, record telegramMonitorRecord, peer string) (telegramMonitorRecord, bool, error) {
	ctx, cancel := context.WithTimeout(parent, 40*time.Second)
	defer cancel()
	select {
	case h.telegram.gate <- struct{}{}:
		defer func() { <-h.telegram.gate }()
	case <-ctx.Done():
		return record, false, ctx.Err()
	}
	cursor, initialized := record.Cursors[peer]
	var batch telegramuser.IncrementalBatch
	var err error
	if initialized {
		batch, err = reader.Scan(ctx, record.ExpectedAccountID, peer, cursor)
	} else {
		batch, err = reader.Baseline(ctx, record.ExpectedAccountID, peer)
	}
	if err != nil {
		return record, false, err
	}
	if batch.AccountID != record.ExpectedAccountID {
		return record, false, &telegramuser.Error{Code: "identity_mismatch"}
	}
	if initialized && !telegramuser.ValidIncremental(batch, record.ExpectedAccountID, peer, cursor) || !initialized && (!telegramuser.ValidBatch(batch.Batch, peer, 1) || batch.AccountID != record.ExpectedAccountID || !telegramuser.ValidCursor(batch.Cursor) || len(batch.Messages) != 0 || batch.Skipped != 0 || batch.Limited) {
		return record, false, &telegramuser.Error{Code: "protocol_error"}
	}
	if ctx.Err() != nil {
		return record, false, ctx.Err()
	}
	if len(batch.Messages) > 0 {
		messages := make([]store.ImportMessage, 0, len(batch.Messages))
		for _, item := range batch.Messages {
			messages = append(messages, store.ImportMessage{ExternalID: item.ExternalID, PeerID: peer, PeerName: batch.PeerName, ConversationKind: "private", Direction: item.Direction, Body: item.Body, OccurredAt: item.OccurredAt})
		}
		if _, err = h.inbox.ImportTelegramContext(ctx, store.ImportRequest{Platform: "telegram", AccountID: record.ExpectedAccountID, AccountName: batch.Name, Messages: messages}); err != nil {
			return record, false, err
		}
	}
	next := cloneTelegramMonitor(record)
	next.Cursors[peer] = batch.Cursor
	if initialized {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		next.LastSuccessAt = &now
		next.SkippedCount += batch.Skipped
	}
	// Crash between import and cursor save replays safely through stable IDs.
	// Never advance the durable cursor before a successful message commit.
	if err = h.saveTelegramMonitor(ctx, next); err != nil {
		return record, false, err
	}
	return next, batch.Limited, nil
}

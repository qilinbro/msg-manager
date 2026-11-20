package api

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/qilinbro/msg-manager/internal/connector/onebot"
	"github.com/qilinbro/msg-manager/internal/store"
)

const maxOneBotPeers = 20
const maxOneBotGaps = 10
const oneBotPollSeconds = 30
const oneBotHistoryCount = 20

type OneBotMonitor struct {
	Enabled   bool     `json:"enabled"`
	State     string   `json:"state"`
	PeerIDs   []string `json:"peer_ids"`
	ReadOnly  bool     `json:"read_only"`
	LastError string   `json:"last_error"`
}

type OneBotReceiveStatus struct {
	Configured bool          `json:"configured"`
	State      string        `json:"state"`
	AccountID  string        `json:"account_id"`
	CheckedAt  string        `json:"checked_at"`
	Receiving  bool          `json:"receiving"`
	Sending    bool          `json:"sending"`
	LastPollAt string        `json:"last_poll_at"`
	LastResult string        `json:"last_result"`
	Skipped    int           `json:"skipped"`
	Gaps       []milkyGap    `json:"gaps"`
	Monitor    OneBotMonitor `json:"monitor"`
}

type OneBotMonitorRequest struct {
	Enabled *bool     `json:"enabled"`
	PeerIDs *[]string `json:"peer_ids"`
}

type OneBotSyncRequest struct {
	PeerID *string `json:"peer_id"`
	Count  *int    `json:"count"`
}

type OneBotSyncResult struct {
	Inserted   int    `json:"inserted"`
	Duplicates int    `json:"duplicates"`
	Skipped    int    `json:"skipped"`
	SourceKind string `json:"source_kind"`
	Sending    bool   `json:"sending_enabled"`
}

type onebotConnection struct {
	client *onebot.Client
	gate   chan struct{}
	mu     sync.RWMutex
	status OneBotReceiveStatus

	monitorMu sync.Mutex
	cancel    context.CancelFunc
	done      chan struct{}
	closed    bool
	peers     []string
}

func (h *handler) onebotReceiveStatus(w http.ResponseWriter) {
	c := h.onebotReceive
	c.mu.RLock()
	status := c.status
	status.Gaps = append([]milkyGap(nil), status.Gaps...)
	status.Monitor.PeerIDs = append([]string(nil), status.Monitor.PeerIDs...)
	c.mu.RUnlock()
	writeJSON(w, http.StatusOK, status)
}

func (h *handler) syncOnebotReceive(w http.ResponseWriter, r *http.Request, traceID string) {
	c := h.onebotReceive
	if c.client == nil || h.inbox == nil {
		writeError(w, http.StatusServiceUnavailable, "QQ_ONEBOT_NOT_CONFIGURED", "请先配置 OneBot 与加密消息库。", traceID)
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "INVALID_INPUT", "同步参数不接受 URL 查询串。", traceID)
		return
	}
	var request OneBotSyncRequest
	if !readInboxJSON(w, r, &request, traceID) {
		return
	}
	peer, count, ok := validateOnebotSync(request)
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_INPUT", "请提供合法联系人 QQ 号与 1..150 条数。", traceID)
		return
	}
	select {
	case c.gate <- struct{}{}:
		defer func() { <-c.gate }()
	default:
		writeError(w, http.StatusServiceUnavailable, "QQ_ONEBOT_BUSY", "OneBot 操作正在进行，请稍后重试。", traceID)
		return
	}
	result, skipped, err := h.fetchOnebotMessages(r.Context(), peer, count)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "QQ_ONEBOT_UNAVAILABLE", onebotReceiveMessage(err), traceID)
		return
	}
	c.mu.Lock()
	c.status.LastPollAt = time.Now().UTC().Format(time.RFC3339)
	c.status.LastResult = "ok"
	c.status.Skipped += skipped
	c.mu.Unlock()
	result.Skipped = skipped
	writeJSON(w, http.StatusOK, result)
}

func validateOnebotSync(request OneBotSyncRequest) (string, int, bool) {
	if request.PeerID == nil || !validMilkyPeer(*request.PeerID) {
		return "", 0, false
	}
	count := oneBotHistoryCount
	if request.Count != nil {
		count = *request.Count
	}
	if count < 1 || count > 150 {
		return "", 0, false
	}
	return *request.PeerID, count, true
}

func (h *handler) fetchOnebotMessages(ctx context.Context, peer string, count int) (OneBotSyncResult, int, error) {
	result := OneBotSyncResult{SourceKind: "onebot_sync", Sending: false}
	history, err := h.onebotReceive.client.FriendHistory(ctx, peer, count)
	if err != nil {
		return result, 0, err
	}
	items := []store.ImportMessage{}
	skipped := 0
	for _, message := range history {
		item, reason, err := h.onebotReceive.client.NormalizeHistory(message, peer, h.onebotReceive.client.SelfID())
		if err != nil || reason != "" {
			skipped++
			continue
		}
		items = append(items, item)
	}
	if len(items) > 0 {
		saved, err := h.inbox.ImportOneBotContext(ctx, store.ImportRequest{
			Platform: "qq", AccountID: h.onebotReceive.client.SelfID(),
			AccountName: "QQ OneBot " + h.onebotReceive.client.SelfID(), Messages: items,
		})
		if err != nil {
			return result, skipped, err
		}
		result.Inserted, result.Duplicates = saved.Inserted, saved.Duplicates
	}
	return result, skipped, nil
}

func (h *handler) configureOnebotMonitor(w http.ResponseWriter, r *http.Request, traceID string) {
	c := h.onebotReceive
	if c.client == nil || h.inbox == nil {
		writeError(w, http.StatusServiceUnavailable, "QQ_ONEBOT_NOT_CONFIGURED", "请先配置 OneBot 与加密消息库。", traceID)
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "INVALID_INPUT", "控制参数不接受 URL 查询串。", traceID)
		return
	}
	var request OneBotMonitorRequest
	if !readInboxJSON(w, r, &request, traceID) {
		return
	}
	if request.Enabled == nil || request.PeerIDs == nil {
		writeError(w, http.StatusBadRequest, "INVALID_INPUT", "请提交 enabled 与 peer_ids。", traceID)
		return
	}
	peers, ok := normalizeOnebotPeers(*request.PeerIDs, *request.Enabled)
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_INPUT", "请填写 1 到 20 个合法私聊 QQ 号。", traceID)
		return
	}
	c.monitorMu.Lock()
	defer c.monitorMu.Unlock()
	if c.closed {
		writeError(w, http.StatusServiceUnavailable, "QQ_ONEBOT_UNAVAILABLE", "后台服务正在停止。", traceID)
		return
	}
	h.stopOnebotMonitorLocked()
	state := "disabled"
	if *request.Enabled {
		state = "running"
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		c.cancel, c.done, c.peers = cancel, done, peers
		go h.runOnebotMonitor(ctx, peers, done)
	}
	c.mu.Lock()
	c.status.Monitor = OneBotMonitor{Enabled: *request.Enabled, State: state, PeerIDs: peers, ReadOnly: true}
	c.status.Receiving = *request.Enabled
	c.mu.Unlock()
	h.onebotReceiveStatus(w)
}

func normalizeOnebotPeers(values []string, enabled bool) ([]string, bool) {
	if len(values) > maxOneBotPeers || (enabled && len(values) == 0) {
		return nil, false
	}
	seen := map[string]bool{}
	peers := make([]string, 0, len(values))
	for _, value := range values {
		if !validMilkyPeer(value) || seen[value] {
			return nil, false
		}
		seen[value] = true
		peers = append(peers, value)
	}
	sort.Strings(peers)
	return peers, true
}

func (h *handler) stopOnebotMonitorLocked() {
	c := h.onebotReceive
	if c.cancel != nil {
		c.cancel()
		if c.done != nil {
			<-c.done
		}
	}
	c.cancel, c.done, c.peers = nil, nil, nil
}

func (h *handler) closeOnebotMonitor() {
	h.onebotReceive.monitorMu.Lock()
	defer h.onebotReceive.monitorMu.Unlock()
	h.onebotReceive.closed = true
	h.stopOnebotMonitorLocked()
}

func (h *handler) runOnebotMonitor(ctx context.Context, peers []string, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(oneBotPollSeconds * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		select {
		case h.onebotReceive.gate <- struct{}{}:
		default:
			continue
		}
		for _, peer := range peers {
			if ctx.Err() != nil {
				break
			}
			pollCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
			_, skipped, err := h.fetchOnebotMessages(pollCtx, peer, oneBotHistoryCount)
			cancel()
			if err != nil {
				h.recordOnebotGap(onebotFailureState(err))
				continue
			}
			c := h.onebotReceive
			c.mu.Lock()
			c.status.LastPollAt = time.Now().UTC().Format(time.RFC3339)
			c.status.LastResult = "ok"
			c.status.Skipped += skipped
			c.mu.Unlock()
		}
		<-h.onebotReceive.gate
	}
}

func (h *handler) recordOnebotGap(reason string) {
	if strings.TrimSpace(reason) == "" {
		reason = "poll_failed"
	}
	c := h.onebotReceive
	c.mu.Lock()
	defer c.mu.Unlock()
	c.status.Gaps = append(c.status.Gaps, milkyGap{At: time.Now().Unix(), Reason: reason})
	if len(c.status.Gaps) > maxOneBotGaps {
		c.status.Gaps = c.status.Gaps[len(c.status.Gaps)-maxOneBotGaps:]
	}
}

func onebotFailureState(err error) string {
	var failure interface{ Error() string }
	if errors.As(err, &failure) {
		return "poll_failed"
	}
	return "poll_failed"
}

func onebotReceiveMessage(err error) string {
	return "OneBot 历史读取未完成，请稍后重试。"
}

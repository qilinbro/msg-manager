package api

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/qilinbro/msg-manager/internal/connector"
	"github.com/qilinbro/msg-manager/internal/connector/adapter"
	"github.com/qilinbro/msg-manager/internal/connector/qqmilky"
	"github.com/qilinbro/msg-manager/internal/store"
)

const maxMilkyPeers = 20
const maxMilkyGaps = 10

type milkyGap struct {
	At     int64  `json:"at"`
	Reason string `json:"reason"`
}

type MilkyMonitor struct {
	Enabled   bool     `json:"enabled"`
	State     string   `json:"state"`
	PeerIDs   []string `json:"peer_ids"`
	ReadOnly  bool     `json:"read_only"`
	LastError string   `json:"last_error"`
}

type MilkyStatus struct {
	Configured       bool         `json:"configured"`
	State            string       `json:"state"`
	AccountID        string       `json:"account_id"`
	Nickname         string       `json:"nickname"`
	MilkyVersion     string       `json:"milky_version"`
	CheckedAt        string       `json:"checked_at"`
	ReceivingEnabled bool         `json:"receiving_enabled"`
	SendingEnabled   bool         `json:"sending_enabled"`
	ReadOnly         bool         `json:"read_only"`
	LastSyncAt       string       `json:"last_sync_at"`
	LastSyncResult   string       `json:"last_sync_result"`
	Skipped          int          `json:"skipped"`
	Gaps             []milkyGap   `json:"gaps"`
	Monitor          MilkyMonitor `json:"monitor"`
}

type MilkyVerification struct {
	ConnectionID     string `json:"connection_id"`
	State            string `json:"state"`
	AccountID        string `json:"account_id"`
	Nickname         string `json:"nickname"`
	ImplName         string `json:"impl_name"`
	ImplVersion      string `json:"impl_version"`
	MilkyVersion     string `json:"milky_version"`
	CheckedAt        string `json:"checked_at"`
	ReceivingEnabled bool   `json:"receiving_enabled"`
	SendingEnabled   bool   `json:"sending_enabled"`
}

type MilkySyncRequest struct {
	PeerID *string `json:"peer_id"`
	Limit  *int    `json:"limit"`
}

type MilkyMonitorRequest struct {
	Enabled *bool     `json:"enabled"`
	PeerIDs *[]string `json:"peer_ids"`
}

type MilkySyncResult struct {
	Inserted       int    `json:"inserted"`
	Duplicates     int    `json:"duplicates"`
	Conversations  int    `json:"conversations"`
	Skipped        int    `json:"skipped"`
	NextCursor     *int64 `json:"next_cursor"`
	SourceKind     string `json:"source_kind"`
	SendingEnabled bool   `json:"sending_enabled"`
}

type milkyConnection struct {
	client *qqmilky.Client
	gate   chan struct{}
	mu     sync.RWMutex
	status MilkyStatus

	monitorMu sync.Mutex
	cancel    context.CancelFunc
	done      chan struct{}
	closed    bool
	peers     []string
}

func newMilkyConnection(cfg qqmilky.Config) (*milkyConnection, error) {
	c := &milkyConnection{
		gate: make(chan struct{}, 1),
		status: MilkyStatus{
			State: "not_configured", ReadOnly: true,
		},
	}
	c.status.Gaps = []milkyGap{}
	c.status.Monitor = MilkyMonitor{State: "disabled", PeerIDs: []string{}, ReadOnly: true}
	if cfg.URL == "" && cfg.AccessToken == "" && cfg.ExpectedSelfID == "" {
		return c, nil
	}
	client, err := qqmilky.NewClient(cfg)
	if err != nil {
		// Never surface provider addresses or access tokens in startup logs.
		return nil, errors.New("QQ_MILKY configuration is invalid; check the local URL, access token, expected account and Milky version")
	}
	c.client = client
	c.status.Configured = true
	c.status.State = "configured"
	c.status.AccountID = client.SelfID()
	c.status.MilkyVersion = qqmilky.DefaultMilkyVersion
	return c, nil
}

func (h *handler) applyMilky(items []connector.Descriptor) {
	if h.milky == nil {
		return
	}
	h.milky.mu.RLock()
	state := h.milky.status.State
	monitor := h.milky.status.Monitor
	monitor.PeerIDs = append([]string(nil), monitor.PeerIDs...)
	configured := h.milky.client != nil
	h.milky.mu.RUnlock()
	for i := range items {
		if items[i].ID != "qq-milky" {
			continue
		}
		items[i].Status = state
		items[i].Capabilities.ReceiveText = configured && monitor.Enabled && h.inbox != nil
		items[i].Capabilities.HistoryImport = configured && h.inbox != nil
		if state == "identity_verified" {
			items[i].Description = "上次检测已核验 QQ 账号与 Milky 版本；这不是持续在线状态。历史同步与显式开启的后台接收可用，发送保持关闭。"
		}
	}
}

func (h *handler) milkyStatus(w http.ResponseWriter) {
	h.milky.mu.RLock()
	status := h.milky.status
	status.Gaps = append([]milkyGap(nil), status.Gaps...)
	status.Monitor.PeerIDs = append([]string(nil), status.Monitor.PeerIDs...)
	h.milky.mu.RUnlock()
	writeJSON(w, http.StatusOK, status)
}

func (h *handler) verifyMilky(w http.ResponseWriter, r *http.Request, traceID string) {
	c := h.milky
	if c.client == nil {
		writeError(w, http.StatusServiceUnavailable, "MILKY_NOT_CONFIGURED", "请先在后台配置 Milky 协议端地址、访问令牌与预期 QQ 账号，再重启服务。", traceID)
		return
	}
	select {
	case c.gate <- struct{}{}:
		defer func() { <-c.gate }()
	default:
		writeError(w, http.StatusServiceUnavailable, "MILKY_BUSY", "QQ Milky 操作正在进行，请稍后重试。", traceID)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	verification, err := c.client.Probe(ctx)
	state := "identity_verified"
	if err != nil {
		state = milkyFailureState(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	c.mu.Lock()
	c.status.State = state
	if err == nil {
		c.status.CheckedAt = now
		c.status.Nickname = verification.Nickname
		c.status.MilkyVersion = verification.MilkyVersion
	}
	c.mu.Unlock()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "MILKY_"+strings.ToUpper(state), milkyFailureMessage(state), traceID)
		return
	}
	writeJSON(w, http.StatusOK, MilkyVerification{
		ConnectionID: "qq-milky", State: state, AccountID: verification.AccountID,
		Nickname: verification.Nickname, ImplName: verification.ImplName,
		ImplVersion: verification.ImplVersion, MilkyVersion: verification.MilkyVersion,
		CheckedAt: now, ReceivingEnabled: false, SendingEnabled: false,
	})
}

func (h *handler) syncMilky(w http.ResponseWriter, r *http.Request, traceID string) {
	c := h.milky
	if c.client == nil {
		writeError(w, http.StatusServiceUnavailable, "MILKY_NOT_CONFIGURED", "请先在后台配置 Milky 协议端，再重启服务。", traceID)
		return
	}
	if h.inbox == nil {
		writeError(w, http.StatusServiceUnavailable, "MILKY_STORAGE_DISABLED", "加密消息库尚未启用，无法保存同步结果。", traceID)
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "INVALID_INPUT", "同步参数不接受 URL 查询串。", traceID)
		return
	}
	var request MilkySyncRequest
	if !readInboxJSON(w, r, &request, traceID) {
		return
	}
	if request.PeerID == nil || !validMilkyPeer(*request.PeerID) {
		writeError(w, http.StatusBadRequest, "INVALID_INPUT", "请提供合法的联系人 QQ 号。", traceID)
		return
	}
	limit := qqmilky.DefaultHistoryLimit
	if request.Limit != nil {
		limit = *request.Limit
	}
	if limit < 1 || limit > qqmilky.MaxHistoryLimit {
		writeError(w, http.StatusBadRequest, "INVALID_INPUT", "同步条数必须在 1 到 30 之间。", traceID)
		return
	}
	select {
	case c.gate <- struct{}{}:
		defer func() { <-c.gate }()
	default:
		writeError(w, http.StatusServiceUnavailable, "MILKY_BUSY", "QQ Milky 操作正在进行，请稍后重试。", traceID)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	messages, next, err := c.client.History(ctx, "friend", *request.PeerID, nil, limit)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "MILKY_"+strings.ToUpper(milkyFailureState(err)), milkyFailureMessage(milkyFailureState(err)), traceID)
		return
	}
	normalized, skipped := h.normalizeMilkyBatch(messages)
	result := MilkySyncResult{Skipped: skipped, NextCursor: next, SourceKind: "protocol_sync", SendingEnabled: false}
	if len(normalized) > 0 {
		saved, err := h.storeMilky(ctx, normalized)
		if err != nil {
			writeInboxError(w, err, traceID)
			return
		}
		result.Inserted, result.Duplicates, result.Conversations = saved.Inserted, saved.Duplicates, saved.Conversations
	}
	c.mu.Lock()
	c.status.LastSyncAt = time.Now().UTC().Format(time.RFC3339)
	c.status.LastSyncResult = "ok"
	c.status.Skipped += skipped
	c.mu.Unlock()
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) normalizeMilkyBatch(messages []qqmilky.IncomingMessage) ([]store.ImportMessage, int) {
	items := make([]store.ImportMessage, 0, len(messages))
	skipped := 0
	for _, message := range messages {
		normalized, err := h.milky.client.Normalize(message, "")
		if err != nil || normalized.SkipReason != "" {
			skipped++
			continue
		}
		items = append(items, store.ImportMessage{
			ExternalID: normalized.ExternalID, PeerID: normalized.PeerID,
			PeerName: normalized.PeerName, Direction: normalized.Direction,
			Body: normalized.Body, OccurredAt: normalized.OccurredAt,
		})
	}
	return items, skipped
}

func (h *handler) storeMilky(ctx context.Context, messages []store.ImportMessage) (store.ImportResult, error) {
	req := store.ImportRequest{
		Platform: "qq", AccountID: h.milky.client.SelfID(),
		AccountName: "QQ Milky " + h.milky.client.SelfID(), Messages: messages,
	}
	return h.inbox.ImportMilkyContext(ctx, req)
}

func (h *handler) configureMilkyMonitor(w http.ResponseWriter, r *http.Request, traceID string) {
	c := h.milky
	if c.client == nil || h.inbox == nil {
		writeError(w, http.StatusServiceUnavailable, "MILKY_NOT_CONFIGURED", "请先配置 Milky 协议端并启用加密消息库。", traceID)
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "INVALID_INPUT", "控制参数不接受 URL 查询串。", traceID)
		return
	}
	var request MilkyMonitorRequest
	if !readInboxJSON(w, r, &request, traceID) {
		return
	}
	if request.Enabled == nil || request.PeerIDs == nil {
		writeError(w, http.StatusBadRequest, "INVALID_INPUT", "请明确提交 enabled 与 peer_ids。", traceID)
		return
	}
	peers, ok := normalizeMilkyPeers(*request.PeerIDs, *request.Enabled)
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_INPUT", "开启后台接收须选择 1 到 20 个明确授权的私聊 QQ 号。", traceID)
		return
	}
	c.mu.RLock()
	verified := c.status.State == "identity_verified"
	c.mu.RUnlock()
	if *request.Enabled && !verified {
		writeError(w, http.StatusServiceUnavailable, "MILKY_NOT_VERIFIED", "请先在设置页完成账号与协议版本核验。", traceID)
		return
	}
	c.monitorMu.Lock()
	defer c.monitorMu.Unlock()
	if c.closed {
		writeError(w, http.StatusServiceUnavailable, "MILKY_UNAVAILABLE", "后台服务正在停止。", traceID)
		return
	}
	h.stopMilkyMonitorLocked()
	state := "disabled"
	if *request.Enabled {
		state = "running"
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		c.cancel, c.done, c.peers = cancel, done, peers
		go h.runMilkyMonitor(ctx, peers, done)
	}
	c.mu.Lock()
	c.status.Monitor = MilkyMonitor{Enabled: *request.Enabled, State: state, PeerIDs: peers, ReadOnly: true}
	c.status.ReceivingEnabled = *request.Enabled
	c.mu.Unlock()
	h.milkyStatus(w)
}

func (h *handler) stopMilkyMonitorLocked() {
	c := h.milky
	if c.cancel != nil {
		c.cancel()
		if c.done != nil {
			<-c.done
		}
	}
	c.cancel, c.done, c.peers = nil, nil, nil
}

func (h *handler) closeMilkyMonitor() {
	h.milky.monitorMu.Lock()
	defer h.milky.monitorMu.Unlock()
	h.milky.closed = true
	h.stopMilkyMonitorLocked()
}

func (h *handler) runMilkyMonitor(ctx context.Context, peers []string, done chan struct{}) {
	defer close(done)
	c := h.milky
	c.client.Run(ctx,
		func(message qqmilky.NormalizedMessage) error {
			if message.SkipReason != "" {
				h.recordMilkyGap("skipped:" + message.SkipReason)
				return nil
			}
			_, err := h.storeMilky(ctx, []store.ImportMessage{{
				ExternalID: message.ExternalID, PeerID: message.PeerID,
				PeerName: message.PeerName, Direction: message.Direction,
				Body: message.Body, OccurredAt: message.OccurredAt,
			}})
			return err
		},
		func(gap qqmilky.Gap) {
			h.recordMilkyGap(gap.Reason)
			// Milky has no replay: bounded reconciliation after reconnect.
			reconcile, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			for _, peer := range peers {
				messages, _, err := c.client.History(reconcile, "friend", peer, nil, qqmilky.DefaultHistoryLimit)
				if err != nil {
					continue
				}
				items, _ := h.normalizeMilkyBatch(messages)
				if len(items) > 0 {
					_, _ = h.storeMilky(reconcile, items)
				}
			}
		})
}

func (h *handler) recordMilkyGap(reason string) {
	h.milky.mu.Lock()
	defer h.milky.mu.Unlock()
	h.milky.status.Gaps = append(h.milky.status.Gaps, milkyGap{At: time.Now().Unix(), Reason: reason})
	if len(h.milky.status.Gaps) > maxMilkyGaps {
		h.milky.status.Gaps = h.milky.status.Gaps[len(h.milky.status.Gaps)-maxMilkyGaps:]
	}
}

func normalizeMilkyPeers(values []string, enabled bool) ([]string, bool) {
	if len(values) > maxMilkyPeers || (enabled && len(values) == 0) {
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

func validMilkyPeer(value string) bool {
	id, ok := adapter.ExactID([]byte(`"` + value + `"`))
	return ok && id == value && len(value) <= 20
}

func milkyFailureState(err error) string {
	var failure *adapter.Error
	if errors.As(err, &failure) {
		switch failure.Kind {
		case adapter.NotConfigured, adapter.InvalidConfig, adapter.Unreachable, adapter.AuthFailed,
			adapter.IdentityMismatch, adapter.LoggedOut, adapter.VersionMismatch, adapter.ProtocolError:
			return string(failure.Kind)
		}
	}
	return "unreachable"
}

func milkyFailureMessage(state string) string {
	switch state {
	case "auth_failed":
		return "Milky 协议端鉴权失败，请检查后台配置的访问令牌。"
	case "identity_mismatch":
		return "协议端登录的 QQ 与预期账号不一致，未通过核验。"
	case "logged_out":
		return "协议端账号当前未登录或不在线。"
	case "version_mismatch":
		return "协议端实现的 Milky 版本不是本产品锁定的版本。"
	case "invalid_config":
		return "协议端配置或请求参数不合法。"
	case "protocol_error":
		return "协议端返回的数据与 Milky 契约不匹配。"
	default:
		return "暂时无法连接 Milky 协议端，请确认本机服务正在运行。"
	}
}

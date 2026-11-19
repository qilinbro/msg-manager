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
	client    *qqmilky.Client
	gate      chan struct{}
	mu        sync.RWMutex
	status    MilkyStatus
	persisted store.QQMilkyMonitorSettings

	// Protected by mu: an in-flight probe cannot undo a newer revocation.
	verificationEpoch uint64

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

// restoreMilkyMonitor loads the explicit allowlist without contacting the
// protocol endpoint. The provider must be verified after restart before the
// saved worker is allowed to run.
func (h *handler) restoreMilkyMonitor() error {
	c := h.milky
	if c.client == nil || h.inbox == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	settings, err := h.inbox.LoadQQMilkyMonitor(ctx, c.client.SelfID())
	if err != nil {
		return err
	}
	c.monitorMu.Lock()
	defer c.monitorMu.Unlock()
	c.persisted = settings
	state := "disabled"
	if settings.Enabled {
		state = "pending_verification"
	}
	c.mu.Lock()
	c.status.Monitor = MilkyMonitor{Enabled: settings.Enabled, State: state, PeerIDs: append([]string{}, settings.PeerIDs...), ReadOnly: true}
	c.status.ReceivingEnabled = false
	c.mu.Unlock()
	return nil
}

// startMilkyMonitorLocked starts only an already verified, persisted
// authorization. The caller must hold monitorMu. Publish the receive state
// before launching the worker, so a fast fatal gap cannot be overwritten.
func (h *handler) startMilkyMonitorLocked(settings store.QQMilkyMonitorSettings) {
	c := h.milky
	h.stopMilkyMonitorLocked()
	c.persisted = store.QQMilkyMonitorSettings{Enabled: settings.Enabled, PeerIDs: append([]string{}, settings.PeerIDs...)}
	var ctx context.Context
	state, lastError := "disabled", ""
	c.mu.Lock()
	if settings.Enabled {
		state = "pending_verification"
		if c.status.State == "identity_verified" && !c.closed {
			state = "running"
			ctx, c.cancel = context.WithCancel(context.Background())
			c.done, c.peers = make(chan struct{}), append([]string{}, settings.PeerIDs...)
		} else if milkyRequiresFreshVerification(c.status.State) {
			lastError = c.status.State
		}
	}
	done := c.done
	c.status.Monitor = MilkyMonitor{Enabled: settings.Enabled, State: state, PeerIDs: append([]string{}, settings.PeerIDs...), ReadOnly: true, LastError: lastError}
	c.status.ReceivingEnabled = state == "running"
	c.mu.Unlock()
	if ctx != nil {
		go h.runMilkyMonitor(ctx, append([]string{}, settings.PeerIDs...), done)
	}
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
		items[i].Capabilities.ReceiveText = configured && monitor.State == "running" && h.inbox != nil
		items[i].Capabilities.HistoryImport = configured && h.inbox != nil
		if state == "identity_verified" {
			items[i].Description = "上次检测已核验 QQ 账号与 Milky 版本；这不是持续在线状态。历史同步与显式开启的后台接收可用，发送保持关闭。"
		}
	}
}

func (h *handler) milkyStatus(w http.ResponseWriter) {
	h.milky.mu.RLock()
	status := h.milky.status
	status.Gaps = append([]milkyGap{}, status.Gaps...)
	status.Monitor.PeerIDs = append([]string{}, status.Monitor.PeerIDs...)
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
	c.mu.RLock()
	epoch := c.verificationEpoch
	c.mu.RUnlock()
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	verification, err := c.client.Probe(ctx)
	c.monitorMu.Lock()
	defer c.monitorMu.Unlock()
	if err != nil {
		state := milkyFailureState(err)
		h.invalidateMilkyVerification(state)
		h.stopMilkyMonitorLocked()
		writeError(w, http.StatusServiceUnavailable, "MILKY_"+strings.ToUpper(state), milkyFailureMessage(state), traceID)
		return
	}
	// A terminal worker keeps its join handle until an external controller
	// reaps it. Never ask the worker to join itself from its gap callback.
	c.mu.RLock()
	restart := c.cancel == nil || c.status.Monitor.State == "pending_verification"
	c.mu.RUnlock()
	if c.done != nil {
		select {
		case <-c.done:
			restart = true
		default:
		}
	}
	if restart {
		h.stopMilkyMonitorLocked()
	}
	now := time.Now().UTC().Format(time.RFC3339)
	c.mu.Lock()
	if c.verificationEpoch != epoch {
		// A fatal event detected during this probe is newer evidence. Only a
		// subsequent explicit fresh verification may restore authorization.
		state := c.status.State
		c.mu.Unlock()
		h.stopMilkyMonitorLocked()
		writeError(w, http.StatusServiceUnavailable, "MILKY_"+strings.ToUpper(state), milkyFailureMessage(state), traceID)
		return
	}
	c.status.State = "identity_verified"
	c.status.CheckedAt = now
	c.status.Nickname = verification.Nickname
	c.status.MilkyVersion = verification.MilkyVersion
	c.mu.Unlock()
	// Constructing a handler or a successful manual history read never resumes
	// the saved receive authorization; this explicit fresh probe does.
	if restart && c.persisted.Enabled && !c.closed && h.inbox != nil {
		h.startMilkyMonitorLocked(c.persisted)
	}
	c.mu.RLock()
	receiving := c.status.ReceivingEnabled
	c.mu.RUnlock()
	writeJSON(w, http.StatusOK, MilkyVerification{
		ConnectionID: "qq-milky", State: "identity_verified", AccountID: verification.AccountID,
		Nickname: verification.Nickname, ImplName: verification.ImplName,
		ImplVersion: verification.ImplVersion, MilkyVersion: verification.MilkyVersion,
		CheckedAt: now, ReceivingEnabled: receiving, SendingEnabled: false,
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
		state := milkyFailureState(err)
		if milkyRequiresFreshVerification(state) {
			h.invalidateMilkyVerification(state)
		}
		writeError(w, http.StatusServiceUnavailable, "MILKY_"+strings.ToUpper(state), milkyFailureMessage(state), traceID)
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
	c.monitorMu.Lock()
	defer c.monitorMu.Unlock()
	if c.closed {
		writeError(w, http.StatusServiceUnavailable, "MILKY_UNAVAILABLE", "后台服务正在停止。", traceID)
		return
	}
	settings := store.QQMilkyMonitorSettings{Enabled: *request.Enabled, PeerIDs: peers}
	old := c.persisted
	// Join before checking verification or changing durable authorization: an
	// old worker's fatal gap must not race a stale enable decision. If saving
	// fails, restore the old authorization only while it is still verified.
	h.stopMilkyMonitorLocked()
	c.mu.RLock()
	verified := c.status.State == "identity_verified"
	c.mu.RUnlock()
	if *request.Enabled && !verified {
		writeError(w, http.StatusServiceUnavailable, "MILKY_NOT_VERIFIED", "请先在设置页完成账号与协议版本核验。", traceID)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := h.inbox.SaveQQMilkyMonitor(ctx, c.client.SelfID(), settings); err != nil {
		c.mu.RLock()
		verifiedNow := c.status.State == "identity_verified"
		c.mu.RUnlock()
		if verifiedNow {
			h.startMilkyMonitorLocked(old)
		} else {
			c.persisted = store.QQMilkyMonitorSettings{Enabled: old.Enabled, PeerIDs: append([]string{}, old.PeerIDs...)}
			monitorState := "disabled"
			if old.Enabled {
				monitorState = "pending_verification"
			}
			c.mu.Lock()
			c.status.Monitor = MilkyMonitor{Enabled: old.Enabled, State: monitorState, PeerIDs: append([]string{}, old.PeerIDs...), ReadOnly: true}
			c.status.ReceivingEnabled = false
			c.mu.Unlock()
		}
		writeInboxError(w, err, traceID)
		return
	}
	h.startMilkyMonitorLocked(settings)
	h.milkyStatus(w)
}

func milkyRequiresFreshVerification(state string) bool {
	return state == string(adapter.IdentityMismatch) || state == string(adapter.VersionMismatch)
}

// invalidateMilkyVerification may run inside the worker. It cancels without
// joining or taking monitorMu, which an external stop-and-join may already hold.
// The persisted allowlist is retained, but only explicit fresh verification can
// restore the receive session. All cancel writes hold both monitorMu and mu.
func (h *handler) invalidateMilkyVerification(state string) {
	c := h.milky
	c.mu.Lock()
	defer c.mu.Unlock()
	c.verificationEpoch++
	c.status.State = state
	c.status.ReceivingEnabled = false
	if c.status.Monitor.Enabled {
		c.status.Monitor.State = "pending_verification"
		c.status.Monitor.LastError = state
	}
	if c.cancel != nil {
		c.cancel()
	}
}

func (h *handler) stopMilkyMonitorLocked() {
	c := h.milky
	c.mu.Lock()
	cancel, done := c.cancel, c.done
	c.cancel, c.done, c.peers = nil, nil, nil
	c.mu.Unlock()
	if cancel != nil {
		cancel()
		if done != nil {
			<-done
		}
	}
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
	allowed := make(map[string]struct{}, len(peers))
	for _, peer := range peers {
		allowed[peer] = struct{}{}
	}
	c.client.Run(ctx,
		func(message qqmilky.NormalizedMessage) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if _, ok := allowed[message.PeerID]; !ok {
				// The protocol stream is account-scoped but not allowlist-scoped;
				// never persist an event for an unselected private chat.
				h.recordMilkyGap("ignored:unselected_peer")
				return nil
			}
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
			if milkyRequiresFreshVerification(gap.Reason) {
				h.invalidateMilkyVerification(gap.Reason)
				return
			}
			// Milky has no replay. Only ordinary transport gaps may trigger
			// bounded history reads, each with its own fresh account/version
			// check and whole-page peer validation in the connector.
			reconcile, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			for _, peer := range peers {
				if reconcile.Err() != nil {
					return
				}
				messages, _, err := c.client.History(reconcile, "friend", peer, nil, qqmilky.DefaultHistoryLimit)
				if err != nil {
					state := milkyFailureState(err)
					h.recordMilkyGap("reconcile:" + state)
					if milkyRequiresFreshVerification(state) {
						h.invalidateMilkyVerification(state)
						return
					}
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

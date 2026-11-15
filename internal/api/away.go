package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/qilinbro/msg-manager/internal/agent"
	"github.com/qilinbro/msg-manager/internal/connector/mailbox"
	"github.com/qilinbro/msg-manager/internal/store"
)

const awayPath = "/api/v1/away-sessions"

type awayMailbox interface {
	Probe(context.Context) (mailbox.Info, error)
	FetchSince(context.Context, uint32, uint32) (mailbox.Batch, error)
}

type awayAnalyzer interface {
	AnalyzeAway(context.Context, agent.AwayInput) (agent.AwayOutput, error)
}

type awayWorker struct {
	stop         context.CancelFunc
	done         chan struct{}
	wake         chan struct{}
	mu           sync.Mutex
	activeID     string
	activeCancel context.CancelFunc
}

type awayResponse struct {
	store.AwaySnapshot
	Account        string `json:"account,omitempty"`
	Model          string `json:"model,omitempty"`
	HandoffEnabled bool   `json:"handoff_enabled"`
}

func awayResource(method, path string) (action, id, task string) {
	if path == awayPath {
		switch method {
		case http.MethodGet:
			return "get", "", ""
		case http.MethodPost:
			return "start", "", ""
		}
	}
	if path == awayPath+"/finfold" && method == http.MethodPost {
		return "finfold", "", ""
	}
	parts := strings.Split(strings.TrimPrefix(path, awayPath+"/"), "/")
	if !strings.HasPrefix(path, awayPath+"/") || !aiResourceID(parts[0]) {
		return "", "", ""
	}
	if len(parts) == 2 && parts[1] == "stop" && method == http.MethodPost {
		return "stop", parts[0], ""
	}
	if len(parts) == 3 && parts[1] == "tasks" && aiResourceID(parts[2]) && method == http.MethodPatch {
		return "update", parts[0], parts[2]
	}
	return "", "", ""
}

func awayMutation(method, path string) bool {
	action, _, _ := awayResource(method, path)
	return action != "" && action != "get"
}

func (h *handler) awayInputs() (awayMailbox, awayAnalyzer, string, string) {
	reader, _ := h.email.client.(awayMailbox)
	model, _ := h.ai.client.(awayAnalyzer)
	h.email.mu.RLock()
	defer h.email.mu.RUnlock()
	var account, folder string
	if h.email.status.Account != nil && h.email.status.Folder != nil {
		account, folder = *h.email.status.Account, *h.email.status.Folder
	}
	return reader, model, account, folder
}

func (h *handler) awayAccountKey(folder string) string { return h.email.accountID + ":" + folder }

func (h *handler) sendAwaySnapshot(w http.ResponseWriter, trace string) {
	snapshot, err := h.inbox.AwaySnapshot()
	if err != nil {
		writeInboxError(w, err, trace)
		return
	}
	reader, model, account, _ := h.awayInputs()
	result := awayResponse{AwaySnapshot: snapshot, HandoffEnabled: h.finfold != nil}
	if reader != nil {
		result.Account = account
	}
	if model != nil {
		result.Model = h.ai.model + " · " + h.ai.endpointHost
	}
	writeJSON(w, 200, result)
}

func (h *handler) serveAway(w http.ResponseWriter, r *http.Request, trace, action, id, task string) {
	if h.inbox == nil {
		writeError(w, 503, "STORAGE_DISABLED", "请先启用本地加密存储。", trace)
		return
	}
	switch action {
	case "get":
		h.sendAwaySnapshot(w, trace)
	case "start":
		h.startAway(w, r, trace)
	case "stop":
		err := h.inbox.FinishAway(id, "stopped", "")
		if err != nil && !errors.Is(err, store.ErrAwayInactive) {
			writeInboxError(w, err, trace)
			return
		}
		h.away.cancelSession(id)
		h.sendAwaySnapshot(w, trace)
	case "update":
		var input struct {
			Draft *string `json:"draft"`
			Done  *bool   `json:"done"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
		if !readInboxJSON(w, r, &input, trace) {
			return
		}
		if input.Draft != nil {
			text := strings.TrimSpace(*input.Draft)
			if text == "" || utf8.RuneCountInString(text) > 8000 || strings.IndexFunc(text, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' && r != '\r' }) >= 0 {
				writeInboxError(w, store.ErrInvalidInput, trace)
				return
			}
			input.Draft = &text
		}
		if input.Draft == nil && input.Done == nil {
			writeInboxError(w, store.ErrInvalidInput, trace)
			return
		}
		if err := h.inbox.UpdateAwayTask(id, task, input.Draft, input.Done); err != nil {
			writeInboxError(w, err, trace)
			return
		}
		h.sendAwaySnapshot(w, trace)
	case "finfold":
		h.handoffFinfold(w, r, trace)
	}
}

func (h *handler) startAway(w http.ResponseWriter, r *http.Request, trace string) {
	var input struct {
		Minutes int    `json:"minutes"`
		Sender  string `json:"sender"`
		Consent bool   `json:"consent"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if !readInboxJSON(w, r, &input, trace) {
		return
	}
	input.Sender = strings.TrimSpace(input.Sender)
	address, err := mail.ParseAddress(input.Sender)
	if !input.Consent || err != nil || address.Address != input.Sender || len(input.Sender) > 254 || (input.Minutes != 15 && input.Minutes != 30 && input.Minutes != 60 && input.Minutes != 120) {
		writeError(w, 400, "AWAY_INVALID_SCOPE", "请核对发件人、时长和模型处理授权。", trace)
		return
	}
	reader, model, account, folder := h.awayInputs()
	if reader == nil || model == nil || account == "" {
		writeError(w, 503, "AWAY_NOT_CONFIGURED", "请先在本机配置只读邮箱和模型。", trace)
		return
	}
	active, err := h.inbox.RunningAway()
	if err != nil {
		writeInboxError(w, err, trace)
		return
	}
	if active != nil && time.Now().Before(active.EndsAt) {
		writeError(w, 409, "AWAY_RUNNING", "已有一次接班正在运行。", trace)
		return
	}
	select {
	case h.email.gate <- struct{}{}:
		defer func() { <-h.email.gate }()
	default:
		writeError(w, 409, "EMAIL_BUSY", "邮箱正在同步，请稍后开始。", trace)
		return
	}
	info, err := reader.Probe(r.Context())
	if err != nil || !validAwayMailbox(info, account, folder) {
		writeError(w, 503, "AWAY_EMAIL_UNAVAILABLE", "无法确认邮箱的只读状态或收件位置。", trace)
		return
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		writeInboxError(w, store.ErrUnavailable, trace)
		return
	}
	now := time.Now().UTC()
	session := store.AwaySession{ID: hex.EncodeToString(random[:]), StartedAt: now, EndsAt: now.Add(time.Duration(input.Minutes) * time.Minute), Sender: input.Sender, AccountKey: h.awayAccountKey(folder), ModelKey: h.ai.configID, UIDValidity: info.UIDValidity, NextUID: info.UIDNext}
	if err := h.inbox.StartAway(session); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, 409, "AWAY_RUNNING", "已有一次接班正在运行。", trace)
			return
		}
		writeInboxError(w, err, trace)
		return
	}
	h.away.notify()
	h.sendAwaySnapshot(w, trace)
}

func validAwayMailbox(info mailbox.Info, account, folder string) bool {
	return info.ReadOnly && info.UIDValidity > 0 && info.UIDNext > 0 && info.AccountID == account && info.Folder == folder
}

func (w *awayWorker) notify() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *awayWorker) cancelSession(id string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.activeID == id {
		w.activeCancel()
	}
}

func (h *handler) startAwayWorker() {
	ctx, cancel := context.WithCancel(context.Background())
	h.away = &awayWorker{stop: cancel, done: make(chan struct{}), wake: make(chan struct{}, 1)}
	go h.runAway(ctx)
}

func (h *handler) runAway(ctx context.Context) {
	defer close(h.away.done)
	if active, err := h.inbox.RunningAway(); err == nil && active != nil {
		_ = h.inbox.RecoverAwayTask(active.ID)
	}
	ticker := time.NewTicker(6 * time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		h.awayCycle(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-h.away.wake:
		}
	}
}

func (h *handler) awayCycle(ctx context.Context) {
	item, err := h.inbox.RunningAway()
	if err != nil || item == nil {
		return
	}
	if !time.Now().Before(item.EndsAt) {
		_ = h.inbox.FinishAway(item.ID, "ended", "")
		return
	}
	reader, model, account, folder := h.awayInputs()
	if reader == nil || model == nil || h.awayAccountKey(folder) != item.AccountKey || h.ai.configID != item.ModelKey {
		_ = h.inbox.FinishAway(item.ID, "stopped", "邮箱或模型配置已变更，请重新授权。")
		return
	}
	cycle, cancel := context.WithDeadline(ctx, item.EndsAt)
	w := h.away
	w.mu.Lock()
	w.activeID, w.activeCancel = item.ID, cancel
	w.mu.Unlock()
	defer func() {
		cancel()
		w.mu.Lock()
		w.activeID, w.activeCancel = "", nil
		w.mu.Unlock()
	}()
	// A stop may have committed while this cycle was installing its cancel hook.
	active, err := h.inbox.RunningAway()
	if err != nil || active == nil || active.ID != item.ID {
		return
	}
	h.pollAway(cycle, *item, reader, account, folder)
	h.processAway(cycle, item.ID, model)
}

func (h *handler) pollAway(ctx context.Context, session store.AwaySession, reader awayMailbox, account, folder string) {
	select {
	case h.email.gate <- struct{}{}:
		defer func() { <-h.email.gate }()
	case <-ctx.Done():
		return
	default:
		return
	}
	batch, err := reader.FetchSince(ctx, session.UIDValidity, session.NextUID)
	if ctx.Err() != nil {
		return
	}
	var providerErr *mailbox.Error
	if errors.As(err, &providerErr) && providerErr.Kind == mailbox.MailboxChanged {
		_ = h.inbox.FinishAway(session.ID, "stopped", "邮箱收件位置已变更，请重新授权。")
		return
	}
	if err != nil || !validAwayMailbox(batch.Info, account, folder) || batch.Info.UIDValidity != session.UIDValidity || batch.Info.UIDNext < session.NextUID {
		_ = h.inbox.AwaySyncError(session.ID, emailFailureMessage(emailFailureState(err)))
		return
	}
	for _, item := range batch.Mails {
		if ctx.Err() != nil {
			return
		}
		if item.UID < session.NextUID || item.UID >= batch.Info.UIDNext ||
			item.InternalDate.IsZero() || item.InternalDate.Before(session.StartedAt.Truncate(time.Second)) ||
			!item.InternalDate.Before(session.EndsAt) || !sameMailAddress(item.FromAddress, session.Sender) {
			continue
		}
		message := emailImportMessage(item, folder, batch.Info.UIDValidity)
		req := store.ImportRequest{Platform: "email", AccountID: h.email.accountID, AccountName: mailLabel(account, 128), Messages: []store.ImportMessage{message}}
		if err := h.inbox.QueueAwayMail(session.ID, req, item.InternalDate); err != nil {
			if !errors.Is(err, store.ErrAwayInactive) {
				_ = h.inbox.AwaySyncError(session.ID, "本地保存失败，稍后重试。")
			}
			return
		}
	}
	if batch.Info.UIDNext > session.NextUID {
		if batch.Skipped > 0 || batch.Truncated && uint64(batch.Info.UIDNext)-uint64(session.NextUID) > uint64(len(batch.Mails)) {
			_ = h.inbox.AwaySyncError(session.ID, "同步只覆盖邮箱最近一批，部分来信可能超出读取窗口或格式范围。")
		}
		_ = h.inbox.AdvanceAway(session.ID, batch.Info.UIDNext)
	}
}

func sameMailAddress(a, b string) bool {
	localA, domainA, okA := strings.Cut(a, "@")
	localB, domainB, okB := strings.Cut(b, "@")
	return okA && okB && localA == localB && strings.EqualFold(domainA, domainB)
}

func (h *handler) processAway(ctx context.Context, id string, model awayAnalyzer) {
	for ctx.Err() == nil {
		select {
		case h.ai.gate <- struct{}{}:
		default:
			return
		}
		task, err := h.inbox.TakeAwayTask(id)
		if err != nil || task == nil {
			<-h.ai.gate
			return
		}
		h.analyzeAwayTask(ctx, id, model, *task)
		<-h.ai.gate
	}
}

func (h *handler) analyzeAwayTask(ctx context.Context, id string, model awayAnalyzer, task store.AwayTask) {
	if ctx.Err() != nil {
		return
	}
	_, err := h.inbox.ReserveAICall(aiDay(), task.ConversationID, h.ai.dailyLimit)
	if err != nil {
		_ = h.inbox.CompleteAwayTask(id, task, "本地模型额度或存储不可用，已保留待办。")
		return
	}
	if ctx.Err() != nil {
		return
	}
	result, err := model.AnalyzeAway(ctx, agent.AwayInput{Subject: task.Subject, Body: task.Body, ReceivedAt: task.ReceivedAt})
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		_ = h.inbox.CompleteAwayTask(id, task, "模型未返回可用草稿，已保留待办。")
		return
	}
	task.Title, task.Requirements, task.Draft = result.Title, result.Requirements, result.Draft
	if result.DeadlineQuote != "" && strings.Contains(task.Body, result.DeadlineQuote) {
		task.Evidence = result.DeadlineQuote
		task.Deadline = agent.AwayDeadline(task.Evidence, task.ReceivedAt)
	}
	_ = h.inbox.CompleteAwayTask(id, task, "")
}

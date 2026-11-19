package api

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/qilinbro/msg-manager/internal/agent"
	"github.com/qilinbro/msg-manager/internal/connector/feishu"
	"github.com/qilinbro/msg-manager/internal/store"
)

// All accepted jobs are bounded and owned by handler lifetime, not the POST's
// request context. There is no persistent retry queue; only a durable one-shot
// claim and read-only recovery. wg.Add and Close are serialized by mu.
type feishuRuntime struct {
	mu         sync.Mutex
	wg         sync.WaitGroup
	ctx        context.Context
	cancel     context.CancelFunc
	operations map[string]context.CancelFunc
	closed     bool
	started    bool
	now        func() time.Time
	sender     feishu.Sender
	transport  string
}

func newFeishuRuntime(sender feishu.Sender, transport string) *feishuRuntime {
	ctx, cancel := context.WithCancel(context.Background())
	return &feishuRuntime{ctx: ctx, cancel: cancel, operations: map[string]context.CancelFunc{}, now: time.Now, sender: sender, transport: transport}
}
func (f *feishuRuntime) available() bool {
	if f == nil || f.sender == nil {
		return false
	}
	if runtime, ok := f.sender.(interface{ Available() bool }); ok {
		return runtime.Available()
	}
	return true
}
func (f *feishuRuntime) begin(id string, timeout time.Duration) (context.Context, func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed || f.ctx.Err() != nil {
		return nil, nil, store.ErrUnavailable
	}
	if _, exists := f.operations[id]; exists {
		return nil, nil, errFeishuBusy
	}
	ctx, cancel := context.WithTimeout(f.ctx, timeout)
	f.operations[id] = cancel
	f.wg.Add(1)
	var once sync.Once
	finish := func() {
		once.Do(func() { cancel(); f.mu.Lock(); delete(f.operations, id); f.mu.Unlock(); f.wg.Done() })
	}
	return ctx, finish, nil
}
func (f *feishuRuntime) cancelOperations() {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, cancel := range f.operations {
		cancel()
	}
}
func (f *feishuRuntime) close() {
	if f == nil {
		return
	}
	f.mu.Lock()
	f.closed = true
	f.cancel()
	for _, cancel := range f.operations {
		cancel()
	}
	f.mu.Unlock()
	f.wg.Wait()
	if closer, ok := f.sender.(io.Closer); ok {
		_ = closer.Close()
	}
}
func (h *handler) startFeishuBackground(ctx context.Context) error {
	if h.feishu == nil || h.inbox == nil {
		return nil
	}
	f := h.feishu
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return store.ErrUnavailable
	}
	if f.started {
		return nil
	}
	// Before any new job can be accepted, resolve leftover running claims and
	// skip all offline boundaries. Constructors deliberately do not perform this.
	if err := h.inbox.RecoverFeishuRunning(ctx, f.now()); err != nil {
		return err
	}
	if err := h.inbox.SkipFeishuBacklog(ctx, f.now(), true); err != nil {
		return err
	}
	f.started = true
	f.wg.Add(1)
	stop := context.AfterFunc(ctx, f.cancel)
	go func() {
		defer f.wg.Done()
		defer stop()
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-f.ctx.Done():
				return
			case <-ticker.C:
				h.feishuTick(f.ctx)
			}
		}
	}()
	return nil
}
func (h *handler) feishuTick(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := lockFeishu(ctx, &h.aiMu, false); err != nil {
		return
	}
	defer h.aiMu.RUnlock()
	if err := h.inbox.SkipFeishuBacklog(ctx, h.feishu.now(), false); err != nil {
		return
	}
	schedule, err := h.inbox.LoadFeishuSchedule(ctx)
	if err != nil || !schedule.Enabled || schedule.NextWindowEnd == nil {
		return
	}
	end, err := time.Parse(time.RFC3339Nano, *schedule.NextWindowEnd)
	if err != nil || h.feishu.now().Before(end) {
		return
	}
	if err = h.validateFeishuBinding(ctx, schedule.FeishuBinding, true); err != nil {
		return
	}
	// Busy does not burn the window: another tick may acquire within the explicit
	// two-minute grace. Accepted running/terminal rows are never retried.
	_, _, _ = h.startFeishuPreview(ctx, schedule.FeishuBinding, &schedule, "")
}
func (h *handler) startFeishuPreview(ctx context.Context, b store.FeishuBinding, schedule *store.FeishuSchedule, authorization string) (*store.FeishuReport, bool, error) {
	now := h.feishu.now()
	start, end := store.FeishuWindow(now)
	if binding, ok := ctx.Value(assistantDispatchKey{}).(assistantDispatchBinding); ok && binding.feishuWindowEnd != "" && binding.feishuWindowEnd != store.FeishuTime(end) {
		return nil, false, store.ErrStaleContext
	}
	if schedule != nil {
		if schedule.NextWindowEnd == nil {
			return nil, false, store.ErrStaleContext
		}
		var err error
		end, err = time.Parse(time.RFC3339Nano, *schedule.NextWindowEnd)
		if err != nil {
			return nil, false, store.ErrUnavailable
		}
		start = end.Add(-12 * time.Hour)
	}
	id := store.FeishuReportID(store.FeishuTime(end))
	if schedule == nil {
		report, err := h.readFeishuReport(ctx, id, false)
		if err != nil {
			return nil, false, err
		}
		if report != nil {
			return report, false, nil
		}
	}
	if h.ai == nil || h.ai.client == nil {
		return nil, false, &agent.Error{Kind: agent.NotConfigured}
	}
	ai := h.ai
	select {
	case ai.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, false, ctx.Err()
	default:
		return nil, false, errFeishuBusy
	}
	releaseGate := true
	defer func() {
		if releaseGate {
			<-ai.gate
		}
	}()
	if err := lockFeishu(ctx, &h.feishuDispatchMu, false); err != nil {
		return nil, false, err
	}
	defer h.feishuDispatchMu.RUnlock()
	if err := h.validateFeishuBinding(ctx, b, true); err != nil {
		return nil, false, err
	}
	messages, err := h.inbox.LoadFeishuMessages(ctx, store.FeishuTime(start), store.FeishuTime(end), 501)
	if err != nil {
		return nil, false, err
	}
	report, groups := selectFeishuInput(messages)
	report.ID = id
	report.WindowStart = store.FeishuTime(start)
	report.WindowEnd = store.FeishuTime(end)
	report.State = "running"
	report.FeishuBinding = b
	report.Trigger = "manual"
	report.CreatedAt = store.FeishuTime(now)
	if schedule != nil {
		report.Trigger = "schedule"
		report.ScheduleRevision = schedule.Revision
	}
	jobCtx, finish, err := h.feishu.begin("preview:"+id, 115*time.Second)
	if err != nil {
		return nil, false, err
	}
	claimed, err := h.inbox.ClaimFeishuReport(ctx, report, now)
	if err != nil || !claimed {
		finish()
		if err != nil {
			return nil, false, err
		}
		existing, e := h.inbox.FeishuReportByID(ctx, id)
		if e != nil {
			return nil, false, e
		}
		if existing == nil {
			return nil, false, store.ErrUnavailable
		}
		if e = h.validateFeishuBinding(ctx, existing.FeishuBinding, false); e != nil {
			return nil, false, e
		}
		if e = h.inbox.CheckFeishuSourceRefs(ctx, existing.SourceRefs); e != nil {
			return nil, false, e
		}
		return existing, false, nil
	}
	releaseGate = false
	// The accepted response is a separate immutable value. The goroutine owns its
	// report copy, so fast providers cannot race JSON marshaling the running DTO.
	accepted := report
	// The durable running row contains provenance, never derived draft data.
	accepted.Summary, accepted.Text, accepted.ContentHash = "", "", ""
	accepted.Todos, accepted.Excerpts = []store.FeishuTodo{}, []store.FeishuExcerpt{}
	go func() {
		defer finish()
		defer func() { <-ai.gate }()
		h.generateFeishuReport(jobCtx, ai, report, groups, strings.TrimPrefix(authorization, "Bearer "))
	}()
	return &accepted, true, nil
}

// Caller holds aiMu and the dispatch read lease through use of this grant.
// A job's cancellation can arrive late; scheduled model processing also needs
// the durable recurring grant before each dispatch and final body persistence.
func (h *handler) validateFeishuGeneration(ctx context.Context, report store.FeishuReport) error {
	if err := h.validateFeishuBinding(ctx, report.FeishuBinding, true); err != nil {
		return err
	}
	if report.Trigger == "schedule" {
		schedule, err := h.inbox.LoadFeishuSchedule(ctx)
		if err != nil {
			return err
		}
		if !schedule.Enabled || schedule.Revision != report.ScheduleRevision || schedule.FeishuBinding != report.FeishuBinding || schedule.LastWindowEnd == nil || *schedule.LastWindowEnd != report.WindowEnd {
			return store.ErrStaleContext
		}
	}
	return h.inbox.CheckFeishuSourceRefs(ctx, report.SourceRefs)
}

func (h *handler) generateFeishuReport(ctx context.Context, ai *aiConnection, report store.FeishuReport, groups []feishuInputGroup, bearer string) {
	attempted := false
	var failure error
	defer func() {
		if recover() != nil {
			failure = store.ErrUnavailable
			attempted = true
		}
		if failure == nil {
			return
		}
		report.State = "failed"
		var modelErr *agent.Error
		if attempted && (ctx.Err() != nil || errors.Is(failure, context.Canceled) || errors.Is(failure, context.DeadlineExceeded) || errors.As(failure, &modelErr) && (modelErr.Kind == agent.Timeout || modelErr.Kind == agent.Unavailable)) {
			report.State = "unknown"
		}
		_, report.ErrorCode, _ = feishuError(failure)
		at := store.FeishuTime(h.feishu.now())
		report.GeneratedAt = &at
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = h.inbox.CompleteFeishuReport(cleanup, report)
	}()
	// A waiting model save cancels accepted jobs before taking aiMu exclusively.
	// If it wins this acquisition, the old job fails binding validation without IO.
	if err := lockFeishu(ctx, &h.aiMu, false); err != nil {
		failure = err
		return
	}
	defer h.aiMu.RUnlock()
	summaries := []string{}
	for _, group := range groups {
		if err := lockFeishu(ctx, &h.feishuDispatchMu, false); err != nil {
			failure = err
			return
		}
		result, err := func() (agent.Result, error) {
			defer h.feishuDispatchMu.RUnlock()
			if err := h.validateFeishuGeneration(ctx, report); err != nil {
				return agent.Result{}, err
			}
			config, err := h.inbox.LoadFeishuConfig(ctx)
			if err != nil {
				return agent.Result{}, err
			}
			if feishuInputContainsSecrets(group.Input, config, h.modelConfig.APIKey, bearer) {
				return agent.Result{}, store.ErrInvalidInput
			}
			if err = agent.ValidateInput(group.Input); err != nil {
				return agent.Result{}, err
			}
			day := h.feishu.now().In(time.FixedZone(store.FeishuTimezone, 8*60*60)).Format("2006-01-02")
			if _, err = h.inbox.ReserveFeishuCallContext(ctx, day, ai.dailyLimit); err != nil {
				return agent.Result{}, err
			}
			if err = ctx.Err(); err != nil {
				return agent.Result{}, err
			}
			attempted = true
			callCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
			defer cancel()
			result, err := ai.client.Analyze(callCtx, group.Input)
			if callCtx.Err() != nil {
				return agent.Result{}, callCtx.Err()
			}
			if err != nil {
				return agent.Result{}, err
			}
			if err = agent.ValidateOutput(group.Input, result.Output); err != nil {
				return agent.Result{}, err
			}
			if feishuOutputContainsSecrets(result.Output, config, h.modelConfig.APIKey, bearer) {
				return agent.Result{}, &agent.Error{Kind: agent.InvalidOutput}
			}
			return result, nil
		}()
		if err != nil {
			failure = err
			return
		}
		title, titleCut := feishuBoundText(group.Title, 120)
		summary, summaryCut := feishuBoundText(result.Output.Summary, 600)
		report.Truncated = report.Truncated || titleCut || summaryCut
		summaries = append(summaries, title+"："+summary)
	}
	report.Summary = strings.Join(summaries, "\n")
	if report.Summary == "" {
		report.Summary = "该固定12小时窗口内没有达到相关性阈值的未暂停会话消息。"
	}
	// Render and terminal persistence have the same source/config privacy barrier
	// as provider dispatch. No previously selected source may pause then resume.
	if err := lockFeishu(ctx, &h.feishuDispatchMu, false); err != nil {
		failure = err
		return
	}
	leased := true
	defer func() {
		if leased {
			h.feishuDispatchMu.RUnlock()
		}
	}()
	if err := h.validateFeishuGeneration(ctx, report); err != nil {
		failure = err
		return
	}
	config, err := h.inbox.LoadFeishuConfig(ctx)
	if err != nil {
		failure = err
		return
	}
	if err = renderFeishuReport(&report, config); err != nil {
		failure = err
		return
	}
	if feishuContainsSecrets(struct {
		Summary  string
		Text     string
		Todos    []store.FeishuTodo
		Excerpts []store.FeishuExcerpt
	}{report.Summary, report.Text, report.Todos, report.Excerpts}, config, h.modelConfig.APIKey, bearer) {
		failure = store.ErrInvalidInput
		return
	}
	report.State = "succeeded"
	at := store.FeishuTime(h.feishu.now())
	report.GeneratedAt = &at
	if err = h.inbox.CompleteFeishuReport(ctx, report); err != nil {
		failure = err
		return
	}
	// Never hold the privacy lease recursively: a config/AI-settings writer may
	// be waiting. The scheduled send obtains its own final source+binding lease.
	leased = false
	h.feishuDispatchMu.RUnlock()
	if report.Trigger == "schedule" {
		_, _, _ = h.startFeishuSend(ctx, report.ID, report.ContentHash, report.FeishuBinding, true)
	}
}

func (h *handler) startFeishuSend(ctx context.Context, id, hash string, b store.FeishuBinding, scheduled bool) (*store.FeishuDelivery, bool, error) {
	// Existing receipts remain readable even after config/model/privacy changes.
	// Their presence wins before any new authorization check or client call.
	report, err := h.inbox.FeishuReportByID(ctx, id)
	if err != nil {
		return nil, false, err
	}
	if report == nil {
		return nil, false, store.ErrNotFound
	}
	if report.Delivery != nil {
		return report.Delivery, false, nil
	}
	if err = lockFeishu(ctx, &h.feishuDispatchMu, false); err != nil {
		return nil, false, err
	}
	release := true
	defer func() {
		if release {
			h.feishuDispatchMu.RUnlock()
		}
	}()
	if err = h.validateFeishuBinding(ctx, b, false); err != nil {
		return nil, false, err
	}
	if err = h.inbox.CheckFeishuSourceRefs(ctx, report.SourceRefs); err != nil {
		return nil, false, err
	}
	config, err := h.inbox.LoadFeishuConfig(ctx)
	if err != nil {
		return nil, false, err
	}
	if report.State != "succeeded" || report.ContentHash != hash || store.FeishuContentHash(*report) != hash {
		return nil, false, store.ErrStaleContext
	}
	input := feishu.SendInput{RecipientOpenID: config.RecipientOpenID, Text: report.Text, UUID: store.FeishuDeliveryID(report.WindowEnd)}
	credentials := feishu.Credentials{AppID: config.AppID, AppSecret: config.AppSecret}
	if err = feishu.ValidateInput(credentials, input); err != nil {
		return nil, false, err
	}
	// An absent runtime is a preflight failure, not a consumed one-shot claim.
	// Receipt replay above stays read-only even if the runtime is now unavailable.
	if !h.feishu.available() {
		return nil, false, &feishu.Error{Code: feishu.CodeAuthUnavailable}
	}
	jobCtx, finish, err := h.feishu.begin("send:"+id, 45*time.Second)
	if err != nil {
		return nil, false, err
	}
	delivery, claimed, err := h.inbox.ClaimFeishuDelivery(ctx, id, hash, b, h.feishu.now(), scheduled)
	if err != nil || !claimed {
		finish()
		if err != nil {
			return nil, false, err
		}
		return &delivery, false, nil
	}
	release = false
	accepted := delivery
	go func() {
		defer finish()
		defer h.feishuDispatchMu.RUnlock()
		h.deliverFeishu(jobCtx, delivery, credentials, input)
	}()
	return &accepted, true, nil
}
func (h *handler) deliverFeishu(ctx context.Context, delivery store.FeishuDelivery, credentials feishu.Credentials, input feishu.SendInput) {
	submitted := false
	defer func() {
		if recover() != nil {
			delivery.State = "unknown"
			delivery.ErrorCode = "FEISHU_SEND_UNKNOWN"
		}
		if delivery.State == "running" {
			delivery.State = "failed"
			delivery.ErrorCode = "FEISHU_CANCELLED"
			if submitted {
				delivery.State = "unknown"
				delivery.ErrorCode = "FEISHU_SEND_UNKNOWN"
			}
		}
		at := store.FeishuTime(h.feishu.now())
		delivery.CompletedAt = &at
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := h.inbox.CompleteFeishuDelivery(cleanup, delivery); err != nil {
			return
		}
		if delivery.ErrorCode == feishu.CodeAuthFailed || delivery.ErrorCode == feishu.CodePermissionDenied || delivery.ErrorCode == feishu.CodeRecipientUnavailable {
			// CAS against the same authorization only; never revoke a newer human grant.
			_ = h.inbox.RevokeFeishuBinding(cleanup, delivery.FeishuBinding)
		}
	}()
	if ctx.Err() != nil {
		return
	}
	submitted = true
	receipt, err := h.feishu.sender.SendText(ctx, credentials, input)
	if err != nil {
		delivery.State = "unknown"
		delivery.ErrorCode = "FEISHU_SEND_UNKNOWN"
		var platformErr *feishu.Error
		if errors.As(err, &platformErr) {
			delivery.ErrorCode = platformErr.Error()
			if !platformErr.Unknown {
				delivery.State = "failed"
			}
		}
		return
	}
	if receipt.MessageID == "" {
		delivery.State = "unknown"
		delivery.ErrorCode = "FEISHU_SEND_UNKNOWN"
		return
	}
	delivery.State = "succeeded"
	delivery.MessageID = receipt.MessageID
	delivery.ErrorCode = ""
}

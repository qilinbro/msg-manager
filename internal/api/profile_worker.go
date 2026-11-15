package api

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/qilinbro/msg-manager/internal/agent"
	"github.com/qilinbro/msg-manager/internal/publicinfo"
	"github.com/qilinbro/msg-manager/internal/store"
)

var errProfileSearchQuota = errors.New("profile search quota reached")

type profileWorker struct {
	h           *handler
	search      publicinfo.Provider
	searchLimit int
	lifecycle   sync.Mutex
	closed      bool
	cancel      context.CancelFunc
	done        chan struct{}
	control     sync.Mutex
	activeMu    sync.Mutex
	activeID    string
	activeStop  context.CancelFunc
	activeDone  chan struct{}
}

func newProfileWorker(h *handler, search publicinfo.Provider, limit int) *profileWorker {
	if limit == 0 {
		limit = 20
	}
	return &profileWorker{h: h, search: search, searchLimit: limit}
}

// StartBackground is deliberately called by main only after net.Listen succeeds.
// Constructing an HTTP handler must not dispatch a billable model request.
func (h *handler) StartBackground(ctx context.Context) error {
	if ctx == nil {
		return store.ErrInvalidInput
	}
	if h.profiles == nil || h.inbox == nil || h.profileModel() == nil {
		return nil
	}
	p := h.profiles
	p.lifecycle.Lock()
	defer p.lifecycle.Unlock()
	if p.closed {
		return store.ErrUnavailable
	}
	if p.cancel != nil {
		return nil
	}
	workerCtx, cancel := context.WithCancel(ctx)
	p.cancel, p.done = cancel, make(chan struct{})
	go func() {
		defer close(p.done)
		p.run(workerCtx)
	}()
	return nil
}

func (p *profileWorker) stop() {
	p.lifecycle.Lock()
	p.closed = true
	cancel, done := p.cancel, p.done
	p.lifecycle.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

func lockProfileControl(ctx context.Context, lock *sync.Mutex) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if lock.TryLock() {
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

func (h *handler) withProfileChange(ctx context.Context, conversation string, change func() error) error {
	if h.profiles == nil {
		return change()
	}
	p := h.profiles
	if err := lockProfileControl(ctx, &p.control); err != nil {
		return err
	}
	defer p.control.Unlock()
	p.activeMu.Lock()
	stop, done := p.activeStop, p.activeDone
	if p.activeID != conversation {
		stop, done = nil, nil
	}
	p.activeMu.Unlock()
	if stop != nil {
		stop()
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return change()
}

func (p *profileWorker) cancelConversation(conversation string) {
	p.activeMu.Lock()
	defer p.activeMu.Unlock()
	if p.activeID == conversation && p.activeStop != nil {
		p.activeStop()
	}
}

func waitProfile(ctx context.Context) bool {
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (p *profileWorker) run(ctx context.Context) {
	for ctx.Err() == nil {
		if err := lockProfileControl(ctx, &p.control); err != nil {
			return
		}
		_, err := p.h.inbox.ScheduleProfileJobs(ctx, p.h.ai.configID)
		var job *store.ProfileJob
		if err == nil {
			job, err = p.h.inbox.ClaimProfileJob(ctx, p.h.ai.configID, 90*time.Second)
		}
		if err != nil || job == nil {
			p.control.Unlock()
			if !waitProfile(ctx) {
				return
			}
			continue
		}
		jobCtx, stop := context.WithTimeout(ctx, 60*time.Second)
		done := make(chan struct{})
		p.activeMu.Lock()
		p.activeID, p.activeStop, p.activeDone = job.ConversationID, stop, done
		p.activeMu.Unlock()
		p.control.Unlock()
		err = p.perform(jobCtx, *job)
		if jobCtx.Err() != nil {
			err = jobCtx.Err()
		}
		if err != nil {
			code, retry := profileFailure(err)
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = p.h.inbox.FailProfileJob(cleanup, *job, code, retry)
			cancel()
		}
		stop()
		p.activeMu.Lock()
		p.activeID, p.activeStop, p.activeDone = "", nil, nil
		close(done)
		p.activeMu.Unlock()
		if !waitProfile(ctx) {
			return
		}
	}
}

func (p *profileWorker) perform(ctx context.Context, job store.ProfileJob) error {
	input, err := p.h.inbox.LoadProfileJobInput(ctx, job)
	if err != nil {
		return err
	}
	// Manual analysis and automatic profiles share one model concurrency gate.
	select {
	case p.h.ai.gate <- struct{}{}:
		defer func() { <-p.h.ai.gate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := p.h.inbox.CheckProfileLease(ctx, job); err != nil {
		return err
	}
	modelInput := agent.ProfileInput{
		Purpose: job.Kind, ConversationID: job.ConversationID,
		DisplayName: input.Profile.DisplayName, PublicName: input.Profile.PublicName,
		Company: input.Profile.Company, Role: input.Profile.Role,
		Messages: []agent.Evidence{}, ConfirmedFacts: []agent.ProfileConfirmedFact{}, Sources: []agent.ProfileSource{},
	}
	for _, fact := range input.Profile.Facts {
		if job.Kind != "organize" || fact.State != "confirmed" || len(fact.Value) > 800 {
			continue
		}
		candidate := append(modelInput.ConfirmedFacts, agent.ProfileConfirmedFact{Field: fact.Field, Value: fact.Value})
		encoded, err := json.Marshal(candidate)
		if err != nil || len(encoded) > 4000 {
			continue
		}
		modelInput.ConfirmedFacts = candidate
		if len(modelInput.ConfirmedFacts) == 8 {
			break
		}
	}
	sources := []store.ProfileSource{}
	if job.Kind == "enrich" {
		// Public matching must not inherit a private nickname or relationship note.
		modelInput.DisplayName = input.Profile.PublicName
		modelInput.Role = ""
		if p.search == nil {
			return &publicinfo.Error{Kind: publicinfo.NotConfigured}
		}
		used, err := p.h.inbox.CountAICallsContext(ctx, aiDay())
		if err != nil {
			return err
		}
		if used >= p.h.ai.dailyLimit {
			return store.ErrQuotaExceeded
		}
		if _, err = p.h.inbox.ReserveProfileSearch(ctx, aiDay(), job.ConversationID, p.searchLimit); err != nil {
			if errors.Is(err, store.ErrQuotaExceeded) {
				return errProfileSearchQuota
			}
			return err
		}
		if err := p.h.inbox.CheckProfileLease(ctx, job); err != nil {
			return err
		}
		found, err := p.search.Search(ctx, input.Profile.PublicName, input.Profile.Company)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return err
		}
		for _, source := range found {
			id := store.ProfileSourceID(job.ConversationID, source.URL, source.ContentHash)
			sources = append(sources, store.ProfileSource{ID: id, URL: source.URL, Title: source.Title, Snippet: source.Snippet, ContentHash: source.ContentHash, RetrievedAt: source.RetrievedAt})
			modelInput.Sources = append(modelInput.Sources, agent.ProfileSource{ID: id, URL: source.URL, Title: source.Title, Snippet: source.Snippet})
		}
		if len(sources) == 0 {
			return p.h.inbox.CompleteProfileJob(ctx, job, store.ProfileJobResult{Tags: []string{}, EvidenceIDs: []string{}, SourceIDs: []string{}, Facts: []store.ProfileFactInput{}, Sources: sources})
		}
	} else {
		for _, message := range input.Messages {
			modelInput.Messages = append(modelInput.Messages, agent.Evidence{ID: message.ID, Body: message.Body, Direction: message.Direction, OccurredAt: message.OccurredAt})
		}
	}
	if err := p.h.inbox.CheckProfileLease(ctx, job); err != nil {
		return err
	}
	if err := agent.ValidateProfileInput(modelInput); err != nil {
		return err
	}
	if _, err := p.h.inbox.ReserveAICallContext(ctx, aiDay(), job.ConversationID, p.h.ai.dailyLimit); err != nil {
		return err
	}
	result, err := p.h.profileModel().OrganizeProfile(ctx, modelInput)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return err
	}
	if err := agent.ValidateProfileOutput(modelInput, result.Output); err != nil {
		return err
	}
	if err := p.h.inbox.CheckProfileLease(ctx, job); err != nil {
		return err
	}
	output := store.ProfileJobResult{Category: result.Output.Category, Tags: result.Output.Tags, Summary: result.Output.Summary, EvidenceIDs: result.Output.EvidenceIDs, SourceIDs: result.Output.SourceIDs, Facts: []store.ProfileFactInput{}, Sources: sources}
	for _, fact := range result.Output.Facts {
		output.Facts = append(output.Facts, store.ProfileFactInput{Field: fact.Field, Value: fact.Value, MessageIDs: fact.MessageIDs, SourceIDs: fact.SourceIDs})
	}
	return p.h.inbox.CompleteProfileJob(ctx, job, output)
}

func profileFailure(err error) (string, bool) {
	if errors.Is(err, context.Canceled) {
		return "PROFILE_CANCELLED", true
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "PROFILE_TIMEOUT", true
	}
	if errors.Is(err, errProfileSearchQuota) {
		return "SEARCH_QUOTA_EXCEEDED", true
	}
	if errors.Is(err, store.ErrQuotaExceeded) {
		return "AI_QUOTA_EXCEEDED", true
	}
	if errors.Is(err, store.ErrStaleContext) || errors.Is(err, store.ErrConflict) {
		return "PROFILE_STALE", false
	}
	var model *agent.Error
	if errors.As(err, &model) {
		switch model.Kind {
		case agent.AuthFailed:
			return "AI_AUTH_FAILED", false
		case agent.QuotaExceeded:
			return "AI_QUOTA_EXCEEDED", true
		case agent.InvalidOutput:
			return "AI_INVALID_OUTPUT", false
		case agent.InvalidInput:
			return "AI_INVALID_INPUT", false
		case agent.Timeout:
			return "AI_TIMEOUT", true
		default:
			return "AI_UNAVAILABLE", true
		}
	}
	var search *publicinfo.Error
	if errors.As(err, &search) {
		switch search.Kind {
		case publicinfo.NotConfigured:
			return "SEARCH_NOT_CONFIGURED", false
		case publicinfo.AuthFailed:
			return "SEARCH_AUTH_FAILED", false
		case publicinfo.QuotaExceeded:
			return "SEARCH_QUOTA_EXCEEDED", true
		case publicinfo.InvalidOutput, publicinfo.InvalidInput:
			return "SEARCH_INVALID_RESULT", false
		case publicinfo.Timeout:
			return "SEARCH_TIMEOUT", true
		default:
			return "SEARCH_UNAVAILABLE", true
		}
	}
	return "PROFILE_UNAVAILABLE", true
}

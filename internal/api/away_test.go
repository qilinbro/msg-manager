package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qilinbro/msg-manager/internal/agent"
	"github.com/qilinbro/msg-manager/internal/connector/mailbox"
	"github.com/qilinbro/msg-manager/internal/store"
)

const (
	testAwayAccount = "receiver@example.test"
	testAwaySender  = "maya@makersweek.example"
	quotedDeadline  = "Please send the public description and a 60-second demo video by 18:00 Beijing time today."
)

type awayTestMailbox struct {
	mu     sync.Mutex
	next   uint32
	latest []mailbox.Mail
}

func (m *awayTestMailbox) Probe(context.Context) (mailbox.Info, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return mailbox.Info{AccountID: testAwayAccount, Folder: "INBOX", UIDValidity: 42, UIDNext: m.next, ReadOnly: true}, nil
}

func (m *awayTestMailbox) Fetch(ctx context.Context) (mailbox.Batch, error) {
	return m.FetchSince(ctx, 42, 1)
}

func (m *awayTestMailbox) FetchSince(_ context.Context, validity, next uint32) (mailbox.Batch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if validity != 42 {
		return mailbox.Batch{}, errors.New("wrong mailbox")
	}
	result := mailbox.Batch{Info: mailbox.Info{AccountID: testAwayAccount, Folder: "INBOX", UIDValidity: 42, UIDNext: m.next, ReadOnly: true}}
	for _, item := range m.latest {
		if item.UID >= next {
			result.Mails = append(result.Mails, item)
		}
	}
	return result, nil
}

func (m *awayTestMailbox) send(sender, body string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	received := time.Now().UTC()
	m.latest = append(m.latest, mailbox.Mail{UID: m.next, FromAddress: sender, To: []string{testAwayAccount}, Subject: "Makers Week materials", Body: body, OccurredAt: received.Format(time.RFC3339Nano), InternalDate: received})
	m.next++
}

func bindAwayMailbox(h *handler) *awayTestMailbox {
	mock := &awayTestMailbox{next: 1}
	account, folder := testAwayAccount, "INBOX"
	h.email.client = mock
	h.email.accountID = "imap-synthetic-account"
	h.email.status = EmailStatus{Configured: true, State: "ready", Account: &account, Folder: &folder}
	return mock
}

func beginAwayTestSession(t *testing.T, h *handler) string {
	t.Helper()
	response := emailTestRequest(h, "POST", awayPath, `{"minutes":30,"sender":"maya@makersweek.example","consent":true}`, true)
	if response.Code != 200 {
		t.Fatalf("start: %d %s", response.Code, response.Body.String())
	}
	var payload awayResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || payload.Session == nil {
		t.Fatal("missing session")
	}
	return payload.Session.ID
}

func waitForAwayDraft(t *testing.T, h *handler) store.AwaySnapshot {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, err := h.inbox.AwaySnapshot()
		if err == nil && len(snapshot.Tasks) == 1 && snapshot.Tasks[0].Draft != "" {
			return snapshot
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("background worker did not produce a draft")
	return store.AwaySnapshot{}
}

func TestAwayWorkerBuildsReviewableTaskWithoutUI(t *testing.T) {
	// Test the actual model transport with a local compatible endpoint. No
	// browser request is needed after start, and the inbox is encrypted.
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		output, _ := json.Marshal(agent.AwayOutput{Title: "提交活动素材", Requirements: []string{"公开介绍", "60 秒演示"}, DeadlineQuote: quotedDeadline, Draft: "Hi Maya, thanks for inviting us."})
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": string(output)}, "finish_reason": "stop"}}})
	}))
	defer provider.Close()

	h := newEmailTestHandler(t)
	inbox := bindAwayMailbox(h)
	config, err := newAIConnection(agent.Config{BaseURL: provider.URL, Model: "away-test"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	h.ai = config
	inbox.send(testAwaySender, "Old mail: "+quotedDeadline)
	if response := emailTestRequest(h, "POST", awayPath, `{"minutes":30,"sender":"maya@makersweek.example","consent":false}`, true); response.Code != 400 {
		t.Fatal("consent was not required")
	}
	if response := emailTestRequest(h, "GET", awayPath, "", false); response.Code != 401 {
		t.Fatal("unauthenticated access")
	}
	for i := 0; i < 3; i++ {
		id := beginAwayTestSession(t, h)
		inbox.send("unrelated@example.test", "No scope for this message")
		inbox.send(testAwaySender, "Hi! "+quotedDeadline)
		if i != 0 {
			h.away.notify()
		} // First run must advance on the timer alone.
		snapshot := waitForAwayDraft(t, h)
		task := snapshot.Tasks[0]
		if task.Evidence != quotedDeadline || task.Title != "提交活动素材" || !strings.Contains(task.Deadline, "T18:00:00+08:00") || !strings.Contains(task.Body, quotedDeadline) {
			t.Fatalf("bad task evidence: %#v", task)
		}
		if !strings.Contains(snapshot.Events[len(snapshot.Events)-1].Kind, "draft") {
			t.Fatal("missing draft receipt")
		}
		response := emailTestRequest(h, "PATCH", fmt.Sprintf("%s/%s/tasks/%s", awayPath, id, task.ID), `{"draft":"My revised reply","done":true}`, true)
		if response.Code != 200 || !strings.Contains(response.Body.String(), "My revised reply") {
			t.Fatalf("patch failed: %d %s", response.Code, response.Body.String())
		}
		response = emailTestRequest(h, "POST", awayPath+"/"+id+"/stop", "", true)
		if response.Code != 200 || !strings.Contains(response.Body.String(), `"status":"stopped"`) {
			t.Fatal("stop failed")
		}
	}
	inboxPage, err := h.inbox.Conversations(store.Filter{Platform: "email"})
	if err != nil || len(inboxPage.Items) != 3 {
		t.Fatalf("dedup/scope: %v, %d records", err, len(inboxPage.Items))
	}
}

type blockedAwayModel struct {
	started   chan struct{}
	cancelled chan struct{}
}

func (m blockedAwayModel) Analyze(context.Context, agent.Input) (agent.Result, error) {
	return agent.Result{}, nil
}
func (m blockedAwayModel) AnalyzeAway(ctx context.Context, _ agent.AwayInput) (agent.AwayOutput, error) {
	close(m.started)
	<-ctx.Done()
	close(m.cancelled)
	return agent.AwayOutput{Title: "late", Requirements: []string{}, Draft: "late"}, nil
}

func TestAwayStopCancelsModelAndKeepsTask(t *testing.T) {
	h := newEmailTestHandler(t)
	inbox := bindAwayMailbox(h)
	model := blockedAwayModel{make(chan struct{}), make(chan struct{})}
	h.ai.client, h.ai.configID = model, "test-model"
	id := beginAwayTestSession(t, h)
	inbox.send(testAwaySender, quotedDeadline)
	h.away.notify()
	select {
	case <-model.started:
	case <-time.After(8 * time.Second):
		t.Fatal("model did not start")
	}
	if response := emailTestRequest(h, "POST", awayPath+"/"+id+"/stop", "", true); response.Code != 200 {
		t.Fatalf("stop: %d", response.Code)
	}
	select {
	case <-model.cancelled:
	case <-time.After(time.Second):
		t.Fatal("stop did not cancel in-flight analysis")
	}
	snapshot, err := h.inbox.AwaySnapshot()
	if err != nil || len(snapshot.Tasks) != 1 || snapshot.Tasks[0].Draft != "" || !snapshot.Tasks[0].DraftFailed {
		t.Fatal("late draft leaked or task was lost")
	}
}

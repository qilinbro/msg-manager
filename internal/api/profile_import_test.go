package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/qilinbro/msg-manager/internal/agent"
	"github.com/qilinbro/msg-manager/internal/store"
)

func TestProfileImportConsentQueuesBeforeAnyModelIOAndAnalyzesEveryBatch(t *testing.T) {
	h, model, _, _ := newAPIProfileFixture(t)
	req := store.ImportRequest{Platform: "qq", AccountID: "import-api-account", AccountName: "合成本人账号", AIOrganize: &store.ImportAIConsent{Confirmed: true, ModelConfigID: apiProfileConfig}}
	for i := 0; i < 25; i++ {
		req.Messages = append(req.Messages, store.ImportMessage{ExternalID: fmt.Sprintf("full-history-%d", i), PeerID: "import-api-peer", PeerName: "合成昵称", Direction: "incoming", Body: fmt.Sprintf("合成聊天 %d", i), OccurredAt: fmt.Sprintf("2026-01-01T00:00:%02dZ", i)})
	}
	var mu sync.Mutex
	seen := map[string]bool{}
	const notes = "人工记录：长期合成项目联系人"
	const firstSummary = "前二十条合成聊天讨论项目资料。"
	batch := 0
	model.run = func(_ context.Context, input agent.ProfileInput) (agent.ProfileResult, error) {
		mu.Lock()
		defer mu.Unlock()
		batch++
		if input.Purpose != "organize" || input.Platform != "qq" || input.PeerID != "import-api-peer" || input.DisplayName != "合成昵称" || input.ManualNotes != notes {
			t.Error("authorized profile metadata missing from organized batch")
		}
		if batch == 1 && input.PreviousSummary != "" {
			t.Error("first batch unexpectedly inherited a summary")
		}
		if batch == 2 && input.PreviousSummary != firstSummary {
			t.Error("second batch lost the previous batch summary")
		}
		if len(input.Messages) > 20 {
			t.Error("unbounded model batch")
		}
		for _, message := range input.Messages {
			if seen[message.ID] {
				t.Error("history message reprocessed")
			}
			seen[message.ID] = true
		}
		result := apiProfileResult(input)
		result.Output.Summary = firstSummary
		if batch == 2 {
			result.Output.Summary = input.PreviousSummary + "最后五条补充后续进度。"
		}
		return result, nil
	}
	raw, _ := json.Marshal(req)
	w := emailTestRequest(h, "POST", "/api/v1/imports/messages", string(raw), true)
	var imported store.ImportResult
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &imported) != nil || imported.Inserted != 25 || imported.AIQueued != 1 || len(imported.ConversationIDs) != 1 {
		t.Fatal("import/authorization/job not committed", w.Code, w.Body.String())
	}
	if model.calls.Load() != 0 {
		t.Fatal("import dispatched model before explicit worker start")
	}
	id := imported.ConversationIDs[0]
	profile := profileAPIGet(t, h, id)
	if !profile.AutoOrganize || len(profile.Jobs) != 1 || profile.Jobs[0].State != "queued" {
		t.Fatal("accepted import lacks durable authorization/job")
	}
	if w := profileAPIPatch(t, h, id, map[string]any{"manual_notes": notes, "confirmed": true, "model_config_id": apiProfileConfig}); w.Code != 200 {
		t.Fatal("authorized profile notes not saved", w.Code)
	}
	if err := h.StartBackground(context.Background()); err != nil {
		t.Fatal(err)
	}
	completed := waitAPIProfile(t, h, id, func(p store.Profile) bool {
		return p.ProcessedSeq == p.MaxSeq && p.MaxSeq > 0 && p.Jobs[0].State == "succeeded"
	})
	if completed.Summary != firstSummary+"最后五条补充后续进度。" {
		t.Fatal("completed profile lost cumulative history")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 25 || model.calls.Load() != 2 {
		t.Fatal("full history not processed", len(seen), model.calls.Load())
	}
}

func TestProfileImportRejectsWrongModelAndPausedConversationWithoutPartialImport(t *testing.T) {
	h, model, id, _ := newAPIProfileFixture(t)
	input := store.ImportRequest{Platform: "qq", AccountID: "synthetic-profile-account", AIOrganize: &store.ImportAIConsent{Confirmed: true, ModelConfigID: strings.Repeat("d", 64)}, Messages: []store.ImportMessage{
		{ExternalID: "not-saved-unseen", PeerID: "new-unseen", PeerName: "未导入联系人", Direction: "incoming", Body: "必须整体回滚", OccurredAt: "2026-01-02T00:00:00Z"},
		{ExternalID: "not-saved-paused", PeerID: "peer-one", PeerName: "已暂停联系人", Direction: "incoming", Body: "必须整体回滚", OccurredAt: "2026-01-02T00:00:01Z"},
	}}
	raw, _ := json.Marshal(input)
	w := emailTestRequest(h, "POST", "/api/v1/imports/messages", string(raw), true)
	if w.Code != 409 {
		t.Fatal("wrong model consent accepted", w.Code, w.Body.String())
	}
	if err := h.inbox.UpdateAISettings(id, "paused", "natural"); err != nil {
		t.Fatal(err)
	}
	input.AIOrganize.ModelConfigID = apiProfileConfig
	raw, _ = json.Marshal(input)
	w = emailTestRequest(h, "POST", "/api/v1/imports/messages", string(raw), true)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "AI_IMPORT_NOT_AUTHORIZED") {
		t.Fatal("paused consent accepted", w.Code, w.Body.String())
	}
	page, err := h.inbox.Conversations(store.Filter{})
	if err != nil || len(page.Items) != 2 || model.calls.Load() != 0 {
		t.Fatal("rejected consent saved partial data or called model")
	}
}

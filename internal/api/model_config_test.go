package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qilinbro/msg-manager/internal/agent"
	"github.com/qilinbro/msg-manager/internal/config"
	"github.com/qilinbro/msg-manager/internal/store"
)

func TestModelConfigAuthenticatedSaveValidationAndWriteOnlyKey(t *testing.T) {
	h := newEmailTestHandler(t)
	body := `{"expected_revision":0,"base_url":"https://api.deepseek.com","model":"deepseek-chat","daily_limit":37,"api_key":"synthetic-secret-config-value","confirmed":true}`
	if w := emailTestRequest(h, http.MethodPatch, modelConfigPath, body, false); w.Code != 401 {
		t.Fatal("missing auth", w.Code)
	}
	if w := emailTestRequest(h, http.MethodGet, modelConfigPath, "", true); w.Code != 200 || strings.Contains(w.Body.String(), `"api_key"`) {
		t.Fatal("read configuration", w.Code)
	}
	for _, bad := range []string{strings.Replace(body, `"confirmed":true`, `"confirmed":false`, 1), strings.Replace(body, `"daily_limit":37`, `"daily_limit":0`, 1), strings.Replace(body, "https://api.deepseek.com", "http://10.0.0.1:8000", 1), strings.Replace(body, "https://api.deepseek.com", "https://user:pass@example.com", 1)} {
		if w := emailTestRequest(h, http.MethodPatch, modelConfigPath, bad, true); w.Code != 400 {
			t.Fatal("invalid save accepted", w.Code, w.Body.String())
		}
	}
	w := emailTestRequest(h, http.MethodPatch, modelConfigPath, body, true)
	var saved modelConfigResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &saved) != nil || !saved.Configured || !saved.KeyConfigured || saved.Revision != 1 || saved.DailyLimit != 37 {
		t.Fatal("save failed", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "synthetic-secret") || strings.Contains(w.Body.String(), `"api_key"`) {
		t.Fatal("response exposed write-only key")
	}
	firstID := saved.ModelConfigID
	if w = emailTestRequest(h, http.MethodPatch, modelConfigPath, body, true); w.Code != 409 {
		t.Fatal("stale save accepted", w.Code)
	}
	change := strings.Replace(strings.Replace(body, `"expected_revision":0`, `"expected_revision":1`, 1), `"api_key":"synthetic-secret-config-value"`, `"api_key":""`, 1)
	if w = emailTestRequest(h, http.MethodPatch, modelConfigPath, strings.Replace(change, "https://api.deepseek.com", "https://model.example.com", 1), true); w.Code != 400 {
		t.Fatal("old key followed changed endpoint", w.Code)
	}
	change = strings.Replace(change, "deepseek-chat", "deepseek-reasoner", 1)
	w = emailTestRequest(h, http.MethodPatch, modelConfigPath, change, true)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &saved) != nil || saved.ModelConfigID == firstID || saved.Revision != 2 {
		t.Fatal("new model not applied", w.Code)
	}
	stored, err := h.inbox.LoadModelConfig(context.Background())
	if err != nil || stored == nil || stored.APIKey != "synthetic-secret-config-value" {
		t.Fatal("blank input did not retain saved key", err)
	}
}

func TestModelConfigFirstSaveRevokesEnvironmentProfileGrants(t *testing.T) {
	h := newEmailTestHandler(t)
	var err error
	h.ai, err = newAIConnection(agent.Config{BaseURL: "https://api.deepseek.com", Model: "environment-model", APIKey: "synthetic-environment-key"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	oldTarget := h.ai.configID
	result, err := h.inbox.Import(store.ImportRequest{Platform: "qq", AccountID: "synthetic-config-account", Messages: []store.ImportMessage{{ExternalID: "synthetic-config-message", PeerID: "synthetic-config-peer", PeerName: "合成联系人", Direction: "incoming", Body: "合成历史", OccurredAt: "2026-01-01T00:00:00Z"}}})
	if err != nil || len(result.ConversationIDs) != 1 {
		t.Fatal("synthetic import", err)
	}
	zero, enabled := int64(0), true
	id := result.ConversationIDs[0]
	profile, err := h.inbox.UpdateProfile(context.Background(), id, store.ProfilePatch{ExpectedRevision: &zero, AutoOrganize: &enabled, ModelConfigID: &oldTarget})
	if err != nil {
		t.Fatal(err)
	}
	if w := emailTestRequest(h, http.MethodPatch, modelConfigPath, `{"expected_revision":0,"base_url":"https://api.deepseek.com","model":"new-model","daily_limit":10,"api_key":"synthetic-new-key","confirmed":true}`, true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	revoked, err := h.inbox.GetProfile(context.Background(), id)
	if err != nil || revoked.AutoOrganize || revoked.ModelConfigID != "" || revoked.Revision != profile.Revision+1 {
		t.Fatal("first save retained environment grants", err)
	}
	if w := emailTestRequest(h, http.MethodPatch, modelConfigPath, `{"expected_revision":1,"base_url":"https://api.deepseek.com","model":"environment-model","daily_limit":10,"api_key":"synthetic-environment-key","confirmed":true}`, true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if count, err := h.inbox.ScheduleProfileJobs(context.Background(), oldTarget); err != nil || count != 0 {
		t.Fatal("A-B-A resumed old grants", count, err)
	}
}

func TestModelConfigRestoresSavedSelectionBeforeBackgroundStarts(t *testing.T) {
	p := filepath.Join(t.TempDir(), "saved-model.db")
	cfg := config.Config{Addr: config.DefaultAddr, APIToken: emailTestToken, DatabasePath: p, DatabaseKey: strings.Repeat("ab", 32)}
	actual, err := NewHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h := actual.(*handler)
	body := `{"expected_revision":0,"base_url":"https://api.deepseek.com","model":"saved-model","daily_limit":11,"api_key":"synthetic-restore-key","confirmed":true}`
	if w := emailTestRequest(h, http.MethodPatch, modelConfigPath, body, true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err = h.Close(); err != nil {
		t.Fatal(err)
	}
	cfg.AI = agent.Config{BaseURL: "https://api.deepseek.com", Model: "environment-model", APIKey: "synthetic-environment-key"}
	actual, err = NewHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h = actual.(*handler)
	defer h.Close()
	w := emailTestRequest(h, http.MethodGet, modelConfigPath, "", true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "saved-model") || strings.Contains(w.Body.String(), "synthetic-") {
		t.Fatal("saved config not restored safely", w.Code)
	}
	if h.profiles.cancel != nil {
		t.Fatal("construction started background model IO")
	}
}

func TestModelConfigCanceledSaveCannotReplaceInflightSnapshot(t *testing.T) {
	h := newEmailTestHandler(t)
	h.aiMu.RLock()
	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest(http.MethodPatch, "http://"+config.DefaultAddr+modelConfigPath, strings.NewReader(`{"expected_revision":0,"base_url":"https://api.deepseek.com","model":"changed","daily_limit":5,"api_key":"synthetic-key","confirmed":true}`)).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+emailTestToken)
	r.Header.Set("Content-Type", "application/json")
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { w := httptest.NewRecorder(); h.ServeHTTP(w, r); done <- w }()
	cancel()
	select {
	case w := <-done:
		if w.Code != 503 {
			t.Fatal("cancelled save", w.Code)
		}
	case <-time.After(time.Second):
		h.aiMu.RUnlock()
		t.Fatal("save ignored cancellation while waiting for inflight model")
	}
	if h.ai.client != nil {
		t.Fatal("cancelled save changed client")
	}
	h.aiMu.RUnlock()
}

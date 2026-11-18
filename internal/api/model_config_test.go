package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
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

func TestModelConfigCloudToKeylessLoopbackClearsKeyAndRevokesConsent(t *testing.T) {
	for _, baseURL := range []string{"http://127.0.0.1:11434/v1", "http://[::1]:11434/v1"} {
		t.Run(baseURL, func(t *testing.T) {
			cfg := config.Config{Addr: config.DefaultAddr, APIToken: emailTestToken, DatabasePath: filepath.Join(t.TempDir(), "keyless-model.db"), DatabaseKey: strings.Repeat("ab", 32)}
			actual, err := NewHandler(cfg)
			if err != nil {
				t.Fatal(err)
			}
			h := actual.(*handler)
			t.Cleanup(func() { _ = h.Close() })
			const cloudURL, cloudModel, cloudKey = "https://api.deepseek.com", "synthetic-cloud-model", "synthetic-cloud-key-never-forwarded"
			if w := emailTestRequest(h, http.MethodPatch, modelConfigPath, modelConfigTestBody(t, cloudURL, cloudModel, cloudKey, 0, true), true); w.Code != 200 {
				t.Fatal("synthetic cloud setup", w.Code, w.Body.String())
			}
			before, beforeAI := h.modelConfig, h.ai
			oldTarget := h.ai.configID
			ctx := context.Background()
			imported, err := h.inbox.Import(store.ImportRequest{Platform: "qq", AccountID: "synthetic-keyless-account", Messages: []store.ImportMessage{{ExternalID: "synthetic-keyless-message", PeerID: "synthetic-keyless-peer", PeerName: "合成联系人", Direction: "incoming", Body: "合成历史", OccurredAt: "2026-01-01T00:00:00Z"}}})
			if err != nil || len(imported.ConversationIDs) != 1 {
				t.Fatal("synthetic consent setup", err)
			}
			id := imported.ConversationIDs[0]
			zero, enabled := int64(0), true
			publicName, notes := "合成公开名称", "保留合成人工记录"
			profile, err := h.inbox.UpdateProfile(ctx, id, store.ProfilePatch{ExpectedRevision: &zero, PublicName: &publicName, ManualNotes: &notes, AutoOrganize: &enabled, PublicSearchEnabled: &enabled, ModelConfigID: &oldTarget})
			if err != nil {
				t.Fatal("synthetic profile grant", err)
			}
			for _, kind := range []string{"organize", "enrich"} {
				if _, err = h.inbox.EnqueueProfileJob(ctx, id, kind, oldTarget, profile.Revision); err != nil {
					t.Fatal("synthetic queued job", err)
				}
			}
			if claim, err := h.inbox.ClaimProfileJob(ctx, oldTarget, time.Minute); err != nil || claim == nil {
				t.Fatal("synthetic running job", err)
			}
			// Store synthetic consent only; no background worker or model call starts.
			schedule, err := h.inbox.LoadDigestSchedule(ctx)
			if err != nil {
				t.Fatal(err)
			}
			schedule, err = h.inbox.SaveDigestSchedule(ctx, store.DigestSchedule{Enabled: true, LocalTime: "09:30", Timezone: "Asia/Shanghai", ModelConfigID: oldTarget, ModelConfigRevision: before.Revision}, schedule.Revision)
			if err != nil {
				t.Fatal("synthetic digest grant", err)
			}
			manual, err := h.inbox.AIContext(id)
			if err != nil {
				t.Fatal(err)
			}
			for _, denied := range []struct {
				name      string
				revision  int64
				confirmed bool
				status    int
				code      string
			}{
				{"unconfirmed", before.Revision, false, 400, "AI_CONFIG_CONFIRMATION_REQUIRED"},
				{"stale", before.Revision - 1, true, 409, "AI_CONFIG_STALE"},
			} {
				t.Run(denied.name, func(t *testing.T) {
					w := emailTestRequest(h, http.MethodPatch, modelConfigPath, modelConfigTestBody(t, baseURL, "synthetic-local-model", "", denied.revision, denied.confirmed), true)
					if w.Code != denied.status || !strings.Contains(w.Body.String(), denied.code) || h.modelConfig != before || h.ai != beforeAI {
						t.Fatal("keyless switch bypassed consent/CAS", w.Code, w.Body.String())
					}
					stored, err := h.inbox.LoadModelConfig(ctx)
					if err != nil || stored == nil || *stored != before {
						t.Fatal("rejected switch mutated persisted configuration", err)
					}
					got, err := h.inbox.GetProfile(ctx, id)
					if err != nil || !got.AutoOrganize || !got.PublicSearchEnabled || got.ModelConfigID != oldTarget || got.Revision != profile.Revision {
						t.Fatal("rejected switch revoked profile consent", err)
					}
					gotSchedule, err := h.inbox.LoadDigestSchedule(ctx)
					if err != nil || gotSchedule != schedule {
						t.Fatal("rejected switch revoked digest consent", err)
					}
				})
			}
			w := emailTestRequest(h, http.MethodPatch, modelConfigPath, modelConfigTestBody(t, baseURL, "synthetic-local-model", "", before.Revision, true), true)
			var saved modelConfigResponse
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &saved) != nil || !saved.Configured || saved.KeyConfigured || saved.SendingEnabled || saved.BaseURL != baseURL || saved.Model != "synthetic-local-model" || saved.Revision != before.Revision+1 || saved.ModelConfigID == "" || saved.ModelConfigID == oldTarget {
				t.Fatal("keyless loopback switch failed", w.Code, w.Body.String())
			}
			if h.modelConfig.APIKey != "" || h.ai == beforeAI || h.ai.client == beforeAI.client || h.ai.external || modelConfigTestClientKey(t, h.ai) != "" {
				t.Fatal("new active loopback configuration retained cloud credentials")
			}
			stored, err := h.inbox.LoadModelConfig(ctx)
			if err != nil || stored == nil || stored.APIKey != "" || *stored != h.modelConfig {
				t.Fatal("saved loopback configuration retained cloud credentials", err)
			}
			for _, response := range []*httptest.ResponseRecorder{w, emailTestRequest(h, http.MethodGet, modelConfigPath, "", true)} {
				if response.Code != 200 || strings.Contains(response.Body.String(), cloudKey) || strings.Contains(response.Body.String(), `"api_key"`) || !strings.Contains(response.Body.String(), `"key_configured":false`) {
					t.Fatal("keyless configuration response leaked or misreported credentials")
				}
			}
			revoked, err := h.inbox.GetProfile(ctx, id)
			if err != nil || revoked.AutoOrganize || revoked.PublicSearchEnabled || revoked.ModelConfigID != "" || revoked.Revision != profile.Revision+1 || revoked.ManualNotes != notes || len(revoked.Jobs) != 2 {
				t.Fatal("keyless switch failed to revoke profile grants without losing notes", err)
			}
			for _, job := range revoked.Jobs {
				if job.State != "cancelled" || job.LeaseToken != "" || job.LastErrorCode == nil || *job.LastErrorCode != "MODEL_CHANGED" {
					t.Fatal("keyless switch left an old queued/running job authorized")
				}
			}
			afterManual, err := h.inbox.AIContext(id)
			if err != nil || afterManual.ContextHash == manual.ContextHash {
				t.Fatal("keyless switch did not stale manual AI confirmation", err)
			}
			afterSchedule, err := h.inbox.LoadDigestSchedule(ctx)
			if err != nil || afterSchedule.Enabled || afterSchedule.ModelConfigID != "" || afterSchedule.ModelConfigRevision != 0 || afterSchedule.Revision != schedule.Revision+1 {
				t.Fatal("keyless switch left digest consent authorized", err)
			}
			if err = h.Close(); err != nil {
				t.Fatal(err)
			}
			cfg.AI = agent.Config{BaseURL: cloudURL, Model: cloudModel, APIKey: cloudKey}
			actual, err = NewHandler(cfg)
			if err != nil {
				t.Fatal("restore saved keyless configuration", err)
			}
			h = actual.(*handler)
			if h.modelConfig.BaseURL != baseURL || h.modelConfig.APIKey != "" || h.modelConfig.Revision != saved.Revision || h.ai.configID != saved.ModelConfigID || h.ai.dailyLimit != saved.DailyLimit || h.ai.external || modelConfigTestClientKey(t, h.ai) != "" || h.profiles.cancel != nil {
				t.Fatal("reopened keyless selection fell back to cloud credentials or started model work")
			}
		})
	}
}

func TestModelConfigChangedURLKeylessValidationCannotInheritCloudKey(t *testing.T) {
	h := newEmailTestHandler(t)
	const cloudKey = "synthetic-cloud-key-kept-only-for-unchanged-url"
	if w := emailTestRequest(h, http.MethodPatch, modelConfigPath, modelConfigTestBody(t, "https://api.deepseek.com", "synthetic-cloud-model", cloudKey, 0, true), true); w.Code != 200 {
		t.Fatal("synthetic cloud setup", w.Code, w.Body.String())
	}
	before, beforeAI := h.modelConfig, h.ai
	for _, denied := range []struct {
		name, baseURL, model, code string
	}{
		{"public-https", "https://api.openai.com/v1", "model", "AI_CONFIG_KEY_REQUIRED"},
		{"different-path-same-host", "https://api.deepseek.com/v1", "model", "AI_CONFIG_KEY_REQUIRED"},
		{"official-compatibility", "https://generativelanguage.googleapis.com/v1beta/openai", "model", "AI_CONFIG_KEY_REQUIRED"},
		{"localhost", "http://localhost:11434/v1", "model", ""},
		{"localhost-prefix", "http://localhost.example.com:11434/v1", "model", ""},
		{"numeric-host-prefix", "http://127.0.0.1.example.com:11434/v1", "model", ""},
		{"private-ip", "http://10.0.0.1:11434/v1", "model", ""},
		{"link-local-ip", "http://169.254.169.254:80/v1", "model", ""},
		{"other-loopback", "http://127.0.0.2:11434/v1", "model", ""},
		{"short-ip", "http://127.1:11434/v1", "model", ""},
		{"integer-ip", "http://2130706433:11434/v1", "model", ""},
		{"hex-ip", "http://0x7f000001:11434/v1", "model", ""},
		{"ipv4-mapped-ipv6", "http://[::ffff:127.0.0.1]:11434/v1", "model", ""},
		{"https-loopback", "https://127.0.0.1:11434/v1", "model", ""},
		{"missing-port", "http://127.0.0.1/v1", "model", ""},
		{"empty-ipv6-port", "http://[::1]:/v1", "model", ""},
		{"zero-port", "http://127.0.0.1:0/v1", "model", ""},
		{"noncanonical-port", "http://127.0.0.1:011434/v1", "model", ""},
		{"oversized-port", "http://127.0.0.1:65536/v1", "model", ""},
		{"arbitrary-path", "http://127.0.0.1:11434/proxy/v1", "model", ""},
		{"official-path-on-loopback", "http://127.0.0.1:11434/compatible-mode/v1", "model", ""},
		{"native-path", "http://[::1]:11434/v1beta", "model", ""},
		{"path-prefix", "http://127.0.0.1:11434/v1/extra", "model", ""},
		{"encoded-path", "http://127.0.0.1:11434/v1/%2e", "model", ""},
		{"empty-query", "http://127.0.0.1:11434/v1?", "model", ""},
		{"query", "http://127.0.0.1:11434/v1?host=example.com", "model", ""},
		{"fragment", "http://127.0.0.1:11434/v1#", "model", ""},
		{"userinfo", "http://synthetic@127.0.0.1:11434/v1", "model", ""},
		{"missing-model", "http://127.0.0.1:11434/v1", "", ""},
		{"invalid-model", "http://[::1]:11434/v1", "invalid model", ""},
		{"oversized-model", "http://127.0.0.1:11434/v1", strings.Repeat("m", 129), ""},
	} {
		t.Run(denied.name, func(t *testing.T) {
			w := emailTestRequest(h, http.MethodPatch, modelConfigPath, modelConfigTestBody(t, denied.baseURL, denied.model, "", before.Revision, true), true)
			if w.Code != 400 || denied.code != "" && !strings.Contains(w.Body.String(), denied.code) || strings.Contains(w.Body.String(), cloudKey) {
				t.Fatal("invalid keyless target accepted or wrong key-required result", w.Code, w.Body.String())
			}
			stored, err := h.inbox.LoadModelConfig(context.Background())
			if err != nil || stored == nil || *stored != before || h.modelConfig != before || h.ai != beforeAI || modelConfigTestClientKey(t, h.ai) != cloudKey {
				t.Fatal("rejected destination changed active or persisted configuration", err)
			}
		})
	}
}

func TestModelConfigSameLoopbackURLBlankKeyRetainsSavedKey(t *testing.T) {
	h := newEmailTestHandler(t)
	const baseURL, key = "http://127.0.0.1:11434/v1", "synthetic-optional-loopback-key"
	if w := emailTestRequest(h, http.MethodPatch, modelConfigPath, modelConfigTestBody(t, baseURL, "local-model", key, 0, true), true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	before := h.modelConfig
	beforeID := h.ai.configID
	w := emailTestRequest(h, http.MethodPatch, modelConfigPath, modelConfigTestBody(t, baseURL, "local-model", "", before.Revision, true), true)
	var saved modelConfigResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &saved) != nil || !saved.KeyConfigured || saved.Revision != before.Revision+1 || saved.ModelConfigID != beforeID {
		t.Fatal("same-URL blank key changed key-retention semantics", w.Code, w.Body.String())
	}
	stored, err := h.inbox.LoadModelConfig(context.Background())
	if err != nil || stored == nil || stored.APIKey != key || h.modelConfig.APIKey != key || modelConfigTestClientKey(t, h.ai) != key || strings.Contains(w.Body.String(), key) || strings.Contains(w.Body.String(), `"api_key"`) {
		t.Fatal("same-URL blank key failed to retain the write-only credential", err)
	}
}

func modelConfigTestBody(t *testing.T, baseURL, model, key string, revision int64, confirmed bool) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"base_url": baseURL, "model": model, "api_key": key, "expected_revision": revision, "confirmed": confirmed, "daily_limit": 17})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func modelConfigTestClientKey(t *testing.T, connection *aiConnection) string {
	t.Helper()
	client, ok := connection.client.(*agent.Client)
	if !ok || client == nil {
		t.Fatal("expected a constructed synthetic model client")
	}
	// Inspect only this synthetic client's validated key, without adding a
	// production credential accessor or performing any inference/network IO.
	cfg := reflect.ValueOf(client).Elem().FieldByName("cfg")
	if !cfg.IsValid() || cfg.Kind() != reflect.Struct {
		t.Fatal("missing synthetic validated client configuration")
	}
	key := cfg.FieldByName("key")
	if !key.IsValid() || key.Kind() != reflect.String {
		t.Fatal("missing synthetic validated client key")
	}
	return key.String()
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

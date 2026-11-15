package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qilinbro/msg-manager/internal/agent"
	"github.com/qilinbro/msg-manager/internal/config"
	"github.com/qilinbro/msg-manager/internal/publicinfo"
	"github.com/qilinbro/msg-manager/internal/store"
)

const apiProfileConfig = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

type apiProfileModel struct {
	calls atomic.Int32
	run   func(context.Context, agent.ProfileInput) (agent.ProfileResult, error)
}

func (m *apiProfileModel) Analyze(context.Context, agent.Input) (agent.Result, error) {
	return agent.Result{}, errors.New("synthetic manual analyzer is unused")
}

func (m *apiProfileModel) OrganizeProfile(ctx context.Context, in agent.ProfileInput) (agent.ProfileResult, error) {
	m.calls.Add(1)
	if m.run != nil {
		return m.run(ctx, in)
	}
	return apiProfileResult(in), nil
}

func apiProfileResult(in agent.ProfileInput) agent.ProfileResult {
	out := agent.ProfileOutput{Category: "client", Tags: []string{"设计"}, Summary: "合成会话讨论工作资料，归类为AI建议。", EvidenceIDs: []string{}, SourceIDs: []string{}, Facts: []agent.ProfileFact{}}
	if in.Purpose == "enrich" {
		out.Category = "other"
		out.Summary = "公开媒体存在同名作者的作品，匹配待核实。"
		if len(in.Sources) > 0 {
			out.SourceIDs = []string{in.Sources[0].ID}
			out.Facts = []agent.ProfileFact{{Field: "public_bio", Value: "同名作者发布过公开设计作品，待核实", MessageIDs: []string{}, SourceIDs: []string{in.Sources[0].ID}}}
		}
	} else if len(in.Messages) > 0 {
		out.EvidenceIDs = []string{in.Messages[0].ID}
		out.Facts = []agent.ProfileFact{{Field: "preference", Value: "未公开的合成客户偏好", MessageIDs: []string{in.Messages[0].ID}, SourceIDs: []string{}}}
	}
	return agent.ProfileResult{Output: out, Usage: agent.Usage{PromptTokens: 20, CompletionTokens: 10}}
}

type apiProfileSearch struct {
	calls atomic.Int32
	run   func(context.Context, string, string) ([]publicinfo.Source, error)
}

func (s *apiProfileSearch) Search(ctx context.Context, name, company string) ([]publicinfo.Source, error) {
	s.calls.Add(1)
	if s.run != nil {
		return s.run(ctx, name, company)
	}
	hash := sha256.Sum256([]byte("synthetic-source-content"))
	return []publicinfo.Source{{ID: "synthetic-search-source", URL: "https://example.com/public-work", Title: "合成公开作品", Snippet: "公开作者发布了设计作品，身份匹配待核实。", ContentHash: hex.EncodeToString(hash[:]), RetrievedAt: time.Now().UTC().Format(time.RFC3339Nano)}}, nil
}

func profileAPIPath(id string) string { return "/api/v1/conversations/" + id + "/profile" }

func newAPIProfileFixture(t *testing.T) (*handler, *apiProfileModel, string, string) {
	t.Helper()
	h := newEmailTestHandler(t)
	_, err := h.inbox.Import(store.ImportRequest{Platform: "qq", AccountID: "synthetic-profile-account", AccountName: "合成测试账号", Messages: []store.ImportMessage{
		{ExternalID: "profile-in-one", PeerID: "peer-one", PeerName: "私密备注甲", Body: "合成工作资料和未公开客户偏好", Direction: "incoming", OccurredAt: "2026-01-01T00:00:00Z"},
		{ExternalID: "profile-in-two", PeerID: "peer-two", PeerName: "私密备注乙", Body: "另一联系人合成资料", Direction: "incoming", OccurredAt: "2026-01-01T00:01:00Z"},
	}})
	if err != nil {
		t.Fatal("synthetic profile import failed", err)
	}
	page, err := h.inbox.Conversations(store.Filter{})
	if err != nil || len(page.Items) != 2 {
		t.Fatal("synthetic profile conversations missing", err)
	}
	var first, second string
	for _, c := range page.Items {
		if c.PeerID == "peer-one" {
			first = c.ID
		} else if c.PeerID == "peer-two" {
			second = c.ID
		}
	}
	model := &apiProfileModel{}
	h.ai.client = model
	h.ai.configID = apiProfileConfig
	h.ai.model = "synthetic-profile-model"
	h.ai.provider = "合成模型"
	h.ai.endpointHost = "model.example.com"
	return h, model, first, second
}

func profileAPIGet(t *testing.T, h *handler, id string) store.Profile {
	t.Helper()
	w := emailTestRequest(h, "GET", profileAPIPath(id), "", true)
	var p store.Profile
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &p) != nil {
		t.Fatalf("profile GET failed: %d %s", w.Code, w.Body.String())
	}
	return p
}

func profileAPIPatch(t *testing.T, h *handler, id string, fields map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	if _, ok := fields["expected_revision"]; !ok {
		fields["expected_revision"] = profileAPIGet(t, h, id).Revision
	}
	raw, _ := json.Marshal(fields)
	return emailTestRequest(h, "PATCH", profileAPIPath(id), string(raw), true)
}

func enqueueAPIProfile(t *testing.T, h *handler, id, kind string) store.ProfileJob {
	t.Helper()
	p := profileAPIGet(t, h, id)
	raw, _ := json.Marshal(map[string]any{"expected_revision": p.Revision, "confirmed": true, "model_config_id": apiProfileConfig})
	w := emailTestRequest(h, "POST", profileAPIPath(id)+"/"+kind, string(raw), true)
	var job store.ProfileJob
	if w.Code != 202 || json.Unmarshal(w.Body.Bytes(), &job) != nil {
		t.Fatalf("profile job request failed: %d %s", w.Code, w.Body.String())
	}
	return job
}

func waitAPIProfile(t *testing.T, h *handler, id string, condition func(store.Profile) bool) store.Profile {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		p := profileAPIGet(t, h, id)
		if condition(p) {
			return p
		}
		select {
		case <-deadline.C:
			t.Fatalf("profile condition timed out; jobs=%+v", p.Jobs)
		case <-tick.C:
		}
	}
}

func claimAPIProfile(t *testing.T, h *handler) store.ProfileJob {
	t.Helper()
	job, err := h.inbox.ClaimProfileJob(context.Background(), apiProfileConfig, 90*time.Second)
	if err != nil || job == nil {
		t.Fatal("synthetic profile job not claimable", err)
	}
	return *job
}

func TestProfileRoutesAuthenticateRejectUnsafeOriginsAndUseStrictCAS(t *testing.T) {
	h, model, id, other := newAPIProfileFixture(t)
	for _, route := range []struct{ method, path, body string }{{"GET", "/api/v1/ai/profile-status", ""}, {"GET", profileAPIPath(id), ""}, {"PATCH", profileAPIPath(id), `{"expected_revision":0,"manual_notes":"note"}`}, {"POST", profileAPIPath(id) + "/generate", `{"expected_revision":0,"confirmed":true,"model_config_id":"` + apiProfileConfig + `"}`}, {"PATCH", profileAPIPath(id) + "/facts/missing", `{"expected_revision":0,"state":"confirmed"}`}} {
		if w := emailTestRequest(h, route.method, route.path, route.body, false); w.Code != 401 {
			t.Fatal("unauthenticated profile route accepted", w.Code)
		}
	}
	for _, header := range []string{"Host", "Origin"} {
		r := httptest.NewRequest("GET", "http://"+config.DefaultAddr+profileAPIPath(id), nil)
		r.Header.Set("Authorization", "Bearer "+emailTestToken)
		r.Header.Set("Origin", "http://127.0.0.1:1420")
		if header == "Host" {
			r.Host = "attacker.example:17890"
		} else {
			r.Header.Set("Origin", "https://attacker.example")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("unsafe profile origin/host allowed", header, w.Code)
		}
	}
	for _, route := range []struct{ method, path string }{{"PATCH", profileAPIPath(id)}, {"POST", profileAPIPath(id) + "/generate"}, {"POST", profileAPIPath(id) + "/enrich"}, {"PATCH", profileAPIPath(id) + "/facts/missing"}} {
		r := httptest.NewRequest("OPTIONS", "http://"+config.DefaultAddr+route.path, nil)
		r.Header.Set("Origin", "http://127.0.0.1:1420")
		r.Header.Set("Access-Control-Request-Method", route.method)
		r.Header.Set("Access-Control-Request-Headers", "authorization,content-type")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 204 {
			t.Fatal("profile CORS mutation unavailable", route.path, w.Code)
		}
	}
	for _, body := range []string{`null`, `[]`, `{}`, `{"expected_revision":0,"key":"must-not-echo"}`, `{"expected_revision":0,"manual_notes":"a","manual_notes":"b"}`, `{"expected_revision":-1}`, `{} {}`} {
		w := emailTestRequest(h, "PATCH", profileAPIPath(id), body, true)
		if w.Code != 400 || strings.Contains(w.Body.String(), "must-not-echo") {
			t.Fatal("invalid profile input accepted or echoed", w.Code)
		}
	}
	if w := profileAPIPatch(t, h, id, map[string]any{"manual_notes": "人工资料", "category_override": "friend"}); w.Code != 200 {
		t.Fatal("valid manual profile rejected", w.Code, w.Body.String())
	}
	if w := profileAPIPatch(t, h, id, map[string]any{"expected_revision": 0, "manual_notes": "stale"}); w.Code != 409 {
		t.Fatal("stale profile revision overwritten", w.Code)
	}
	if w := emailTestRequest(h, "GET", profileAPIPath("00000000-0000-0000-0000-000000000000"), "", true); w.Code != 404 {
		t.Fatal("foreign/missing profile returned", w.Code)
	}
	w := emailTestRequest(h, "GET", "/api/v1/conversations?category=friend", "", true)
	var page store.ConversationPage
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Items) != 1 || page.Items[0].ID != id || page.Items[0].Category != "friend" {
		t.Fatal("profile category filter not scoped", w.Code, w.Body.String())
	}
	if profileAPIGet(t, h, other).ManualNotes != "" || model.calls.Load() != 0 || h.profiles.cancel != nil {
		t.Fatal("profile metadata/constructor performed model work or leaked manual data")
	}
	status := emailTestRequest(h, "GET", "/api/v1/ai/profile-status", "", true)
	var profileStatus struct {
		ModelConfigured  bool `json:"model_configured"`
		SearchConfigured bool `json:"search_configured"`
		DailyLimit       int  `json:"daily_limit"`
		SearchDailyLimit int  `json:"search_daily_limit"`
		SendingEnabled   bool `json:"sending_enabled"`
	}
	if status.Code != 200 || json.Unmarshal(status.Body.Bytes(), &profileStatus) != nil || !profileStatus.ModelConfigured || profileStatus.SearchConfigured || profileStatus.DailyLimit != 50 || profileStatus.SearchDailyLimit != 20 || profileStatus.SendingEnabled || model.calls.Load() != 0 {
		t.Fatal("profile status invented a configured search service or performed work", status.Code, status.Body.String())
	}
}

func TestProfileConsentSearchAndPausePermissions(t *testing.T) {
	h, model, id, _ := newAPIProfileFixture(t)
	h.ai.client = nil
	if w := profileAPIPatch(t, h, id, map[string]any{"manual_notes": "无需模型即可保存"}); w.Code != 200 {
		t.Fatal("manual notes require model", w.Code)
	}
	if w := profileAPIPatch(t, h, id, map[string]any{"auto_organize": true, "confirmed": true, "model_config_id": apiProfileConfig}); w.Code != 503 {
		t.Fatal("enabled unconfigured model", w.Code)
	}
	h.ai.client = model
	for _, fields := range []map[string]any{{"auto_organize": true}, {"auto_organize": true, "confirmed": true, "model_config_id": strings.Repeat("d", 64)}} {
		if w := profileAPIPatch(t, h, id, fields); w.Code != 409 {
			t.Fatal("model consent not enforced", w.Code)
		}
	}
	if w := profileAPIPatch(t, h, id, map[string]any{"public_search_enabled": true, "public_name": "合成公开姓名", "confirmed": true, "model_config_id": apiProfileConfig}); w.Code != 503 {
		t.Fatal("enabled unconfigured search", w.Code)
	}
	h.profiles.search = &apiProfileSearch{}
	if w := profileAPIPatch(t, h, id, map[string]any{"public_search_enabled": true, "confirmed": true, "model_config_id": apiProfileConfig}); w.Code != 400 {
		t.Fatal("search accepted missing public name", w.Code)
	}
	if w := profileAPIPatch(t, h, id, map[string]any{"auto_organize": true, "confirmed": true, "model_config_id": apiProfileConfig}); w.Code != 200 {
		t.Fatal("valid automatic consent rejected", w.Code, w.Body.String())
	}
	if err := h.inbox.UpdateAISettings(id, "paused", ""); err != nil {
		t.Fatal(err)
	}
	if w := profileAPIPatch(t, h, id, map[string]any{"public_search_enabled": true, "public_name": "合成公开姓名", "confirmed": true, "model_config_id": apiProfileConfig}); w.Code != 409 || !strings.Contains(w.Body.String(), "AI_PAUSED") {
		t.Fatal("AI pause did not block new permission", w.Code)
	}
	if w := profileAPIPatch(t, h, id, map[string]any{"manual_notes": "暂停仍保留授权标记并可人工编辑", "auto_organize": true}); w.Code != 200 {
		t.Fatal("pause blocked manual preservation", w.Code, w.Body.String())
	}
	p := profileAPIGet(t, h, id)
	if !p.AIPaused || !p.AutoOrganize || p.PublicSearchEnabled || model.calls.Load() != 0 {
		t.Fatal("paused flags or side-effect boundary wrong")
	}
}

func TestProfileAcceptedJobOutlivesPageAndOnlyStartsExplicitly(t *testing.T) {
	h, model, id, other := newAPIProfileFixture(t)
	p := profileAPIGet(t, h, id)
	body, _ := json.Marshal(map[string]any{"expected_revision": p.Revision, "confirmed": true, "model_config_id": apiProfileConfig})
	pageCtx, cancelPage := context.WithCancel(context.Background())
	r := httptest.NewRequest("POST", "http://"+config.DefaultAddr+profileAPIPath(id)+"/generate", strings.NewReader(string(body))).WithContext(pageCtx)
	r.Header.Set("Authorization", "Bearer "+emailTestToken)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://127.0.0.1:1420")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	cancelPage()
	if w.Code != 202 || model.calls.Load() != 0 || h.profiles.cancel != nil {
		t.Fatal("queued HTTP request performed model work", w.Code, w.Body.String())
	}
	profileAPIGet(t, h, id)
	if model.calls.Load() != 0 {
		t.Fatal("GET dispatched billable work")
	}
	if err := h.StartBackground(context.Background()); err != nil {
		t.Fatal(err)
	}
	p = waitAPIProfile(t, h, id, func(p store.Profile) bool {
		return len(p.Facts) == 1 && len(p.Jobs) > 0 && p.Jobs[0].State == "succeeded"
	})
	if model.calls.Load() != 1 || p.Facts[0].State != "unverified" || p.Facts[0].Origin != "chat" || p.Category != "client" || len(p.EvidenceIDs) == 0 {
		t.Fatal("accepted job result not scoped/unverified")
	}
	confirmedSection := strings.Split(strings.Split(p.DocumentMarkdown, "## 已确认资料")[1], "## 待核实候选")[0]
	if strings.Contains(confirmedSection, p.Facts[0].Value) {
		t.Fatal("unverified candidate entered confirmed document section")
	}
	if len(profileAPIGet(t, h, other).Facts) != 0 {
		t.Fatal("model result crossed contact boundary")
	}
	confirm, _ := json.Marshal(map[string]any{"expected_revision": p.Revision, "state": "confirmed"})
	if w := emailTestRequest(h, "PATCH", profileAPIPath(id)+"/facts/"+p.Facts[0].ID, string(confirm), true); w.Code != 200 {
		t.Fatal("explicit confirmation failed", w.Code, w.Body.String())
	}
	p = profileAPIGet(t, h, id)
	confirmedSection = strings.Split(strings.Split(p.DocumentMarkdown, "## 已确认资料")[1], "## 待核实候选")[0]
	if p.Facts[0].State != "confirmed" || !strings.Contains(confirmedSection, "未公开的合成客户偏好") {
		t.Fatal("confirmed fact did not enter dedicated document")
	}
	if w := emailTestRequest(h, "PATCH", profileAPIPath(other)+"/facts/"+p.Facts[0].ID, `{"expected_revision":0,"state":"confirmed"}`, true); w.Code != 404 {
		t.Fatal("fact review crossed contact scope", w.Code)
	}
}

func TestProfilePublicEnrichmentUsesOnlyApprovedFieldsAndUnverifiedSources(t *testing.T) {
	h, model, id, _ := newAPIProfileFixture(t)
	enqueueAPIProfile(t, h, id, "generate")
	if err := h.profiles.perform(context.Background(), claimAPIProfile(t, h)); err != nil {
		t.Fatal("synthetic organize failed", err)
	}
	p := profileAPIGet(t, h, id)
	if _, err := h.inbox.SetProfileFact(context.Background(), id, p.Facts[0].ID, "confirmed", p.Revision); err != nil {
		t.Fatal(err)
	}
	search := &apiProfileSearch{run: func(ctx context.Context, name, company string) ([]publicinfo.Source, error) {
		if name != "公开姓名" || company != "公开公司" {
			t.Error("search did not receive only approved identity")
		}
		hash := sha256.Sum256([]byte("public-work"))
		return []publicinfo.Source{{ID: "provider-source", URL: "https://example.com/work", Title: "公开作品", Snippet: "同名公开作者发表作品，匹配待核实。", ContentHash: hex.EncodeToString(hash[:]), RetrievedAt: time.Now().UTC().Format(time.RFC3339Nano)}}, nil
	}}
	h.profiles.search = search
	if w := profileAPIPatch(t, h, id, map[string]any{"public_name": "公开姓名", "company": "公开公司", "role": "私人职业备注", "public_search_enabled": true, "confirmed": true, "model_config_id": apiProfileConfig}); w.Code != 200 {
		t.Fatal("public settings failed", w.Code, w.Body.String())
	}
	model.run = func(ctx context.Context, in agent.ProfileInput) (agent.ProfileResult, error) {
		if in.Purpose != "enrich" || in.DisplayName != "公开姓名" || in.PublicName != "公开姓名" || in.Company != "公开公司" || in.Role != "" || len(in.Messages) != 0 || len(in.ConfirmedFacts) != 0 || len(in.Sources) != 1 {
			t.Error("private profile data reached enrichment model")
		}
		return apiProfileResult(in), nil
	}
	enqueueAPIProfile(t, h, id, "enrich")
	if err := h.profiles.perform(context.Background(), claimAPIProfile(t, h)); err != nil {
		t.Fatal("synthetic enrichment failed", err)
	}
	p = profileAPIGet(t, h, id)
	if len(p.Sources) != 1 || search.calls.Load() != 1 || model.calls.Load() != 2 {
		t.Fatal("public enrichment did not produce source-backed candidates")
	}
	var publicFact *store.ProfileFact
	for i := range p.Facts {
		if p.Facts[i].Origin == "public" {
			publicFact = &p.Facts[i]
		}
	}
	if publicFact == nil || publicFact.State != "unverified" || len(publicFact.MessageIDs) != 0 || len(publicFact.SourceIDs) != 1 || publicFact.SourceIDs[0] != p.Sources[0].ID {
		t.Fatal("public source was confirmed or lost evidence scope")
	}
}

func TestProfileEmptySearchSkipsModelAndSharesDailyBudgets(t *testing.T) {
	t.Run("empty-results", func(t *testing.T) {
		h, model, id, _ := newAPIProfileFixture(t)
		search := &apiProfileSearch{run: func(context.Context, string, string) ([]publicinfo.Source, error) { return []publicinfo.Source{}, nil }}
		h.profiles.search = search
		if w := profileAPIPatch(t, h, id, map[string]any{"public_name": "公开姓名", "public_search_enabled": true, "confirmed": true, "model_config_id": apiProfileConfig}); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		enqueueAPIProfile(t, h, id, "enrich")
		if err := h.profiles.perform(context.Background(), claimAPIProfile(t, h)); err != nil {
			t.Fatal(err)
		}
		p := profileAPIGet(t, h, id)
		if search.calls.Load() != 1 || model.calls.Load() != 0 || len(p.Sources) != 0 || len(p.Facts) != 0 {
			t.Fatal("empty search invented evidence or called model")
		}
		if used, err := h.inbox.CountProfileSearch(context.Background(), aiDay()); err != nil || used != 1 {
			t.Fatal("search attempt not metered", err)
		}
	})
	t.Run("shared-model-budget", func(t *testing.T) {
		h, model, id, _ := newAPIProfileFixture(t)
		h.ai.dailyLimit = 1
		if _, err := h.inbox.ReserveAICall(aiDay(), id, 1); err != nil {
			t.Fatal(err)
		}
		enqueueAPIProfile(t, h, id, "generate")
		if err := h.profiles.perform(context.Background(), claimAPIProfile(t, h)); !errors.Is(err, store.ErrQuotaExceeded) {
			t.Fatal("automatic profile bypassed shared model quota", err)
		}
		if model.calls.Load() != 0 {
			t.Fatal("quota exhausted but model called")
		}
	})
	t.Run("search-budget", func(t *testing.T) {
		h, model, id, _ := newAPIProfileFixture(t)
		search := &apiProfileSearch{}
		h.profiles.search = search
		h.profiles.searchLimit = 1
		if _, err := h.inbox.ReserveProfileSearch(context.Background(), aiDay(), id, 1); err != nil {
			t.Fatal(err)
		}
		if w := profileAPIPatch(t, h, id, map[string]any{"public_name": "公开姓名", "public_search_enabled": true, "confirmed": true, "model_config_id": apiProfileConfig}); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		enqueueAPIProfile(t, h, id, "enrich")
		if err := h.profiles.perform(context.Background(), claimAPIProfile(t, h)); !errors.Is(err, errProfileSearchQuota) {
			t.Fatal("search budget bypassed", err)
		}
		if model.calls.Load() != 0 || search.calls.Load() != 0 {
			t.Fatal("exhausted search budget dispatched request")
		}
	})
}

func TestProfileWorkerRejectsCrossConversationModelEvidence(t *testing.T) {
	h, model, id, other := newAPIProfileFixture(t)
	messages, err := h.inbox.Messages(other, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	model.run = func(ctx context.Context, in agent.ProfileInput) (agent.ProfileResult, error) {
		result := apiProfileResult(in)
		result.Output.EvidenceIDs = []string{messages.Items[0].ID}
		result.Output.Facts[0].MessageIDs = []string{messages.Items[0].ID}
		return result, nil
	}
	enqueueAPIProfile(t, h, id, "generate")
	err = h.profiles.perform(context.Background(), claimAPIProfile(t, h))
	var bad *agent.Error
	if !errors.As(err, &bad) || bad.Kind != agent.InvalidOutput {
		t.Fatal("cross-contact model evidence was accepted", err)
	}
	if p := profileAPIGet(t, h, id); p.Summary != "" || len(p.Facts) != 0 {
		t.Fatal("invalid model result was persisted")
	}
}

func TestProfileWorkerRejectsInventedPublicSource(t *testing.T) {
	h, model, id, _ := newAPIProfileFixture(t)
	h.profiles.search = &apiProfileSearch{}
	if w := profileAPIPatch(t, h, id, map[string]any{"public_name": "公开姓名", "public_search_enabled": true, "confirmed": true, "model_config_id": apiProfileConfig}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	model.run = func(ctx context.Context, in agent.ProfileInput) (agent.ProfileResult, error) {
		result := apiProfileResult(in)
		result.Output.SourceIDs = []string{"invented-source-from-another-contact"}
		result.Output.Facts[0].SourceIDs = []string{"invented-source-from-another-contact"}
		return result, nil
	}
	enqueueAPIProfile(t, h, id, "enrich")
	err := h.profiles.perform(context.Background(), claimAPIProfile(t, h))
	var bad *agent.Error
	if !errors.As(err, &bad) || bad.Kind != agent.InvalidOutput {
		t.Fatal("model invented a source outside authorized search results", err)
	}
	if p := profileAPIGet(t, h, id); p.Summary != "" || len(p.Facts) != 0 || len(p.Sources) != 0 {
		t.Fatal("invalid public model result was persisted")
	}
}

func TestProfileOrganizeSkipsOversizedConfirmedFactsAndBoundsJSONBudget(t *testing.T) {
	h, model, id, _ := newAPIProfileFixture(t)
	search := &apiProfileSearch{}
	h.profiles.search = search
	if w := profileAPIPatch(t, h, id, map[string]any{"public_name": "公开姓名", "public_search_enabled": true, "confirmed": true, "model_config_id": apiProfileConfig}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	longBio := strings.Repeat("文", 300)         // 900 UTF-8 bytes, valid as a stored public-summary candidate.
	escapedTooLarge := strings.Repeat("<", 700) // Each '<' occupies six bytes after JSON encoding.
	model.run = func(ctx context.Context, in agent.ProfileInput) (agent.ProfileResult, error) {
		if in.Purpose != "enrich" {
			t.Fatal("fixture expected public enrichment")
		}
		result := apiProfileResult(in)
		result.Output.Summary = longBio
		result.Output.Facts = []agent.ProfileFact{{Field: "project", Value: escapedTooLarge, MessageIDs: []string{}, SourceIDs: []string{in.Sources[0].ID}}}
		for _, suffix := range "abcdefg" {
			result.Output.Facts = append(result.Output.Facts, agent.ProfileFact{Field: "project", Value: strings.Repeat(`"`, 700) + string(suffix), MessageIDs: []string{}, SourceIDs: []string{in.Sources[0].ID}})
		}
		return result, nil
	}
	enqueueAPIProfile(t, h, id, "enrich")
	if err := h.profiles.perform(context.Background(), claimAPIProfile(t, h)); err != nil {
		t.Fatal("synthetic public candidates failed", err)
	}
	p := profileAPIGet(t, h, id)
	if len(p.Facts) != 9 {
		t.Fatal("fixture did not create eight public facts plus the 900-byte public biography")
	}
	original := make(map[string]string, len(p.Facts))
	for _, fact := range p.Facts {
		original[fact.ID] = fact.Value
		body, _ := json.Marshal(map[string]any{"expected_revision": profileAPIGet(t, h, id).Revision, "state": "confirmed"})
		if w := emailTestRequest(h, "PATCH", profileAPIPath(id)+"/facts/"+fact.ID, string(body), true); w.Code != 200 {
			t.Fatal("manual public candidate confirmation failed", w.Code, w.Body.String())
		}
	}
	p = profileAPIGet(t, h, id)
	confirmedValues := map[string]bool{}
	var confirmedLongBio bool
	for _, fact := range p.Facts {
		if fact.State != "confirmed" {
			t.Fatal("fixture candidate was not confirmed")
		}
		confirmedValues[fact.Field+"\x00"+fact.Value] = true
		confirmedLongBio = confirmedLongBio || fact.Field == "public_bio" && fact.Value == longBio
	}
	if !confirmedLongBio {
		t.Fatal("900-byte public biography was lost before organize")
	}
	checkedInput := false
	model.run = func(ctx context.Context, in agent.ProfileInput) (agent.ProfileResult, error) {
		checkedInput = true
		encoded, err := json.Marshal(in.ConfirmedFacts)
		if err != nil || len(encoded) > 4000 || len(in.ConfirmedFacts) == 0 || len(in.ConfirmedFacts) > 8 {
			t.Fatal("confirmed-fact JSON budget was exceeded or all usable context was dropped")
		}
		for _, fact := range in.ConfirmedFacts {
			if len(fact.Value) > 800 || fact.Value == escapedTooLarge || !confirmedValues[fact.Field+"\x00"+fact.Value] {
				t.Fatal("worker forwarded an oversized, truncated, or invented confirmed fact")
			}
		}
		if in.Purpose != "organize" || agent.ValidateProfileInput(in) != nil {
			t.Fatal("confirmed facts prevented a valid organize input")
		}
		return apiProfileResult(in), nil
	}
	enqueueAPIProfile(t, h, id, "generate")
	if err := h.profiles.perform(context.Background(), claimAPIProfile(t, h)); err != nil {
		t.Fatal("organize was blocked by previously confirmed public facts", err)
	}
	p = profileAPIGet(t, h, id)
	if !checkedInput || model.calls.Load() != 2 || search.calls.Load() != 1 || p.Summary == "" {
		t.Fatal("organize did not finish with bounded confirmed-fact context")
	}
	for _, fact := range p.Facts {
		if before, ok := original[fact.ID]; ok {
			if fact.State != "confirmed" || fact.Value != before {
				t.Fatal("context selection truncated or changed the stored confirmed fact")
			}
			delete(original, fact.ID)
		}
	}
	if len(original) != 0 {
		t.Fatal("context selection deleted stored confirmed facts")
	}
}

func TestProfilePauseCancelsAndJoinsBeforeUpdatingSettings(t *testing.T) {
	h, model, id, _ := newAPIProfileFixture(t)
	entered, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	allowReturn := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(allowReturn)
	model.run = func(ctx context.Context, in agent.ProfileInput) (agent.ProfileResult, error) {
		close(entered)
		<-ctx.Done()
		close(cancelled)
		<-release
		return apiProfileResult(in), nil // A late success must still be discarded.
	}
	if w := profileAPIPatch(t, h, id, map[string]any{"auto_organize": true, "confirmed": true, "model_config_id": apiProfileConfig}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := h.StartBackground(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("automatic job did not start")
	}
	response := make(chan *httptest.ResponseRecorder, 1)
	requestBody, _ := json.Marshal(map[string]any{"expected_revision": profileAPIGet(t, h, id).Revision, "auto_organize": false})
	go func() { response <- emailTestRequest(h, "PATCH", profileAPIPath(id), string(requestBody), true) }()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("pause did not cancel model context")
	}
	select {
	case <-response:
		t.Fatal("pause returned before old job joined")
	default:
	}
	allowReturn()
	select {
	case w := <-response:
		if w.Code != 200 {
			t.Fatal("pause failed", w.Code, w.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pause did not complete after cancellation")
	}
	p := profileAPIGet(t, h, id)
	if p.AutoOrganize || len(p.Facts) != 0 || p.Summary != "" || model.calls.Load() != 1 {
		t.Fatal("canceled late model result saved or new work started")
	}
}

func TestProfileAIPauseCancelsRunningJobAndRejectsNewWork(t *testing.T) {
	h, model, id, _ := newAPIProfileFixture(t)
	entered := make(chan struct{})
	model.run = func(ctx context.Context, in agent.ProfileInput) (agent.ProfileResult, error) {
		close(entered)
		<-ctx.Done()
		return apiProfileResult(in), nil // Pause must reject a late successful provider.
	}
	enqueueAPIProfile(t, h, id, "generate")
	if err := h.StartBackground(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("queued profile job did not enter model")
	}
	h.profiles.activeMu.Lock()
	jobDone := h.profiles.activeDone
	h.profiles.activeMu.Unlock()
	if w := emailTestRequest(h, "PATCH", "/api/v1/conversations/"+id+"/ai-settings", `{"mode":"paused","tone":""}`, true); w.Code != 200 {
		t.Fatal("AI pause rejected", w.Code, w.Body.String())
	}
	select {
	case <-jobDone:
	case <-time.After(5 * time.Second):
		t.Fatal("AI pause did not cancel running job")
	}
	p := profileAPIGet(t, h, id)
	if !p.AIPaused || len(p.Facts) != 0 || p.Summary != "" || model.calls.Load() != 1 {
		t.Fatal("paused AI persisted a late model result")
	}
	body, _ := json.Marshal(map[string]any{"expected_revision": p.Revision, "confirmed": true, "model_config_id": apiProfileConfig})
	if w := emailTestRequest(h, "POST", profileAPIPath(id)+"/generate", string(body), true); w.Code != 409 || !strings.Contains(w.Body.String(), "AI_PAUSED") {
		t.Fatal("paused AI accepted new profile work", w.Code, w.Body.String())
	}
}

func TestProfileCloseCancelsRunningJobAndNoConstructedHandlerDispatch(t *testing.T) {
	h, model, id, _ := newAPIProfileFixture(t)
	entered, exited := make(chan struct{}), make(chan struct{})
	model.run = func(ctx context.Context, in agent.ProfileInput) (agent.ProfileResult, error) {
		close(entered)
		<-ctx.Done()
		close(exited)
		return agent.ProfileResult{}, ctx.Err()
	}
	enqueueAPIProfile(t, h, id, "generate")
	if model.calls.Load() != 0 || h.profiles.cancel != nil {
		t.Fatal("construction/request dispatched work before explicit lifecycle start")
	}
	if err := h.StartBackground(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("queued model job did not start")
	}
	closed := make(chan error, 1)
	go func() { closed <- h.Close() }()
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not cancel running model")
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close failed to wait for worker")
	}
	if model.calls.Load() != 1 {
		t.Fatal("Close started another model request")
	}
}

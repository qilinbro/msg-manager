package integration_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qilinbro/msg-manager/internal/api"
	"github.com/qilinbro/msg-manager/internal/config"
	"github.com/qilinbro/msg-manager/internal/connector"
	"github.com/qilinbro/msg-manager/internal/domain"
)

const token = "test-token-with-at-least-32-characters"

func newHandler(t *testing.T) http.Handler {
	t.Helper()
	h, err := api.NewHandler(config.Config{Addr: config.DefaultAddr, APIToken: token})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func request(h http.Handler, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://"+config.DefaultAddr+path, nil)
	for key, value := range headers {
		r.Header.Set(key, value)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func checkError(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status=%d want=%d body=%s", w.Code, status, w.Body.String())
	}
	var body api.ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != code || body.Error.Message == "" || body.Error.TraceID == "" || body.Error.TraceID != w.Header().Get("X-Trace-ID") {
		t.Fatalf("invalid error envelope: %+v", body)
	}
}

func TestHealthDoesNotClaimProductionReadiness(t *testing.T) {
	h := newHandler(t)
	live := request(h, http.MethodGet, "/health/live", nil)
	if live.Code != http.StatusOK || !strings.Contains(live.Body.String(), `"stage":"scaffold"`) {
		t.Fatalf("unexpected liveness: %d %s", live.Code, live.Body.String())
	}
	checkError(t, request(h, http.MethodGet, "/health/ready", nil), http.StatusServiceUnavailable, "NOT_READY")
	if _, err := api.NewHandler(config.Config{Addr: config.DefaultAddr}); err == nil {
		t.Fatal("created handler without mandatory credentials")
	}
}

func TestAllBusinessRoutesRequireBearer(t *testing.T) {
	h := newHandler(t)
	for _, path := range []string{"/api/v1", "/api/v1/system", "/api/v1/connections", "/api/v1/messages", "/api/v1/contacts", "/api/v1/drafts", "/api/v1/approvals", "/api/v1/jobs", "/api/v1/audit", "/api/v1/reports", "/api/v1/not-found"} {
		for _, value := range []string{"", "Basic " + token, "Bearer wrong", "Bearer " + token + " ", "bearer " + token} {
			checkError(t, request(h, http.MethodGet, path, map[string]string{"Authorization": value}), http.StatusUnauthorized, "UNAUTHORIZED")
		}
	}
	r := httptest.NewRequest(http.MethodGet, "http://"+config.DefaultAddr+"/api/v1/system", nil)
	r.Header.Add("Authorization", "Bearer "+token)
	r.Header.Add("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	checkError(t, w, http.StatusUnauthorized, "UNAUTHORIZED")
}

func TestSystemReportsOnlyPlannedCapabilities(t *testing.T) {
	h := newHandler(t)
	w := request(h, http.MethodGet, "/api/v1/system", map[string]string{"Authorization": "Bearer " + token})
	if w.Code != http.StatusOK {
		t.Fatalf("system status: %d", w.Code)
	}
	var body api.SystemResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Version != "0.1.0" || body.Stage != "scaffold" || body.WorkspaceID != domain.LocalWorkspaceID || !body.Automation.Paused || body.Automation.AutoSendEnabled || body.Storage.Configured || body.Storage.Driver != "sqlite" {
		t.Fatalf("misleading system state: %+v", body)
	}
	assertPlannedConnectors(t, body.Connectors)
	connections := request(h, http.MethodGet, "/api/v1/connections", map[string]string{"Authorization": "Bearer " + token})
	var list api.ListResponse[connector.Descriptor]
	if err := json.Unmarshal(connections.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if list.NextCursor != nil {
		t.Fatal("unexpected next cursor")
	}
	assertPlannedConnectors(t, list.Items)
}

func assertPlannedConnectors(t *testing.T, items []connector.Descriptor) {
	t.Helper()
	if len(items) != 5 || items[0].ID != "qq-onebot" || items[1].ID != "weixin-clawbot" || items[2].ID != "telegram-bot" || items[3].ID != "email-imap" || items[4].ID != "wechat-db" {
		t.Fatalf("unexpected connector catalog: %+v", items)
	}
	for _, item := range items {
		expectedImplementation := "planned"
		if item.ID == "qq-onebot" {
			expectedImplementation = "probe_ready"
		}
		if item.ID == "email-imap" {
			expectedImplementation = "manual_sync_ready"
		}
		if item.ID == "wechat-db" {
			expectedImplementation = "manual_read_ready"
		}
		if item.Status != "not_configured" || item.ImplementationStatus != expectedImplementation || item.Capabilities.ReceiveText || item.Capabilities.SendText || item.Capabilities.HistoryImport || item.Description == "" || item.DisplayName == "" {
			t.Fatalf("connector falsely claims implemented capabilities: %+v", item)
		}
	}
}

func TestCollectionsHaveNoFabricatedData(t *testing.T) {
	h := newHandler(t)
	for _, path := range []string{"messages", "contacts", "drafts", "approvals", "jobs", "audit", "reports"} {
		w := request(h, http.MethodGet, "/api/v1/"+path, map[string]string{"Authorization": "Bearer " + token})
		if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"items":[],"next_cursor":null}` {
			t.Fatalf("%s returned fabricated or non-contract data: %d %s", path, w.Code, w.Body.String())
		}
	}
}

func TestEventStreamIsExplicitlyUnavailableBehindSecurityBoundary(t *testing.T) {
	h := newHandler(t)
	headers := map[string]string{
		"Authorization": "Bearer " + token,
		"Accept":        "text/event-stream",
		"Last-Event-ID": "previous-cursor",
		"Origin":        "http://127.0.0.1:1420",
	}
	w := request(h, http.MethodGet, "/api/v1/events?cursor=previous-cursor", headers)
	checkError(t, w, http.StatusNotImplemented, "NOT_IMPLEMENTED")
	if w.Header().Get("Content-Type") != "application/json; charset=utf-8" ||
		strings.Contains(w.Body.String(), "previous-cursor") ||
		strings.Contains(w.Body.String(), "data:") ||
		strings.Contains(w.Body.String(), token) ||
		strings.Contains(w.Header().Get("Authorization"), token) {
		t.Fatal("unimplemented event endpoint exposed credentials or claimed a live stream/cursor")
	}
	checkError(t, request(h, http.MethodGet, "/api/v1/events", nil), http.StatusUnauthorized, "UNAUTHORIZED")
	checkError(t, request(h, http.MethodGet, "/api/v1/events", map[string]string{
		"Origin": "https://untrusted.example",
	}), http.StatusForbidden, "ORIGIN_DENIED")
	r := httptest.NewRequest(http.MethodGet, "http://"+config.DefaultAddr+"/api/v1/events", nil)
	r.Host = "untrusted.example:17890"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	checkError(t, w, http.StatusForbidden, "HOST_DENIED")
}

func TestEveryMutationIsUnavailable(t *testing.T) {
	h := newHandler(t)
	paths := []string{"/api/v1/connections", "/api/v1/connections/test/revoke", "/api/v1/imports", "/api/v1/contacts", "/api/v1/contacts/test", "/api/v1/contacts/test/policy", "/api/v1/drafts/test", "/api/v1/approvals/test/approve", "/api/v1/approvals/test/reject", "/api/v1/outbox/test/resolve", "/api/v1/automation/pause", "/api/v1/automation/resume", "/api/v1/send", "/api/v1/unregistered"}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		for _, path := range paths {
			checkError(t, request(h, method, path, map[string]string{"Authorization": "Bearer " + token}), http.StatusNotImplemented, "NOT_IMPLEMENTED")
			checkError(t, request(h, method, path, nil), http.StatusUnauthorized, "UNAUTHORIZED")
		}
	}
	checkError(t, request(h, http.MethodGet, "/api/v1/unregistered", map[string]string{"Authorization": "Bearer " + token}), http.StatusNotFound, "NOT_FOUND")
}

func TestOriginsAndHostsAreStrictlyValidated(t *testing.T) {
	h := newHandler(t)
	for _, origin := range []string{"http://127.0.0.1:1420", "http://localhost:1420", "tauri://localhost", "http://tauri.localhost", "https://tauri.localhost"} {
		w := request(h, http.MethodGet, "/api/v1/system", map[string]string{"Authorization": "Bearer " + token, "Origin": origin})
		if w.Code != http.StatusOK || w.Header().Get("Access-Control-Allow-Origin") != origin {
			t.Fatalf("allowed origin rejected: %q", origin)
		}
	}
	for _, origin := range []string{"null", "https://evil.example", "http://127.0.0.1:1420.evil.example", "http://localhost:1421", "http://localhost:1420/", "http://localhost:1420, https://evil.example"} {
		w := request(h, http.MethodGet, "/api/v1/system", map[string]string{"Authorization": "Bearer " + token, "Origin": origin})
		checkError(t, w, http.StatusForbidden, "ORIGIN_DENIED")
		if w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("CORS reflected an untrusted origin")
		}
	}
	for _, host := range []string{"evil.example:17890", "localhost.evil.example:17890", "192.168.0.1:17890", "localhost:80", "localhost", "localhost.:17890"} {
		r := httptest.NewRequest(http.MethodGet, "http://"+config.DefaultAddr+"/api/v1/system", nil)
		r.Host = host
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		checkError(t, w, http.StatusForbidden, "HOST_DENIED")
	}
	w := request(h, http.MethodGet, "/api/v1/system", map[string]string{"Authorization": "Bearer " + token, "Sec-Fetch-Site": "cross-site"})
	checkError(t, w, http.StatusForbidden, "ORIGIN_DENIED")
}

func TestBrowserPreflightIsReadOnlyAndDoesNotBypassAuth(t *testing.T) {
	h := newHandler(t)
	headers := map[string]string{"Origin": "http://127.0.0.1:1420", "Access-Control-Request-Method": "GET", "Access-Control-Request-Headers": "authorization, content-type"}
	w := request(h, http.MethodOptions, "/api/v1/system", headers)
	if w.Code != http.StatusNoContent || w.Body.Len() != 0 || w.Header().Get("Access-Control-Allow-Methods") != "GET" {
		t.Fatalf("invalid CORS preflight response: %d %s", w.Code, w.Body.String())
	}
	checkError(t, request(h, http.MethodGet, "/api/v1/system", headers), http.StatusUnauthorized, "UNAUTHORIZED")
	headers["Access-Control-Request-Method"] = "POST"
	checkError(t, request(h, http.MethodOptions, "/api/v1/system", headers), http.StatusForbidden, "PREFLIGHT_DENIED")
	headers["Access-Control-Request-Method"] = "GET"
	headers["Access-Control-Request-Headers"] = "x-admin-token"
	checkError(t, request(h, http.MethodOptions, "/api/v1/system", headers), http.StatusForbidden, "PREFLIGHT_DENIED")
}

func TestWorkspaceIsDerivedFromServerIdentity(t *testing.T) {
	h := newHandler(t)
	for _, query := range []string{"?workspace_id=other", "?workspace_id=local-default&workspace_id=other", "?workspace_id="} {
		checkError(t, request(h, http.MethodGet, "/api/v1/system"+query, map[string]string{"Authorization": "Bearer " + token}), http.StatusForbidden, "SCOPE_DENIED")
	}
	checkError(t, request(h, http.MethodGet, "/api/v1/messages", map[string]string{"Authorization": "Bearer " + token, "X-Workspace-ID": "other"}), http.StatusForbidden, "SCOPE_DENIED")
}

func TestResponsesNeverExposeCredentials(t *testing.T) {
	h := newHandler(t)
	for _, path := range []string{"/health/live", "/health/ready", "/api/v1/system", "/api/v1/connections", "/api/v1/messages", "/api/v1/" + token} {
		w := request(h, http.MethodGet, path, map[string]string{"Authorization": "Bearer " + token})
		if strings.Contains(w.Body.String(), token) || strings.Contains(w.Header().Get("Authorization"), token) {
			t.Fatal("response exposed an API credential")
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("sensitive response is cacheable")
		}
	}
}

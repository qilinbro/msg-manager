package integration_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/qilinbro/msg-manager/internal/api"
	"github.com/qilinbro/msg-manager/internal/config"
	"github.com/qilinbro/msg-manager/internal/connector/onebot"
)

const qqVerifyPath = "/api/v1/connections/qq-onebot/verify"
const upstreamToken = "synthetic-onebot-test-token"
const qqAccount = "123456789"

func TestQQVerificationNeedsOperatorConfigurationAndAuth(t *testing.T) {
	h := newHandler(t)
	checkError(t, request(h, http.MethodGet, qqVerifyPath, nil), http.StatusUnauthorized, "UNAUTHORIZED")
	checkError(t, request(h, http.MethodGet, qqVerifyPath, map[string]string{"Origin": "https://untrusted.example"}), http.StatusForbidden, "ORIGIN_DENIED")
	checkError(t, request(h, http.MethodGet, qqVerifyPath, map[string]string{"Authorization": "Bearer " + token}), http.StatusServiceUnavailable, "QQ_NOT_CONFIGURED")
	// The browser cannot supply an alternative URL or upstream access token.
	checkError(t, request(h, http.MethodGet, qqVerifyPath+"?url=http://untrusted.example&access_token=other", map[string]string{"Authorization": "Bearer " + token}), http.StatusServiceUnavailable, "QQ_NOT_CONFIGURED")
}

func TestQQVerificationChecksRealTransportWithoutEnablingMessaging(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+upstreamToken || r.Method != http.MethodPost {
			t.Error("missing OneBot header authentication or wrong method")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/get_login_info":
			_, _ = w.Write([]byte(`{"status":"ok","retcode":0,"data":{"user_id":123456789,"nickname":"测试昵称"}}`))
		case "/get_status":
			_, _ = w.Write([]byte(`{"status":"ok","retcode":0,"data":{"online":true,"good":true}}`))
		default:
			t.Error("unexpected action; probe must never send messages")
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()
	h, err := api.NewHandler(config.Config{Addr: config.DefaultAddr, APIToken: token,
		QQOneBot: onebot.Config{URL: upstream.URL, AccessToken: upstreamToken, ExpectedSelfID: qqAccount}})
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Authorization": "Bearer " + token}
	checkError(t, request(h, http.MethodGet, qqVerifyPath, nil), http.StatusUnauthorized, "UNAUTHORIZED")
	if calls.Load() != 0 {
		t.Fatal("unauthenticated request reached provider")
	}
	w := request(h, http.MethodGet, qqVerifyPath, headers)
	if w.Code != http.StatusOK {
		t.Fatalf("verification status %d: %s", w.Code, w.Body.String())
	}
	var result api.QQVerification
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.State != "identity_verified" || result.AccountID != qqAccount || !result.Online || result.ReceivingEnabled || result.SendingEnabled || result.CheckedAt == "" {
		t.Fatalf("verification falsely enabled messaging: %+v", result)
	}
	if strings.Contains(w.Body.String(), upstreamToken) || strings.Contains(w.Body.String(), upstream.URL) {
		t.Fatal("provider credential/address leaked")
	}
	system := request(h, http.MethodGet, "/api/v1/system", headers)
	if calls.Load() != 2 {
		t.Fatal("status display unexpectedly re-probed the provider")
	}
	var state api.SystemResponse
	if err := json.Unmarshal(system.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Connectors[0].Status != "identity_verified" || state.Connectors[0].Capabilities.ReceiveText || state.Connectors[0].Capabilities.SendText || state.Automation.AutoSendEnabled {
		t.Fatal("verification overstated account readiness")
	}
	checkError(t, request(h, http.MethodGet, "/health/ready", nil), http.StatusServiceUnavailable, "NOT_READY")
	checkError(t, request(h, http.MethodPost, "/api/v1/send", headers), http.StatusNotImplemented, "NOT_IMPLEMENTED")
}

func TestQQVerificationFailureNeverEchoesProviderData(t *testing.T) {
	for _, tc := range []struct {
		name, response, code string
		status               int
	}{
		{"wrong_account", `{"status":"ok","retcode":0,"data":{"user_id":987654321,"nickname":"PRIVATE"}}`, "QQ_IDENTITY_MISMATCH", 200},
		{"unauthorized", "PRIVATE " + upstreamToken, "QQ_AUTH_FAILED", 401},
		{"bad_response", "PRIVATE " + upstreamToken, "QQ_PROTOCOL_ERROR", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.response))
			}))
			defer upstream.Close()
			h, err := api.NewHandler(config.Config{Addr: config.DefaultAddr, APIToken: token, QQOneBot: onebot.Config{URL: upstream.URL, AccessToken: upstreamToken, ExpectedSelfID: qqAccount}})
			if err != nil {
				t.Fatal(err)
			}
			w := request(h, http.MethodGet, qqVerifyPath, map[string]string{"Authorization": "Bearer " + token})
			checkError(t, w, http.StatusServiceUnavailable, tc.code)
			for _, sensitive := range []string{upstreamToken, upstream.URL, "PRIVATE", "987654321"} {
				if strings.Contains(w.Body.String(), sensitive) {
					t.Fatal("upstream details leaked")
				}
			}
		})
	}
}

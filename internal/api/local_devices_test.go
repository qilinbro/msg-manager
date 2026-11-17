package api

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qilinbro/msg-manager/internal/config"
	"github.com/qilinbro/msg-manager/internal/store"
)

const deviceTestOrigin = "http://127.0.0.1:1420"

type syntheticLocalDevice struct {
	id      string
	private *ecdsa.PrivateKey
	expires time.Time
}

type syntheticDeviceChallenge struct {
	ID        string `json:"challenge_id"`
	Nonce     string `json:"nonce"`
	ExpiresAt string `json:"expires_at"`
	Message   string `json:"message"`
}

func localDeviceRequest(h *handler, method, path, body, token, origin string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, h.deviceServiceOrigin()+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func pairSyntheticLocalDevice(t *testing.T, h *handler, master string) syntheticLocalDevice {
	t.Helper()
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&private.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"public_key":"` + base64.RawURLEncoding.EncodeToString(der) + `"}`
	w := localDeviceRequest(h, http.MethodPost, localDevicesPath, body, master, deviceTestOrigin)
	var result struct {
		DeviceID       string `json:"device_id"`
		ExpiresAt      string `json:"expires_at"`
		ServiceOrigin  string `json:"service_origin"`
		FrontendOrigin string `json:"frontend_origin"`
	}
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
		t.Fatal("synthetic device pairing failed", w.Code, w.Body.String())
	}
	if _, ok := deviceRawURL(result.DeviceID, 16); !ok || result.ServiceOrigin != h.deviceServiceOrigin() || result.FrontendOrigin != deviceTestOrigin || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Set-Cookie") != "" {
		t.Fatal("unbound or ambient pairing response")
	}
	expires, err := time.Parse(time.RFC3339, result.ExpiresAt)
	if err != nil || expires.Sub(h.deviceAuth.clock()) > deviceGrantLifetime || expires.Sub(h.deviceAuth.clock()) < deviceGrantLifetime-time.Minute {
		t.Fatal("unexpected grant lifetime")
	}
	if strings.Contains(w.Body.String(), master) || strings.Contains(w.Body.String(), "access_token") || strings.Contains(w.Body.String(), "private") {
		t.Fatal("pairing leaked a secret")
	}
	return syntheticLocalDevice{id: result.DeviceID, private: private, expires: expires}
}

func challengeSyntheticDevice(t *testing.T, h *handler, device syntheticLocalDevice) syntheticDeviceChallenge {
	t.Helper()
	w := localDeviceRequest(h, http.MethodPost, deviceChallengesPath, `{"device_id":"`+device.id+`"}`, "", deviceTestOrigin)
	var result syntheticDeviceChallenge
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
		t.Fatal("synthetic challenge failed", w.Code, w.Body.String())
	}
	_, validID := deviceRawURL(result.ID, 32)
	_, validNonce := deviceRawURL(result.Nonce, 32)
	expires, err := time.Parse(time.RFC3339, result.ExpiresAt)
	if !validID || !validNonce || err != nil || expires.Sub(h.deviceAuth.clock()) > deviceChallengeTTL || expires.Sub(h.deviceAuth.clock()) < deviceChallengeTTL-2*time.Second {
		t.Fatal("invalid or overlong challenge")
	}
	expected := strings.Join([]string{"sociald-device-auth-v1", h.deviceServiceOrigin(), deviceTestOrigin, device.id, result.ID, result.Nonce, result.ExpiresAt}, "\n")
	if result.Message != expected {
		t.Fatal("challenge was not bound to exact service, frontend, device and expiry")
	}
	return result
}

func signSyntheticDevice(t *testing.T, device syntheticLocalDevice, challenge syntheticDeviceChallenge, message string) string {
	t.Helper()
	digest := sha256.Sum256([]byte(message))
	r, s, err := ecdsa.Sign(rand.Reader, device.private, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])
	body, err := json.Marshal(map[string]string{"device_id": device.id, "challenge_id": challenge.ID, "signature": base64.RawURLEncoding.EncodeToString(signature)})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func openSyntheticDeviceSession(t *testing.T, h *handler, device syntheticLocalDevice) string {
	t.Helper()
	challenge := challengeSyntheticDevice(t, h, device)
	w := localDeviceRequest(h, http.MethodPost, deviceSessionsPath, signSyntheticDevice(t, device, challenge, challenge.Message), "", deviceTestOrigin)
	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresAt   string `json:"expires_at"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || !strings.HasPrefix(result.AccessToken, "sda1.") {
		t.Fatal("synthetic session failed", w.Code)
	}
	expires, err := time.Parse(time.RFC3339, result.ExpiresAt)
	if err != nil || !expires.After(h.deviceAuth.clock()) || expires.Sub(h.deviceAuth.clock()) > deviceAccessLifetime || expires.After(device.expires) {
		t.Fatal("unexpected access lifetime")
	}
	return result.AccessToken
}

func TestLocalDeviceAuthenticationAndImmediateRevocation(t *testing.T) {
	h := newEmailTestHandler(t)
	if w := localDeviceRequest(h, "POST", localDevicesPath, `{}`, "", deviceTestOrigin); w.Code != 401 {
		t.Fatal("anonymous pairing accepted", w.Code)
	}
	device := pairSyntheticLocalDevice(t, h, emailTestToken)
	access := openSyntheticDeviceSession(t, h, device)
	if access == openSyntheticDeviceSession(t, h, device) {
		t.Fatal("sessions did not receive independent nonces")
	}
	for _, entry := range []struct {
		path, token, origin string
		status              int
	}{
		{"/api/v1/system", access, deviceTestOrigin, 200},
		{"/api/v1/system", access, "http://localhost:1420", 401},
		{"/api/v1/system", access, "", 401},
		{"/api/v1/system", access, "null", 403},
		{"/api/v1/system", access, "https://attacker.example", 403},
		{"/api/v1/system?workspace_id=foreign", access, deviceTestOrigin, 403},
		{"/api/v1/system", emailTestToken, "", 200},
	} {
		if w := localDeviceRequest(h, "GET", entry.path, "", entry.token, entry.origin); w.Code != entry.status {
			t.Fatal("authorization boundary", entry.path, entry.origin, w.Code)
		}
	}
	if w := localDeviceRequest(h, "POST", localDevicesPath, `{}`, access, deviceTestOrigin); w.Code != 403 || !strings.Contains(w.Body.String(), "MASTER_REQUIRED") {
		t.Fatal("device access minted another grant", w.Code)
	}
	other := pairSyntheticLocalDevice(t, h, emailTestToken)
	if w := localDeviceRequest(h, "DELETE", localDevicesPath+"/"+other.id, "", access, deviceTestOrigin); w.Code != 403 {
		t.Fatal("device revoked another device", w.Code)
	}
	for i := 0; i < 2; i++ {
		if w := localDeviceRequest(h, "DELETE", localDevicesPath+"/"+other.id, "", emailTestToken, deviceTestOrigin); w.Code != 204 {
			t.Fatal("master revoke not idempotent", w.Code)
		}
	}
	pending := challengeSyntheticDevice(t, h, device)
	if w := localDeviceRequest(h, "DELETE", localDevicesPath+"/"+device.id, "", access, deviceTestOrigin); w.Code != 204 {
		t.Fatal("self revoke failed", w.Code)
	}
	if w := localDeviceRequest(h, "GET", "/api/v1/system", "", access, deviceTestOrigin); w.Code != 401 {
		t.Fatal("revoked access survived", w.Code)
	}
	if w := localDeviceRequest(h, "POST", deviceSessionsPath, signSyntheticDevice(t, device, pending, pending.Message), "", deviceTestOrigin); w.Code != 401 {
		t.Fatal("revoke left a usable pending challenge", w.Code)
	}
	if w := localDeviceRequest(h, "POST", deviceChallengesPath, `{"device_id":"`+device.id+`"}`, "", deviceTestOrigin); w.Code != 401 {
		t.Fatal("revoked device created a fresh challenge", w.Code)
	}
}

func TestLocalDeviceProofIsOriginBoundSingleUseAndConcurrentSafe(t *testing.T) {
	h := newEmailTestHandler(t)
	device := pairSyntheticLocalDevice(t, h, emailTestToken)
	challenge := challengeSyntheticDevice(t, h, device)
	body := signSyntheticDevice(t, device, challenge, challenge.Message)
	if w := localDeviceRequest(h, "POST", deviceSessionsPath, body, "", "http://localhost:1420"); w.Code != 401 {
		t.Fatal("proof crossed frontend origins", w.Code)
	}
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- localDeviceRequest(h, "POST", deviceSessionsPath, body, "", deviceTestOrigin).Code
		}()
	}
	wg.Wait()
	close(codes)
	counts := map[int]int{}
	for code := range codes {
		counts[code]++
	}
	if counts[200] != 1 || counts[401] != 1 {
		t.Fatal("a proof was reused or another origin consumed it", counts)
	}
	for _, change := range []func(string) string{
		func(s string) string {
			return strings.Replace(s, h.deviceServiceOrigin(), "http://localhost:"+h.port, 1)
		},
		func(s string) string { return strings.Replace(s, deviceTestOrigin, "http://localhost:1420", 1) },
		func(s string) string { return s + "\nextra" },
	} {
		challenge := challengeSyntheticDevice(t, h, device)
		wrong := signSyntheticDevice(t, device, challenge, change(challenge.Message))
		if w := localDeviceRequest(h, "POST", deviceSessionsPath, wrong, "", deviceTestOrigin); w.Code != 401 {
			t.Fatal("a modified message was accepted", w.Code)
		}
		correct := signSyntheticDevice(t, device, challenge, challenge.Message)
		if w := localDeviceRequest(h, "POST", deviceSessionsPath, correct, "", deviceTestOrigin); w.Code != 401 {
			t.Fatal("failed proof did not consume its challenge", w.Code)
		}
	}
}

func TestLocalDeviceExpiryAndChallengeCacheBounds(t *testing.T) {
	h := newEmailTestHandler(t)
	device := pairSyntheticLocalDevice(t, h, emailTestToken)
	access := openSyntheticDeviceSession(t, h, device)
	base := time.Now().UTC().Truncate(time.Second)
	h.deviceAuth.now = func() time.Time { return base }
	for i := 0; i < maxDeviceChallenges; i++ {
		challengeSyntheticDevice(t, h, device)
	}
	w := localDeviceRequest(h, "POST", deviceChallengesPath, `{"device_id":"`+device.id+`"}`, "", deviceTestOrigin)
	if w.Code != 429 || w.Header().Get("Retry-After") != "60" {
		t.Fatal("challenge cache was not bounded", w.Code)
	}
	h.deviceAuth.now = func() time.Time { return base.Add(deviceChallengeTTL) }
	challenge := challengeSyntheticDevice(t, h, device)
	if len(h.deviceAuth.challenges) != 1 {
		t.Fatal("expired challenges were retained")
	}
	body := signSyntheticDevice(t, device, challenge, challenge.Message)
	h.deviceAuth.now = func() time.Time { return base.Add(2 * deviceChallengeTTL) }
	if w := localDeviceRequest(h, "POST", deviceSessionsPath, body, "", deviceTestOrigin); w.Code != 401 {
		t.Fatal("challenge valid at its expiry boundary", w.Code)
	}
	h.deviceAuth.now = func() time.Time { return base.Add(deviceAccessLifetime + time.Second) }
	if w := localDeviceRequest(h, "GET", "/api/v1/system", "", access, deviceTestOrigin); w.Code != 401 {
		t.Fatal("access token did not expire", w.Code)
	}
	openSyntheticDeviceSession(t, h, device)
	h.deviceAuth.now = func() time.Time { return device.expires }
	if w := localDeviceRequest(h, "POST", deviceChallengesPath, `{"device_id":"`+device.id+`"}`, "", deviceTestOrigin); w.Code != 401 {
		t.Fatal("grant valid at its expiry boundary", w.Code)
	}
}

func TestLocalDeviceStrictJSONHostOriginAndPreflight(t *testing.T) {
	h := newEmailTestHandler(t)
	device := pairSyntheticLocalDevice(t, h, emailTestToken)
	good := `{"device_id":"` + device.id + `"}`
	for _, bad := range []string{`null`, `[]`, `{}`, good + `{}`, `{"device_id":null}`, `{"device_id":42}`, `{"Device_ID":"` + device.id + `"}`, `{"device_id":"` + device.id + `","DEVICE_ID":"other"}`, `{"device_id":"` + device.id + `","device_id":"` + device.id + `"}`, `{"device_id":"` + device.id + `","url":"http://attacker.example"}`, "{\"device_id\":\"\xff\"}"} {
		if w := localDeviceRequest(h, "POST", deviceChallengesPath, bad, "", deviceTestOrigin); w.Code != 400 {
			t.Fatal("ambiguous JSON accepted", w.Code)
		}
	}
	if w := localDeviceRequest(h, "POST", deviceChallengesPath, strings.Repeat(" ", maxDeviceJSONBytes+1), "", deviceTestOrigin); w.Code != 413 {
		t.Fatal("unbounded auth body", w.Code)
	}
	for _, entry := range []struct {
		name   string
		mutate func(*http.Request)
		status int
	}{
		{"no origin", func(r *http.Request) { r.Header.Del("Origin") }, 403},
		{"null origin", func(r *http.Request) { r.Header.Set("Origin", "null") }, 403},
		{"duplicate origin", func(r *http.Request) { r.Header.Add("Origin", deviceTestOrigin) }, 403},
		{"wrong host", func(r *http.Request) { r.Host = "attacker.example:" + h.port }, 403},
		{"non JSON", func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, 400},
		{"charset", func(r *http.Request) { r.Header.Set("Content-Type", "application/json;charset=latin1") }, 400},
		{"duplicate content type", func(r *http.Request) { r.Header.Add("Content-Type", "application/json") }, 400},
		{"scope", func(r *http.Request) { r.Header.Set("X-Workspace-ID", "foreign") }, 403},
	} {
		t.Run(entry.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", h.deviceServiceOrigin()+deviceChallengesPath, strings.NewReader(good))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Origin", deviceTestOrigin)
			entry.mutate(r)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != entry.status {
				t.Fatal("auth boundary", w.Code)
			}
		})
	}
	for _, path := range []string{deviceChallengesPath + "?ignored=1", "/api/v1/auth/%63hallenges"} {
		if w := localDeviceRequest(h, "POST", path, good, "", deviceTestOrigin); w.Code != 400 {
			t.Fatal("noncanonical endpoint accepted", w.Code)
		}
	}
	for _, entry := range []struct{ method, path string }{{"POST", localDevicesPath}, {"POST", deviceChallengesPath}, {"POST", deviceSessionsPath}, {"DELETE", localDevicesPath + "/" + device.id}} {
		r := httptest.NewRequest("OPTIONS", h.deviceServiceOrigin()+entry.path, nil)
		r.Header.Set("Origin", deviceTestOrigin)
		r.Header.Set("Access-Control-Request-Method", entry.method)
		r.Header.Set("Access-Control-Request-Headers", "content-type,authorization")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 204 || w.Header().Get("Access-Control-Allow-Origin") != deviceTestOrigin {
			t.Fatal("trusted preflight rejected", w.Code)
		}
		r.Header.Set("Access-Control-Request-Headers", "x-workspace-id")
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("extra preflight authority allowed", w.Code)
		}
	}
	private, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&private.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"invalid", base64.RawURLEncoding.EncodeToString(der)} {
		if w := localDeviceRequest(h, "POST", localDevicesPath, `{"public_key":"`+value+`"}`, emailTestToken, deviceTestOrigin); w.Code != 400 {
			t.Fatal("invalid or non P-256 pairing key accepted", w.Code)
		}
	}
}

func TestLocalDeviceBackendRestartAndPermanentIssuerInvalidation(t *testing.T) {
	cfg := config.Config{Addr: config.DefaultAddr, APIToken: emailTestToken, DatabasePath: filepath.Join(t.TempDir(), "devices.db"), DatabaseKey: strings.Repeat("ab", 32)}
	open := func() *handler {
		t.Helper()
		result, err := NewHandler(cfg)
		if err != nil {
			t.Fatal(err)
		}
		return result.(*handler)
	}
	h := open()
	t.Cleanup(func() { _ = h.Close() })
	device := pairSyntheticLocalDevice(t, h, cfg.APIToken)
	access := openSyntheticDeviceSession(t, h, device)
	pending := challengeSyntheticDevice(t, h, device)
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	h = open()
	if w := localDeviceRequest(h, "GET", "/api/v1/system", "", access, deviceTestOrigin); w.Code != 200 {
		t.Fatal("stable backend restart lost authorization", w.Code)
	}
	if w := localDeviceRequest(h, "POST", deviceSessionsPath, signSyntheticDevice(t, device, pending, pending.Message), "", deviceTestOrigin); w.Code != 401 {
		t.Fatal("restart restored an old challenge", w.Code)
	}
	openSyntheticDeviceSession(t, h, device)
	for _, master := range []string{strings.Repeat("B", 48), emailTestToken} {
		if err := h.Close(); err != nil {
			t.Fatal(err)
		}
		cfg.APIToken = master
		h = open()
		if w := localDeviceRequest(h, "POST", deviceChallengesPath, `{"device_id":"`+device.id+`"}`, "", deviceTestOrigin); w.Code != 401 {
			t.Fatal("A-B-A master change resurrected a grant", w.Code)
		}
		if w := localDeviceRequest(h, "GET", "/api/v1/system", "", access, deviceTestOrigin); w.Code != 401 {
			t.Fatal("old access survived a master change", w.Code)
		}
	}
	device = pairSyntheticLocalDevice(t, h, cfg.APIToken)
	for _, addr := range []string{"127.0.0.1:17891", config.DefaultAddr} {
		if err := h.Close(); err != nil {
			t.Fatal(err)
		}
		cfg.Addr = addr
		h = open()
		if w := localDeviceRequest(h, "POST", deviceChallengesPath, `{"device_id":"`+device.id+`"}`, "", deviceTestOrigin); w.Code != 401 {
			t.Fatal("A-B-A service address change resurrected a grant", w.Code)
		}
	}
}

func TestLocalDeviceListenerIPChangesPermanentlyPurgeAndDoNotBreakMaster(t *testing.T) {
	for _, changedAddr := range []string{"127.0.0.2:17890", "[::1]:17890"} {
		t.Run(changedAddr, func(t *testing.T) {
			cfg := config.Config{Addr: config.DefaultAddr, APIToken: emailTestToken, DatabasePath: filepath.Join(t.TempDir(), "listener.db"), DatabaseKey: strings.Repeat("cd", 32)}
			open := func() *handler {
				t.Helper()
				result, err := NewHandler(cfg)
				if err != nil {
					t.Fatal(err)
				}
				return result.(*handler)
			}
			h := open()
			t.Cleanup(func() { _ = h.Close() })
			device := pairSyntheticLocalDevice(t, h, cfg.APIToken)
			access := openSyntheticDeviceSession(t, h, device)
			issuer := h.deviceIssuerHash()
			if err := h.Close(); err != nil {
				t.Fatal(err)
			}
			cfg.Addr = changedAddr
			h = open()
			if h.deviceIssuerHash() == issuer {
				t.Fatal("actual listener IP was not bound to the issuer")
			}
			if _, err := h.inbox.LoadLocalDevice(context.Background(), device.id); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("IP-only address change did not physically purge old grant", err)
			}
			if w := localDeviceRequest(h, "GET", "/api/v1/system", "", cfg.APIToken, deviceTestOrigin); w.Code != 200 {
				t.Fatal("legacy master authentication was disabled", w.Code)
			}
			if w := localDeviceRequest(h, "GET", "/api/v1/system", "", access, deviceTestOrigin); w.Code != 401 {
				t.Fatal("old device bearer accepted at a different listener IP", w.Code)
			}
			for _, entry := range []struct{ path, body, token string }{
				{localDevicesPath, `{}`, cfg.APIToken},
				{deviceChallengesPath, `{"device_id":"` + device.id + `"}`, ""},
				{deviceSessionsPath, `{}`, ""},
			} {
				if w := localDeviceRequest(h, "POST", entry.path, entry.body, entry.token, deviceTestOrigin); w.Code != 503 || !strings.Contains(w.Body.String(), "AUTH_UNAVAILABLE") {
					t.Fatal("noncanonical listener advertised browser device authorization", entry.path, w.Code)
				}
			}
			if err := h.Close(); err != nil {
				t.Fatal(err)
			}
			cfg.Addr = config.DefaultAddr
			h = open()
			if w := localDeviceRequest(h, "POST", deviceChallengesPath, `{"device_id":"`+device.id+`"}`, "", deviceTestOrigin); w.Code != 401 {
				t.Fatal("A-B-A listener IP change resurrected old proof authorization", w.Code)
			}
			if w := localDeviceRequest(h, "GET", "/api/v1/system", "", access, deviceTestOrigin); w.Code != 401 {
				t.Fatal("A-B-A listener IP change resurrected old access", w.Code)
			}
		})
	}
}

func TestLocalDeviceAuthBypassesAIConfigurationLock(t *testing.T) {
	h := newEmailTestHandler(t)
	device := pairSyntheticLocalDevice(t, h, emailTestToken)
	access := openSyntheticDeviceSession(t, h, device)
	challenge := challengeSyntheticDevice(t, h, device)
	der, err := x509.MarshalPKIXPublicKey(&device.private.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct {
		method, path, body, token string
		status                    int
	}{
		{"POST", localDevicesPath, `{"public_key":"` + base64.RawURLEncoding.EncodeToString(der) + `"}`, emailTestToken, 201},
		{"POST", deviceChallengesPath, `{"device_id":"` + device.id + `"}`, "", 200},
		{"POST", deviceSessionsPath, signSyntheticDevice(t, device, challenge, challenge.Message), "", 200},
		{"DELETE", localDevicesPath + "/" + device.id, "", access, 204},
	} {
		h.aiMu.Lock()
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			done <- localDeviceRequest(h, entry.method, entry.path, entry.body, entry.token, deviceTestOrigin)
		}()
		select {
		case response := <-done:
			h.aiMu.Unlock()
			if response.Code != entry.status {
				t.Fatal("authentication failed while AI lock held", entry.path, response.Code)
			}
		case <-time.After(2 * time.Second):
			h.aiMu.Unlock()
			t.Fatal("authentication blocked behind AI configuration lock", entry.path)
		}
	}
}

func TestLocalDeviceProtocolLifetimesAndBounds(t *testing.T) {
	if deviceGrantLifetime != 90*24*time.Hour || deviceAccessLifetime != 15*time.Minute || deviceChallengeTTL != time.Minute || maxDeviceChallenges != 256 || maxDeviceJSONBytes != 4096 {
		t.Fatal("public authentication protocol bounds changed without a contract update")
	}
}

func TestLocalDevicePublicIssuerCannotSignAndErrorsDoNotRevoke(t *testing.T) {
	h := newEmailTestHandler(t)
	device := pairSyntheticLocalDevice(t, h, emailTestToken)
	access := openSyntheticDeviceSession(t, h, device)
	parts := strings.Split(access, ".")
	payload := strings.Join(parts[:4], ".")
	publicIssuer, err := hex.DecodeString(h.deviceIssuerHash())
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, publicIssuer)
	_, _ = mac.Write([]byte("sociald-device-access-v1\x00" + payload))
	forged := payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	for _, bad := range []string{forged, "device:" + device.id, access + "extra", strings.Replace(access, "sda1", "sda2", 1)} {
		if w := localDeviceRequest(h, "GET", "/api/v1/system", "", bad, deviceTestOrigin); w.Code != 401 || strings.Contains(w.Body.String(), bad) || strings.Contains(w.Body.String(), emailTestToken) {
			t.Fatal("forged access accepted or echoed", w.Code)
		}
	}
	tooLong, err := h.issueDeviceAccess(device.id, time.Now().UTC().Add(deviceAccessLifetime+time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if w := localDeviceRequest(h, "GET", "/api/v1/system", "", tooLong, deviceTestOrigin); w.Code != 401 {
		t.Fatal("overlong access accepted", w.Code)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("GET", h.deviceServiceOrigin()+"/api/v1/system", nil).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+access)
	r.Header.Set("Origin", deviceTestOrigin)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatal("canceled storage lookup became a revoked grant", w.Code)
	}
	if w = localDeviceRequest(h, "GET", "/api/v1/system", "", access, deviceTestOrigin); w.Code != 200 {
		t.Fatal("temporary failure deleted grant", w.Code)
	}
	if err := h.inbox.Close(); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct{ method, path, body, token string }{{"GET", "/api/v1/system", "", access}, {"POST", deviceChallengesPath, fmt.Sprintf(`{"device_id":%q}`, device.id), ""}} {
		if w = localDeviceRequest(h, entry.method, entry.path, entry.body, entry.token, deviceTestOrigin); w.Code != 503 || !strings.Contains(w.Body.String(), "AUTH_UNAVAILABLE") {
			t.Fatal("storage outage misreported as revoked authorization", w.Code)
		}
	}
}

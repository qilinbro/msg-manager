package api

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/qilinbro/msg-manager/internal/domain"
	"github.com/qilinbro/msg-manager/internal/store"
)

const (
	localDevicesPath     = "/api/v1/auth/devices"
	deviceChallengesPath = "/api/v1/auth/challenges"
	deviceSessionsPath   = "/api/v1/auth/sessions"
	deviceGrantLifetime  = 90 * 24 * time.Hour
	deviceAccessLifetime = 15 * time.Minute
	deviceChallengeTTL   = time.Minute
	maxDeviceChallenges  = 256
	maxDeviceJSONBytes   = 4096
)

type devicePrincipalKey struct{}

type deviceChallenge struct {
	deviceID       string
	frontendOrigin string
	serviceOrigin  string
	expiresAt      time.Time
	message        string
}

type localDeviceAuth struct {
	mu         sync.Mutex
	challenges map[string]deviceChallenge
	closed     bool
	// Tests may replace the clock before serving requests. Production uses UTC.
	now func() time.Time
}

func (a *localDeviceAuth) clock() time.Time {
	if a.now != nil {
		return a.now().UTC()
	}
	return time.Now().UTC()
}

func (h *handler) deviceServiceOrigin() string { return "http://127.0.0.1:" + h.port }

func (h *handler) supportsLocalDevices() bool { return h.listenAddr == "127.0.0.1:"+h.port }

func (h *handler) deviceIssuerHash() string {
	// This public binding is deliberately NOT the MAC key used for access tokens.
	// Bind the actual listener too: canonical browser URLs must not conceal an
	// IP-only address change and let a proxy at the old listener relay proofs.
	hash := sha256.New()
	_, _ = hash.Write([]byte("sociald-device-issuer-v1\x00"))
	_, _ = hash.Write(h.tokenHash[:])
	_, _ = hash.Write([]byte("\x00" + h.listenAddr))
	return hex.EncodeToString(hash.Sum(nil))
}

func (h *handler) restoreLocalDevices() error {
	if h.inbox == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Purge on startup, not just at authentication: A -> B -> A master-token or
	// address changes must not resurrect an earlier browser authorization.
	return h.inbox.PruneLocalDevices(ctx, h.deviceIssuerHash(), h.deviceServiceOrigin(), h.deviceAuth.clock())
}

func (h *handler) closeLocalDevices() {
	h.deviceAuth.mu.Lock()
	defer h.deviceAuth.mu.Unlock()
	h.deviceAuth.closed = true
	h.deviceAuth.challenges = nil
}

func deviceIDFromPath(path string) (string, bool) {
	if !strings.HasPrefix(path, localDevicesPath+"/") {
		return "", false
	}
	id := strings.TrimPrefix(path, localDevicesPath+"/")
	_, ok := deviceRawURL(id, 16)
	return id, ok
}

func deviceAuthMutation(method, path string) bool {
	if method == http.MethodPost {
		return path == localDevicesPath || path == deviceChallengesPath || path == deviceSessionsPath
	}
	_, ok := deviceIDFromPath(path)
	return method == http.MethodDelete && ok
}

func publicDeviceAuth(method, path string) bool {
	return method == http.MethodPost && (path == deviceChallengesPath || path == deviceSessionsPath)
}

func deviceRawURL(value string, size int) ([]byte, bool) {
	if len(value) != base64.RawURLEncoding.EncodedLen(size) {
		return nil, false
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(value)
	return decoded, err == nil && len(decoded) == size && base64.RawURLEncoding.EncodeToString(decoded) == value
}

func randomDeviceValue(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

// Only these two proof endpoints are anonymous. Host/Origin checks and CORS
// validation in ServeHTTP run first; there are no cookies or ambient authority.
func (h *handler) serveDeviceAuth(w http.ResponseWriter, r *http.Request, traceID string) {
	origin := r.Header.Get("Origin")
	if origin == "" || !allowedOrigins[origin] {
		writeError(w, http.StatusForbidden, "ORIGIN_DENIED", "设备授权必须来自受支持的本机工作台。", traceID)
		return
	}
	if !validScopeClaims(r, domain.LocalWorkspaceID) {
		writeError(w, http.StatusForbidden, "SCOPE_DENIED", "请求不属于当前工作区。", traceID)
		return
	}
	if r.URL.RawQuery != "" || r.URL.RawPath != "" {
		writeError(w, http.StatusBadRequest, "AUTH_INVALID_REQUEST", "设备授权不接受查询参数或编码路径。", traceID)
		return
	}
	if !h.supportsLocalDevices() {
		writeError(w, http.StatusServiceUnavailable, "AUTH_UNAVAILABLE", "设备授权仅支持规范的 127.0.0.1 本机监听地址。", traceID)
		return
	}
	h.deviceAuth.mu.Lock()
	closed := h.deviceAuth.closed
	h.deviceAuth.mu.Unlock()
	if h.inbox == nil || closed {
		writeError(w, http.StatusServiceUnavailable, "AUTH_UNAVAILABLE", "本机持久授权暂不可用；请检查后台与加密消息库。", traceID)
		return
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == localDevicesPath:
		h.createLocalDevice(w, r, traceID)
	case r.Method == http.MethodPost && r.URL.Path == deviceChallengesPath:
		h.challengeLocalDevice(w, r, traceID)
	case r.Method == http.MethodPost && r.URL.Path == deviceSessionsPath:
		h.openDeviceSession(w, r, traceID)
	case r.Method == http.MethodDelete:
		if id, ok := deviceIDFromPath(r.URL.Path); ok {
			h.revokeLocalDevice(w, r, id, traceID)
			return
		}
		fallthrough
	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "该设备授权接口不存在。", traceID)
	}
}

// Auth requests have an independent 4 KiB limit and an exact, case-sensitive
// field set. A generic struct decoder would also accept differently-cased JSON
// names, creating ambiguous duplicate aliases for security-critical fields.
func readDeviceJSON(w http.ResponseWriter, r *http.Request, names []string, traceID string) (map[string]string, bool) {
	invalid := func() (map[string]string, bool) {
		writeError(w, http.StatusBadRequest, "AUTH_INVALID_REQUEST", "设备授权请求格式不正确，请重新连接。", traceID)
		return nil, false
	}
	kind, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || kind != "application/json" || len(r.Header.Values("Content-Type")) != 1 || len(params) > 1 || (len(params) == 1 && !strings.EqualFold(params["charset"], "utf-8")) {
		return invalid()
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxDeviceJSONBytes+1))
	if err != nil {
		return invalid()
	}
	if len(body) > maxDeviceJSONBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "设备授权请求不能超过 4 KiB。", traceID)
		return nil, false
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 || body[0] != '{' || !utf8.Valid(body) || !uniqueJSON(body) {
		return invalid()
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(body, &raw) != nil || len(raw) != len(names) {
		return invalid()
	}
	values := make(map[string]string, len(names))
	for _, name := range names {
		value, ok := raw[name]
		var text string
		if !ok || json.Unmarshal(value, &text) != nil || text == "" {
			return invalid()
		}
		values[name] = text
	}
	if r.Context().Err() != nil {
		writeError(w, http.StatusServiceUnavailable, "AUTH_UNAVAILABLE", "设备授权请求已取消，请重新连接。", traceID)
		return nil, false
	}
	return values, true
}

func (h *handler) createLocalDevice(w http.ResponseWriter, r *http.Request, traceID string) {
	// A browser access token can use the workspace, but cannot mint another
	// long-lived authorization. Pairing always requires the original master.
	if !validToken(r, h.tokenHash) {
		writeError(w, http.StatusForbidden, "MASTER_REQUIRED", "新增设备授权需要重新输入本机服务主令牌。", traceID)
		return
	}
	input, ok := readDeviceJSON(w, r, []string{"public_key"}, traceID)
	if !ok {
		return
	}
	publicDER, err := base64.RawURLEncoding.Strict().DecodeString(input["public_key"])
	var public *ecdsa.PublicKey
	if err == nil && len(publicDER) <= 256 && base64.RawURLEncoding.EncodeToString(publicDER) == input["public_key"] {
		parsed, parseErr := x509.ParsePKIXPublicKey(publicDER)
		if parseErr == nil {
			public, _ = parsed.(*ecdsa.PublicKey)
		}
	}
	if public == nil || public.Curve != elliptic.P256() || public.X == nil || public.Y == nil || !public.Curve.IsOnCurve(public.X, public.Y) {
		writeError(w, http.StatusBadRequest, "AUTH_INVALID_REQUEST", "设备授权需要有效的 P-256 公钥。", traceID)
		return
	}
	id, err := randomDeviceValue(16)
	if err != nil {
		writeDeviceStoreError(w, err, traceID)
		return
	}
	now := h.deviceAuth.clock().Truncate(time.Second)
	device := store.LocalDevice{ID: id, PublicKey: publicDER, ServiceOrigin: h.deviceServiceOrigin(), FrontendOrigin: r.Header.Get("Origin"), IssuerHash: h.deviceIssuerHash(), CreatedAt: now, ExpiresAt: now.Add(deviceGrantLifetime)}
	if err := h.inbox.CreateLocalDevice(r.Context(), device); err != nil {
		writeDeviceStoreError(w, err, traceID)
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		DeviceID       string `json:"device_id"`
		ExpiresAt      string `json:"expires_at"`
		ServiceOrigin  string `json:"service_origin"`
		FrontendOrigin string `json:"frontend_origin"`
	}{id, device.ExpiresAt.Format(time.RFC3339), device.ServiceOrigin, device.FrontendOrigin})
}

func (h *handler) loadAuthorizedDevice(ctx context.Context, id, origin string) (store.LocalDevice, error) {
	device, err := h.inbox.LoadLocalDevice(ctx, id)
	if err != nil {
		return store.LocalDevice{}, err
	}
	if device.ServiceOrigin != h.deviceServiceOrigin() || device.FrontendOrigin != origin || subtle.ConstantTimeCompare([]byte(device.IssuerHash), []byte(h.deviceIssuerHash())) != 1 || !h.deviceAuth.clock().Before(device.ExpiresAt) {
		return store.LocalDevice{}, store.ErrNotFound
	}
	return device, nil
}

func (h *handler) challengeLocalDevice(w http.ResponseWriter, r *http.Request, traceID string) {
	input, ok := readDeviceJSON(w, r, []string{"device_id"}, traceID)
	if !ok {
		return
	}
	id := input["device_id"]
	if _, ok := deviceRawURL(id, 16); !ok {
		writeDeviceStoreError(w, store.ErrNotFound, traceID)
		return
	}
	if _, err := h.loadAuthorizedDevice(r.Context(), id, r.Header.Get("Origin")); err != nil {
		writeDeviceStoreError(w, err, traceID)
		return
	}
	challengeID, err := randomDeviceValue(32)
	if err != nil {
		writeDeviceStoreError(w, err, traceID)
		return
	}
	nonce, err := randomDeviceValue(32)
	if err != nil {
		writeDeviceStoreError(w, err, traceID)
		return
	}
	now := h.deviceAuth.clock().Truncate(time.Second)
	expires := now.Add(deviceChallengeTTL)
	expiresText := expires.Format(time.RFC3339)
	origin, service := r.Header.Get("Origin"), h.deviceServiceOrigin()
	message := strings.Join([]string{"sociald-device-auth-v1", service, origin, id, challengeID, nonce, expiresText}, "\n")
	h.deviceAuth.mu.Lock()
	for key, challenge := range h.deviceAuth.challenges {
		if !now.Before(challenge.expiresAt) {
			delete(h.deviceAuth.challenges, key)
		}
	}
	if h.deviceAuth.closed {
		h.deviceAuth.mu.Unlock()
		writeDeviceStoreError(w, store.ErrUnavailable, traceID)
		return
	}
	if len(h.deviceAuth.challenges) >= maxDeviceChallenges {
		h.deviceAuth.mu.Unlock()
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "AUTH_BUSY", "本机授权请求较多，请稍后重试。", traceID)
		return
	}
	if h.deviceAuth.challenges == nil {
		h.deviceAuth.challenges = make(map[string]deviceChallenge)
	}
	h.deviceAuth.challenges[challengeID] = deviceChallenge{deviceID: id, frontendOrigin: origin, serviceOrigin: service, expiresAt: expires, message: message}
	h.deviceAuth.mu.Unlock()
	writeJSON(w, http.StatusOK, struct {
		ChallengeID string `json:"challenge_id"`
		Nonce       string `json:"nonce"`
		ExpiresAt   string `json:"expires_at"`
		Message     string `json:"message"`
	}{challengeID, nonce, expiresText, message})
}

func (h *handler) openDeviceSession(w http.ResponseWriter, r *http.Request, traceID string) {
	input, ok := readDeviceJSON(w, r, []string{"device_id", "challenge_id", "signature"}, traceID)
	if !ok {
		return
	}
	id, challengeID := input["device_id"], input["challenge_id"]
	_, validID := deviceRawURL(id, 16)
	_, validChallenge := deviceRawURL(challengeID, 32)
	signature, validSignature := deviceRawURL(input["signature"], 64)
	if !validID || !validChallenge || !validSignature {
		writeDeviceStoreError(w, store.ErrNotFound, traceID)
		return
	}
	origin := r.Header.Get("Origin")
	h.deviceAuth.mu.Lock()
	challenge, found := h.deviceAuth.challenges[challengeID]
	matched := found && !h.deviceAuth.closed && challenge.deviceID == id && challenge.frontendOrigin == origin && challenge.serviceOrigin == h.deviceServiceOrigin()
	if matched {
		// Consume before signature verification. The same challenge can never
		// create two sessions, including under concurrent requests or bad proofs.
		delete(h.deviceAuth.challenges, challengeID)
	}
	h.deviceAuth.mu.Unlock()
	if !matched || !h.deviceAuth.clock().Before(challenge.expiresAt) {
		writeDeviceStoreError(w, store.ErrNotFound, traceID)
		return
	}
	device, err := h.loadAuthorizedDevice(r.Context(), id, origin)
	if err != nil {
		writeDeviceStoreError(w, err, traceID)
		return
	}
	parsed, err := x509.ParsePKIXPublicKey(device.PublicKey)
	public, _ := parsed.(*ecdsa.PublicKey)
	if err != nil || public == nil || public.Curve != elliptic.P256() {
		writeDeviceStoreError(w, store.ErrUnavailable, traceID)
		return
	}
	digest := sha256.Sum256([]byte(challenge.message))
	if !ecdsa.Verify(public, digest[:], new(big.Int).SetBytes(signature[:32]), new(big.Int).SetBytes(signature[32:])) {
		writeDeviceStoreError(w, store.ErrNotFound, traceID)
		return
	}
	expires := h.deviceAuth.clock().Truncate(time.Second).Add(deviceAccessLifetime)
	if device.ExpiresAt.Before(expires) {
		expires = device.ExpiresAt
	}
	access, err := h.issueDeviceAccess(id, expires)
	if err != nil {
		writeDeviceStoreError(w, err, traceID)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		AccessToken string `json:"access_token"`
		ExpiresAt   string `json:"expires_at"`
	}{access, expires.Format(time.RFC3339)})
}

func (h *handler) deviceAccessMAC(payload string) []byte {
	mac := hmac.New(sha256.New, h.tokenHash[:])
	_, _ = mac.Write([]byte("sociald-device-access-v1\x00"))
	_, _ = mac.Write([]byte(payload))
	return mac.Sum(nil)
}

func (h *handler) issueDeviceAccess(id string, expires time.Time) (string, error) {
	nonce, err := randomDeviceValue(16)
	if err != nil {
		return "", err
	}
	payload := strings.Join([]string{"sda1", id, strconv.FormatInt(expires.Unix(), 10), nonce}, ".")
	return payload + "." + base64.RawURLEncoding.EncodeToString(h.deviceAccessMAC(payload)), nil
}

func (h *handler) authorizeDeviceAccess(r *http.Request) (string, error) {
	values := r.Header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") || len(values[0]) > 4103 || r.Header.Get("Origin") == "" || h.inbox == nil || !h.supportsLocalDevices() {
		return "", store.ErrNotFound
	}
	value := strings.TrimPrefix(values[0], "Bearer ")
	parts := strings.Split(value, ".")
	if len(parts) != 5 || parts[0] != "sda1" {
		return "", store.ErrNotFound
	}
	_, validID := deviceRawURL(parts[1], 16)
	_, validNonce := deviceRawURL(parts[3], 16)
	mac, validMAC := deviceRawURL(parts[4], sha256.Size)
	if !validID || !validNonce || !validMAC {
		return "", store.ErrNotFound
	}
	expires, err := strconv.ParseInt(parts[2], 10, 64)
	now := h.deviceAuth.clock()
	if err != nil || strconv.FormatInt(expires, 10) != parts[2] || !now.Before(time.Unix(expires, 0)) || expires > now.Add(deviceAccessLifetime).Unix() || !hmac.Equal(mac, h.deviceAccessMAC(strings.Join(parts[:4], "."))) {
		return "", store.ErrNotFound
	}
	h.deviceAuth.mu.Lock()
	closed := h.deviceAuth.closed
	h.deviceAuth.mu.Unlock()
	if closed {
		return "", store.ErrUnavailable
	}
	device, err := h.loadAuthorizedDevice(r.Context(), parts[1], r.Header.Get("Origin"))
	if err != nil {
		return "", err
	}
	if expires > device.ExpiresAt.Unix() {
		return "", store.ErrNotFound
	}
	return device.ID, nil
}

func (h *handler) authenticateRequest(w http.ResponseWriter, r *http.Request, traceID string) (*http.Request, bool) {
	ctx := withLocalPrincipal(r.Context())
	if validToken(r, h.tokenHash) {
		return r.WithContext(ctx), true
	}
	id, err := h.authorizeDeviceAccess(r)
	if err == nil {
		return r.WithContext(context.WithValue(ctx, devicePrincipalKey{}, id)), true
	}
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrInvalidInput) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "需要有效的本机访问令牌或设备授权。", traceID)
	} else {
		writeDeviceStoreError(w, err, traceID)
	}
	return r, false
}

func (h *handler) revokeLocalDevice(w http.ResponseWriter, r *http.Request, id, traceID string) {
	if !validToken(r, h.tokenHash) {
		current, _ := r.Context().Value(devicePrincipalKey{}).(string)
		if current != id {
			writeError(w, http.StatusForbidden, "DEVICE_SCOPE_DENIED", "当前设备只能撤销自身授权。", traceID)
			return
		}
	}
	if err := h.inbox.RevokeLocalDevice(r.Context(), id); err != nil {
		writeDeviceStoreError(w, err, traceID)
		return
	}
	h.deviceAuth.mu.Lock()
	for key, challenge := range h.deviceAuth.challenges {
		if challenge.deviceID == id {
			delete(h.deviceAuth.challenges, key)
		}
	}
	h.deviceAuth.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func writeDeviceStoreError(w http.ResponseWriter, err error, traceID string) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeError(w, http.StatusUnauthorized, "DEVICE_UNAUTHORIZED", "设备授权已失效，请重新输入本机服务主令牌。", traceID)
	case errors.Is(err, store.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "AUTH_INVALID_REQUEST", "设备授权信息无效，请重新连接。", traceID)
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "DEVICE_LIMIT_REACHED", "设备授权数量已达上限，请撤销不再使用的授权后重试。", traceID)
	default:
		writeError(w, http.StatusServiceUnavailable, "AUTH_UNAVAILABLE", "本机持久授权暂不可用；请检查后台与加密消息库。", traceID)
	}
}

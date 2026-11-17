package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qilinbro/msg-manager/internal/api"
	"github.com/qilinbro/msg-manager/internal/config"
)

func TestBindLocalHandlerOccupiedPortPreservesDeviceGrants(t *testing.T) {
	// This listener and encrypted database belong only to this synthetic test.
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = occupied.Close() })
	cfg := config.Config{
		Addr:         occupied.Addr().String(),
		APIToken:     strings.Repeat("a", 32),
		DatabasePath: filepath.Join(t.TempDir(), "synthetic-inbox.db"),
		DatabaseKey:  strings.Repeat("1", 64),
	}
	active, err := api.NewHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = active.(interface{ Close() error }).Close() })

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	request := func(path, body, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "http://"+cfg.Addr+path, strings.NewReader(body))
		r.Header.Set("Origin", "http://127.0.0.1:1420")
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		active.ServeHTTP(w, r)
		return w
	}
	paired := request("/api/v1/auth/devices", `{"public_key":"`+base64.RawURLEncoding.EncodeToString(der)+`"}`, cfg.APIToken)
	var device struct {
		ID string `json:"device_id"`
	}
	if paired.Code != http.StatusCreated || json.Unmarshal(paired.Body.Bytes(), &device) != nil || device.ID == "" {
		t.Fatalf("synthetic enrollment failed: status %d", paired.Code)
	}

	replacement := cfg
	replacement.APIToken = strings.Repeat("b", 32)
	listener, handler, err := bindLocalHandler(replacement)
	if listener != nil {
		_ = listener.Close()
	}
	if closer, ok := handler.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
	if err == nil {
		t.Fatal("duplicate startup unexpectedly acquired the occupied port")
	}
	if listener != nil || handler != nil {
		t.Fatal("failed startup published a listener or handler")
	}
	challenge := request("/api/v1/auth/challenges", `{"device_id":"`+device.ID+`"}`, "")
	if challenge.Code != http.StatusOK {
		t.Fatalf("failed duplicate startup revoked the active service's device: status %d", challenge.Code)
	}
}

func TestBindLocalHandlerConstructionFailureReleasesPort(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Addr: addr, APIToken: strings.Repeat("a", 32),
		// A directory, not a database file: validation passes, store opening fails.
		DatabasePath: t.TempDir(), DatabaseKey: strings.Repeat("1", 64),
	}
	listener, handler, err := bindLocalHandler(cfg)
	if listener != nil {
		_ = listener.Close()
	}
	if closer, ok := handler.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
	if err == nil || listener != nil || handler != nil {
		t.Fatal("handler construction failure did not fail closed")
	}
	rebound, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal("failed startup leaked its listener", err)
	}
	_ = rebound.Close()
}

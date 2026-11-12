package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"net"
	"net/http"
	"strings"

	"github.com/qilinbro/msg-manager/internal/domain"
)

var allowedOrigins = map[string]bool{
	"http://127.0.0.1:1420":   true,
	"http://localhost:1420":   true,
	"tauri://localhost":       true,
	"http://tauri.localhost":  true,
	"https://tauri.localhost": true,
}

type principalKey struct{}

type principal struct {
	workspaceID string
}

func withLocalPrincipal(ctx context.Context) context.Context {
	return context.WithValue(ctx, principalKey{}, principal{workspaceID: domain.LocalWorkspaceID})
}

func authenticatedWorkspace(ctx context.Context) string {
	p, _ := ctx.Value(principalKey{}).(principal)
	return p.workspaceID
}

func validHost(authority, port string) bool {
	host, requestPort, err := net.SplitHostPort(authority)
	if err != nil || requestPort != port {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validToken(r *http.Request, expected [32]byte) bool {
	values := r.Header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
		return false
	}
	token := strings.TrimPrefix(values[0], "Bearer ")
	if len(token) < 32 || len(token) > 4096 {
		return false
	}
	digest := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(digest[:], expected[:]) == 1
}

func validScopeClaims(r *http.Request, workspaceID string) bool {
	// Claims never select a workspace; conflicting client claims are rejected.
	for _, value := range r.URL.Query()["workspace_id"] {
		if value != workspaceID {
			return false
		}
	}
	for _, value := range r.Header.Values("X-Workspace-ID") {
		if value != workspaceID {
			return false
		}
	}
	return true
}

func validPreflightHeaders(r *http.Request) bool {
	for _, value := range r.Header.Values("Access-Control-Request-Headers") {
		for _, field := range strings.Split(value, ",") {
			switch strings.ToLower(strings.TrimSpace(field)) {
			case "authorization", "content-type":
			default:
				return false
			}
		}
	}
	return true
}

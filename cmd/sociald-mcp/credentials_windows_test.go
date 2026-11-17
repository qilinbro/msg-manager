//go:build windows

package main

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/qilinbro/msg-manager/internal/localcredentials/credentialstest"
)

func savedCredentialCommand(t *testing.T, baseURL, path string) *exec.Cmd {
	t.Helper()
	cmd := processCommand(t, processTestContext(t), baseURL, processTestToken)
	filtered := cmd.Env[:0]
	for _, setting := range cmd.Env {
		if !strings.HasPrefix(setting, "SOCIALD_API_TOKEN=") && !strings.HasPrefix(setting, "SOCIALD_CREDENTIAL_FILE=") {
			filtered = append(filtered, setting)
		}
	}
	cmd.Env = append(filtered, "SOCIALD_CREDENTIAL_FILE="+path)
	return cmd
}

func TestMCPExecutableSavedCredentialRestart(t *testing.T) {
	var requests atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+processTestToken || r.URL.Path != "/api/v1/system" {
			t.Error("saved-credential child sent an unexpected authenticated request")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"version":"synthetic-saved-credential"}`)
	}))
	t.Cleanup(backend.Close)
	path := credentialstest.Write(t, backend.URL, processTestToken)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for attempt := range 2 {
		ctx := processTestContext(t)
		p := &processClient{cmd: savedCredentialCommand(t, backend.URL, path)}
		p.cmd.Stderr = &p.stderr
		client := sdk.NewClient(&sdk.Implementation{Name: "synthetic-saved-process", Version: "1.0"}, nil)
		p.session, err = client.Connect(ctx, &sdk.CommandTransport{Command: p.cmd, TerminateDuration: 2 * time.Second}, &sdk.ClientSessionOptions{ProtocolVersion: processProtocolVersion})
		if err != nil {
			t.Fatalf("saved credential did not initialize a fresh subprocess: %v", err)
		}
		t.Cleanup(func() { _ = p.session.Close() })
		tools, err := p.session.ListTools(ctx, &sdk.ListToolsParams{})
		if err != nil || len(tools.Tools) != 6 {
			t.Fatal("saved-credential child did not expose the existing six tools")
		}
		if requests.Load() != int32(attempt) {
			t.Fatal("saved credential loading or discovery contacted HTTP")
		}
		result, err := p.session.CallTool(ctx, &sdk.CallToolParams{Name: "get_system_status"})
		if err != nil || result.IsError {
			t.Fatal("saved credential was not accepted by the synthetic authenticated backend")
		}
		p.close(t)
	}
	if requests.Load() != 2 {
		t.Fatal("fresh MCP subprocesses did not each make exactly one tool request")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("MCP changed the saved credential while starting or restarting")
	}
}

func TestMCPExecutableSavedCredentialFailures(t *testing.T) {
	var requests atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "synthetic startup must not contact HTTP", http.StatusInternalServerError)
	}))
	t.Cleanup(backend.Close)
	for _, tc := range []struct {
		name       string
		file       func(*testing.T) string
		emptyToken bool
		invalidURL bool
	}{
		{"missing", func(t *testing.T) string { return filepath.Join(t.TempDir(), "missing-private", "service.dpapi") }, false, false},
		{"wrong-entropy", func(t *testing.T) string {
			return credentialstest.WritePlaintext(t, []byte(`{"version":1}`), "synthetic-wrong-purpose")
		}, false, false},
		{"corrupt-record", func(t *testing.T) string {
			return credentialstest.WritePlaintext(t, []byte(`{"version":1,"extra":true}`), credentialstest.Purpose)
		}, false, false},
		{"url-mismatch", func(t *testing.T) string { return credentialstest.Write(t, "http://127.0.0.1:1", processTestToken) }, false, false},
		{"present-empty-token", func(t *testing.T) string { return credentialstest.Write(t, backend.URL, processTestToken) }, true, false},
		{"url-validated-first", func(t *testing.T) string {
			return credentialstest.WritePlaintext(t, []byte(`{"version":1,"extra":true}`), credentialstest.Purpose)
		}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := tc.file(t)
			before, beforeErr := os.ReadFile(path)
			if beforeErr != nil && !os.IsNotExist(beforeErr) {
				t.Fatal(beforeErr)
			}
			baseURL := backend.URL
			diagnostic := "SOCIALD_CREDENTIAL_FILE"
			if tc.invalidURL {
				baseURL += "/synthetic-disallowed-path"
				diagnostic = "SOCIALD_URL"
			}
			cmd := savedCredentialCommand(t, baseURL, path)
			if tc.emptyToken {
				cmd.Env = append(cmd.Env, "SOCIALD_API_TOKEN=")
				diagnostic = "SOCIALD_API_TOKEN"
			}
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), diagnostic) {
				t.Fatal("saved credential failure did not exit 2 with a stderr-only setting-name diagnostic")
			}
			for _, secret := range []string{processTestToken, path, backend.URL} {
				if strings.Contains(stderr.String(), secret) {
					t.Fatal("saved credential diagnostics exposed synthetic private configuration")
				}
			}
			after, afterErr := os.ReadFile(path)
			if beforeErr != nil {
				if !os.IsNotExist(afterErr) {
					t.Fatal("MCP created a missing credential file")
				}
			} else if afterErr != nil || !bytes.Equal(before, after) {
				t.Fatal("MCP changed a rejected credential file")
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatal("saved credential configuration failures contacted HTTP")
	}
}

func TestMCPExecutableExplicitTokenBypassesBadSavedCredential(t *testing.T) {
	path := credentialstest.WritePlaintext(t, []byte(`{"version":999}`), "synthetic-wrong-purpose")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "synthetic startup must not contact HTTP", http.StatusInternalServerError)
	}))
	t.Cleanup(backend.Close)
	cmd := savedCredentialCommand(t, backend.URL, path)
	cmd.Env = append(cmd.Env, "SOCIALD_API_TOKEN="+processTestToken)
	cmd.Stdin = strings.NewReader("")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil || stdout.Len() != 0 || stderr.Len() != 0 || requests.Load() != 0 {
		t.Fatal("explicit-token clean EOF attempted saved credential validation, HTTP or polluted stdio")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("explicit-token mode changed a saved credential")
	}
}

package agent

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestOfficialCompatibilityPathsRemainHostSchemeAndPortBound(t *testing.T) {
	for _, entry := range []struct{ host, path string }{
		{"generativelanguage.googleapis.com", "/v1beta/openai"},
		{"dashscope.aliyuncs.com", "/compatible-mode/v1"},
	} {
		for _, port := range []string{"", ":443"} {
			for _, trailing := range []string{"", "/"} {
				base := "https://" + entry.host + port + entry.path + trailing
				cfg, err := validateConfig(Config{BaseURL: base, Model: "synthetic-model", APIKey: "synthetic-key"})
				if err != nil || cfg.endpoint.Path != entry.path+"/chat/completions" || cfg.host != entry.host || cfg.port != "443" || cfg.local {
					t.Fatal("official compatibility path rejected or routed incorrectly", base, err)
				}
			}
		}
		for _, base := range []string{
			"https://example.com" + entry.path,
			"https://" + entry.host + ".example.com" + entry.path,
			"https://" + entry.host + ":444" + entry.path,
			"https://" + entry.host + ":" + entry.path,
			"https://" + entry.host + ":0443" + entry.path,
			"https://" + entry.host + "." + entry.path,
			"http://" + entry.host + ":443" + entry.path,
			"http://127.0.0.1:1234" + entry.path,
			"https://" + entry.host + entry.path + "//",
			"https://" + entry.host + entry.path + "/chat/completions",
			"https://" + entry.host + entry.path + "?key=synthetic",
			"https://" + entry.host + entry.path + "#fragment",
			"https://" + entry.host + strings.ReplaceAll(entry.path, "/", "%2F"),
		} {
			if _, err := NewClient(Config{BaseURL: base, Model: "synthetic-model", APIKey: "synthetic-key"}); err == nil {
				t.Fatal("compatibility exception escaped its exact boundary", base)
			}
		}
	}
	for _, base := range []string{
		"https://generativelanguage.googleapis.com/v1beta/models",
		"https://generativelanguage.googleapis.com/compatible-mode/v1",
		"https://dashscope.aliyuncs.com/v1beta/openai",
		"https://dashscope.aliyuncs.com/api/v1",
		"https://api.anthropic.com/v1/messages",
		"https://api.openai.com/v1/responses",
	} {
		if _, err := NewClient(Config{BaseURL: base, Model: "synthetic-model", APIKey: "synthetic-key"}); err == nil {
			t.Fatal("unsupported native/cross-provider path accepted", base)
		}
	}
}

// Only local synthetic TLS servers are contacted. A generated trusted test
// certificate names the actual configured host; TLS name verification, SNI,
// public DNS validation and pinned dialing stay enabled rather than bypassed.
func TestOfficialCompatibilityPathsSendNativeChatToolsOverPinnedTLS(t *testing.T) {
	for _, entry := range []struct{ host, path string }{
		{"generativelanguage.googleapis.com", "/v1beta/openai"},
		{"dashscope.aliyuncs.com", "/compatible-mode/v1"},
	} {
		t.Run(entry.host, func(t *testing.T) {
			key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			certificate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "synthetic-compatibility-test"}, DNSNames: []string{entry.host}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
			der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := x509.ParseCertificate(der)
			if err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				index := calls.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != entry.path+"/chat/completions" || r.URL.RawQuery != "" || r.Host != entry.host || r.TLS == nil || r.TLS.ServerName != entry.host || r.Header.Get("Authorization") != "Bearer synthetic-compatibility-key" {
					t.Error("compatibility endpoint changed host, auth, SNI or path")
				}
				var request toolCompletionRequest
				if json.NewDecoder(r.Body).Decode(&request) != nil || request.Model != "synthetic-user-model" || len(request.Tools) != 1 || request.Tools[0].Function.Name != "get_system_status" || request.ToolChoice != "auto" || request.ParallelToolCalls || request.Stream {
					t.Error("compatibility endpoint lost native bounded tools contract")
				}
				if index == 1 {
					_, _ = io.WriteString(w, assistantToolCompletion())
					return
				}
				if index != 2 || len(request.Messages) != 4 || request.Messages[2].Role != "assistant" || len(request.Messages[2].ToolCalls) != 1 || request.Messages[3].Role != "tool" || request.Messages[3].ToolCallID != "call_1" {
					t.Error("native tool call/result linkage was not preserved")
				}
				_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"Synthetic compatibility response."},"finish_reason":"stop"}]}`)
			}))
			server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}
			server.StartTLS()
			defer server.Close()
			client, err := NewClient(Config{BaseURL: "https://" + entry.host + entry.path, Model: "synthetic-user-model", APIKey: "synthetic-compatibility-key"})
			if err != nil {
				t.Fatal(err)
			}
			client.roots = x509.NewCertPool()
			client.roots.AddCert(parsed)
			client.lookupIP = func(_ context.Context, host string) ([]net.IPAddr, error) {
				if host != entry.host {
					t.Error("DNS validation used an unexpected hostname")
				}
				return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
			}
			client.dialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				if address != "8.8.8.8:443" {
					t.Error("dial was not pinned to validated public destination")
				}
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}
			input := assistantToolInput()
			first, err := client.ChatTools(t.Context(), input)
			if err != nil || len(first.Message.ToolCalls) != 1 {
				t.Fatal("synthetic native tool call failed", err)
			}
			input.Messages = append(input.Messages, first.Message, ToolMessage{Role: "tool", ToolCallID: first.Message.ToolCalls[0].ID, Content: `{"status":"synthetic"}`})
			last, err := client.ChatTools(t.Context(), input)
			if err != nil || len(last.Message.ToolCalls) != 0 || last.Message.Content != "Synthetic compatibility response." || calls.Load() != 2 {
				t.Fatal("synthetic native tool continuation failed", err)
			}
			client.lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
				return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("127.0.0.1")}}, nil
			}
			_, err = client.ChatTools(t.Context(), assistantToolInput())
			kindIs(t, err, InvalidConfig)
			if calls.Load() != 2 {
				t.Fatal("mixed public/private DNS answers reached the provider")
			}
		})
	}
}

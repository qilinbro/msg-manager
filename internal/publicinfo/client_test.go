package publicinfo

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"
)

const testKey = "synthetic-public-provider-secret"

func kindIs(t *testing.T, err error, want Kind) {
	t.Helper()
	var typed *Error
	if !errors.As(err, &typed) || typed.Kind != want || strings.Contains(fmt.Sprint(err), testKey) {
		t.Fatalf("unexpected safe public-search failure kind: %v", err)
	}
}

func searchClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	client, err := NewClient(Config{APIKey: testKey})
	if err != nil {
		t.Fatal(err)
	}
	// A fake public DNS answer is pinned to the synthetic TLS listener. The
	// test-only host matches httptest's certificate; production has no override.
	client.host = "example.com"
	client.lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	}
	client.dialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "8.8.8.8:443" {
			t.Error("search destination was not pinned")
		}
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	client.roots = x509.NewCertPool()
	client.roots.AddCert(server.Certificate())
	return client
}

func sourceResponse(results ...map[string]string) string {
	if results == nil {
		results = []map[string]string{}
	}
	raw, _ := json.Marshal(map[string]any{"results": results, "query": "synthetic query", "response_time": 0.1})
	return string(raw)
}

func result(url, title, content string) map[string]string {
	return map[string]string{"url": url, "title": title, "content": content}
}

func TestOptionalSearchConfigurationAndRedaction(t *testing.T) {
	if ValidateConfig(Config{}) != nil {
		t.Fatal("disabled search blocked startup")
	}
	_, err := NewClient(Config{})
	kindIs(t, err, NotConfigured)
	for _, key := range []string{" private", "private\nkey", strings.Repeat("x", 4097)} {
		_, err = NewClient(Config{APIKey: key})
		kindIs(t, err, InvalidConfig)
	}
	client, _ := NewClient(Config{APIKey: testKey})
	for _, value := range []string{fmt.Sprint(Config{APIKey: testKey}), fmt.Sprintf("%#v", Config{APIKey: testKey}), fmt.Sprint(client), fmt.Sprintf("%#v", client)} {
		if strings.Contains(value, testKey) {
			t.Fatal("configuration formatting exposed provider credential")
		}
	}
}

func TestSafePublicSourceLinksAndSensitiveDetails(t *testing.T) {
	for _, value := range []string{"https://example.com/work", "http://www.example.com:80/works?q=作品", "https://example.com:443/a%20b", "https://8.8.8.8/"} {
		if !SafeURL(value) {
			t.Fatal("safe display link rejected")
		}
	}
	for _, value := range []string{"javascript:alert(1)", "data:text/html,hi", "https://user:password@example.com/", "https://127.0.0.1/", "https://[::1]/", "https://[::ffff:127.0.0.1]/", "https://192.168.0.2/", "https://169.254.169.254/", "https://100.64.0.1/", "https://example.local/", "https://localhost/", "https://example.internal/", "https://example.com:8080/", "https://2130706433/", "https://127.1/", "https://example.com\\@localhost/", "https://example.com/\n"} {
		if SafeURL(value) {
			t.Fatalf("unsafe source link accepted: %q", value)
		}
	}
	for _, value := range []string{"联系test@example.com", "电话13812345678", "家庭住址示例", "medical history unknown", "身份证123456789012345678", "+1 (202) 555-0123"} {
		if !SensitiveText(value) {
			t.Fatal("obvious sensitive contact material accepted")
		}
	}
	if SensitiveText("2019 - 2025 创作公开作品") {
		t.Fatal("ordinary professional date range treated as a contact number")
	}
}

func TestSearchSendsOnlyMinimalConfirmedFieldsAndBoundedResults(t *testing.T) {
	var calls atomic.Int32
	client := searchClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "POST" || r.URL.Path != "/search" || r.Header.Get("Authorization") != "Bearer "+testKey || r.URL.RawQuery != "" {
			t.Error("unexpected search route or credential location")
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if json.Unmarshal(raw, &body) != nil || len(body) != 6 || body["search_depth"] != "basic" || body["max_results"] != float64(5) || body["include_raw_content"] != false || body["include_answer"] != false || body["include_images"] != false {
			t.Error("search request contract changed")
		}
		query, _ := body["query"].(string)
		if !strings.Contains(query, "合成作者") || !strings.Contains(query, "合成公司") || strings.Contains(string(raw), testKey) || strings.Contains(string(raw), "conversation_id") || strings.Contains(string(raw), "messages") {
			t.Error("search must contain only selected public fields")
		}
		_, _ = io.WriteString(w, sourceResponse(
			result("https://example.com/work", "公开作品", "第一行作品介绍\n第二行职业信息"),
			result("https://example.com/work", "重复", "第一行作品介绍\n第二行职业信息"),
			result("https://127.0.0.1/private", "内网", "不应保存"),
			result("https://example.com/phone", "联系方式", "test@example.com"),
			result("https://example.com/more", "另一作品", strings.Repeat("作品", 300)),
		))
	})
	sources, err := client.Search(context.Background(), "合成作者", "合成公司")
	if err != nil || len(sources) != 2 || calls.Load() != 1 || len(sources[1].Snippet) > 1000 || !utf8.ValidString(sources[1].Snippet) || strings.Contains(sources[0].Snippet, "\n") || sources[0].RetrievedAt == "" || len(sources[0].ContentHash) != 64 {
		t.Fatal("bounded validated public sources were not returned", err)
	}
	repeated, err := client.Search(context.Background(), "合成作者", "合成公司")
	if err != nil || repeated[0].ID != sources[0].ID || repeated[1].ID != sources[1].ID {
		t.Fatal("same public evidence was not stable for deduplication")
	}
	for _, name := range []string{"", "wxid_private", "合成\n作者", "test@example.com", "13812345678"} {
		_, err = client.Search(context.Background(), name, "合成公司")
		kindIs(t, err, InvalidInput)
	}
	if calls.Load() != 2 {
		t.Fatal("invalid query reached the search provider")
	}
}

func TestSearchRejectsMixedPrivateDNSAndRequiresVerifiedTLS(t *testing.T) {
	var calls atomic.Int32
	client := searchClient(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); _, _ = io.WriteString(w, sourceResponse()) })
	client.lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("127.0.0.1")}}, nil
	}
	_, err := client.Search(context.Background(), "合成作者", "")
	kindIs(t, err, InvalidConfig)
	if calls.Load() != 0 {
		t.Fatal("mixed public/private DNS reached a network connection")
	}
	client.lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	}
	client.roots = x509.NewCertPool()
	_, err = client.Search(context.Background(), "合成作者", "")
	kindIs(t, err, Unavailable)
	if calls.Load() != 0 {
		t.Fatal("untrusted certificate reached an authenticated request")
	}
}

func TestSearchDoesNotFollowRedirectOrExposeProviderErrors(t *testing.T) {
	for _, status := range []int{302, 401, 403, 402, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			client := searchClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Location", "http://127.0.0.1/private")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, testKey+" private provider detail")
			})
			_, err := client.Search(context.Background(), "合成作者", "")
			want := Unavailable
			if status == 401 || status == 403 {
				want = AuthFailed
			}
			if status == 402 || status == 429 {
				want = QuotaExceeded
			}
			kindIs(t, err, want)
			if calls.Load() != 1 {
				t.Fatal("search request redirected or retried")
			}
		})
	}
}

func TestSearchBoundsMalformedAndEchoedResponsesAndCancellation(t *testing.T) {
	for _, body := range []string{"not json", `{}`, `{"results":null}`, `{"results":[],"echo":"` + testKey + `"}`, strings.Repeat("x", (256<<10)+1), "{\xff}", sourceResponse(result("https://example.com/a", "a", "a"), result("https://example.com/b", "b", "b"), result("https://example.com/c", "c", "c"), result("https://example.com/d", "d", "d"), result("https://example.com/e", "e", "e"), result("https://example.com/f", "f", "f"))} {
		client := searchClient(t, func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, body) })
		_, err := client.Search(context.Background(), "合成作者", "")
		kindIs(t, err, InvalidOutput)
	}
	var calls atomic.Int32
	client := searchClient(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.Search(ctx, "合成作者", "")
	kindIs(t, err, Timeout)
	if calls.Load() != 0 {
		t.Fatal("canceled search reached provider")
	}
}

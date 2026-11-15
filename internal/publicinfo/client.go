// Package publicinfo searches publicly indexed professional information. It
// never receives chat bodies, platform credentials or arbitrary fetch URLs.
package publicinfo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type Config struct{ APIKey string }

func (Config) String() string   { return "public search configuration (redacted)" }
func (Config) GoString() string { return "public search configuration (redacted)" }

type Kind string

const (
	NotConfigured Kind = "not_configured"
	InvalidConfig Kind = "invalid_config"
	InvalidInput  Kind = "invalid_input"
	AuthFailed    Kind = "auth_failed"
	QuotaExceeded Kind = "quota_exceeded"
	Unavailable   Kind = "unavailable"
	Timeout       Kind = "timeout"
	InvalidOutput Kind = "invalid_output"
)

type Error struct{ Kind Kind }

func (e *Error) Error() string { return "public search failed: " + string(e.Kind) }
func fail(kind Kind) error     { return &Error{Kind: kind} }

type Source struct {
	ID          string `json:"id"`
	URL         string `json:"url"`
	Title       string `json:"title"`
	Snippet     string `json:"snippet"`
	ContentHash string `json:"content_hash"`
	RetrievedAt string `json:"retrieved_at"`
}

type Provider interface {
	Search(context.Context, string, string) ([]Source, error)
}

type Client struct {
	key         string
	host        string
	lookupIP    func(context.Context, string) ([]net.IPAddr, error)
	dialContext func(context.Context, string, string) (net.Conn, error)
	roots       *x509.CertPool
}

func (*Client) String() string   { return "public search client (redacted)" }
func (*Client) GoString() string { return "public search client (redacted)" }

func ValidateConfig(cfg Config) error {
	if cfg.APIKey == "" {
		return nil // An unconfigured optional search service must not block startup.
	}
	if len(cfg.APIKey) > 4096 {
		return fail(InvalidConfig)
	}
	for _, ch := range cfg.APIKey {
		if ch < 33 || ch > 126 {
			return fail(InvalidConfig)
		}
	}
	return nil
}

func NewClient(cfg Config) (*Client, error) {
	if ValidateConfig(cfg) != nil {
		return nil, fail(InvalidConfig)
	}
	if cfg.APIKey == "" {
		return nil, fail(NotConfigured)
	}
	return &Client{key: cfg.APIKey, host: "api.tavily.com", lookupIP: net.DefaultResolver.LookupIPAddr, dialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext}, nil
}

func text(value string, limit int, empty bool) bool {
	if len(value) > limit || !utf8.ValidString(value) || (!empty && strings.TrimSpace(value) == "") {
		return false
	}
	for _, ch := range value {
		if unicode.IsControl(ch) && ch != '\n' && ch != '\r' && ch != '\t' {
			return false
		}
	}
	return true
}

var emailPattern = regexp.MustCompile(`(?i)[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}`)
var phonePattern = regexp.MustCompile(`(?:^|[^0-9])(?:\+?86[- ]?)?1[3-9][0-9]{9}(?:$|[^0-9])|(?:^|[^0-9])[0-9]{15,19}(?:$|[^0-9])|\+?[0-9][0-9 ()-]{9,}[0-9]`)

// SensitiveText excludes obvious contact/identity details from generated
// candidates. This is a narrow output guard, not a claim of semantic truth.
func SensitiveText(value string) bool {
	if emailPattern.MatchString(value) {
		return true
	}
	for _, candidate := range phonePattern.FindAllString(value, -1) {
		digits := 0
		for _, ch := range candidate {
			if ch >= '0' && ch <= '9' {
				digits++
			}
		}
		if digits >= 10 {
			return true
		}
	}
	lower := strings.ToLower(value)
	for _, marker := range []string{"身份证", "家庭住址", "家庭地址", "实时位置", "病史", "性取向", "宗教信仰", "政治倾向", "癌症", "抑郁症", "糖尿病", "social security", "home address", "medical history", "sexual orientation", "religious affiliation", "political affiliation"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.Zone() != "" {
		return false
	}
	if ip.Is4() {
		for _, cidr := range []string{"0.0.0.0/8", "100.64.0.0/10", "169.254.0.0/16", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4"} {
			if netip.MustParsePrefix(cidr).Contains(ip) {
				return false
			}
		}
		return true
	}
	if !netip.MustParsePrefix("2000::/3").Contains(ip) {
		return false
	}
	for _, cidr := range []string{"2001::/23", "2001:db8::/32", "2002::/16"} {
		if netip.MustParsePrefix(cidr).Contains(ip) {
			return false
		}
	}
	return true
}

func publicHost(host string) bool {
	if host != strings.ToLower(host) || len(host) > 253 || strings.ContainsAny(host, "/\\?#@%[] \t\r\n:") {
		return false
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return false
	}
	for _, part := range labels {
		if len(part) < 1 || len(part) > 63 || part[0] == '-' || part[len(part)-1] == '-' {
			return false
		}
		for _, ch := range part {
			if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-') {
				return false
			}
		}
	}
	for _, suffix := range []string{".local", ".localhost", ".internal", ".invalid", ".onion", ".test", ".lan", ".home"} {
		if strings.HasSuffix(host, suffix) {
			return false
		}
	}
	if _, err := strconv.ParseUint(strings.ReplaceAll(host, ".", ""), 10, 64); err == nil {
		return false
	}
	return true
}

// SafeURL validates display-only result links. Search never fetches a result
// URL; only the fixed authenticated provider endpoint is requested.
func SafeURL(value string) bool {
	if !text(value, 2048, false) || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\\\r\n\t") {
		return false
	}
	u, err := url.Parse(value)
	if err != nil || u == nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.Opaque != "" {
		return false
	}
	if port := u.Port(); port != "" && !(u.Scheme == "https" && port == "443" || u.Scheme == "http" && port == "80") {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if ip, err := netip.ParseAddr(host); err == nil {
		return publicIP(ip)
	}
	return publicHost(host)
}

func (c *Client) destination(ctx context.Context) (string, error) {
	addresses, err := c.lookupIP(ctx, c.host)
	if err != nil || len(addresses) < 1 || len(addresses) > 32 {
		return "", fail(Unavailable)
	}
	for _, address := range addresses {
		ip, ok := netip.AddrFromSlice(address.IP)
		if !ok || address.Zone != "" || !publicIP(ip) {
			return "", fail(InvalidConfig)
		}
	}
	return net.JoinHostPort(addresses[0].IP.String(), "443"), nil
}

func bounded(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func (c *Client) Search(parent context.Context, name, company string) ([]Source, error) {
	if c == nil || c.key == "" {
		return nil, fail(NotConfigured)
	}
	if parent == nil || !text(name, 256, false) || !text(company, 512, true) || strings.ContainsAny(name+company, "\r\n\t") || strings.TrimSpace(name) != name || strings.TrimSpace(company) != company || SensitiveText(name) || SensitiveText(company) || strings.HasPrefix(strings.ToLower(name), "wxid_") || len(c.key) >= 8 && strings.Contains(name+company, c.key) {
		return nil, fail(InvalidInput)
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		return nil, fail(Timeout)
	}
	query := strings.TrimSpace(strings.ReplaceAll(name, `"`, " ")+" "+strings.ReplaceAll(company, `"`, " ")) + " 职业 公司 公开作品 -电话 -住址 -病史 -身份证"
	body, _ := json.Marshal(struct {
		Query             string `json:"query"`
		SearchDepth       string `json:"search_depth"`
		MaxResults        int    `json:"max_results"`
		IncludeAnswer     bool   `json:"include_answer"`
		IncludeRawContent bool   `json:"include_raw_content"`
		IncludeImages     bool   `json:"include_images"`
	}{query, "basic", 5, false, false, false})
	destination, err := c.destination(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fail(Timeout)
		}
		return nil, err
	}
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return c.dialContext(ctx, network, destination)
	}, TLSClientConfig: &tls.Config{ServerName: c.host, RootCAs: c.roots, MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 12 * time.Second, MaxResponseHeaderBytes: 32 << 10, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+c.host+"/search", bytes.NewReader(body))
	if err != nil {
		return nil, fail(InvalidConfig)
	}
	request.Header.Set("Authorization", "Bearer "+c.key)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		var timeout net.Error
		if ctx.Err() != nil || errors.As(err, &timeout) && timeout.Timeout() {
			return nil, fail(Timeout)
		}
		return nil, fail(Unavailable)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		switch response.StatusCode {
		case 401, 403:
			return nil, fail(AuthFailed)
		case 402, 429:
			return nil, fail(QuotaExceeded)
		default:
			return nil, fail(Unavailable)
		}
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (256<<10)+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, fail(Timeout)
		}
		return nil, fail(Unavailable)
	}
	if len(raw) > 256<<10 || !utf8.Valid(raw) || len(c.key) >= 8 && bytes.Contains(raw, []byte(c.key)) {
		return nil, fail(InvalidOutput)
	}
	var envelope struct {
		Results []struct {
			URL     string `json:"url"`
			Title   string `json:"title"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Results == nil || len(envelope.Results) > 5 {
		return nil, fail(InvalidOutput)
	}
	out := []Source{}
	seen := map[string]bool{}
	for _, result := range envelope.Results {
		if len(c.key) >= 8 && strings.Contains(result.URL+result.Title+result.Content, c.key) {
			return nil, fail(InvalidOutput)
		}
		if !SafeURL(result.URL) || !text(result.Title, 4096, false) || !text(result.Content, 64000, false) || SensitiveText(result.Title) || SensitiveText(result.Content) {
			continue
		}
		title, snippet := bounded(strings.Join(strings.Fields(result.Title), " "), 512), bounded(strings.Join(strings.Fields(result.Content), " "), 1000)
		fingerprint := sha256.Sum256([]byte(result.URL + "\x00" + snippet))
		hash := hex.EncodeToString(fingerprint[:])
		if seen[hash] {
			continue
		}
		seen[hash] = true
		out = append(out, Source{ID: "public-" + hash, URL: result.URL, Title: title, Snippet: snippet, ContentHash: hash, RetrievedAt: time.Now().UTC().Format(time.RFC3339Nano)})
	}
	return out, nil
}

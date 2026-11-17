package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/qilinbro/msg-manager/internal/localcredentials"
)

const (
	serverName     = "social-intelligence-os"
	serverVersion  = "0.1.0"
	maxToolMessage = 1 << 20
)

var safeID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type Server struct {
	baseURL string
	token   string
	http    *http.Client
	gate    chan struct{}
}

func NewFromEnv() (*Server, error) {
	base := strings.TrimSpace(os.Getenv("SOCIALD_URL"))
	if base == "" {
		base = "http://127.0.0.1:17890"
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || strings.Contains(base, "#") || parsed.Opaque != "" || parsed.Path != "" && parsed.Path != "/" || parsed.RawPath != "" {
		return nil, errors.New("SOCIALD_URL must be a local http URL without credentials or a path")
	}
	host := parsed.Hostname()
	if host != "127.0.0.1" && host != "localhost" {
		return nil, errors.New("SOCIALD_URL must point to localhost")
	}
	port := parsed.Port()
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 || parsed.Host != net.JoinHostPort(host, port) {
		return nil, errors.New("SOCIALD_URL requires an explicit port between 1 and 65535")
	}
	// Validate and canonicalize the endpoint before touching any credentials.
	// Stored tokens are bound to this exact URL, including the port.
	base = "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(n))
	token, explicit := os.LookupEnv("SOCIALD_API_TOKEN")
	if !explicit {
		token, err = localcredentials.LoadToken(base)
		if err != nil {
			return nil, err
		}
	}
	if len(token) < 32 || len(token) > 4096 || strings.IndexFunc(token, func(r rune) bool { return r < 33 || r > 126 }) >= 0 {
		return nil, errors.New("SOCIALD_API_TOKEN requires 32 to 4096 printable ASCII characters without whitespace")
	}
	// Normalize localhost to a literal address: no DNS, proxy or redirect can
	// move the bearer token outside the configured local service. An explicit
	// environment value (including an invalid empty one) never reads a file.
	return &Server{baseURL: base, token: token, http: localHTTPClient(), gate: make(chan struct{}, 4)}, nil
}

func localHTTPClient() *http.Client {
	return &http.Client{
		Timeout:       40 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			Proxy:                  nil,
			DialContext:            (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
			ResponseHeaderTimeout:  35 * time.Second,
			MaxConnsPerHost:        4,
			MaxIdleConnsPerHost:    4,
			IdleConnTimeout:        30 * time.Second,
			MaxResponseHeaderBytes: 32 << 10,
		},
	}
}

type tool struct {
	Name        string
	Description string
	InputSchema map[string]any
}

func tools() []tool {
	return []tool{
		{Name: "get_system_status", Description: "读取本地服务状态、存储状态和连接器能力。", InputSchema: objectSchema(nil, nil)},
		{Name: "search_conversations", Description: "在已保存消息的会话中按关键词、平台和状态搜索。", InputSchema: objectSchema(map[string]any{
			"q":        stringSchema(256),
			"platform": enumSchema([]string{"qq", "weixin", "telegram", "shanliao", "email"}),
			"category": enumSchema([]string{"friend", "family", "colleague", "client", "partner", "other", "unclassified"}),
			"unread":   boolSchema(), "pinned": boolSchema(), "archived": boolSchema(),
			"cursor": stringSchema(2048), "limit": numberSchema(1, 100),
		}, nil)},
		{Name: "get_conversation_messages", Description: "读取一个已保存会话的文本消息。", InputSchema: objectSchema(map[string]any{
			"conversation_id": stringSchema(128), "cursor": stringSchema(2048), "limit": numberSchema(1, 100),
		}, []string{"conversation_id"})},
		{Name: "get_wechat_sessions", Description: "读取已配置的本机微信私聊/群聊会话列表，不返回路径或密钥。", InputSchema: objectSchema(nil, nil)},
		{Name: "read_wechat_messages", Description: "仅对用户选定的微信会话读取最近文本并导入加密消息中心，会新增消息、联系人和未读数；已授权的自动整理或日报可能随后把新消息发送给配置的模型（可能是外部服务并产生费用）。可选正文关键词，源微信库只读，不发送聊天消息。", InputSchema: objectSchema(map[string]any{
			"peer_id": stringSchema(128), "keyword": stringSchema(128),
		}, []string{"peer_id"})},
		{Name: "get_latest_digest", Description: "读取最近一份本地消息摘要和提醒。", InputSchema: objectSchema(nil, nil)},
	}
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "additionalProperties": false, "properties": properties, "required": required}
}

func stringSchema(max int) map[string]any { return map[string]any{"type": "string", "maxLength": max} }
func numberSchema(min, max int) map[string]any {
	return map[string]any{"type": "integer", "minimum": min, "maximum": max}
}
func boolSchema() map[string]any { return map[string]any{"type": "boolean"} }
func enumSchema(values []string) map[string]any {
	return map[string]any{"type": "string", "enum": values}
}

// Validate against the same small schema exposed to clients. Do not rely on
// clients or model-generated arguments to enforce the published contract.
func validateArguments(name string, args map[string]json.RawMessage) error {
	var schema map[string]any
	for _, definition := range tools() {
		if definition.Name == name {
			schema = definition.InputSchema
			break
		}
	}
	if schema == nil {
		return errors.New("unknown MCP tool")
	}
	properties := schema["properties"].(map[string]any)
	for _, required := range schema["required"].([]string) {
		if _, exists := args[required]; !exists {
			return fmt.Errorf("%s is required", required)
		}
	}
	for key, raw := range args {
		property, exists := properties[key]
		if !exists {
			return errors.New("tool arguments contain an unsupported field")
		}
		definition := property.(map[string]any)
		switch definition["type"] {
		case "string":
			value, err := rawString(raw)
			if err != nil || strings.IndexFunc(value, unicode.IsControl) >= 0 {
				return fmt.Errorf("%s must be a string without control characters", key)
			}
			if max, ok := definition["maxLength"].(int); ok && utf8.RuneCountInString(value) > max {
				return fmt.Errorf("%s is too long", key)
			}
			if choices, ok := definition["enum"].([]string); ok {
				matched := false
				for _, choice := range choices {
					matched = matched || value == choice
				}
				if !matched {
					return fmt.Errorf("%s is not a supported value", key)
				}
			}
			// HTTP API bounds these fields in UTF-8 bytes, not characters.
			maxBytes := map[string]int{"q": 256, "cursor": 2048, "peer_id": 128}[key]
			if maxBytes > 0 && len(value) > maxBytes {
				return fmt.Errorf("%s exceeds the UTF-8 byte limit", key)
			}
			if key == "conversation_id" && !safeID.MatchString(value) {
				return errors.New("conversation_id is invalid")
			}
			if key == "peer_id" && (value == "" || strings.TrimSpace(value) != value || strings.ContainsAny(value, "/\\")) {
				return errors.New("peer_id is invalid")
			}
		case "boolean":
			if string(raw) != "true" && string(raw) != "false" {
				return fmt.Errorf("%s must be boolean", key)
			}
		case "integer":
			if _, err := rawInt(raw, definition["minimum"].(int), definition["maximum"].(int)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Server) callTool(ctx context.Context, name string, args map[string]json.RawMessage) ([]byte, error) {
	if err := validateArguments(name, args); err != nil {
		return nil, err
	}
	switch name {
	case "get_system_status":
		return s.get(ctx, "/api/v1/system", nil)
	case "search_conversations":
		return s.searchConversations(ctx, args)
	case "get_conversation_messages":
		id, err := requiredString(args, "conversation_id", 128, safeID.MatchString)
		if err != nil {
			return nil, err
		}
		values, err := pageValues(args)
		if err != nil {
			return nil, err
		}
		return s.get(ctx, "/api/v1/conversations/"+url.PathEscape(id)+"/messages", values)
	case "get_wechat_sessions":
		return s.get(ctx, "/api/v1/connections/wechat-db/sessions", nil)
	case "read_wechat_messages":
		peer, err := requiredString(args, "peer_id", 128, func(value string) bool { return !strings.ContainsAny(value, "\\/\x00\r\n") })
		if err != nil {
			return nil, err
		}
		keyword, err := optionalString(args, "keyword", 128)
		if err != nil {
			return nil, err
		}
		body := map[string]string{"peer_id": peer}
		if keyword != "" {
			body["keyword"] = keyword
		}
		return s.post(ctx, "/api/v1/connections/wechat-db/read", body)
	case "get_latest_digest":
		return s.get(ctx, "/api/v1/digest/latest", nil)
	default:
		return nil, errors.New("unknown MCP tool")
	}
}

func (s *Server) searchConversations(ctx context.Context, args map[string]json.RawMessage) ([]byte, error) {
	values := url.Values{}
	for _, name := range []string{"q", "platform", "category", "cursor"} {
		if value, ok := args[name]; ok {
			text, err := rawString(value)
			if err != nil {
				return nil, fmt.Errorf("%s must be a string", name)
			}
			max := 256
			if name == "cursor" {
				max = 2048
			}
			if len([]rune(text)) > max {
				return nil, fmt.Errorf("%s is too long", name)
			}
			if text != "" {
				values.Set(name, text)
			}
		}
	}
	for _, name := range []string{"unread", "pinned", "archived"} {
		if value, ok := args[name]; ok {
			var flag bool
			if err := json.Unmarshal(value, &flag); err != nil {
				return nil, fmt.Errorf("%s must be boolean", name)
			}
			values.Set(name, strconv.FormatBool(flag))
		}
	}
	if value, ok := args["limit"]; ok {
		limit, err := rawInt(value, 1, 100)
		if err != nil {
			return nil, err
		}
		values.Set("limit", strconv.Itoa(limit))
	} else {
		values.Set("limit", "50")
	}
	return s.get(ctx, "/api/v1/conversations", values)
}

func pageValues(args map[string]json.RawMessage) (url.Values, error) {
	values := url.Values{}
	if value, ok := args["cursor"]; ok {
		cursor, err := rawString(value)
		if err != nil {
			return nil, errors.New("cursor must be a string")
		}
		if len([]rune(cursor)) > 2048 {
			return nil, errors.New("cursor is too long")
		}
		if cursor != "" {
			values.Set("cursor", cursor)
		}
	}
	limit := 50
	if value, ok := args["limit"]; ok {
		parsed, err := rawInt(value, 1, 100)
		if err != nil {
			return nil, err
		}
		limit = parsed
	}
	values.Set("limit", strconv.Itoa(limit))
	return values, nil
}

func requiredString(args map[string]json.RawMessage, name string, max int, valid func(string) bool) (string, error) {
	value, ok := args[name]
	if !ok {
		return "", fmt.Errorf("%s is required", name)
	}
	text, err := rawString(value)
	if err != nil || text == "" || len([]rune(text)) > max || !valid(text) {
		return "", fmt.Errorf("%s is invalid", name)
	}
	return text, nil
}

func optionalString(args map[string]json.RawMessage, name string, max int) (string, error) {
	value, ok := args[name]
	if !ok {
		return "", nil
	}
	text, err := rawString(value)
	if err != nil {
		return "", fmt.Errorf("%s must be a string", name)
	}
	if len([]rune(strings.TrimSpace(text))) > max || strings.IndexFunc(text, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("%s is invalid", name)
	}
	return strings.TrimSpace(text), nil
}

func rawString(raw json.RawMessage) (string, error) {
	var value string
	if len(raw) == 0 || raw[0] != '"' || !utf8.Valid(raw) {
		return "", errors.New("expected a UTF-8 string")
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	return value, nil
}
func rawInt(raw json.RawMessage, min, max int) (int, error) {
	var value int
	if err := json.Unmarshal(raw, &value); err != nil || value < min || value > max {
		return 0, fmt.Errorf("integer must be between %d and %d", min, max)
	}
	return value, nil
}

func (s *Server) get(ctx context.Context, path string, values url.Values) ([]byte, error) {
	if len(values) > 0 {
		path += "?" + values.Encode()
	}
	return s.request(ctx, http.MethodGet, path, nil)
}

func (s *Server) post(ctx context.Context, path string, body any) ([]byte, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return s.request(ctx, http.MethodPost, path, encoded)
}

func (s *Server) request(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	if s.gate != nil {
		select {
		case s.gate <- struct{}{}:
			defer func() { <-s.gate }()
		case <-ctx.Done():
			return nil, errors.New("local service request cancelled")
		default:
			return nil, errors.New("too many local service requests; try again after pending calls finish")
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, s.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+s.token)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := s.http.Do(request)
	if err != nil {
		return nil, errors.New("local social service is unavailable")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxToolMessage+1))
	if err != nil {
		return nil, errors.New("local social service response could not be read")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("local social service returned HTTP %d", response.StatusCode)
	}
	if len(data) > maxToolMessage {
		return nil, errors.New("local service response exceeds 1 MiB; reduce the message page limit or narrow the search; use the workbench for an oversized session list")
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return nil, errors.New("local social service returned a non-JSON response")
	}
	if !json.Valid(data) {
		return nil, errors.New("local social service returned invalid JSON")
	}
	return data, nil
}

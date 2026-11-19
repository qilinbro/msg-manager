package telegrambot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/qilinbro/msg-manager/internal/connector/nonebotbridge"
)

type callerFunc func(context.Context, string, any, any) error

func (f callerFunc) Call(ctx context.Context, action string, in, out any) error {
	return f(ctx, action, in, out)
}
func testConfig() Config {
	return Config{Token: "123456:" + strings.Repeat("A", 35), ExpectedBotID: "123456", AllowedPrivateChatIDs: []string{"42"}, Limit: 20}
}
func testPage() Page {
	return Page{Verification: Verification{BotID: "123456", IsBot: true}, Events: []Event{{UpdateID: "10", Type: "message", Message: &Message{ChatID: "42", MessageID: "7", SenderID: "42", ChatType: "private", Body: "  原始 @name 文本\n", Date: 1767225600}}}, NextOffset: "11"}
}
func resultCaller(t *testing.T, value any) callerFunc {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return func(_ context.Context, _ string, _ any, out any) error { return json.Unmarshal(raw, out) }
}
func expectKind(t *testing.T, err error, kind nonebotbridge.Kind) {
	t.Helper()
	if err == nil || nonebotbridge.KindOf(err) != kind {
		t.Fatalf("kind = %v; want %s", err, kind)
	}
}

func TestConfigIsCompleteCanonicalBoundedAndRedacted(t *testing.T) {
	if err := ValidateConfig(Config{}); err != nil {
		t.Fatal(err)
	}
	base := testConfig()
	if err := ValidateConfig(base); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Config){
		"partial":       func(c *Config) { c.Token = "" },
		"wrong-prefix":  func(c *Config) { c.ExpectedBotID = "987654" },
		"short-token":   func(c *Config) { c.Token = "123456:short" },
		"token-newline": func(c *Config) { c.Token += "\n" },
		"duplicate":     func(c *Config) { c.AllowedPrivateChatIDs = []string{"42", "42"} },
		"empty-allow":   func(c *Config) { c.AllowedPrivateChatIDs = nil },
		"group":         func(c *Config) { c.AllowedPrivateChatIDs = []string{"-42"} },
		"leading-zero":  func(c *Config) { c.AllowedPrivateChatIDs = []string{"042"} },
		"zero":          func(c *Config) { c.AllowedPrivateChatIDs = []string{"0"} },
		"float":         func(c *Config) { c.AllowedPrivateChatIDs = []string{"4.2e1"} },
		"unsafe-int":    func(c *Config) { c.AllowedPrivateChatIDs = []string{"9007199254740993"} },
		"limit":         func(c *Config) { c.Limit = 101 },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := base
			mutate(&cfg)
			expectKind(t, ValidateConfig(cfg), nonebotbridge.InvalidConfig)
		})
	}
	for _, value := range []string{fmt.Sprint(base), fmt.Sprintf("%+v", base), fmt.Sprintf("%#v", base)} {
		if strings.Contains(value, base.Token) || strings.Contains(value, "42") {
			t.Fatal("config formatting disclosed secrets or peers")
		}
	}
	raw, _ := json.Marshal(base)
	if strings.Contains(string(raw), base.Token) || strings.Contains(string(raw), "42") || strings.Contains(string(raw), "123456") {
		t.Fatal("config JSON disclosed identity or token")
	}
	cfg := base
	client, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg.AllowedPrivateChatIDs[0] = "99"
	if !reflect.DeepEqual(client.AllowedPrivateChatIDs(), []string{"42"}) {
		t.Fatal("client retained mutable allowlist")
	}
	ids := client.AllowedPrivateChatIDs()
	ids[0] = "88"
	if client.AllowedPrivateChatIDs()[0] != "42" {
		t.Fatal("client exposed mutable allowlist")
	}
}

func TestActualBridgeProtocolHasOnlyFixedActionsAndExplicitScope(t *testing.T) {
	calls := []string{}
	caller := callerFunc(func(_ context.Context, action string, in, out any) error {
		calls = append(calls, action)
		raw, _ := json.Marshal(in)
		var payload map[string]json.RawMessage
		if json.Unmarshal(raw, &payload) != nil {
			t.Fatal("invalid bridge payload")
		}
		var token, bot string
		_ = json.Unmarshal(payload["token"], &token)
		_ = json.Unmarshal(payload["expected_bot_id"], &bot)
		if token != testConfig().Token || bot != "123456" {
			t.Fatal("missing scoped bridge credentials")
		}
		switch action {
		case "telegram.probe":
			if len(payload) != 3 {
				t.Fatal("probe includes uncontrolled fields")
			}
			return resultCaller(t, Verification{BotID: "123456", IsBot: true})(context.Background(), action, in, out)
		case "telegram.poll":
			if len(payload) != 5 || string(payload["offset"]) != `"0"` || string(payload["limit"]) != "20" {
				t.Fatal("poll contract differs")
			}
			return resultCaller(t, testPage())(context.Background(), action, in, out)
		default:
			t.Fatal("unexpected bridge action")
			return errors.New("unexpected")
		}
	})
	client, err := New(testConfig(), caller)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	page, err := client.Poll(context.Background(), "0")
	if err != nil {
		t.Fatal(err)
	}
	if page.Events[0].Message.Body != "  原始 @name 文本\n" || !reflect.DeepEqual(calls, []string{"telegram.probe", "telegram.poll"}) {
		t.Fatal("text altered or hidden operation invoked")
	}
}

func TestProbeRequiresExplicitVerifiedBotAndNoWebhook(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		kind      nonebotbridge.Kind
	}{
		{"wrong-bot", `{"bot_id":"9","is_bot":true,"webhook_present":false}`, nonebotbridge.IdentityMismatch},
		{"human", `{"bot_id":"123456","is_bot":false,"webhook_present":false}`, nonebotbridge.IdentityMismatch},
		{"webhook", `{"bot_id":"123456","is_bot":true,"webhook_present":true}`, nonebotbridge.WebhookPresent},
		{"missing-webhook", `{"bot_id":"123456","is_bot":true}`, nonebotbridge.ProtocolError},
		{"missing-is-bot", `{"bot_id":"123456","webhook_present":false}`, nonebotbridge.ProtocolError},
		{"duplicate", `{"bot_id":"123456","bot_id":"123456","is_bot":true,"webhook_present":false}`, nonebotbridge.ProtocolError},
		{"unknown-field", `{"bot_id":"123456","is_bot":true,"webhook_present":false,"token":"sensitive"}`, nonebotbridge.ProtocolError},
		{"trailing", `{"bot_id":"123456","is_bot":true,"webhook_present":false} {}`, nonebotbridge.ProtocolError},
		{"null", `null`, nonebotbridge.ProtocolError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := New(testConfig(), callerFunc(func(_ context.Context, _ string, _ any, out any) error {
				p := out.(*json.RawMessage)
				*p = json.RawMessage(tc.raw)
				return nil
			}))
			_, err := client.Probe(context.Background())
			expectKind(t, err, tc.kind)
			if strings.Contains(fmt.Sprint(err), "sensitive") {
				t.Fatal("raw error leaked")
			}
		})
	}
}

func TestPollRejectsMalformedOrOutOfScopeEntirePages(t *testing.T) {
	mutations := map[string]func(*Page){
		"wrong-bot":             func(p *Page) { p.BotID = "987654" },
		"webhook":               func(p *Page) { p.WebhookPresent = true },
		"wrong-watermark":       func(p *Page) { p.NextOffset = "12" },
		"null-events":           func(p *Page) { p.Events = nil },
		"negative-update":       func(p *Page) { p.Events[0].UpdateID = "-1" },
		"leading-zero":          func(p *Page) { p.Events[0].UpdateID = "010" },
		"old-update":            func(p *Page) { p.Events[0].UpdateID = "0"; p.NextOffset = "1" },
		"duplicate-update":      func(p *Page) { p.Events = append(p.Events, p.Events[0]) },
		"foreign-chat":          func(p *Page) { p.Events[0].Message.ChatID = "99" },
		"group":                 func(p *Page) { p.Events[0].Message.ChatType = "group" },
		"foreign-sender":        func(p *Page) { p.Events[0].Message.SenderID = "99" },
		"zero-message":          func(p *Page) { p.Events[0].Message.MessageID = "0" },
		"empty-body":            func(p *Page) { p.Events[0].Message.Body = "" },
		"nul-body":              func(p *Page) { p.Events[0].Message.Body = "x\x00y" },
		"long-body":             func(p *Page) { p.Events[0].Message.Body = strings.Repeat("x", MaxBodyBytes+1) },
		"zero-date":             func(p *Page) { p.Events[0].Message.Date = 0 },
		"future-date":           func(p *Page) { p.Events[0].Message.Date = 999999999999 },
		"unsupported-with-body": func(p *Page) { p.Events[0].Reason = "unsupported_media" },
		"edit-with-body":        func(p *Page) { p.Events[0].Type = "edited_message" },
		"arbitrary-reason":      func(p *Page) { p.Events[0].Message = nil; p.Events[0].Reason = "secret text" },
		"arbitrary-type": func(p *Page) {
			p.Events[0].Message = nil
			p.Events[0].Reason = "unsupported_event"
			p.Events[0].Type = "secret text"
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			p := testPage()
			mutate(&p)
			client, _ := New(testConfig(), resultCaller(t, p))
			if _, err := client.Poll(context.Background(), "1"); err == nil {
				t.Fatal("accepted unsafe page")
			}
		})
	}
}

func TestPollUnsupportedPagesAndEmptyPagesHaveCommitBasedCandidateOffset(t *testing.T) {
	p := testPage()
	p.Events = append(p.Events,
		Event{UpdateID: "11", Type: "message", Reason: "not_authorized"},
		Event{UpdateID: "12", Type: "message", Reason: "not_private"},
		Event{UpdateID: "13", Type: "message", Reason: "unsupported_media"},
		Event{UpdateID: "14", Type: "edited_message", Reason: "unsupported_edit"},
		Event{UpdateID: "15", Type: "unknown", Reason: "unsupported_event"})
	p.NextOffset = "16"
	client, _ := New(testConfig(), resultCaller(t, p))
	if _, err := client.Poll(context.Background(), "0"); err != nil {
		t.Fatal(err)
	}
	empty := Page{Verification: p.Verification, Events: []Event{}, NextOffset: "16"}
	client, _ = New(testConfig(), resultCaller(t, empty))
	if _, err := client.Poll(context.Background(), "16"); err != nil {
		t.Fatal(err)
	}
	// Poll is stateless: only a caller's durable offset, never an in-memory
	// successful fetch, can decide the next request's acknowledgement watermark.
	requests := 0
	client, _ = New(testConfig(), callerFunc(func(ctx context.Context, action string, in, out any) error {
		requests++
		raw, _ := json.Marshal(in)
		if !strings.Contains(string(raw), `"offset":"0"`) {
			t.Fatal("fetch advanced an uncommitted offset")
		}
		return resultCaller(t, testPage())(ctx, action, in, out)
	}))
	for i := 0; i < 2; i++ {
		if _, err := client.Poll(context.Background(), "0"); err != nil {
			t.Fatal(err)
		}
	}
	if requests != 2 {
		t.Fatal("unexpected request count")
	}
}

func TestWireFieldsAreExactRequiredNonnullAndPreserveUTF16(t *testing.T) {
	base := `{"bot_id":"123456","is_bot":true,"webhook_present":false,"events":[{"update_id":"10","type":"message","reason":"","message":{"chat_id":"42","message_id":"7","sender_id":"42","chat_type":"private","body":"original","date":1767225600}}],"next_offset":"11"}`
	cases := []string{
		strings.Replace(base, `"bot_id"`, `"BOT_ID"`, 1),
		strings.Replace(base, `"is_bot"`, `"IS_BOT"`, 1),
		strings.Replace(base, `"webhook_present":false`, `"webhook_present":null`, 1),
		strings.Replace(base, `"events"`, `"Events"`, 1),
		strings.Replace(base, `"update_id"`, `"UPDATE_ID"`, 1),
		strings.Replace(base, `"reason":"",`, ``, 1),
		strings.Replace(base, `"message"`, `"Message"`, 1),
		strings.Replace(base, `"chat_id"`, `"CHAT_ID"`, 1),
		strings.Replace(base, `"body":"original"`, `"body":null`, 1),
		strings.Replace(base, `"body":"original"`, `"body":"\ud800"`, 1),
		strings.Replace(base, `"body":"original"`, `"body":"\udc00"`, 1),
		strings.Replace(base, `"body":"original"`, `"body":"\ud800X\udc00"`, 1),
		strings.Replace(base, `"date":1767225600`, `"date":null`, 1),
		strings.Replace(base, `"reason":""`, `"reason":null`, 1),
		strings.Replace(base, `"next_offset"`, `"NEXT_OFFSET"`, 1),
		`{"bot_id":"123456","is_bot":true,"webhook_present":false,"events":[null],"next_offset":"0"}`,
		`{"bot_id":"123456","is_bot":true,"webhook_present":false,"events":[{"update_id":"10","type":"message","reason":"unsupported_media","message":null}],"next_offset":"11"}`,
	}
	for i, raw := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			client, _ := New(testConfig(), callerFunc(func(_ context.Context, _ string, _ any, out any) error {
				*out.(*json.RawMessage) = json.RawMessage(raw)
				return nil
			}))
			_, err := client.Poll(context.Background(), "0")
			expectKind(t, err, nonebotbridge.ProtocolError)
		})
	}
	for rawBody, want := range map[string]string{`"\ud83d\ude00"`: "😀", `"literal \\ud800"`: `literal \ud800`} {
		raw := strings.Replace(base, `"original"`, rawBody, 1)
		client, _ := New(testConfig(), callerFunc(func(_ context.Context, _ string, _ any, out any) error {
			*out.(*json.RawMessage) = json.RawMessage(raw)
			return nil
		}))
		page, err := client.Poll(context.Background(), "0")
		if err != nil || page.Events[0].Message.Body != want {
			t.Fatalf("valid UTF16 body rejected: %v", err)
		}
	}
}

func TestRuntimeConfiguredAndSanitizedReceiveOnlyErrorKinds(t *testing.T) {
	bridge, err := nonebotbridge.New(nonebotbridge.Config{})
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(testConfig(), bridge)
	if err != nil {
		t.Fatal(err)
	}
	if client.RuntimeConfigured() {
		t.Fatal("empty Python runtime reported configured")
	}
	_, err = client.Probe(context.Background())
	expectKind(t, err, nonebotbridge.NotConfigured)
	for _, kind := range []nonebotbridge.Kind{nonebotbridge.AuthFailed, nonebotbridge.RateLimited, nonebotbridge.PermissionDenied, nonebotbridge.PlatformRejected, nonebotbridge.Unreachable, nonebotbridge.WebhookPresent} {
		client, _ = New(testConfig(), callerFunc(func(context.Context, string, any, any) error {
			return &nonebotbridge.Error{Kind: kind, RetryAfter: 48 * time.Hour, PlatformCode: 987654, SendState: "secret"}
		}))
		_, err = client.Probe(context.Background())
		expectKind(t, err, kind)
		failure := err.(*nonebotbridge.Error)
		if failure.RetryAfter != 24*time.Hour || failure.PlatformCode != 0 || failure.SendState != "" {
			t.Fatal("diagnostic not bounded and sanitized")
		}
	}
}

func TestCancellationAndBridgeErrorsNeverExposeProviderText(t *testing.T) {
	calls := 0
	client, _ := New(testConfig(), callerFunc(func(context.Context, string, any, any) error {
		calls++
		return errors.New("token SECRET and private message")
	}))
	_, err := client.Probe(context.Background())
	expectKind(t, err, nonebotbridge.ProtocolError)
	if strings.Contains(err.Error(), "SECRET") {
		t.Fatal("bridge diagnostic leaked")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.Poll(ctx, "0")
	expectKind(t, err, nonebotbridge.Canceled)
	if calls != 1 {
		t.Fatal("canceled request invoked worker")
	}
	client, _ = New(testConfig(), nil)
	_, err = client.Probe(context.Background())
	expectKind(t, err, nonebotbridge.NotConfigured)
	client, _ = New(Config{}, nil)
	_, err = client.Probe(context.Background())
	expectKind(t, err, nonebotbridge.NotConfigured)
}

func TestIdleEpochReseekAcceptsLowerIDsWithoutRelaxingRequestedOffset(t *testing.T) {
	page := testPage()
	page.Events[0].UpdateID = "200000000"
	page.NextOffset = "200000001"
	calls := 0
	client, err := New(testConfig(), callerFunc(func(ctx context.Context, action string, in, out any) error {
		calls++
		if action != "telegram.poll" {
			t.Fatal("idle recovery invoked an extra action")
		}
		return resultCaller(t, page)(ctx, action, in, out)
	}))
	if err != nil {
		t.Fatal(err)
	}
	// Freshness and epoch rotation belong to the encrypted store/API. A worker
	// must not silently lower an offset or perform a second platform request.
	_, err = client.Poll(context.Background(), "900000001")
	expectKind(t, err, nonebotbridge.ProtocolError)
	got, err := client.Poll(context.Background(), "0")
	if err != nil || got.NextOffset != "200000001" || got.Events[0].UpdateID != "200000000" || calls != 2 {
		t.Fatalf("authorized zero-offset epoch page rejected: %+v %v calls=%d", got, err, calls)
	}
}

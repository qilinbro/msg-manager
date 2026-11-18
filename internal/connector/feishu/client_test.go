package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// All identities, tokens, secrets, text and receipts in this package are
// synthetic. RoundTripper interception makes real network access impossible.
var syntheticCredentials = Credentials{AppID: "cli_synthetic_app", AppSecret: "synthetic-secret-not-a-credential"}
var syntheticInput = SendInput{RecipientOpenID: "ou_synthetic_owner", Text: "合成摘要：核对会议安排。", UUID: "fd_0123456789abcdef0123456789abcdef"}

const syntheticToken = "synthetic.token.not.real"
const syntheticReceipt = `{"code":0,"msg":"success","data":{"message_id":"om_synthetic"}}`

type roundTripFunc func(*http.Request) (*http.Response, error)

func (transport roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func fakeClient(transport roundTripFunc) *Client {
	client := NewClient()
	client.http.Transport = transport
	return client
}

func fakeResponse(request *http.Request, status int, body string) *http.Response {
	return &http.Response{
		Request:       request,
		StatusCode:    status,
		Header:        http.Header{"Content-Type": {"application/json; charset=utf-8"}},
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
	}
}

func tokenBody(token string, expiry int64) string {
	body, _ := json.Marshal(map[string]any{
		"code": 0, "access_token": token, "expires_in": expiry,
		"token_type": "Bearer", "scope": sendScope,
	})
	return string(body)
}

func assertFailure(t *testing.T, err error, code string, unknown bool) {
	t.Helper()
	var failure *Error
	if !errors.As(err, &failure) || failure.Code != code || failure.Unknown != unknown || err.Error() != code {
		t.Fatalf("unexpected failure: %v; want %s, unknown=%v", err, code, unknown)
	}
}

func TestFixedOAuthAndDoubleEncodedPrivateText(t *testing.T) {
	var requests atomic.Int32
	longToken := strings.Repeat("J", 4096) // New JWTs must not be truncated to an old token size.
	input := syntheticInput
	input.Text = "合成摘要\n待办：核对\"合同\"、路径\\草稿与 < > &。"
	client := fakeClient(func(request *http.Request) (*http.Response, error) {
		requests.Add(1)
		if request.Method != http.MethodPost || request.URL.Scheme != "https" || request.GetBody != nil {
			t.Fatal("request policy changed")
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		switch request.URL.String() {
		case tokenURL:
			if request.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || request.Header.Get("Authorization") != "" {
				t.Fatal("OAuth authentication must use exactly one method")
			}
			form, err := url.ParseQuery(string(body))
			if err != nil || len(form) != 4 || form.Get("grant_type") != "client_credentials" ||
				form.Get("client_id") != syntheticCredentials.AppID || form.Get("client_secret") != syntheticCredentials.AppSecret ||
				form.Get("scope") != sendScope {
				t.Fatal("unexpected OAuth request")
			}
			return fakeResponse(request, 200, tokenBody(longToken, 7200)), nil
		case messageURL:
			if request.Header.Get("Authorization") != "Bearer "+longToken || request.Header.Get("Content-Type") != "application/json; charset=utf-8" {
				t.Fatal("unexpected send headers")
			}
			if len(body) > MaxRequestBytes || strings.Contains(string(body), syntheticCredentials.AppSecret) || strings.Contains(string(body), longToken) {
				t.Fatal("message body exceeded its scope or byte budget")
			}
			var outer map[string]json.RawMessage
			if json.Unmarshal(body, &outer) != nil || len(outer) != 4 {
				t.Fatal("invalid outer message")
			}
			var recipient, kind, content, uuid string
			_ = json.Unmarshal(outer["receive_id"], &recipient)
			_ = json.Unmarshal(outer["msg_type"], &kind)
			_ = json.Unmarshal(outer["uuid"], &uuid)
			if json.Unmarshal(outer["content"], &content) != nil {
				t.Fatal("content must be a serialized JSON string, not a nested object")
			}
			var inner struct {
				Text string `json:"text"`
			}
			if json.Unmarshal([]byte(content), &inner) != nil || inner.Text != input.Text || recipient != input.RecipientOpenID || kind != "text" || uuid != input.UUID {
				t.Fatal("text, UUID, or private recipient changed in serialization")
			}
			return fakeResponse(request, 200, syntheticReceipt), nil
		default:
			t.Fatal("unapproved destination")
			return nil, errors.New("unapproved destination")
		}
	})
	receipt, err := client.SendText(context.Background(), syntheticCredentials, input)
	if err != nil || receipt.MessageID != "om_synthetic" || requests.Load() != 2 {
		t.Fatalf("send: receipt=%+v, error=%v, requests=%d", receipt, err, requests.Load())
	}
}

func TestProductionTransportCannotUseProxyOrRedirect(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://synthetic.invalid:3128")
	t.Setenv("HTTP_PROXY", "http://synthetic.invalid:3128")
	client := NewClient()
	transport, ok := client.http.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil || !transport.DisableKeepAlives || transport.ForceAttemptHTTP2 ||
		transport.TLSNextProto == nil || transport.TLSClientConfig == nil || transport.TLSClientConfig.InsecureSkipVerify ||
		transport.TLSHandshakeTimeout <= 0 || transport.ResponseHeaderTimeout <= 0 || transport.MaxResponseHeaderBytes <= 0 ||
		client.http.Timeout <= 0 || client.http.Timeout > 25*time.Second {
		t.Fatal("production transport is not fixed, bounded and non-replaying")
	}
	if err := client.http.CheckRedirect(&http.Request{}, nil); err == nil {
		t.Fatal("redirects must be rejected")
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRedirectsNeverReachAnotherHost(t *testing.T) {
	for _, stage := range []string{"token", "message"} {
		t.Run(stage, func(t *testing.T) {
			var count atomic.Int32
			client := fakeClient(func(request *http.Request) (*http.Response, error) {
				count.Add(1)
				if request.URL.String() == tokenURL && stage == "message" {
					return fakeResponse(request, 200, tokenBody(syntheticToken, 7200)), nil
				}
				if request.URL.String() != tokenURL && request.URL.String() != messageURL {
					t.Fatal("a redirect reached an unapproved URL")
				}
				response := fakeResponse(request, 307, `{}`)
				response.Header.Set("Location", "https://synthetic.invalid/capture")
				return response, nil
			})
			_, err := client.SendText(context.Background(), syntheticCredentials, syntheticInput)
			if stage == "token" {
				assertFailure(t, err, CodeAuthUnavailable, false)
				if count.Load() != 1 {
					t.Fatal("token redirect was followed or retried")
				}
			} else {
				assertFailure(t, err, CodeSendUnknown, true)
				if count.Load() != 2 {
					t.Fatal("message redirect was followed or retried")
				}
			}
		})
	}
}

func TestInvalidInputNeverContactsEvenTokenEndpoint(t *testing.T) {
	tests := []struct {
		name        string
		credentials Credentials
		input       SendInput
		code        string
	}{
		{"invalid app", Credentials{AppID: "https://synthetic.invalid", AppSecret: "synthetic"}, syntheticInput, CodeInvalidInput},
		{"secret header injection", Credentials{AppID: syntheticCredentials.AppID, AppSecret: "synthetic\r\nsecret"}, syntheticInput, CodeInvalidInput},
		{"empty secret", Credentials{AppID: syntheticCredentials.AppID}, syntheticInput, CodeInvalidInput},
	}
	for name, change := range map[string]func(*SendInput){
		"group ID":              func(input *SendInput) { input.RecipientOpenID = "oc_synthetic_group" },
		"empty UUID":            func(input *SendInput) { input.UUID = "" },
		"long UUID":             func(input *SendInput) { input.UUID = strings.Repeat("a", 51) },
		"invalid UUID":          func(input *SendInput) { input.UUID = "synthetic?secret" },
		"empty text":            func(input *SendInput) { input.Text = " \n\t" },
		"invalid UTF8":          func(input *SendInput) { input.Text = string([]byte{0xff}) },
		"control byte":          func(input *SendInput) { input.Text = "synthetic\x00text" },
		"credential reflection": func(input *SendInput) { input.Text = "private: " + syntheticCredentials.AppSecret },
	} {
		input := syntheticInput
		change(&input)
		tests = append(tests, struct {
			name        string
			credentials Credentials
			input       SendInput
			code        string
		}{name, syntheticCredentials, input, CodeInvalidInput})
	}
	for name, text := range map[string]string{
		"UTF8 byte limit":    strings.Repeat("汉", 4001),
		"escaped JSON limit": strings.Repeat("\\", 6000),
	} {
		input := syntheticInput
		input.Text = text
		tests = append(tests, struct {
			name        string
			credentials Credentials
			input       SendInput
			code        string
		}{name, syntheticCredentials, input, CodeMessageTooLarge})
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := fakeClient(func(*http.Request) (*http.Response, error) {
				t.Fatal("invalid input contacted Feishu")
				return nil, nil
			})
			_, err := client.SendText(context.Background(), test.credentials, test.input)
			assertFailure(t, err, test.code, false)
		})
	}
}

func TestExactOuterJSONBudgetAndUUIDBoundary(t *testing.T) {
	input := syntheticInput
	input.UUID = strings.Repeat("a", 50)
	if err := ValidateInput(syntheticCredentials, input); err != nil {
		t.Fatal("50-character UUID must fit", err)
	}
	input.UUID += "a"
	assertFailure(t, ValidateInput(syntheticCredentials, input), CodeInvalidInput, false)
	input = syntheticInput
	lastAccepted := 0
	for size := 1; size <= 5000; size++ {
		input.Text = strings.Repeat("\\", size)
		body, err := encodeInput(syntheticCredentials, input)
		if err != nil {
			assertFailure(t, err, CodeMessageTooLarge, false)
			if lastAccepted != size-1 {
				t.Fatal("request boundary is inconsistent")
			}
			if size >= MaxTextBytes {
				t.Fatal("test did not reach the outer-JSON byte limit first")
			}
			return
		}
		if len(body) > MaxRequestBytes {
			t.Fatal("oversized outer request was accepted")
		}
		lastAccepted = size
	}
	t.Fatal("did not exercise exact JSON size boundary")
}

func TestTokenResponsesAreStrictAndNeverMistakenForDelivery(t *testing.T) {
	tests := []struct {
		name string
		body string
		code string
	}{
		{"missing code", `{"access_token":"synthetic","expires_in":7200,"token_type":"Bearer","scope":"im:message:send_as_bot"}`, CodeAuthUnavailable},
		{"null code", `{"code":null}`, CodeAuthUnavailable},
		{"alias code", `{"CODE":0}`, CodeAuthUnavailable},
		{"duplicate code", `{"code":1,"code":0}`, CodeAuthUnavailable},
		{"old token schema", `{"code":0,"tenant_access_token":"synthetic","expire":7200}`, CodeAuthUnavailable},
		{"zero expiry", tokenBody(syntheticToken, 0), CodeAuthUnavailable},
		{"negative expiry", tokenBody(syntheticToken, -1), CodeAuthUnavailable},
		{"huge expiry", tokenBody(syntheticToken, 86401), CodeAuthUnavailable},
		{"empty token", tokenBody("", 7200), CodeAuthUnavailable},
		{"oversized token", tokenBody(strings.Repeat("T", maxTokenSize+1), 7200), CodeAuthUnavailable},
		{"header token", tokenBody("synthetic\r\ninvalid", 7200), CodeAuthUnavailable},
		{"trailing JSON", tokenBody(syntheticToken, 7200) + `{}`, CodeAuthUnavailable},
		{"wrong scope", strings.ReplaceAll(tokenBody(syntheticToken, 7200), sendScope, "im:message"), CodePermissionDenied},
		{"scope escalation", strings.ReplaceAll(tokenBody(syntheticToken, 7200), sendScope, sendScope+" im:message"), CodePermissionDenied},
		{"invalid secret", `{"code":20002,"error_description":"synthetic-secret-not-a-credential"}`, CodeAuthFailed},
		{"unapproved scope", `{"code":20068}`, CodePermissionDenied},
		{"monthly quota", `{"code":99991403}`, CodeQuotaExceeded},
		{"rate limit", `{"code":99991400}`, CodeRateLimited},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			client := fakeClient(func(request *http.Request) (*http.Response, error) {
				calls.Add(1)
				if request.URL.String() != tokenURL {
					t.Fatal("invalid token response triggered a message")
				}
				return fakeResponse(request, 200, test.body), nil
			})
			_, err := client.SendText(context.Background(), syntheticCredentials, syntheticInput)
			assertFailure(t, err, test.code, false)
			if calls.Load() != 1 || strings.Contains(err.Error(), syntheticCredentials.AppSecret) {
				t.Fatal("token error retried or exposed credentials")
			}
		})
	}
}

func TestMessageRequiresUnambiguousSuccessReceipt(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		code    string
		unknown bool
	}{
		{"missing code", 200, `{"data":{"message_id":"om_synthetic"}}`, CodeSendUnknown, true},
		{"null code", 200, `{"code":null,"data":{"message_id":"om_synthetic"}}`, CodeSendUnknown, true},
		{"alias code", 200, `{"CODE":0,"data":{"message_id":"om_synthetic"}}`, CodeSendUnknown, true},
		{"duplicate code", 200, `{"code":1,"code":0,"data":{"message_id":"om_synthetic"}}`, CodeSendUnknown, true},
		{"duplicate ID", 200, `{"code":0,"data":{"message_id":"om_first","message_id":"om_second"}}`, CodeSendUnknown, true},
		{"missing ID", 200, `{"code":0,"data":{}}`, CodeSendUnknown, true},
		{"empty ID", 200, `{"code":0,"data":{"message_id":""}}`, CodeSendUnknown, true},
		{"null ID", 200, `{"code":0,"data":{"message_id":null}}`, CodeSendUnknown, true},
		{"unsafe ID", 200, `{"code":0,"data":{"message_id":"om_\nsynthetic"}}`, CodeSendUnknown, true},
		{"malformed JSON", 200, `{"code":0`, CodeSendUnknown, true},
		{"trailing JSON", 200, syntheticReceipt + `null`, CodeSendUnknown, true},
		{"server rejection", 503, `{"code":230001}`, CodeSendUnknown, true},
		{"server success contradiction", 503, syntheticReceipt, CodeSendUnknown, true},
		{"HTTP contradiction", 400, syntheticReceipt, CodeSendUnknown, true},
		{"business contradiction", 200, `{"code":230020,"data":{"message_id":"om_synthetic"}}`, CodeSendUnknown, true},
		{"rate limited", 429, `{"code":230020}`, CodeRateLimited, false},
		{"generic rate limited", 400, `{"code":99991400}`, CodeRateLimited, false},
		{"monthly quota", 429, `{"code":99991403}`, CodeQuotaExceeded, false},
		{"recipient disabled", 200, `{"code":230035}`, CodeRecipientUnavailable, false},
		{"recipient invalid", 400, `{"code":230034}`, CodeRecipientUnavailable, false},
		{"bot disabled", 200, `{"code":230006}`, CodePermissionDenied, false},
		{"permission denied", 200, `{"code":99991672}`, CodePermissionDenied, false},
		{"expired token", 200, `{"code":99991665}`, CodeAuthFailed, false},
		{"DLP denied", 200, `{"code":230028}`, CodeRejected, false},
		{"sensitive denied", 200, `{"code":230022}`, CodeRejected, false},
		{"still sending", 200, `{"code":230049}`, CodeSendPending, true},
		{"unrecognized business code", 200, `{"code":230999}`, CodeSendUnknown, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			client := fakeClient(func(request *http.Request) (*http.Response, error) {
				calls.Add(1)
				if request.URL.String() == tokenURL {
					return fakeResponse(request, 200, tokenBody(syntheticToken, 7200)), nil
				}
				return fakeResponse(request, test.status, test.body), nil
			})
			receipt, err := client.SendText(context.Background(), syntheticCredentials, syntheticInput)
			assertFailure(t, err, test.code, test.unknown)
			if receipt.MessageID != "" || calls.Load() != 2 {
				t.Fatal("failure was converted to success or replayed")
			}
		})
	}
}

func TestTokenCacheRefreshAndCredentialChange(t *testing.T) {
	var tokenCalls, sendCalls atomic.Int32
	clock := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	client := fakeClient(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() == tokenURL {
			index := tokenCalls.Add(1)
			return fakeResponse(request, 200, tokenBody(fmt.Sprintf("synthetic-token-%d", index), 7200)), nil
		}
		sendCalls.Add(1)
		return fakeResponse(request, 200, syntheticReceipt), nil
	})
	client.now = func() time.Time { return clock }
	send := func(credentials Credentials) {
		t.Helper()
		if _, err := client.SendText(context.Background(), credentials, syntheticInput); err != nil {
			t.Fatal(err)
		}
	}
	send(syntheticCredentials)
	clock = clock.Add(7139 * time.Second)
	send(syntheticCredentials)
	if tokenCalls.Load() != 1 {
		t.Fatal("token was not cached to its actual expiry margin")
	}
	clock = clock.Add(time.Second)
	send(syntheticCredentials)
	if tokenCalls.Load() != 2 {
		t.Fatal("token was not refreshed before expiry")
	}
	changed := syntheticCredentials
	changed.AppSecret = "another-synthetic-secret"
	send(changed)
	changed.AppID = "cli_another_synthetic_app"
	send(changed)
	if tokenCalls.Load() != 4 || sendCalls.Load() != 5 {
		t.Fatal("cache reused another application or secret")
	}
}

func TestConcurrentTokenAcquisitionIsCoalesced(t *testing.T) {
	var tokenCalls, sendCalls atomic.Int32
	client := fakeClient(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() == tokenURL {
			tokenCalls.Add(1)
			return fakeResponse(request, 200, tokenBody(syntheticToken, 7200)), nil
		}
		sendCalls.Add(1)
		return fakeResponse(request, 200, syntheticReceipt), nil
	})
	var workers sync.WaitGroup
	for index := range 32 {
		workers.Go(func() {
			input := syntheticInput
			input.UUID = fmt.Sprintf("synthetic-%d", index)
			if _, err := client.SendText(context.Background(), syntheticCredentials, input); err != nil {
				t.Error(err)
			}
		})
	}
	workers.Wait()
	if tokenCalls.Load() != 1 || sendCalls.Load() != 32 {
		t.Fatalf("cache did not coalesce: tokens=%d sends=%d", tokenCalls.Load(), sendCalls.Load())
	}
}

func TestInvalidTokenDoesNotRetryCurrentMessage(t *testing.T) {
	var tokenCalls, sendCalls atomic.Int32
	client := fakeClient(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() == tokenURL {
			index := tokenCalls.Add(1)
			return fakeResponse(request, 200, tokenBody(fmt.Sprintf("synthetic-token-%d", index), 7200)), nil
		}
		if sendCalls.Add(1) == 1 {
			return fakeResponse(request, 200, `{"code":99991665}`), nil
		}
		return fakeResponse(request, 200, syntheticReceipt), nil
	})
	_, err := client.SendText(context.Background(), syntheticCredentials, syntheticInput)
	assertFailure(t, err, CodeAuthFailed, false)
	if tokenCalls.Load() != 1 || sendCalls.Load() != 1 {
		t.Fatal("current message was replayed after token failure")
	}
	// A separate, newly authorized logical message may acquire a fresh token.
	input := syntheticInput
	input.UUID = "fd_separate_synthetic_operation"
	if _, err := client.SendText(context.Background(), syntheticCredentials, input); err != nil {
		t.Fatal(err)
	}
	if tokenCalls.Load() != 2 || sendCalls.Load() != 2 {
		t.Fatal("invalid cached token was not discarded for a future operation")
	}
}

func TestNetworkErrorsAreSanitizedAndNeverRetried(t *testing.T) {
	for _, stage := range []string{"token", "message"} {
		t.Run(stage, func(t *testing.T) {
			var calls atomic.Int32
			client := fakeClient(func(request *http.Request) (*http.Response, error) {
				calls.Add(1)
				if stage == "message" && request.URL.String() == tokenURL {
					return fakeResponse(request, 200, tokenBody(syntheticToken, 7200)), nil
				}
				return nil, errors.New("synthetic network error containing " + syntheticCredentials.AppSecret)
			})
			_, err := client.SendText(context.Background(), syntheticCredentials, syntheticInput)
			if stage == "token" {
				assertFailure(t, err, CodeAuthUnavailable, false)
				if calls.Load() != 1 {
					t.Fatal("token request was retried")
				}
			} else {
				assertFailure(t, err, CodeSendUnknown, true)
				if calls.Load() != 2 {
					t.Fatal("uncertain message was replayed")
				}
			}
			if strings.Contains(err.Error(), syntheticCredentials.AppSecret) || strings.Contains(err.Error(), "network error") {
				t.Fatal("upstream diagnostic escaped")
			}
		})
	}
}

type brokenBody struct{ closed *atomic.Bool }

func (body brokenBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (body brokenBody) Close() error             { body.closed.Store(true); return nil }

func TestUnreadableOversizedAndNonJSONResponses(t *testing.T) {
	for _, stage := range []string{"token", "message"} {
		for _, kind := range []string{"broken", "oversized", "wrong-type"} {
			t.Run(stage+"/"+kind, func(t *testing.T) {
				var closed atomic.Bool
				client := fakeClient(func(request *http.Request) (*http.Response, error) {
					if stage == "message" && request.URL.String() == tokenURL {
						return fakeResponse(request, 200, tokenBody(syntheticToken, 7200)), nil
					}
					response := fakeResponse(request, 200, syntheticReceipt)
					switch kind {
					case "broken":
						response.Body = brokenBody{closed: &closed}
					case "oversized":
						response.Body = io.NopCloser(strings.NewReader(strings.Repeat(" ", maxResponseSize+1)))
						response.ContentLength = -1 // Chunked or lying length cannot bypass the read cap.
					case "wrong-type":
						response.Header.Set("Content-Type", "text/html")
					}
					return response, nil
				})
				_, err := client.SendText(context.Background(), syntheticCredentials, syntheticInput)
				if stage == "token" {
					assertFailure(t, err, CodeAuthUnavailable, false)
				} else {
					assertFailure(t, err, CodeSendUnknown, true)
				}
				if kind == "broken" && !closed.Load() {
					t.Fatal("response body was not closed")
				}
			})
		}
	}
}

func TestCancellationBoundaryAndClose(t *testing.T) {
	for _, stage := range []string{"before", "token", "message"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var calls atomic.Int32
			client := fakeClient(func(request *http.Request) (*http.Response, error) {
				calls.Add(1)
				if stage == "message" && request.URL.String() == tokenURL {
					return fakeResponse(request, 200, tokenBody(syntheticToken, 7200)), nil
				}
				cancel()
				return nil, request.Context().Err()
			})
			if stage == "before" {
				cancel()
			}
			_, err := client.SendText(ctx, syntheticCredentials, syntheticInput)
			switch stage {
			case "before":
				assertFailure(t, err, CodeInvalidInput, false)
				if calls.Load() != 0 {
					t.Fatal("already-canceled work contacted a platform")
				}
			case "token":
				assertFailure(t, err, CodeAuthUnavailable, false)
			case "message":
				assertFailure(t, err, CodeSendUnknown, true)
			}
		})
	}
	client := fakeClient(func(*http.Request) (*http.Response, error) {
		t.Fatal("closed client contacted platform")
		return nil, nil
	})
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := client.SendText(context.Background(), syntheticCredentials, syntheticInput)
	assertFailure(t, err, CodeClosed, false)
}

func TestAcquiredTokenCannotBeReflectedInMessage(t *testing.T) {
	var calls atomic.Int32
	client := fakeClient(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		if request.URL.String() != tokenURL {
			t.Fatal("cached token was reflected in a message")
		}
		return fakeResponse(request, 200, tokenBody(syntheticToken, 7200)), nil
	})
	input := syntheticInput
	input.Text = "synthetic private value: " + syntheticToken
	_, err := client.SendText(context.Background(), syntheticCredentials, input)
	assertFailure(t, err, CodeInvalidInput, false)
	if calls.Load() != 1 {
		t.Fatal("token-reflection protection made an unexpected request")
	}
}

func TestReceiptCannotReflectPrivateValues(t *testing.T) {
	for name, private := range map[string]string{"secret": syntheticCredentials.AppSecret, "token": syntheticToken, "recipient": syntheticInput.RecipientOpenID} {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			client := fakeClient(func(request *http.Request) (*http.Response, error) {
				calls.Add(1)
				if request.URL.String() == tokenURL {
					return fakeResponse(request, 200, tokenBody(syntheticToken, 7200)), nil
				}
				body, _ := json.Marshal(map[string]any{"code": 0, "data": map[string]any{"message_id": "om_" + private}})
				return fakeResponse(request, 200, string(body)), nil
			})
			receipt, err := client.SendText(context.Background(), syntheticCredentials, syntheticInput)
			assertFailure(t, err, CodeSendUnknown, true)
			if receipt.MessageID != "" || calls.Load() != 2 {
				t.Fatal("reflected private value escaped, or a message was retried")
			}
		})
	}
}

func TestSensitiveTypesRedactDiagnosticsAndJSON(t *testing.T) {
	client := NewClient()
	client.cache.token = syntheticToken
	for _, value := range []any{syntheticCredentials, syntheticInput, client} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
			formatted := fmt.Sprintf(format, value)
			for _, private := range []string{syntheticCredentials.AppSecret, syntheticInput.RecipientOpenID, syntheticInput.Text, syntheticToken} {
				if strings.Contains(formatted, private) {
					t.Fatal("sensitive diagnostic formatting")
				}
			}
		}
		raw, err := json.Marshal(value)
		if err != nil || string(raw) != `"[redacted]"` {
			t.Fatal("sensitive JSON serialization")
		}
	}
	if (&Error{Code: "synthetic-private-upstream", Unknown: true}).Error() != CodeSendUnknown {
		t.Fatal("non-enumerated diagnostic escaped")
	}
}

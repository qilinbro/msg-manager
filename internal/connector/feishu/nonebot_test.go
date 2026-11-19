package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qilinbro/msg-manager/internal/connector/nonebotbridge"
)

type feishuBridgeFunc func(context.Context, string, any, any) error

func (call feishuBridgeFunc) Call(ctx context.Context, action string, payload, output any) error {
	return call(ctx, action, payload, output)
}

type feishuConfiguredBridge struct {
	feishuBridgeFunc
	configured bool
}

func (caller feishuConfiguredBridge) Configured() bool { return caller.configured }

func TestNoneBotAvailabilityDoesNotMakeCallsOrClaimIdentity(t *testing.T) {
	var calls int
	call := feishuBridgeFunc(func(context.Context, string, any, any) error {
		calls++
		return nil
	})
	var nilClient *NoneBotClient
	if nilClient.Available() {
		t.Fatal("nil client is available")
	}
	for _, test := range []struct {
		caller    nonebotbridge.Caller
		available bool
	}{
		{nil, false},
		{(*nonebotbridge.Client)(nil), false},
		{feishuConfiguredBridge{call, false}, false},
		{feishuConfiguredBridge{call, true}, true},
		{call, true},
	} {
		client := NewNoneBotClient(test.caller)
		if client.Available() != test.available {
			t.Fatal("unexpected configuration availability")
		}
		_ = client.Close()
		if client.Available() {
			t.Fatal("closed client is available")
		}
	}
	if calls != 0 {
		t.Fatal("availability performed an operation")
	}
}

func TestNoneBotUsesExactFrozenPayloadAndOneCall(t *testing.T) {
	var calls atomic.Int32
	client := NewNoneBotClient(feishuBridgeFunc(func(ctx context.Context, action string, value, output any) error {
		calls.Add(1)
		if action != "feishu.send" || ctx.Err() != nil {
			t.Fatal("unexpected bridge operation")
		}
		payload, ok := value.(noneBotSendPayload)
		if !ok || payload.AppID != syntheticCredentials.AppID || payload.AppSecret != syntheticCredentials.AppSecret ||
			payload.RecipientOpenID != syntheticInput.RecipientOpenID || payload.Text != syntheticInput.Text || payload.UUID != syntheticInput.UUID {
			t.Fatal("bridge changed frozen send inputs")
		}
		for _, diagnostic := range []string{fmt.Sprint(value), fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value)} {
			if strings.Contains(diagnostic, syntheticCredentials.AppSecret) || strings.Contains(diagnostic, syntheticInput.Text) || strings.Contains(diagnostic, syntheticInput.RecipientOpenID) {
				t.Fatal("wire payload has an unsafe diagnostic representation")
			}
		}
		encoded, err := json.Marshal(value)
		var wire map[string]string
		if err != nil || json.Unmarshal(encoded, &wire) != nil || len(wire) != 5 ||
			wire["app_id"] != syntheticCredentials.AppID || wire["app_secret"] != syntheticCredentials.AppSecret ||
			wire["recipient_open_id"] != syntheticInput.RecipientOpenID || wire["text"] != syntheticInput.Text || wire["uuid"] != syntheticInput.UUID {
			t.Fatal("unexpected selected stdin payload")
		}
		return json.Unmarshal([]byte(`{"message_id":"om_nonebot_synthetic_receipt"}`), output)
	}))
	defer client.Close()
	receipt, err := client.SendText(context.Background(), syntheticCredentials, syntheticInput)
	if err != nil || receipt.MessageID != "om_nonebot_synthetic_receipt" || calls.Load() != 1 {
		t.Fatal("one bridge operation did not return a validated receipt")
	}
}

func TestNoneBotInvalidInputNeverStartsBridge(t *testing.T) {
	var calls atomic.Int32
	client := NewNoneBotClient(feishuBridgeFunc(func(context.Context, string, any, any) error {
		calls.Add(1)
		return errors.New("must not start worker")
	}))
	defer client.Close()
	for _, mutate := range []func(*Credentials, *SendInput){
		func(c *Credentials, _ *SendInput) { c.AppID = "other_origin" },
		func(c *Credentials, _ *SendInput) { c.AppSecret = "invalid\nsecret" },
		func(_ *Credentials, in *SendInput) { in.RecipientOpenID = "oc_group" },
		func(_ *Credentials, in *SendInput) { in.UUID = "unbounded?uuid" },
		func(c *Credentials, in *SendInput) { in.Text = "do not send " + c.AppSecret },
		func(_ *Credentials, in *SendInput) { in.Text = strings.Repeat("x", MaxTextBytes+1) },
	} {
		credentials, input := syntheticCredentials, syntheticInput
		mutate(&credentials, &input)
		if _, err := client.SendText(context.Background(), credentials, input); err == nil {
			t.Fatal("invalid input was accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, ctx := range []context.Context{nil, ctx} {
		_, err := client.SendText(ctx, syntheticCredentials, syntheticInput)
		assertFailure(t, err, CodeInvalidInput, false)
	}
	if calls.Load() != 0 {
		t.Fatal("invalid operation started a bridge worker")
	}
}

func TestNoneBotBridgeFailureMatrixIsConservativeAndNeverRetried(t *testing.T) {
	tests := []struct {
		name    string
		failure error
		code    string
		unknown bool
	}{
		{"plain error", errors.New("synthetic private error must not be exposed"), CodeSendUnknown, true},
		{"lost reply", &nonebotbridge.Error{Kind: nonebotbridge.ProtocolError, SendState: "unknown"}, CodeSendUnknown, true},
		{"missing send state", &nonebotbridge.Error{Kind: nonebotbridge.Unreachable}, CodeSendUnknown, true},
		{"canceled after possible send", &nonebotbridge.Error{Kind: nonebotbridge.Canceled, SendState: "unknown"}, CodeSendUnknown, true},
		{"auth after possible send", &nonebotbridge.Error{Kind: nonebotbridge.AuthFailed, SendState: "unknown"}, CodeSendUnknown, true},
		{"auth rejection", &nonebotbridge.Error{Kind: nonebotbridge.AuthFailed, PlatformCode: 99991665, SendState: "not_sent"}, CodeAuthFailed, false},
		{"policy 21001", &nonebotbridge.Error{Kind: nonebotbridge.PermissionDenied, PlatformCode: 21001, SendState: "not_sent"}, CodePermissionDenied, false},
		{"scope rejected", &nonebotbridge.Error{Kind: nonebotbridge.PermissionDenied, PlatformCode: 20068, SendState: "not_sent"}, CodePermissionDenied, false},
		{"rate limited", &nonebotbridge.Error{Kind: nonebotbridge.RateLimited, PlatformCode: 99991400, SendState: "not_sent"}, CodeRateLimited, false},
		{"quota", &nonebotbridge.Error{Kind: nonebotbridge.RateLimited, PlatformCode: 99991403, SendState: "not_sent"}, CodeQuotaExceeded, false},
		{"recipient", &nonebotbridge.Error{Kind: nonebotbridge.PlatformRejected, PlatformCode: 230013, SendState: "not_sent"}, CodeRecipientUnavailable, false},
		{"explicit message rejection", &nonebotbridge.Error{Kind: nonebotbridge.PlatformRejected, PlatformCode: 230001, SendState: "not_sent"}, CodeRejected, false},
		{"pending is not definite rejection", &nonebotbridge.Error{Kind: nonebotbridge.PlatformRejected, PlatformCode: 230049, SendState: "not_sent"}, CodeSendPending, true},
		{"unfamiliar platform code", &nonebotbridge.Error{Kind: nonebotbridge.PlatformRejected, PlatformCode: 987654321, SendState: "not_sent"}, CodeSendUnknown, true},
		{"auth transport before send", &nonebotbridge.Error{Kind: nonebotbridge.Unavailable, SendState: "not_sent"}, CodeAuthUnavailable, false},
		{"not configured", &nonebotbridge.Error{Kind: nonebotbridge.NotConfigured, SendState: "not_sent"}, CodeAuthUnavailable, false},
		{"invalid bridge config", &nonebotbridge.Error{Kind: nonebotbridge.InvalidConfig, SendState: "not_sent"}, CodeAuthUnavailable, false},
		{"token protocol rejected", &nonebotbridge.Error{Kind: nonebotbridge.ProtocolError, SendState: "not_sent"}, CodeAuthUnavailable, false},
		{"canceled before send", &nonebotbridge.Error{Kind: nonebotbridge.Canceled, SendState: "not_sent"}, CodeAuthUnavailable, false},
		{"explicit unknown", &nonebotbridge.Error{Kind: nonebotbridge.SendUnknown, SendState: "not_sent"}, CodeSendUnknown, true},
		{"wrong platform error", &nonebotbridge.Error{Kind: nonebotbridge.IdentityMismatch, SendState: "not_sent"}, CodeSendUnknown, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls int
			client := NewNoneBotClient(feishuBridgeFunc(func(context.Context, string, any, any) error {
				calls++
				return fmt.Errorf("synthetic private wrapper: %w", test.failure)
			}))
			defer client.Close()
			_, err := client.SendText(context.Background(), syntheticCredentials, syntheticInput)
			assertFailure(t, err, test.code, test.unknown)
			if calls != 1 || strings.Contains(err.Error(), "synthetic") {
				t.Fatal("bridge error was retried or unsanitized")
			}
		})
	}
}

func TestNoneBotRequiresStrictSafeReceipt(t *testing.T) {
	for _, body := range []string{
		`{}`, `null`, `[]`, `{"MessageID":"om_synthetic"}`, `{"MESSAGE_ID":"om_synthetic"}`,
		`{"message_id":null}`, `{"message_id":2}`, `{"message_id":""}`,
		`{"message_id":"om_one","message_id":"om_two"}`, `{"message_id":"om_synthetic"} {}`,
		`{"message_id":"om_synthetic","private_text":"must not expose"}`,
		`{"message_id":"unsafe\nreceipt"}`,
		`{"message_id":"` + strings.Repeat("x", 257) + `"}`,
		`{"message_id":"` + syntheticCredentials.AppSecret + `"}`,
		`{"message_id":"` + syntheticInput.RecipientOpenID + `"}`,
	} {
		var calls int
		client := NewNoneBotClient(feishuBridgeFunc(func(_ context.Context, _ string, _, output any) error {
			calls++
			return json.Unmarshal([]byte(body), output)
		}))
		_, err := client.SendText(context.Background(), syntheticCredentials, syntheticInput)
		assertFailure(t, err, CodeSendUnknown, true)
		if calls != 1 {
			t.Fatal("malformed receipt retried")
		}
		_ = client.Close()
	}
}

func TestNoneBotAuthFailureDoesNotReplayButSeparateOperationCanRun(t *testing.T) {
	var calls int
	client := NewNoneBotClient(feishuBridgeFunc(func(_ context.Context, _ string, _, output any) error {
		calls++
		if calls == 1 {
			return &nonebotbridge.Error{Kind: nonebotbridge.AuthFailed, PlatformCode: 99991665, SendState: "not_sent"}
		}
		return json.Unmarshal([]byte(`{"message_id":"om_separate_synthetic_operation"}`), output)
	}))
	defer client.Close()
	_, err := client.SendText(context.Background(), syntheticCredentials, syntheticInput)
	assertFailure(t, err, CodeAuthFailed, false)
	if calls != 1 {
		t.Fatal("auth failure replayed the current operation")
	}
	input := syntheticInput
	input.UUID = "fd_separate_synthetic_operation"
	if _, err = client.SendText(context.Background(), syntheticCredentials, input); err != nil || calls != 2 {
		t.Fatal("separately authorized operation failed")
	}
}

func TestNoneBotCloseCancelsInFlightAndNeverStartsAnotherWorker(t *testing.T) {
	entered := make(chan struct{})
	client := NewNoneBotClient(feishuBridgeFunc(func(ctx context.Context, _ string, _, _ any) error {
		close(entered)
		<-ctx.Done()
		return &nonebotbridge.Error{Kind: nonebotbridge.Canceled, SendState: "unknown"}
	}))
	finished := make(chan error, 1)
	go func() {
		_, err := client.SendText(context.Background(), syntheticCredentials, syntheticInput)
		finished <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("bridge was not entered")
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		assertFailure(t, err, CodeSendUnknown, true)
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not cancel bridge worker")
	}
	_, err := client.SendText(context.Background(), syntheticCredentials, syntheticInput)
	assertFailure(t, err, CodeClosed, false)
	_ = client.Close()
	var nilClient *NoneBotClient
	_ = nilClient.Close()
	_, err = nilClient.SendText(context.Background(), syntheticCredentials, syntheticInput)
	assertFailure(t, err, CodeAuthUnavailable, false)
	_, err = NewNoneBotClient(nil).SendText(context.Background(), syntheticCredentials, syntheticInput)
	assertFailure(t, err, CodeAuthUnavailable, false)
}

func TestNoneBotCanceledReplyCannotBeMistakenForSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := NewNoneBotClient(feishuBridgeFunc(func(_ context.Context, _ string, _, output any) error {
		cancel()
		return json.Unmarshal([]byte(`{"message_id":"om_canceled_synthetic_receipt"}`), output)
	}))
	defer client.Close()
	_, err := client.SendText(ctx, syntheticCredentials, syntheticInput)
	assertFailure(t, err, CodeSendUnknown, true)
}

package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/qilinbro/msg-manager/internal/connector/nonebotbridge"
)

// NoneBotClient sends only through the isolated, real NoneBot adapter worker.
// The caller must first persist the frozen report and its one-shot claim, just
// as for Client. A bridge error never falls back to the legacy HTTP client.
// Each call has a fresh worker and therefore no cross-operation token cache.
type NoneBotClient struct {
	mu        sync.Mutex
	caller    nonebotbridge.Caller
	lifecycle context.Context
	cancel    context.CancelFunc
	closed    bool
}

var _ Sender = (*NoneBotClient)(nil)

func NewNoneBotClient(caller nonebotbridge.Caller) *NoneBotClient {
	lifecycle, cancel := context.WithCancel(context.Background())
	return &NoneBotClient{caller: caller, lifecycle: lifecycle, cancel: cancel}
}

// Available permits the owner to reject an unconfigured runtime before burning
// a durable delivery claim. It performs no I/O and does not prove platform
// authentication or recipient ownership. Test callers need only implement Call.
func (client *NoneBotClient) Available() bool {
	if client == nil {
		return false
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closed || client.caller == nil {
		return false
	}
	if configured, ok := client.caller.(interface{ Configured() bool }); ok {
		return configured.Configured()
	}
	return true
}

// Close prevents new operations and cancels workers already in flight. Their
// outcome remains unknown unless the bridge can positively prove non-delivery.
func (client *NoneBotClient) Close() error {
	if client == nil {
		return nil
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	client.closed = true
	if client.cancel != nil {
		client.cancel()
	}
	return nil
}

type noneBotSendPayload struct {
	AppID           string `json:"app_id"`
	AppSecret       string `json:"app_secret"`
	RecipientOpenID string `json:"recipient_open_id"`
	Text            string `json:"text"`
	UUID            string `json:"uuid"`
}

// JSON is the deliberately selected stdin wire payload, not a diagnostic form.
func (noneBotSendPayload) String() string           { return "feishu.noneBotSendPayload{[redacted]}" }
func (payload noneBotSendPayload) GoString() string { return payload.String() }

type noneBotSendResult struct {
	MessageID string `json:"message_id"`
}

func (result *noneBotSendResult) UnmarshalJSON(data []byte) error {
	fields, ok := objectFields(data)
	if !ok || len(fields) != 1 || json.Unmarshal(fields["message_id"], &result.MessageID) != nil {
		return errors.New("invalid NoneBot Feishu result")
	}
	return nil
}

func (client *NoneBotClient) SendText(ctx context.Context, credentials Credentials, input SendInput) (Receipt, error) {
	if err := ValidateInput(credentials, input); err != nil {
		return Receipt{}, err
	}
	if ctx == nil || ctx.Err() != nil {
		return Receipt{}, fail(CodeInvalidInput, false)
	}
	if client == nil {
		return Receipt{}, fail(CodeAuthUnavailable, false)
	}
	client.mu.Lock()
	if client.closed {
		client.mu.Unlock()
		return Receipt{}, fail(CodeClosed, false)
	}
	caller := client.caller
	if caller == nil || client.lifecycle == nil {
		client.mu.Unlock()
		return Receipt{}, fail(CodeAuthUnavailable, false)
	}
	operation, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(client.lifecycle, cancel)
	client.mu.Unlock()
	defer cancel()
	defer stop()
	if operation.Err() != nil {
		return Receipt{}, fail(CodeClosed, false)
	}
	payload := noneBotSendPayload{
		AppID: credentials.AppID, AppSecret: credentials.AppSecret,
		RecipientOpenID: input.RecipientOpenID, Text: input.Text, UUID: input.UUID,
	}
	var result noneBotSendResult
	if err := caller.Call(operation, "feishu.send", payload, &result); err != nil {
		return Receipt{}, noneBotFailure(err)
	}
	// A missing, malformed or lost reply after a possible message submission is
	// not evidence of non-delivery. Neither cancellation nor parsing retries it.
	if operation.Err() != nil || !headerSafe(result.MessageID, 256) ||
		strings.Contains(result.MessageID, credentials.AppSecret) ||
		strings.Contains(result.MessageID, input.RecipientOpenID) {
		return Receipt{}, fail(CodeSendUnknown, true)
	}
	return Receipt{MessageID: result.MessageID}, nil
}

func noneBotFailure(err error) error {
	var failure *nonebotbridge.Error
	if !errors.As(err, &failure) || failure == nil || failure.SendState != "not_sent" {
		return fail(CodeSendUnknown, true)
	}
	// Only a positive, structured non-delivery verdict can produce failed.
	// Unknown/unsupported kinds and contradictory status stay conservative.
	switch failure.Kind {
	case nonebotbridge.AuthFailed:
		return fail(CodeAuthFailed, false)
	case nonebotbridge.PermissionDenied:
		return fail(CodePermissionDenied, false)
	case nonebotbridge.RateLimited:
		if failure.PlatformCode == 99991403 {
			return fail(CodeQuotaExceeded, false)
		}
		return fail(CodeRateLimited, false)
	case nonebotbridge.PlatformRejected:
		code, unknown := messageFailure(failure.PlatformCode)
		return fail(code, unknown)
	case nonebotbridge.NotConfigured, nonebotbridge.InvalidConfig, nonebotbridge.Unreachable,
		nonebotbridge.ProtocolError, nonebotbridge.Canceled, nonebotbridge.Unavailable:
		return fail(CodeAuthUnavailable, false)
	default:
		return fail(CodeSendUnknown, true)
	}
}

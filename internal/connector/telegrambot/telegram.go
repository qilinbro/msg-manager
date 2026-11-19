// Package telegrambot implements a manual, receive-only Telegram Bot API
// boundary using the real Python NoneBot adapter through a restricted bridge.
// It does not log in a personal Telegram account, send, or fetch chat history.
package telegrambot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qilinbro/msg-manager/internal/connector/nonebotbridge"
)

const (
	DefaultLimit          = 20
	MaxLimit              = 100
	MaxPrivateChats       = 20
	MaxBodyBytes          = 16000
	MaxID           int64 = 1<<52 - 1
	MaxUpdateID     int64 = math.MaxInt64 - 1
)

// Config is operator-owned. Formatting and JSON must never expose a bot token
// or the authorized contact list to logs, browsers, or model tools.
type Config struct {
	Token                 string   `json:"-"`
	ExpectedBotID         string   `json:"-"`
	AllowedPrivateChatIDs []string `json:"-"`
	Limit                 int      `json:"-"`
}

func (c Config) Configured() bool { return c.Token != "" }
func (Config) String() string     { return "Telegram bot configuration (redacted)" }
func (Config) GoString() string   { return "Telegram bot configuration (redacted)" }
func (c Config) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Configured bool `json:"configured"`
		Redacted   bool `json:"redacted"`
	}{c.Configured(), true})
}

func invalid() error  { return &nonebotbridge.Error{Kind: nonebotbridge.InvalidConfig} }
func protocol() error { return &nonebotbridge.Error{Kind: nonebotbridge.ProtocolError} }

func decimal(value string, positive bool, maximum int64) bool {
	if value == "" || len(value) > 19 || len(value) > 1 && value[0] == '0' {
		return false
	}
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	n, err := strconv.ParseInt(value, 10, 64)
	return err == nil && n >= 0 && (!positive || n > 0) && n <= maximum
}

func ValidID(value string) bool     { return decimal(value, true, MaxID) }
func ValidOffset(value string) bool { return decimal(value, false, math.MaxInt64) }

func ValidateAllowed(ids []string) bool {
	if len(ids) < 1 || len(ids) > MaxPrivateChats {
		return false
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !ValidID(id) || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

// ValidateConfig permits only the completely empty, unconfigured value or a
// complete bot identity and explicit bounded private-chat allowlist.
func ValidateConfig(c Config) error {
	if c.Token == "" && c.ExpectedBotID == "" && len(c.AllowedPrivateChatIDs) == 0 && c.Limit == 0 {
		return nil
	}
	if !ValidID(c.ExpectedBotID) || !ValidateAllowed(c.AllowedPrivateChatIDs) || c.Limit < 0 || c.Limit > MaxLimit {
		return invalid()
	}
	prefix, secret, ok := strings.Cut(c.Token, ":")
	if !ok || prefix != c.ExpectedBotID || len(secret) < 20 || len(secret) > 128 {
		return invalid()
	}
	for _, ch := range secret {
		if !(ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-') {
			return invalid()
		}
	}
	return nil
}

type Verification struct {
	BotID          string `json:"bot_id"`
	IsBot          bool   `json:"is_bot"`
	WebhookPresent bool   `json:"webhook_present"`
}

type Message struct {
	ChatID    string `json:"chat_id"`
	MessageID string `json:"message_id"`
	SenderID  string `json:"sender_id"`
	ChatType  string `json:"chat_type"`
	Body      string `json:"body"`
	Date      int64  `json:"date"`
}

type Event struct {
	UpdateID string   `json:"update_id"`
	Type     string   `json:"type"`
	Reason   string   `json:"reason"`
	Message  *Message `json:"message,omitempty"`
}

type Page struct {
	Verification
	Events     []Event `json:"events"`
	NextOffset string  `json:"next_offset"`
}

type Client struct {
	cfg    Config
	caller nonebotbridge.Caller
}

func New(c Config, caller nonebotbridge.Caller) (*Client, error) {
	if err := ValidateConfig(c); err != nil {
		return nil, err
	}
	c.AllowedPrivateChatIDs = append([]string(nil), c.AllowedPrivateChatIDs...)
	if c.Configured() && c.Limit == 0 {
		c.Limit = DefaultLimit
	}
	return &Client{cfg: c, caller: caller}, nil
}
func (c *Client) Configured() bool { return c != nil && c.cfg.Configured() }
func (c *Client) RuntimeConfigured() bool {
	if c == nil || c.caller == nil {
		return false
	}
	if runtime, ok := c.caller.(interface{ Configured() bool }); ok {
		return runtime.Configured()
	}
	return true
}
func (c *Client) BotID() string {
	if c == nil {
		return ""
	}
	return c.cfg.ExpectedBotID
}
func (c *Client) AllowedPrivateChatIDs() []string {
	if c == nil {
		return nil
	}
	return append([]string(nil), c.cfg.AllowedPrivateChatIDs...)
}
func (c *Client) String() string   { return "Telegram bot client (redacted)" }
func (c *Client) GoString() string { return c.String() }

type request struct {
	Token                 string   `json:"token"`
	ExpectedBotID         string   `json:"expected_bot_id"`
	AllowedPrivateChatIDs []string `json:"allowed_private_chat_ids"`
	Offset                string   `json:"offset,omitempty"`
	Limit                 int      `json:"limit,omitempty"`
}

func (request) String() string   { return "Telegram bridge request (redacted)" }
func (request) GoString() string { return "Telegram bridge request (redacted)" }

func sanitized(ctx context.Context, err error) error {
	if ctx != nil && ctx.Err() != nil {
		return &nonebotbridge.Error{Kind: nonebotbridge.Canceled}
	}
	var bridge *nonebotbridge.Error
	if errors.As(err, &bridge) {
		switch bridge.Kind {
		case nonebotbridge.NotConfigured, nonebotbridge.InvalidConfig, nonebotbridge.Unreachable, nonebotbridge.AuthFailed, nonebotbridge.RateLimited, nonebotbridge.ProtocolError, nonebotbridge.IdentityMismatch, nonebotbridge.WebhookPresent, nonebotbridge.Canceled, nonebotbridge.PermissionDenied, nonebotbridge.PlatformRejected, nonebotbridge.Unavailable:
			wait := bridge.RetryAfter
			if wait < 0 {
				wait = 0
			}
			if wait > 24*time.Hour {
				wait = 24 * time.Hour
			}
			return &nonebotbridge.Error{Kind: bridge.Kind, RetryAfter: wait}
		}
	}
	return protocol()
}

func (c *Client) call(ctx context.Context, action string, offset string, target any) error {
	if ctx == nil {
		return invalid()
	}
	if ctx.Err() != nil {
		return &nonebotbridge.Error{Kind: nonebotbridge.Canceled}
	}
	if !c.Configured() || !c.RuntimeConfigured() {
		return &nonebotbridge.Error{Kind: nonebotbridge.NotConfigured}
	}
	payload := request{Token: c.cfg.Token, ExpectedBotID: c.cfg.ExpectedBotID, AllowedPrivateChatIDs: c.AllowedPrivateChatIDs()}
	if action == "telegram.poll" {
		payload.Offset, payload.Limit = offset, c.cfg.Limit
	}
	var raw json.RawMessage
	if err := c.caller.Call(ctx, action, payload, &raw); err != nil {
		return sanitized(ctx, err)
	}
	if ctx.Err() != nil {
		return &nonebotbridge.Error{Kind: nonebotbridge.Canceled}
	}
	if err := decodeResult(raw, target); err != nil {
		return protocol()
	}
	return nil
}

func verify(v Verification, expected string) error {
	if !ValidID(v.BotID) || !v.IsBot || v.BotID != expected {
		return &nonebotbridge.Error{Kind: nonebotbridge.IdentityMismatch}
	}
	if v.WebhookPresent {
		return &nonebotbridge.Error{Kind: nonebotbridge.WebhookPresent}
	}
	return nil
}

func (c *Client) Probe(ctx context.Context) (Verification, error) {
	var wire struct {
		BotID          string `json:"bot_id"`
		IsBot          *bool  `json:"is_bot"`
		WebhookPresent *bool  `json:"webhook_present"`
	}
	if err := c.call(ctx, "telegram.probe", "", &wire); err != nil {
		return Verification{}, err
	}
	if wire.IsBot == nil || wire.WebhookPresent == nil {
		return Verification{}, protocol()
	}
	v := Verification{BotID: wire.BotID, IsBot: *wire.IsBot, WebhookPresent: *wire.WebhookPresent}
	if err := verify(v, c.BotID()); err != nil {
		return Verification{}, err
	}
	return v, nil
}

func (c *Client) Poll(ctx context.Context, offset string) (Page, error) {
	if !ValidOffset(offset) {
		return Page{}, invalid()
	}
	var wire struct {
		BotID          string   `json:"bot_id"`
		IsBot          *bool    `json:"is_bot"`
		WebhookPresent *bool    `json:"webhook_present"`
		Events         *[]Event `json:"events"`
		NextOffset     string   `json:"next_offset"`
	}
	if err := c.call(ctx, "telegram.poll", offset, &wire); err != nil {
		return Page{}, err
	}
	if wire.IsBot == nil || wire.WebhookPresent == nil || wire.Events == nil {
		return Page{}, protocol()
	}
	page := Page{Verification: Verification{BotID: wire.BotID, IsBot: *wire.IsBot, WebhookPresent: *wire.WebhookPresent}, Events: *wire.Events, NextOffset: wire.NextOffset}
	if err := ValidatePage(page, c.BotID(), c.AllowedPrivateChatIDs(), offset, c.cfg.Limit); err != nil {
		return Page{}, err
	}
	return page, nil
}

var knownTypes = map[string]bool{
	"message": true, "edited_message": true, "channel_post": true, "edited_channel_post": true,
	"business_connection": true, "business_message": true, "edited_business_message": true, "deleted_business_messages": true,
	"message_reaction": true, "message_reaction_count": true, "inline_query": true, "chosen_inline_result": true,
	"callback_query": true, "shipping_query": true, "pre_checkout_query": true, "purchased_paid_media": true,
	"poll": true, "poll_answer": true, "my_chat_member": true, "chat_member": true, "chat_join_request": true,
	"chat_boost": true, "removed_chat_boost": true, "unknown": true,
}

func validReason(event Event) bool {
	switch event.Reason {
	case "not_private", "not_authorized", "unsupported_media", "unsupported_message", "malformed_message":
		return event.Type == "message"
	case "unsupported_edit":
		return event.Type == "edited_message" || event.Type == "edited_channel_post" || event.Type == "edited_business_message"
	case "unsupported_event":
		return event.Type != "message" && event.Type != "edited_message" && event.Type != "edited_channel_post" && event.Type != "edited_business_message"
	}
	return false
}

// ValidatePage is shared with the encrypted-store boundary so even an internal
// caller cannot smuggle unsupported text, a foreign peer, or an invented cursor
// into the narrow Telegram transaction. offset is the actual requested offset,
// not an eternal ID watermark: the store/API selects 0 for an expired receive
// lease before I/O. Never relax this check or retry a lower offset in the worker.
func ValidatePage(page Page, expectedBotID string, allowed []string, offset string, limit int) error {
	if !ValidID(expectedBotID) || !ValidateAllowed(allowed) || !ValidOffset(offset) || limit < 1 || limit > MaxLimit {
		return invalid()
	}
	if err := verify(page.Verification, expectedBotID); err != nil {
		return err
	}
	if page.Events == nil || len(page.Events) > limit || !ValidOffset(page.NextOffset) {
		return protocol()
	}
	allow := map[string]bool{}
	for _, id := range allowed {
		allow[id] = true
	}
	minimum, _ := strconv.ParseInt(offset, 10, 64)
	next := minimum
	last := int64(-1)
	for _, event := range page.Events {
		if !decimal(event.UpdateID, false, MaxUpdateID) || !knownTypes[event.Type] {
			return protocol()
		}
		id, _ := strconv.ParseInt(event.UpdateID, 10, 64)
		if id < minimum || id <= last {
			return protocol()
		}
		last, next = id, id+1
		if event.Reason != "" {
			if event.Message != nil || !validReason(event) {
				return protocol()
			}
			continue
		}
		m := event.Message
		if event.Type != "message" || m == nil || !ValidID(m.ChatID) || !allow[m.ChatID] || m.SenderID != m.ChatID || m.ChatType != "private" || !ValidID(m.MessageID) || len(m.Body) < 1 || len(m.Body) > MaxBodyBytes || !utf8.ValidString(m.Body) || strings.ContainsRune(m.Body, 0) || m.Date < 1 || m.Date > time.Now().Add(24*time.Hour).Unix() {
			return protocol()
		}
	}
	if page.NextOffset != strconv.FormatInt(next, 10) {
		return protocol()
	}
	return nil
}

// ExternalMessageID is a server-owned namespace, separate from Desktop export
// import identities. The durable event table is the authoritative provenance.
func ExternalMessageID(m Message) string {
	return fmt.Sprintf("telegrambot:private:%s:%s", m.ChatID, m.MessageID)
}

package onebot

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/qilinbro/msg-manager/internal/connector"
	"github.com/qilinbro/msg-manager/internal/domain"
)

var ErrUnsupported = errors.New("OneBot event is not a supported incoming friend text message")

// NormalizePrivateText validates a single supplied event; it does not receive,
// acknowledge or persist events. The caller must bind scope to expectedID using
// authenticated connection configuration. ContactID and ConversationID below
// are stable, account-qualified external lookup keys; ingestion must resolve
// them to persisted internal identities. Message.ID remains unset until then.
func NormalizePrivateText(raw []byte, scope domain.ConnectionScope, expectedID string, received time.Time) (connector.Event, error) {
	if err := scope.Validate(); err != nil {
		return connector.Event{}, err
	}
	selfID, ok := positiveID(expectedID)
	if !ok || received.IsZero() {
		return connector.Event{}, failure(InvalidConfig)
	}
	var event struct {
		Time        json.RawMessage `json:"time"`
		SelfID      json.RawMessage `json:"self_id"`
		PostType    string          `json:"post_type"`
		MessageType string          `json:"message_type"`
		SubType     string          `json:"sub_type"`
		MessageID   json.RawMessage `json:"message_id"`
		UserID      json.RawMessage `json:"user_id"`
		Message     json.RawMessage `json:"message"`
		GroupID     json.RawMessage `json:"group_id"`
		Sender      json.RawMessage `json:"sender"`
	}
	if decodeJSON(raw, &event) != nil {
		return connector.Event{}, failure(ProtocolError)
	}
	actualSelf, ok := exactID(event.SelfID, false)
	if !ok {
		return connector.Event{}, failure(ProtocolError)
	}
	if actualSelf != selfID {
		return connector.Event{}, failure(IdentityMismatch)
	}
	if event.PostType != "message" || event.MessageType != "private" || event.SubType != "friend" || len(event.GroupID) > 0 {
		return connector.Event{}, ErrUnsupported
	}
	peerID, validPeer := exactID(event.UserID, false)
	messageID, validMessage := exactID(event.MessageID, true)
	if !validPeer || !validMessage {
		return connector.Event{}, failure(ProtocolError)
	}
	if peerID == selfID {
		return connector.Event{}, ErrUnsupported
	}
	// If a sender object is present, it must agree with the private peer. Never
	// take a conflicting sender or nickname as an identity mapping instruction.
	if len(event.Sender) > 0 {
		var sender struct {
			UserID json.RawMessage `json:"user_id"`
		}
		if decodeJSON(event.Sender, &sender) != nil {
			return connector.Event{}, failure(ProtocolError)
		}
		senderID, valid := exactID(sender.UserID, false)
		if !valid || senderID != peerID {
			return connector.Event{}, failure(ProtocolError)
		}
	}
	// OneBot time is an integer Unix timestamp, not an ID or floating number.
	timestamp, err := strconv.ParseInt(strings.TrimSpace(string(event.Time)), 10, 64)
	if err != nil || timestamp < 1 || timestamp > 253402300799 {
		return connector.Event{}, failure(ProtocolError)
	}
	body, err := privateText(event.Message)
	if err != nil {
		return connector.Event{}, err
	}
	conversation := domain.ConversationScope{
		ConnectionScope: scope,
		ContactID:       "qq:" + selfID + ":" + peerID,
		ConversationID:  "qq:private:" + selfID + ":" + peerID,
	}
	return connector.Event{
		Scope: scope, ExternalEventID: "qq:private:" + selfID + ":" + peerID + ":" + messageID,
		Kind: "message.private.text", ReceivedAt: received.UTC(),
		Message: &domain.Message{
			Scope: conversation, ExternalMessageID: messageID, Version: 1,
			Direction: "inbound", Body: body,
			OccurredAt: time.Unix(timestamp, 0).UTC(), ReceivedAt: received.UTC(),
		},
	}, nil
}

func privateText(raw json.RawMessage) (string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, `"`) {
		var text string
		if json.Unmarshal(raw, &text) != nil {
			return "", failure(ProtocolError)
		}
		if strings.Contains(text, "[CQ:") {
			return "", ErrUnsupported
		}
		// String messages use CQ escaping. Decode escaped literals only once;
		// a literal encoded [CQ:...] is text, not an executable control segment.
		text = strings.NewReplacer("&#91;", "[", "&#93;", "]", "&amp;", "&").Replace(text)
		if text == "" {
			return "", ErrUnsupported
		}
		return text, nil
	}
	if !strings.HasPrefix(trimmed, "[") {
		return "", failure(ProtocolError)
	}
	var segments []struct {
		Type string `json:"type"`
		Data struct {
			Text *string `json:"text"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &segments) != nil {
		return "", failure(ProtocolError)
	}
	var text strings.Builder
	for _, segment := range segments {
		if segment.Type != "text" {
			return "", ErrUnsupported
		}
		if segment.Data.Text == nil {
			return "", failure(ProtocolError)
		}
		text.WriteString(*segment.Data.Text)
	}
	if text.Len() == 0 {
		return "", ErrUnsupported
	}
	return text.String(), nil
}

package onebot

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/qilinbro/msg-manager/internal/store"
)

// HistoryMessage is the minimal private-message shape returned by
// get_friend_msg_history. IDs stay as RawMessage so large integers are never
// decoded through float64.
type HistoryMessage struct {
	MessageID json.RawMessage `json:"message_id"`
	UserID    json.RawMessage `json:"user_id"`
	Time      json.RawMessage `json:"time"`
	Message   json.RawMessage `json:"message"`
	Sender    json.RawMessage `json:"sender"`
}

// FriendHistory performs one bounded read-only call. Nimbus returns a plain
// array ordered newest-to-oldest; count is capped at 150 by the provider.
func (c *Client) FriendHistory(ctx context.Context, userID string, count int) ([]HistoryMessage, error) {
	if c == nil || count < 1 || count > 150 {
		return nil, failure(InvalidConfig)
	}
	peer, ok := positiveID(userID)
	if !ok {
		return nil, failure(InvalidConfig)
	}
	data, err := c.Call(ctx, "get_friend_msg_history", map[string]any{"user_id": peer, "count": count})
	if err != nil {
		return nil, err
	}
	var messages []HistoryMessage
	if err := decodeJSON(data, &messages); err != nil {
		return nil, failure(ProtocolError)
	}
	for _, m := range messages {
		if _, ok := exactID(m.MessageID, true); !ok {
			return nil, failure(ProtocolError)
		}
	}
	return messages, nil
}

// SelfID returns the configured expected account identifier.
func (c *Client) SelfID() string {
	if c == nil {
		return ""
	}
	return c.config.selfID
}

// Normalize converts one provider message into a store import record.
// Only friend-to-self private plain text is accepted; self-sent messages,
// media and unsupported segments are reported with a stable skip reason.
func (c *Client) NormalizeHistory(m HistoryMessage, peerID string, selfID string) (store.ImportMessage, string, error) {
	messageID, ok := exactID(m.MessageID, true)
	if !ok {
		return store.ImportMessage{}, "", failure(ProtocolError)
	}
	canonicalSelf, ok := positiveID(selfID)
	if !ok {
		return store.ImportMessage{}, "", failure(InvalidConfig)
	}
	if peerID == canonicalSelf {
		return store.ImportMessage{}, "", failure(IdentityMismatch)
	}
	actualPeer, ok := exactID(m.UserID, false)
	if !ok || actualPeer != peerID {
		return store.ImportMessage{}, "", failure(IdentityMismatch)
	}
	if len(m.Sender) > 0 {
		var sender struct {
			UserID json.RawMessage `json:"user_id"`
		}
		if err := decodeJSON(m.Sender, &sender); err != nil {
			return store.ImportMessage{}, "", failure(ProtocolError)
		}
		senderID, ok := exactID(sender.UserID, false)
		if !ok || senderID != peerID {
			return store.ImportMessage{}, "", failure(IdentityMismatch)
		}
	}
	timestamp, err := strconv.ParseInt(strings.TrimSpace(string(m.Time)), 10, 64)
	if err != nil || timestamp < 1 || timestamp > 253402300799 {
		return store.ImportMessage{}, "", failure(ProtocolError)
	}
	body, err := privateText(m.Message)
	if err != nil {
		if errorsIsUnsupported(err) {
			return store.ImportMessage{}, "unsupported_text", nil
		}
		return store.ImportMessage{}, "", err
	}
	name := peerID
	if len(m.Sender) > 0 {
		var sender struct {
			Nickname string `json:"nickname"`
		}
		if err := decodeJSON(m.Sender, &sender); err == nil && sender.Nickname != "" {
			name = sender.Nickname
		}
	}
	return store.ImportMessage{
		ExternalID: "onebot:friend:" + peerID + ":" + messageID,
		PeerID:     peerID, PeerName: name, Direction: "incoming",
		Body: body, OccurredAt: time.Unix(timestamp, 0).UTC().Format(time.RFC3339Nano),
	}, "", nil
}

func errorsIsUnsupported(err error) bool { return err == ErrUnsupported }

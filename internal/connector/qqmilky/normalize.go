package qqmilky

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qilinbro/msg-manager/internal/connector/adapter"
)

const maxBodyBytes = 16000

type outgoingSegment struct {
	Type string       `json:"type"`
	Data outgoingText `json:"data"`
}

type outgoingText struct {
	Text string `json:"text"`
}

func parseID(value string) (int64, error) {
	canonical, ok := adapter.ExactID(json.RawMessage(strconv.Quote(value)))
	if !ok {
		return 0, adapter.Failure(adapter.InvalidConfig)
	}
	n, err := strconv.ParseInt(canonical, 10, 64)
	if err != nil || n <= 0 {
		return 0, adapter.Failure(adapter.InvalidConfig)
	}
	return n, nil
}

// Normalize converts a Milky message into a product-neutral record. Non-text
// segments are counted and reported, never uploaded to a model. The first
// version supports friend private chats only.
func (c *Client) Normalize(message IncomingMessage, selfID string) (NormalizedMessage, error) {
	peerID, ok := adapter.ExactID(message.PeerID)
	if !ok {
		return NormalizedMessage{}, adapter.Failure(adapter.ProtocolError)
	}
	seq, ok := adapter.ExactID(message.MessageSeq)
	if !ok {
		return NormalizedMessage{}, adapter.Failure(adapter.ProtocolError)
	}
	senderID, ok := adapter.ExactID(message.SenderID)
	if !ok {
		return NormalizedMessage{}, adapter.Failure(adapter.ProtocolError)
	}
	if message.Time <= 0 {
		return NormalizedMessage{}, adapter.Failure(adapter.ProtocolError)
	}
	if selfID == "" {
		selfID = c.base.SelfID()
	}
	name := peerID
	if message.Friend != nil {
		if message.Friend.Remark != "" {
			name = message.Friend.Remark
		} else if message.Friend.Nickname != "" {
			name = message.Friend.Nickname
		}
	}
	direction := "incoming"
	if senderID == selfID {
		direction = "outgoing"
	}
	result := NormalizedMessage{
		ExternalID: "milky:friend:" + peerID + ":" + seq,
		PeerID:     peerID, PeerName: name, Direction: direction,
		OccurredAt: time.Unix(message.Time, 0).UTC().Format(time.RFC3339Nano),
		MessageSeq: seq, Scene: message.Scene,
	}
	if message.Scene != "friend" {
		result.SkipReason = "unsupported_scene"
		result.Unsupported = append(result.Unsupported, message.Scene)
		return result, nil
	}
	var text strings.Builder
	for _, segment := range message.Segments {
		if segment.Type == "text" {
			var data struct {
				Text string `json:"text"`
			}
			if err := adapter.DecodeJSON(segment.Data, &data); err != nil {
				return NormalizedMessage{}, err
			}
			text.WriteString(data.Text)
			continue
		}
		result.Unsupported = append(result.Unsupported, segment.Type)
	}
	body := text.String()
	if !utf8.ValidString(body) || len(body) > maxBodyBytes {
		return NormalizedMessage{}, adapter.Failure(adapter.ProtocolError)
	}
	result.Body = body
	if body == "" {
		result.SkipReason = "non_text_only"
	}
	return result, nil
}

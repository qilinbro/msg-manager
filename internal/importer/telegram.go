// Package importer converts user-selected offline exports into the common
// message format. It does not log in, retrieve history, or read attachments.
package importer

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrInvalidExport = errors.New("invalid or unsupported Telegram private chat export")
	ErrLimitExceeded = errors.New("Telegram export exceeds the supported import limits")
)

const (
	maxExportBytes = 8 << 20
	maxMessages    = 1000
	maxBodyBytes   = 16000
)

type Message struct {
	ExternalID string `json:"external_id"`
	PeerID     string `json:"peer_id"`
	PeerName   string `json:"peer_name"`
	Direction  string `json:"direction"`
	Body       string `json:"body"`
	OccurredAt string `json:"occurred_at"`
}

type Result struct {
	Messages []Message `json:"messages"`
	Skipped  int       `json:"skipped"`
}

// TelegramPrivate accepts one Telegram Desktop personal_chat result.json.
// selfID must be supplied by the operator; identity is never inferred from
// profile, contact, hidden entity, or attachment data in the export.
func TelegramPrivate(data []byte, selfID string) (Result, error) {
	if len(data) > maxExportBytes {
		return Result{}, ErrLimitExceeded
	}
	selfID, ok := positiveID(selfID)
	if !ok || !utf8.Valid(data) || validateJSON(data) != nil {
		return Result{}, ErrInvalidExport
	}
	var chat struct {
		ID       json.RawMessage   `json:"id"`
		Name     string            `json:"name"`
		Type     string            `json:"type"`
		Messages []json.RawMessage `json:"messages"`
	}
	if json.Unmarshal(data, &chat) != nil || chat.Type != "personal_chat" || chat.Messages == nil {
		return Result{}, ErrInvalidExport
	}
	peerID, ok := rawID(chat.ID)
	if !ok || peerID == selfID {
		return Result{}, ErrInvalidExport
	}
	if len(chat.Messages) > maxMessages || len(chat.Name) > 512 {
		return Result{}, ErrLimitExceeded
	}
	result := Result{Messages: make([]Message, 0, len(chat.Messages))}
	for _, raw := range chat.Messages {
		var item map[string]json.RawMessage
		if json.Unmarshal(raw, &item) != nil || item == nil {
			return Result{}, ErrInvalidExport
		}
		var kind string
		if json.Unmarshal(item["type"], &kind) != nil {
			return Result{}, ErrInvalidExport
		}
		if kind != "message" || hasMedia(item) {
			result.Skipped++
			continue
		}
		var sender string
		if json.Unmarshal(item["from_id"], &sender) != nil {
			result.Skipped++
			continue
		}
		direction := "incoming"
		if sender == "user"+selfID {
			direction = "outgoing"
		} else if sender != "user"+peerID {
			result.Skipped++
			continue
		}
		messageID, ok := rawID(item["id"])
		if !ok {
			return Result{}, ErrInvalidExport
		}
		externalID := peerID + ":" + messageID
		if len(externalID) > 128 {
			return Result{}, ErrLimitExceeded
		}
		body, err := plainText(item["text"])
		if err != nil {
			return Result{}, err
		}
		if strings.TrimSpace(body) == "" {
			result.Skipped++
			continue
		}
		stamp, err := unixTime(item["date_unixtime"])
		if err != nil {
			return Result{}, err
		}
		result.Messages = append(result.Messages, Message{
			ExternalID: externalID, PeerID: peerID, PeerName: chat.Name,
			Direction: direction, Body: body, OccurredAt: stamp,
		})
	}
	return result, nil
}

func positiveID(s string) (string, bool) {
	if len(s) == 0 || len(s) > 128 {
		return "", false
	}
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return "", false
		}
	}
	s = strings.TrimLeft(s, "0")
	return s, s != ""
}

func rawID(raw json.RawMessage) (string, bool) {
	var quoted string
	if json.Unmarshal(raw, &quoted) == nil {
		return positiveID(quoted)
	}
	return positiveID(string(raw))
}

func unixTime(raw json.RawMessage) (string, error) {
	value, ok := rawID(raw)
	if !ok {
		return "", ErrInvalidExport
	}
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || seconds < 946684800 || seconds >= 4133980800 {
		return "", ErrInvalidExport
	}
	return time.Unix(seconds, 0).UTC().Format(time.RFC3339), nil
}

func hasMedia(item map[string]json.RawMessage) bool {
	for _, field := range []string{
		"photo", "file", "media_type", "poll", "contact_information",
		"location_information", "venue_information", "game_information",
		"invoice_information", "document", "rich_message",
	} {
		if _, present := item[field]; present {
			return true
		}
	}
	return false
}

func plainText(raw json.RawMessage) (string, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		if len(text) > maxBodyBytes {
			return "", ErrLimitExceeded
		}
		return text, nil
	}
	var parts []json.RawMessage
	if json.Unmarshal(raw, &parts) != nil || parts == nil {
		return "", ErrInvalidExport
	}
	var body strings.Builder
	for _, part := range parts {
		var text string
		if json.Unmarshal(part, &text) != nil {
			var entity struct {
				Type string  `json:"type"`
				Text *string `json:"text"`
			}
			if json.Unmarshal(part, &entity) != nil || entity.Type == "" || entity.Text == nil {
				return "", ErrInvalidExport
			}
			text = *entity.Text
		}
		if body.Len()+len(text) > maxBodyBytes {
			return "", ErrLimitExceeded
		}
		body.WriteString(text)
	}
	return body.String(), nil
}

// Validate before typed decoding because encoding/json otherwise accepts
// duplicate keys by silently retaining the last value. Depth is also bounded.
func validateJSON(data []byte) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := jsonValue(d, 0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrInvalidExport
	}
	return nil
}

func jsonValue(d *json.Decoder, depth int) error {
	if depth > 32 {
		return ErrInvalidExport
	}
	token, err := d.Token()
	if err != nil {
		return ErrInvalidExport
	}
	delimiter, container := token.(json.Delim)
	if !container {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			keyToken, err := d.Token()
			key, ok := keyToken.(string)
			if err != nil || !ok || seen[key] {
				return ErrInvalidExport
			}
			seen[key] = true
			if err := jsonValue(d, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := jsonValue(d, depth+1); err != nil {
				return err
			}
		}
	default:
		return ErrInvalidExport
	}
	if _, err := d.Token(); err != nil {
		return ErrInvalidExport
	}
	return nil
}

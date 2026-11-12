package store

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const storedTimeLayout = "2006-01-02T15:04:05.000000000Z"

func stableID(parts ...string) string {
	raw, _ := json.Marshal(append([]string{workspaceID}, parts...))
	sum := sha256.Sum256(raw)
	sum[6] = (sum[6] & 0x0f) | 0x80
	sum[8] = (sum[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

func validResourceID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, ch := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if ch != '-' {
				return false
			}
		} else if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
			return false
		}
	}
	return true
}

func validPlatform(value string) bool {
	return value == "qq" || value == "weixin" || value == "telegram" || value == "shanliao" || value == "email"
}
func validLabel(value string, required bool) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > 128 || strings.TrimSpace(value) != value || required && value == "" {
		return false
	}
	for _, ch := range value {
		if unicode.IsControl(ch) {
			return false
		}
	}
	return true
}

func normalizeImport(req ImportRequest) (ImportRequest, error) {
	if !validPlatform(req.Platform) || !validLabel(req.AccountID, true) || !validLabel(req.AccountName, false) || len(req.Messages) < 1 || len(req.Messages) > 1000 {
		return ImportRequest{}, ErrInvalidInput
	}
	result := req
	result.Messages = append([]ImportMessage(nil), req.Messages...)
	if result.AccountName == "" {
		result.AccountName = result.AccountID
	}
	for i := range result.Messages {
		m := &result.Messages[i]
		if !validLabel(m.ExternalID, true) || !validLabel(m.PeerID, true) || !validLabel(m.PeerName, false) || (m.Direction != "incoming" && m.Direction != "outgoing") || len(m.Body) < 1 || len(m.Body) > 16000 || !utf8.ValidString(m.Body) || strings.ContainsRune(m.Body, 0) {
			return ImportRequest{}, ErrInvalidInput
		}
		at, err := time.Parse(time.RFC3339Nano, m.OccurredAt)
		if err != nil || at.Before(time.Unix(0, 0)) || at.After(time.Now().Add(24*time.Hour)) {
			return ImportRequest{}, ErrInvalidInput
		}
		m.OccurredAt = at.UTC().Format(storedTimeLayout)
		if m.PeerName == "" {
			m.PeerName = m.PeerID
		}
		if m.Email != nil {
			if req.Platform != "email" || !validEmailHeaders(m.Email) {
				return ImportRequest{}, ErrInvalidInput
			}
		}
	}
	return result, nil
}

func validEmailHeaders(e *EmailHeaders) bool {
	if !utf8.ValidString(e.Subject) || len(e.Subject) > 4096 || strings.ContainsAny(e.Subject, "\x00\r\n") || !validLabel(e.FromName, false) || !validLabel(e.Folder, true) || len(e.To) > 100 {
		return false
	}
	for _, address := range append([]string{e.FromAddress}, e.To...) {
		parsed, err := mail.ParseAddress(address)
		if err != nil || parsed.Address != address || len(address) > 320 || strings.ContainsAny(address, "\r\n\x00") {
			return false
		}
	}
	return true
}

func normalizedLimit(limit int) (int, error) {
	if limit == 0 {
		return 50, nil
	}
	if limit < 1 || limit > 100 {
		return 0, ErrInvalidInput
	}
	return limit, nil
}

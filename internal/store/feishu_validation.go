package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

func validFeishuLedgerID(value, prefix string) bool {
	if len(value) != len(prefix)+32 || !strings.HasPrefix(value, prefix) {
		return false
	}
	for _, c := range value[len(prefix):] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// FeishuSafeText is shared by selection, rendering and Store provenance checks.
// These are plain-text quotes, never HTML/mentions or model-authored tasks.
func FeishuSafeText(value string) string {
	value = strings.ToValidUTF8(value, "")
	var out strings.Builder
	for _, r := range value {
		switch r {
		case '<':
			out.WriteRune('＜')
		case '>':
			out.WriteRune('＞')
		case '@':
			out.WriteRune('＠')
		case '&':
			out.WriteRune('＆')
		case '\r':
			out.WriteRune(' ')
		default:
			if !unicode.IsControl(r) || r == '\n' || r == '\t' {
				out.WriteRune(r)
			}
		}
	}
	return strings.TrimSpace(out.String())
}
func FeishuBoundText(value string, maxBytes int) (string, bool) {
	safe := FeishuSafeText(value)
	if maxBytes < 0 {
		maxBytes = 0
	}
	if len(safe) <= maxBytes {
		return safe, false
	}
	for maxBytes > 0 && !utf8.RuneStart(safe[maxBytes]) {
		maxBytes--
	}
	return safe[:maxBytes], true
}
func FeishuQuote(value string, maxRunes int) (string, bool) {
	safe := FeishuSafeText(value)
	if maxRunes < 0 {
		maxRunes = 0
	}
	runes := []rune(safe)
	if len(runes) <= maxRunes {
		return safe, false
	}
	return string(runes[:maxRunes]) + "…", true
}
func FeishuTaskCandidate(body string) bool {
	lower := strings.ToLower(body)
	for _, marker := range []string{"请", "麻烦", "需要", "待办", "截止", "确认", "完成", "?", "？", "please", "todo", "deadline", "confirm"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
func feishuErrorCodeValid(value string) bool {
	if len(value) > 100 {
		return false
	}
	for _, r := range value {
		if !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	return true
}
func feishuCanonicalTime(value *string) (time.Time, bool) {
	if value == nil {
		return time.Time{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, *value)
	return at, err == nil && !at.IsZero() && *value == FeishuTime(at)
}

// validateFeishuEvidenceTx verifies every nested reference against the immutable
// claimed conversations and exact source window. It also proves each todo is a
// deterministic quote of an incoming explicit task marker, not Analyze.Draft.
func validateFeishuEvidenceTx(ctx context.Context, tx *sql.Tx, r, current FeishuReport) error {
	members := map[string]bool{}
	for _, ref := range current.SourceRefs {
		members[ref.ConversationID] = true
	}
	start, _ := time.Parse(time.RFC3339Nano, current.WindowStart)
	end, _ := time.Parse(time.RFC3339Nano, current.WindowEnd)
	type evidence struct{ conversationID, body, direction, occurredAt string }
	cache := map[string]evidence{}
	load := func(id string) (evidence, error) {
		if !validResourceID(id) {
			return evidence{}, ErrInvalidInput
		}
		if value, ok := cache[id]; ok {
			return value, nil
		}
		var value evidence
		err := tx.QueryRowContext(ctx, `SELECT conversation_id,body,direction,occurred_at FROM messages WHERE workspace_id=? AND id=? AND occurred_at>=? AND occurred_at<?`, workspaceID, id, start.UTC().Format(storedTimeLayout), end.UTC().Format(storedTimeLayout)).Scan(&value.conversationID, &value.body, &value.direction, &value.occurredAt)
		if errors.Is(err, sql.ErrNoRows) {
			return evidence{}, ErrFeishuSourceChanged
		}
		if err != nil {
			return evidence{}, wechatMonitorDBError(ctx)
		}
		if !members[value.conversationID] {
			return evidence{}, ErrInvalidInput
		}
		cache[id] = value
		return value, nil
	}
	seenTodos := map[string]bool{}
	for _, todo := range r.Todos {
		if !todo.NeedsReview || len(todo.EvidenceIDs) != 1 || !feishuText(todo.Text, 500, false) {
			return ErrInvalidInput
		}
		id := todo.EvidenceIDs[0]
		if seenTodos[id] {
			return ErrInvalidInput
		}
		seenTodos[id] = true
		source, err := load(id)
		if err != nil {
			return err
		}
		body, _ := FeishuBoundText(source.body, 600)
		quote, _ := FeishuQuote(body, 100)
		if source.direction != "incoming" || !FeishuTaskCandidate(body) || todo.Text != "待人工核对："+quote {
			return ErrInvalidInput
		}
	}
	seenExcerpts := map[string]bool{}
	for _, excerpt := range r.Excerpts {
		if !validResourceID(excerpt.ConversationID) || !feishuText(excerpt.Text, 500, false) || seenExcerpts[excerpt.MessageID] {
			return ErrInvalidInput
		}
		seenExcerpts[excerpt.MessageID] = true
		source, err := load(excerpt.MessageID)
		if err != nil {
			return err
		}
		quote, _ := FeishuQuote(source.body, 120)
		if excerpt.ConversationID != source.conversationID || excerpt.OccurredAt != source.occurredAt || excerpt.Text != quote {
			return ErrInvalidInput
		}
	}
	config, err := loadFeishuConfigTx(ctx, tx)
	if err != nil {
		return err
	}
	texts := []string{r.Summary, r.Text}
	for _, todo := range r.Todos {
		texts = append(texts, todo.Text)
	}
	for _, excerpt := range r.Excerpts {
		texts = append(texts, excerpt.Text)
	}
	for _, text := range texts {
		for _, secret := range []string{config.AppSecret, config.RecipientOpenID} {
			if secret != "" && strings.Contains(text, secret) {
				return ErrInvalidInput
			}
		}
	}
	return nil
}

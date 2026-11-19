package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/qilinbro/msg-manager/internal/connector/telegrambot"
)

var ErrTelegramCursorChanged = errors.New("telegram bot cursor changed")

// Telegram keeps pending updates for at most 24 hours and may randomize IDs
// after a week without new updates. Never submit an old acknowledgement offset
// after a 24-hour gap in nonempty committed pages: read the remaining queue from
// offset 0 in a new local epoch instead. Empty polls do not renew this lease.
const telegramBotCursorLease = 24 * time.Hour

// TelegramBotCursor is internal receive state, never an HTTP request/response.
// Revision fences ABA even if a later epoch reaches the same numeric offset.
type TelegramBotCursor struct {
	NextOffset  string
	Revision    int64
	Epoch       int64
	LastEventAt string
	UpdatedAt   string
}

func (c TelegramBotCursor) PollOffset(now time.Time) string {
	if c.NextOffset == "0" {
		return "0"
	}
	last, err := time.Parse(time.RFC3339Nano, c.LastEventAt)
	if err != nil || now.Before(last) || now.Sub(last) >= telegramBotCursorLease {
		return "0"
	}
	return c.NextOffset
}

type TelegramBotCommitResult struct {
	Events        int    `json:"events"`
	Imported      int    `json:"imported"`
	Duplicates    int    `json:"duplicates"`
	Unsupported   int    `json:"unsupported"`
	NotAuthorized int    `json:"not_authorized"`
	Conversations int    `json:"conversations"`
	NextOffset    string `json:"next_offset"`
	Partial       bool   `json:"partial"`
	SourceKind    string `json:"source_kind"`
	AIQueued      int    `json:"ai_queued"`
}

func telegramStoreError(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return ErrUnavailable
}

func (s *Store) LoadTelegramBotCursor(ctx context.Context, botID string) (TelegramBotCursor, error) {
	if ctx == nil || !telegrambot.ValidID(botID) {
		return TelegramBotCursor{}, ErrInvalidInput
	}
	if err := s.lockContext(ctx); err != nil {
		return TelegramBotCursor{}, err
	}
	defer s.mu.Unlock()
	out := TelegramBotCursor{NextOffset: "0"}
	err := s.db.QueryRowContext(ctx, `SELECT next_offset,revision,epoch,last_event_at,updated_at FROM telegram_bot_cursors WHERE workspace_id=? AND bot_id=?`, workspaceID, botID).Scan(&out.NextOffset, &out.Revision, &out.Epoch, &out.LastEventAt, &out.UpdatedAt)
	if err == sql.ErrNoRows {
		return TelegramBotCursor{NextOffset: "0"}, nil
	}
	if err != nil {
		return TelegramBotCursor{}, telegramStoreError(ctx)
	}
	if !telegrambot.ValidOffset(out.NextOffset) || out.Revision < 0 || out.Epoch < 0 {
		return TelegramBotCursor{}, ErrUnavailable
	}
	return out, nil
}

// CommitTelegramBot is deliberately narrower than ingestion.Store.CommitBatch.
// It atomically commits a validated single page's messages, minimal unsupported
// reasons, provenance, and acknowledgement watermark, without creating AI jobs.
// A stale fetch must be repeated by a new manual action, not partially applied.
// requestOffset is exactly the value used for the single platform read; an old
// cursor may be reset only under the durable lease, never by a browser/model.
func (s *Store) CommitTelegramBot(ctx context.Context, botID string, allowed []string, expected TelegramBotCursor, requestOffset string, page telegrambot.Page) (TelegramBotCommitResult, error) {
	zero := TelegramBotCommitResult{}
	if ctx == nil || !telegrambot.ValidOffset(expected.NextOffset) || expected.Revision < 0 || expected.Epoch < 0 || telegrambot.ValidatePage(page, botID, allowed, requestOffset, telegrambot.MaxLimit) != nil {
		return zero, ErrInvalidInput
	}
	if err := s.lockContext(ctx); err != nil {
		return zero, err
	}
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return zero, telegramStoreError(ctx)
	}
	defer tx.Rollback()
	current := TelegramBotCursor{NextOffset: "0"}
	err = tx.QueryRowContext(ctx, `SELECT next_offset,revision,epoch,last_event_at,updated_at FROM telegram_bot_cursors WHERE workspace_id=? AND bot_id=?`, workspaceID, botID).Scan(&current.NextOffset, &current.Revision, &current.Epoch, &current.LastEventAt, &current.UpdatedAt)
	if err != nil && err != sql.ErrNoRows {
		return zero, telegramStoreError(ctx)
	}
	nowTime := time.Now().UTC()
	// Exact snapshot + revision prevents a late page from an earlier epoch
	// committing after reset, including when numeric offsets repeat (ABA).
	// A lease crossing during fetch fails closed; there is no automatic reread.
	if current != expected || requestOffset != current.PollOffset(nowTime) {
		return zero, ErrTelegramCursorChanged
	}
	epoch := current.Epoch
	reset := requestOffset != current.NextOffset
	if current.Revision == math.MaxInt64 || reset && epoch == math.MaxInt64 {
		return zero, ErrUnavailable
	}
	lastEventAt := current.LastEventAt
	if reset {
		epoch++
		lastEventAt = ""
	}
	now := nowTime.Format(time.RFC3339Nano)
	if len(page.Events) > 0 {
		lastEventAt = now
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO telegram_bot_cursors(workspace_id,bot_id,next_offset) VALUES(?,?,?) ON CONFLICT(workspace_id,bot_id) DO NOTHING`, workspaceID, botID, current.NextOffset); err != nil {
		return zero, telegramStoreError(ctx)
	}
	result := TelegramBotCommitResult{Events: len(page.Events), NextOffset: page.NextOffset, SourceKind: "telegram_bot"}
	touched := map[string]bool{}
	changed := false
	for _, event := range page.Events {
		// Unsupported events have no raw object or body here. Their fingerprint
		// covers only the allowlisted event type and stable sanitized reason.
		raw, _ := json.Marshal(event)
		sum := sha256.Sum256(raw)
		digest := hex.EncodeToString(sum[:])
		var existing string
		err = tx.QueryRowContext(ctx, `SELECT event_digest FROM telegram_bot_events WHERE workspace_id=? AND bot_id=? AND epoch=? AND update_id=?`, workspaceID, botID, epoch, event.UpdateID).Scan(&existing)
		if err == nil {
			if existing != digest {
				return zero, ErrConflict
			}
			result.Duplicates++
			continue
		}
		if err != sql.ErrNoRows {
			return zero, telegramStoreError(ctx)
		}
		var messageID any
		if event.Message != nil {
			id, conversationID, inserted, provenanceAdded, err := saveTelegramBotMessage(ctx, tx, botID, *event.Message)
			if err != nil {
				return zero, err
			}
			messageID = id
			touched[conversationID] = true
			if inserted {
				result.Imported++
			} else {
				result.Duplicates++
			}
			changed = changed || inserted || provenanceAdded
		} else if event.Reason == "not_authorized" || event.Reason == "not_private" {
			result.NotAuthorized++
		} else {
			result.Unsupported++
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO telegram_bot_events(workspace_id,bot_id,epoch,update_id,event_type,reason,message_id,event_digest,received_at) VALUES(?,?,?,?,?,?,?,?,?)`, workspaceID, botID, epoch, event.UpdateID, event.Type, event.Reason, messageID, digest, now); err != nil {
			return zero, telegramStoreError(ctx)
		}
	}
	update, err := tx.ExecContext(ctx, `UPDATE telegram_bot_cursors SET next_offset=?,revision=revision+1,epoch=?,last_event_at=?,updated_at=? WHERE workspace_id=? AND bot_id=? AND next_offset=? AND revision=? AND epoch=?`, page.NextOffset, epoch, lastEventAt, now, workspaceID, botID, expected.NextOffset, expected.Revision, expected.Epoch)
	if err != nil {
		return zero, telegramStoreError(ctx)
	}
	rows, err := update.RowsAffected()
	if err != nil || rows != 1 {
		return zero, ErrTelegramCursorChanged
	}
	if changed {
		if _, err = tx.ExecContext(ctx, `UPDATE workspaces SET revision=revision+1 WHERE id=?`, workspaceID); err != nil {
			return zero, telegramStoreError(ctx)
		}
	}
	if err = ctx.Err(); err != nil {
		return zero, err
	}
	if err = tx.Commit(); err != nil {
		return zero, telegramStoreError(ctx)
	}
	result.Conversations = len(touched)
	result.Partial = result.Unsupported > 0 || result.NotAuthorized > 0
	return result, nil
}

func saveTelegramBotMessage(ctx context.Context, tx *sql.Tx, botID string, m telegrambot.Message) (messageID, conversationID string, inserted, provenanceAdded bool, err error) {
	fail := func(e error) (string, string, bool, bool, error) { return "", "", false, false, e }
	connectionID := stableID("connection", "telegram", "bot:"+botID)
	contactID := stableID("contact", connectionID, m.ChatID)
	conversationID = stableID("conversation", connectionID, m.ChatID)
	externalID := telegrambot.ExternalMessageID(m)
	messageID = stableID("message", conversationID, externalID)
	// Legacy source CHECKs remain untouched. telegram_bot_messages is the
	// server-only authority used by read models to expose telegram_bot source.
	if _, err = tx.ExecContext(ctx, `INSERT INTO connections(id,workspace_id,platform,account_id,display_name,source) VALUES(?,?,'telegram',?,?,'import') ON CONFLICT(id) DO NOTHING`, connectionID, workspaceID, "bot:"+botID, "Telegram Bot "+botID); err != nil {
		return fail(telegramStoreError(ctx))
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO contacts(id,workspace_id,connection_id,peer_id,display_name) VALUES(?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, contactID, workspaceID, connectionID, m.ChatID, "Telegram 私聊 "+m.ChatID); err != nil {
		return fail(telegramStoreError(ctx))
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO conversations(id,workspace_id,connection_id,contact_id,peer_id,title,conversation_kind) VALUES(?,?,?,?,?,?,'private') ON CONFLICT(id) DO NOTHING`, conversationID, workspaceID, connectionID, contactID, m.ChatID, "Telegram 私聊 "+m.ChatID); err != nil {
		return fail(telegramStoreError(ctx))
	}
	var kind string
	if err = tx.QueryRowContext(ctx, `SELECT conversation_kind FROM conversations WHERE workspace_id=? AND id=?`, workspaceID, conversationID).Scan(&kind); err != nil {
		return fail(telegramStoreError(ctx))
	}
	if kind != "private" {
		return fail(ErrConflict)
	}
	at := time.Unix(m.Date, 0).UTC().Format("2006-01-02T15:04:05.000000000Z")
	var body, direction, occurredAt string
	var email sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT body,direction,occurred_at,email_json FROM messages WHERE workspace_id=? AND conversation_id=? AND external_id=?`, workspaceID, conversationID, externalID).Scan(&body, &direction, &occurredAt, &email)
	if err == nil {
		if body != m.Body || direction != "incoming" || occurredAt != at || email.Valid {
			return fail(ErrConflict)
		}
	} else if err == sql.ErrNoRows {
		if _, err = tx.ExecContext(ctx, `INSERT INTO messages(id,workspace_id,conversation_id,external_id,direction,body,occurred_at,is_read,source) VALUES(?,?,?,?,'incoming',?,?,0,'import')`, messageID, workspaceID, conversationID, externalID, m.Body, at); err != nil {
			return fail(telegramStoreError(ctx))
		}
		inserted = true
	} else {
		return fail(telegramStoreError(ctx))
	}
	var existingConnection, existingBot, existingChat, existingExternal string
	err = tx.QueryRowContext(ctx, `SELECT connection_id,bot_id,chat_id,external_message_id FROM telegram_bot_messages WHERE message_id=? AND workspace_id=?`, messageID, workspaceID).Scan(&existingConnection, &existingBot, &existingChat, &existingExternal)
	if err == nil {
		if existingConnection != connectionID || existingBot != botID || existingChat != m.ChatID || existingExternal != m.MessageID {
			return fail(ErrConflict)
		}
	} else if err == sql.ErrNoRows {
		if _, err = tx.ExecContext(ctx, `INSERT INTO telegram_bot_messages(message_id,workspace_id,connection_id,bot_id,chat_id,external_message_id) VALUES(?,?,?,?,?,?)`, messageID, workspaceID, connectionID, botID, m.ChatID, m.MessageID); err != nil {
			return fail(telegramStoreError(ctx))
		}
		provenanceAdded = true
	} else {
		return fail(telegramStoreError(ctx))
	}
	return messageID, conversationID, inserted, provenanceAdded, nil
}

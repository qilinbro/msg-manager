package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"
)

type pageCursor struct {
	Version  int    `json:"v"`
	Revision int64  `json:"r"`
	Filter   string `json:"f"`
	Time     string `json:"t"`
	ID       string `json:"id,omitempty"`
	Pinned   int    `json:"p,omitempty"`
	Seq      int64  `json:"s,omitempty"`
}

func fingerprint(parts ...any) string {
	raw, _ := json.Marshal(parts)
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}
func encodeCursor(cursor pageCursor) string {
	raw, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(raw)
}
func decodeCursor(value, filter string, revision int64) (pageCursor, error) {
	if len(value) > 2048 {
		return pageCursor{}, ErrInvalidCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return pageCursor{}, ErrInvalidCursor
	}
	var cursor pageCursor
	if json.Unmarshal(raw, &cursor) != nil || cursor.Version != 1 || cursor.Filter != filter || cursor.Revision != revision {
		return pageCursor{}, ErrInvalidCursor
	}
	if _, err := time.Parse(storedTimeLayout, cursor.Time); err != nil {
		return pageCursor{}, ErrInvalidCursor
	}
	return cursor, nil
}

func beginRead(s *Store) (*sql.Tx, int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, 0, ErrUnavailable
	}
	var revision int64
	if tx.QueryRow(`SELECT revision FROM workspaces WHERE id=?`, workspaceID).Scan(&revision) != nil {
		_ = tx.Rollback()
		return nil, 0, ErrUnavailable
	}
	return tx, revision, nil
}

func (s *Store) Conversations(filter Filter) (ConversationPage, error) {
	limit, err := normalizedLimit(filter.Limit)
	if err != nil {
		return ConversationPage{}, err
	}
	if filter.Platform != "" && !validPlatform(filter.Platform) || !utf8.ValidString(filter.Q) || len(filter.Q) > 512 || strings.ContainsRune(filter.Q, 0) {
		return ConversationPage{}, ErrInvalidInput
	}
	filter.Q = strings.TrimSpace(filter.Q)
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, revision, err := beginRead(s)
	if err != nil {
		return ConversationPage{}, err
	}
	defer tx.Rollback()
	query := `SELECT v.id,v.connection_id,c.platform,c.account_id,c.display_name,v.peer_id,v.title,
 (SELECT m.body FROM messages m WHERE m.conversation_id=v.id AND m.workspace_id=v.workspace_id ORDER BY m.occurred_at DESC,m.seq DESC LIMIT 1) AS last_message,
 (SELECT m.occurred_at FROM messages m WHERE m.conversation_id=v.id AND m.workspace_id=v.workspace_id ORDER BY m.occurred_at DESC,m.seq DESC LIMIT 1) AS last_message_at,
 (SELECT count(*) FROM messages m WHERE m.conversation_id=v.id AND m.workspace_id=v.workspace_id AND m.direction='incoming' AND m.is_read=0) AS unread_count,
 v.pinned,v.archived FROM conversations v JOIN connections c ON c.id=v.connection_id AND c.workspace_id=v.workspace_id WHERE v.workspace_id=? AND v.archived=?`
	args := []any{workspaceID, filter.Archived}
	if filter.Platform != "" {
		query += ` AND c.platform=?`
		args = append(args, filter.Platform)
	}
	if filter.Unread {
		query += ` AND EXISTS (SELECT 1 FROM messages m WHERE m.conversation_id=v.id AND m.workspace_id=v.workspace_id AND m.direction='incoming' AND m.is_read=0)`
	}
	if filter.Pinned {
		query += ` AND v.pinned=1`
	}
	if filter.Q != "" {
		// Literal substring matching supports Chinese short terms and treats SQL
		// wildcard/FTS syntax as text. Scope/platform/archive predicates remain
		// mandatory, including in the correlated message evidence lookup.
		query += ` AND (instr(lower(v.title),lower(?))>0 OR instr(lower(v.peer_id),lower(?))>0 OR instr(lower(c.account_id),lower(?))>0 OR instr(lower(c.display_name),lower(?))>0 OR EXISTS(SELECT 1 FROM messages m WHERE m.conversation_id=v.id AND m.workspace_id=v.workspace_id AND (instr(lower(m.body),lower(?))>0 OR instr(lower(coalesce(m.email_json,'')),lower(?))>0)))`
		args = append(args, filter.Q, filter.Q, filter.Q, filter.Q, filter.Q, filter.Q)
	}
	fp := fingerprint("conversations", workspaceID, filter.Q, filter.Platform, filter.Unread, filter.Archived, filter.Pinned)
	query = `SELECT * FROM (` + query + `) result WHERE last_message_at IS NOT NULL`
	if filter.Cursor != "" {
		cursor, err := decodeCursor(filter.Cursor, fp, revision)
		if err != nil {
			return ConversationPage{}, err
		}
		if !validResourceID(cursor.ID) || cursor.Pinned < 0 || cursor.Pinned > 1 {
			return ConversationPage{}, ErrInvalidCursor
		}
		query += ` AND (pinned<? OR (pinned=? AND (last_message_at<? OR (last_message_at=? AND id<?))))`
		args = append(args, cursor.Pinned, cursor.Pinned, cursor.Time, cursor.Time, cursor.ID)
	}
	query += ` ORDER BY pinned DESC,last_message_at DESC,id DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := tx.Query(query, args...)
	if err != nil {
		return ConversationPage{}, ErrUnavailable
	}
	items := []Conversation{}
	for rows.Next() {
		var item Conversation
		if rows.Scan(&item.ID, &item.ConnectionID, &item.Platform, &item.AccountID, &item.AccountName, &item.PeerID, &item.Title, &item.LastMessage, &item.LastMessageAt, &item.UnreadCount, &item.Pinned, &item.Archived) != nil {
			_ = rows.Close()
			return ConversationPage{}, ErrUnavailable
		}
		items = append(items, item)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return ConversationPage{}, ErrUnavailable
	}
	page := ConversationPage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[limit-1]
		pin := 0
		if last.Pinned {
			pin = 1
		}
		value := encodeCursor(pageCursor{Version: 1, Revision: revision, Filter: fp, Time: last.LastMessageAt, ID: last.ID, Pinned: pin})
		page.NextCursor = &value
	}
	if tx.Commit() != nil {
		return ConversationPage{}, ErrUnavailable
	}
	return page, nil
}

func (s *Store) Messages(conversationID, cursor string, limit int) (MessagePage, error) {
	if !validResourceID(conversationID) {
		return MessagePage{}, ErrInvalidInput
	}
	limit, err := normalizedLimit(limit)
	if err != nil {
		return MessagePage{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, revision, err := beginRead(s)
	if err != nil {
		return MessagePage{}, err
	}
	defer tx.Rollback()
	var exists int
	err = tx.QueryRow(`SELECT 1 FROM conversations WHERE workspace_id=? AND id=?`, workspaceID, conversationID).Scan(&exists)
	if err == sql.ErrNoRows {
		return MessagePage{}, ErrNotFound
	}
	if err != nil {
		return MessagePage{}, ErrUnavailable
	}
	query := `SELECT id,conversation_id,external_id,direction,body,occurred_at,is_read,seq,source,email_json FROM messages WHERE workspace_id=? AND conversation_id=?`
	args := []any{workspaceID, conversationID}
	fp := fingerprint("messages", workspaceID, conversationID)
	if cursor != "" {
		position, err := decodeCursor(cursor, fp, revision)
		if err != nil {
			return MessagePage{}, err
		}
		if position.Seq < 1 {
			return MessagePage{}, ErrInvalidCursor
		}
		query += ` AND (occurred_at<? OR (occurred_at=? AND seq<?))`
		args = append(args, position.Time, position.Time, position.Seq)
	}
	query += ` ORDER BY occurred_at DESC,seq DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := tx.Query(query, args...)
	if err != nil {
		return MessagePage{}, ErrUnavailable
	}
	items := []Message{}
	sequences := []int64{}
	for rows.Next() {
		var item Message
		var seq int64
		var emailJSON sql.NullString
		if rows.Scan(&item.ID, &item.ConversationID, &item.ExternalID, &item.Direction, &item.Body, &item.OccurredAt, &item.Read, &seq, &item.Source, &emailJSON) != nil {
			_ = rows.Close()
			return MessagePage{}, ErrUnavailable
		}
		if emailJSON.Valid {
			var email EmailHeaders
			if json.Unmarshal([]byte(emailJSON.String), &email) != nil {
				_ = rows.Close()
				return MessagePage{}, ErrUnavailable
			}
			item.Email = &email
		}
		items = append(items, item)
		sequences = append(sequences, seq)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return MessagePage{}, ErrUnavailable
	}
	page := MessagePage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		value := encodeCursor(pageCursor{Version: 1, Revision: revision, Filter: fp, Time: page.Items[limit-1].OccurredAt, Seq: sequences[limit-1]})
		page.NextCursor = &value
	}
	if tx.Commit() != nil {
		return MessagePage{}, ErrUnavailable
	}
	// SQL selects the newest page first and derives its older-page cursor from
	// the descending boundary. Render each returned page in reading order.
	for left, right := 0, len(page.Items)-1; left < right; left, right = left+1, right-1 {
		page.Items[left], page.Items[right] = page.Items[right], page.Items[left]
	}
	return page, nil
}

func (s *Store) UpdateConversation(id string, patch ConversationPatch) error {
	if !validResourceID(id) || patch.Read == nil && patch.Pinned == nil && patch.Archived == nil {
		return ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return ErrUnavailable
	}
	defer tx.Rollback()
	var exists int
	err = tx.QueryRow(`SELECT 1 FROM conversations WHERE id=? AND workspace_id=?`, id, workspaceID).Scan(&exists)
	if err == sql.ErrNoRows {
		return ErrNotFound
	}
	if err != nil {
		return ErrUnavailable
	}
	if patch.Read != nil {
		if _, err := tx.Exec(`UPDATE messages SET is_read=? WHERE conversation_id=? AND workspace_id=? AND direction='incoming'`, *patch.Read, id, workspaceID); err != nil {
			return ErrUnavailable
		}
	}
	if patch.Pinned != nil {
		if _, err := tx.Exec(`UPDATE conversations SET pinned=? WHERE id=? AND workspace_id=?`, *patch.Pinned, id, workspaceID); err != nil {
			return ErrUnavailable
		}
	}
	if patch.Archived != nil {
		if _, err := tx.Exec(`UPDATE conversations SET archived=? WHERE id=? AND workspace_id=?`, *patch.Archived, id, workspaceID); err != nil {
			return ErrUnavailable
		}
	}
	if _, err := tx.Exec(`UPDATE workspaces SET revision=revision+1 WHERE id=?`, workspaceID); err != nil {
		return ErrUnavailable
	}
	if tx.Commit() != nil {
		return ErrUnavailable
	}
	return nil
}

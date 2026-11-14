package store

import (
	"context"
	"database/sql"
	"encoding/json"
)

func (s *Store) Import(req ImportRequest) (ImportResult, error) {
	return s.importBatch(req, "import")
}

// ImportIMAP is used only by the server-configured read-only mailbox adapter.
// Browser imports cannot promote their source to a live protocol sync.
func (s *Store) ImportIMAP(req ImportRequest) (ImportResult, error) {
	if req.Platform != "email" {
		return ImportResult{}, ErrInvalidInput
	}
	for _, message := range req.Messages {
		if message.Email == nil {
			return ImportResult{}, ErrInvalidInput
		}
	}
	return s.importBatch(req, "imap")
}

// ImportWeChat is reserved for the server-configured read-only database
// connector. Request JSON cannot choose or promote its own ingestion source.
func (s *Store) ImportWeChat(req ImportRequest) (ImportResult, error) {
	return s.ImportWeChatContext(context.Background(), req)
}

// ImportWeChatContext also honors cancellation while waiting for the store
// lock or writing the transaction. It never starts a detached background
// import after the request has gone away.
func (s *Store) ImportWeChatContext(ctx context.Context, req ImportRequest) (ImportResult, error) {
	if req.Platform != "weixin" {
		return ImportResult{}, ErrInvalidInput
	}
	for _, message := range req.Messages {
		if message.Email != nil {
			return ImportResult{}, ErrInvalidInput
		}
	}
	return s.importBatchContext(ctx, req, "wechatdb")
}

func (s *Store) importBatch(req ImportRequest, source string) (ImportResult, error) {
	return s.importBatchContext(context.Background(), req, source)
}

func (s *Store) importBatchContext(ctx context.Context, req ImportRequest, source string) (ImportResult, error) {
	if ctx == nil {
		return ImportResult{}, ErrInvalidInput
	}
	if err := ctx.Err(); err != nil {
		return ImportResult{}, err
	}
	req, err := normalizeImport(req)
	if err != nil {
		return ImportResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ImportResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ImportResult{}, ErrUnavailable
	}
	defer tx.Rollback()
	connectionID := stableID("connection", req.Platform, req.AccountID)
	// v4 provenance is additive so the v3 graph (including AI evidence) never
	// needs rebuilding. Legacy columns retain their original constrained values.
	legacySource := source
	var connectorSource any
	if source == "wechatdb" {
		legacySource, connectorSource = "import", "wechatdb"
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO connections(id,workspace_id,platform,account_id,display_name,source,connector_source) VALUES (?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET
 source=CASE WHEN connections.source=excluded.source THEN connections.source ELSE 'mixed' END,
 connector_source=CASE WHEN connections.connector_source IS NULL AND excluded.connector_source IS NULL THEN NULL
 WHEN coalesce(connections.connector_source,connections.source)=coalesce(excluded.connector_source,excluded.source) THEN coalesce(connections.connector_source,excluded.connector_source)
 ELSE 'mixed' END`, connectionID, workspaceID, req.Platform, req.AccountID, req.AccountName, legacySource, connectorSource); err != nil {
		return ImportResult{}, ErrUnavailable
	}
	result := ImportResult{}
	touched := map[string]bool{}
	for _, m := range req.Messages {
		contactID := stableID("contact", connectionID, m.PeerID)
		conversationID := stableID("conversation", connectionID, m.PeerID)
		touched[conversationID] = true
		if _, err := tx.ExecContext(ctx, `INSERT INTO contacts(id,workspace_id,connection_id,peer_id,display_name) VALUES (?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, contactID, workspaceID, connectionID, m.PeerID, m.PeerName); err != nil {
			return ImportResult{}, ErrUnavailable
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO conversations(id,workspace_id,connection_id,contact_id,peer_id,title) VALUES (?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, conversationID, workspaceID, connectionID, contactID, m.PeerID, m.PeerName); err != nil {
			return ImportResult{}, ErrUnavailable
		}
		var body, direction, occurredAt string
		var existingEmail sql.NullString
		var emailJSON any
		if m.Email != nil {
			encoded, _ := json.Marshal(m.Email)
			emailJSON = string(encoded)
		}
		err := tx.QueryRowContext(ctx, `SELECT body,direction,occurred_at,email_json FROM messages WHERE workspace_id=? AND conversation_id=? AND external_id=?`, workspaceID, conversationID, m.ExternalID).Scan(&body, &direction, &occurredAt, &existingEmail)
		if err == nil {
			if body != m.Body || direction != m.Direction || occurredAt != m.OccurredAt || existingEmail.Valid != (m.Email != nil) || (m.Email != nil && existingEmail.String != emailJSON) {
				return ImportResult{}, ErrConflict
			}
			result.Duplicates++
			continue
		}
		if err != sql.ErrNoRows {
			return ImportResult{}, ErrUnavailable
		}
		read := m.Direction == "outgoing"
		if m.Read != nil {
			read = *m.Read
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO messages(id,workspace_id,conversation_id,external_id,direction,body,occurred_at,is_read,source,email_json,connector_source) VALUES (?,?,?,?,?,?,?,?,?,?,?)`, stableID("message", conversationID, m.ExternalID), workspaceID, conversationID, m.ExternalID, m.Direction, m.Body, m.OccurredAt, read, legacySource, emailJSON, connectorSource); err != nil {
			return ImportResult{}, ErrUnavailable
		}
		result.Inserted++
	}
	if result.Inserted > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE workspaces SET revision=revision+1 WHERE id=?`, workspaceID); err != nil {
			return ImportResult{}, ErrUnavailable
		}
	}
	if err := ctx.Err(); err != nil {
		return ImportResult{}, err
	}
	if tx.Commit() != nil {
		return ImportResult{}, ErrUnavailable
	}
	result.Conversations = len(touched)
	return result, nil
}

func (s *Store) Connections() ([]Connection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT c.id,c.platform,c.account_id,c.display_name,coalesce(c.connector_source,c.source),count(m.seq) FROM connections c LEFT JOIN conversations v ON v.connection_id=c.id LEFT JOIN messages m ON m.conversation_id=v.id AND m.workspace_id=c.workspace_id WHERE c.workspace_id=? GROUP BY c.id ORDER BY c.platform,c.account_id,c.id`, workspaceID)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer rows.Close()
	items := []Connection{}
	for rows.Next() {
		var c Connection
		if rows.Scan(&c.ID, &c.Platform, &c.AccountID, &c.DisplayName, &c.Source, &c.MessageCount) != nil {
			return nil, ErrUnavailable
		}
		items = append(items, c)
	}
	if rows.Err() != nil {
		return nil, ErrUnavailable
	}
	return items, nil
}

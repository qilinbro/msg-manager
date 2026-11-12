package store

import "database/sql"

func (s *Store) Import(req ImportRequest) (ImportResult, error) {
	req, err := normalizeImport(req)
	if err != nil {
		return ImportResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return ImportResult{}, ErrUnavailable
	}
	defer tx.Rollback()
	connectionID := stableID("connection", req.Platform, req.AccountID)
	if _, err := tx.Exec(`INSERT INTO connections(id,workspace_id,platform,account_id,display_name,source) VALUES (?,?,?,?,?,'import') ON CONFLICT(id) DO NOTHING`, connectionID, workspaceID, req.Platform, req.AccountID, req.AccountName); err != nil {
		return ImportResult{}, ErrUnavailable
	}
	result := ImportResult{}
	touched := map[string]bool{}
	for _, m := range req.Messages {
		contactID := stableID("contact", connectionID, m.PeerID)
		conversationID := stableID("conversation", connectionID, m.PeerID)
		touched[conversationID] = true
		if _, err := tx.Exec(`INSERT INTO contacts(id,workspace_id,connection_id,peer_id,display_name) VALUES (?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, contactID, workspaceID, connectionID, m.PeerID, m.PeerName); err != nil {
			return ImportResult{}, ErrUnavailable
		}
		if _, err := tx.Exec(`INSERT INTO conversations(id,workspace_id,connection_id,contact_id,peer_id,title) VALUES (?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, conversationID, workspaceID, connectionID, contactID, m.PeerID, m.PeerName); err != nil {
			return ImportResult{}, ErrUnavailable
		}
		var body, direction, occurredAt string
		err := tx.QueryRow(`SELECT body,direction,occurred_at FROM messages WHERE workspace_id=? AND conversation_id=? AND external_id=?`, workspaceID, conversationID, m.ExternalID).Scan(&body, &direction, &occurredAt)
		if err == nil {
			if body != m.Body || direction != m.Direction || occurredAt != m.OccurredAt {
				return ImportResult{}, ErrConflict
			}
			result.Duplicates++
			continue
		}
		if err != sql.ErrNoRows {
			return ImportResult{}, ErrUnavailable
		}
		read := m.Direction == "outgoing"
		if _, err := tx.Exec(`INSERT INTO messages(id,workspace_id,conversation_id,external_id,direction,body,occurred_at,is_read) VALUES (?,?,?,?,?,?,?,?)`, stableID("message", conversationID, m.ExternalID), workspaceID, conversationID, m.ExternalID, m.Direction, m.Body, m.OccurredAt, read); err != nil {
			return ImportResult{}, ErrUnavailable
		}
		result.Inserted++
	}
	if result.Inserted > 0 {
		if _, err := tx.Exec(`UPDATE workspaces SET revision=revision+1 WHERE id=?`, workspaceID); err != nil {
			return ImportResult{}, ErrUnavailable
		}
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
	rows, err := s.db.Query(`SELECT c.id,c.platform,c.account_id,c.display_name,c.source,count(m.seq) FROM connections c LEFT JOIN conversations v ON v.connection_id=c.id LEFT JOIN messages m ON m.conversation_id=v.id AND m.workspace_id=c.workspace_id WHERE c.workspace_id=? GROUP BY c.id ORDER BY c.platform,c.account_id,c.id`, workspaceID)
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

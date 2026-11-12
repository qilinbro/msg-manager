package store

import "time"

func validAIDay(day string) bool {
	parsed, err := time.Parse("2006-01-02", day)
	return err == nil && parsed.Format("2006-01-02") == day
}

// ReserveAICall records an attempt before outbound model IO. Failed provider
// calls deliberately consume a slot; retries cannot bypass the local budget.
func (s *Store) ReserveAICall(day, conversationID string, limit int) (string, error) {
	if !validAIDay(day) || limit < 1 || limit > 10000 {
		return "", ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return "", ErrUnavailable
	}
	defer tx.Rollback()
	if _, err = aiConversation(tx, conversationID); err != nil {
		return "", err
	}
	var count int
	if tx.QueryRow(`SELECT count(*) FROM ai_call_reservations WHERE workspace_id=? AND day=?`, workspaceID, day).Scan(&count) != nil {
		return "", ErrUnavailable
	}
	if count >= limit {
		return "", ErrQuotaExceeded
	}
	id, err := newAIID()
	if err != nil {
		return "", err
	}
	if _, err = tx.Exec(`INSERT INTO ai_call_reservations(id,workspace_id,conversation_id,day,created_at)VALUES(?,?,?,?,?)`, id, workspaceID, conversationID, day, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return "", ErrUnavailable
	}
	if tx.Commit() != nil {
		return "", ErrUnavailable
	}
	return id, nil
}

func (s *Store) CountAICalls(day string) (int, error) {
	if !validAIDay(day) {
		return 0, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var count int
	if s.db.QueryRow(`SELECT count(*) FROM ai_call_reservations WHERE workspace_id=? AND day=?`, workspaceID, day).Scan(&count) != nil {
		return 0, ErrUnavailable
	}
	return count, nil
}

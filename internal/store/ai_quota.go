package store

import (
	"context"
	"time"
)

// aiCallCountSQL is shared by conversation analysis, profiles, digests and the
// global assistant. Reserving either kind must see every persisted attempt.
const aiCallCountSQL = `SELECT
 (SELECT count(*) FROM ai_call_reservations WHERE workspace_id=? AND day=?) +
 (SELECT count(*) FROM ai_assistant_call_reservations WHERE workspace_id=? AND day=?) +
 (SELECT count(*) FROM ai_feishu_call_reservations WHERE workspace_id=? AND day=?)`

func validAIDay(day string) bool {
	parsed, err := time.Parse("2006-01-02", day)
	return err == nil && parsed.Format("2006-01-02") == day
}

// ReserveAICall records an attempt before outbound model IO. Failed provider
// calls deliberately consume a slot; retries cannot bypass the local budget.
func (s *Store) ReserveAICall(day, conversationID string, limit int) (string, error) {
	return s.ReserveAICallContext(context.Background(), day, conversationID, limit)
}

func (s *Store) ReserveAICallContext(ctx context.Context, day, conversationID string, limit int) (string, error) {
	if !validAIDay(day) || limit < 1 || limit > 10000 {
		return "", ErrInvalidInput
	}
	if err := s.lockContext(ctx); err != nil {
		return "", err
	}
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", ErrUnavailable
	}
	defer tx.Rollback()
	if _, err = aiConversation(tx, conversationID); err != nil {
		return "", err
	}
	var count int
	if tx.QueryRowContext(ctx, aiCallCountSQL, workspaceID, day, workspaceID, day, workspaceID, day).Scan(&count) != nil {
		return "", ErrUnavailable
	}
	if count >= limit {
		return "", ErrQuotaExceeded
	}
	id, err := newAIID()
	if err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO ai_call_reservations(id,workspace_id,conversation_id,day,created_at)VALUES(?,?,?,?,?)`, id, workspaceID, conversationID, day, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return "", ErrUnavailable
	}
	if tx.Commit() != nil {
		return "", ErrUnavailable
	}
	return id, nil
}

func (s *Store) CountAICalls(day string) (int, error) {
	return s.CountAICallsContext(context.Background(), day)
}

func (s *Store) CountAICallsContext(ctx context.Context, day string) (int, error) {
	if !validAIDay(day) {
		return 0, ErrInvalidInput
	}
	if err := s.lockContext(ctx); err != nil {
		return 0, err
	}
	defer s.mu.Unlock()
	var count int
	if s.db.QueryRowContext(ctx, aiCallCountSQL, workspaceID, day, workspaceID, day, workspaceID, day).Scan(&count) != nil {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		return 0, ErrUnavailable
	}
	return count, nil
}

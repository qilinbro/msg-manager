package store

import (
	"context"
	"time"
)

// ReserveAssistantCallContext consumes the shared workspace/day model budget
// before outbound IO. Attempts remain charged after provider errors or restarts.
// No chat content, model key, device bearer or synthetic conversation is stored.
func (s *Store) ReserveAssistantCallContext(ctx context.Context, day string, limit int) (string, error) {
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
	var count int
	if tx.QueryRowContext(ctx, aiCallCountSQL, workspaceID, day, workspaceID, day, workspaceID, day).Scan(&count) != nil {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return "", ErrUnavailable
	}
	if count >= limit {
		return "", ErrQuotaExceeded
	}
	id, err := newAIID()
	if err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO ai_assistant_call_reservations(id,workspace_id,purpose,day,created_at) VALUES(?,?,'assistant',?,?)`, id, workspaceID, day, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return "", ErrUnavailable
	}
	if err = tx.Commit(); err != nil {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return "", ErrUnavailable
	}
	return id, nil
}

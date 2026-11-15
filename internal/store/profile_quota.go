package store

import (
	"context"
)

func (s *Store) ReserveProfileSearch(ctx context.Context, day, conversationID string, limit int) (string, error) {
	if !validAIDay(day) || limit < 1 || limit > 1000 {
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
	p, err := profileRow(ctx, tx, conversationID)
	if err != nil {
		return "", err
	}
	if err = ensureProfile(ctx, tx, p); err != nil {
		return "", err
	}
	var count int
	if tx.QueryRowContext(ctx, `SELECT count(*) FROM profile_search_reservations WHERE workspace_id=? AND day=?`, workspaceID, day).Scan(&count) != nil {
		return "", ErrUnavailable
	}
	if count >= limit {
		return "", ErrQuotaExceeded
	}
	id, err := newAIID()
	if err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO profile_search_reservations(id,workspace_id,conversation_id,day,created_at)VALUES(?,?,?,?,?)`, id, workspaceID, conversationID, day, profileNow()); err != nil {
		return "", ErrUnavailable
	}
	if tx.Commit() != nil {
		return "", ErrUnavailable
	}
	return id, nil
}

func (s *Store) CountProfileSearch(ctx context.Context, day string) (int, error) {
	if !validAIDay(day) {
		return 0, ErrInvalidInput
	}
	if err := s.lockContext(ctx); err != nil {
		return 0, err
	}
	defer s.mu.Unlock()
	var count int
	if s.db.QueryRowContext(ctx, `SELECT count(*) FROM profile_search_reservations WHERE workspace_id=? AND day=?`, workspaceID, day).Scan(&count) != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, ErrUnavailable
	}
	return count, nil
}

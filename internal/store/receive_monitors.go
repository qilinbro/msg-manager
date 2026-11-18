package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"time"
	"unicode/utf8"
)

const receiveMonitorStateLimit = 16384

func validReceiveMonitorBinding(scope, binding string) bool {
	return (scope == "email" || scope == "telegram") && validLabel(binding, true)
}

func validReceiveMonitorState(state json.RawMessage) bool {
	object := bytes.TrimSpace(state)
	return len(state) <= receiveMonitorStateLimit && len(object) >= 2 && object[0] == '{' && utf8.Valid(state) && json.Valid(state)
}

func (s *Store) LoadReceiveMonitor(ctx context.Context, scope, binding string) (json.RawMessage, error) {
	if !validReceiveMonitorBinding(scope, binding) {
		return nil, ErrInvalidInput
	}
	if err := s.lockContext(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT state_json FROM receive_monitors WHERE workspace_id=? AND scope=? AND binding=?`, workspaceID, scope, binding).Scan(&value)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrUnavailable
	}
	result := json.RawMessage(value)
	if !validReceiveMonitorState(result) {
		return nil, ErrUnavailable
	}
	return result, nil
}

func (s *Store) SaveReceiveMonitor(ctx context.Context, scope, binding string, state json.RawMessage) error {
	if !validReceiveMonitorBinding(scope, binding) || !validReceiveMonitorState(state) {
		return ErrInvalidInput
	}
	if err := s.lockContext(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `INSERT INTO receive_monitors(workspace_id,scope,binding,state_json,updated_at)
VALUES(?,?,?,?,?) ON CONFLICT(workspace_id,scope,binding) DO UPDATE SET state_json=excluded.state_json,updated_at=excluded.updated_at`,
		workspaceID, scope, binding, string(state), time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrUnavailable
	}
	return nil
}

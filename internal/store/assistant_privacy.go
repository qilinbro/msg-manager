package store

import "context"

// HasPausedConversationsContext is a conservative privacy fence for global
// summaries whose historic output may not enumerate every source conversation.
// Human REST reads remain unchanged; the assistant decides when this fence is
// necessary before forwarding stored content or replaying model history.
func (s *Store) HasPausedConversationsContext(ctx context.Context) (bool, error) {
	if err := s.lockContext(ctx); err != nil {
		return false, err
	}
	defer s.mu.Unlock()
	var paused bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ai_preferences WHERE workspace_id=? AND mode='paused')`, workspaceID).Scan(&paused); err != nil {
		return false, wechatMonitorDBError(ctx)
	}
	return paused, nil
}

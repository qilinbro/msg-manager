package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"time"
)

// QQMilkyMonitorSettings contains only the explicit private-chat allowlist.
// The protocol endpoint and access token are process-local configuration and
// are never written to the encrypted message database.
type QQMilkyMonitorSettings struct {
	Enabled bool     `json:"enabled"`
	PeerIDs []string `json:"peer_ids"`
}

func defaultQQMilkyMonitorSettings() QQMilkyMonitorSettings {
	return QQMilkyMonitorSettings{PeerIDs: []string{}}
}

func validQQMilkyID(value string) bool {
	if len(value) < 2 || len(value) > 20 || value[0] < '1' || value[0] > '9' {
		return false
	}
	for _, ch := range value[1:] {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func normalizeQQMilkyMonitor(accountID string, settings QQMilkyMonitorSettings) (QQMilkyMonitorSettings, error) {
	if !validQQMilkyID(accountID) || len(settings.PeerIDs) > 20 || (settings.Enabled && len(settings.PeerIDs) == 0) {
		return QQMilkyMonitorSettings{}, ErrInvalidInput
	}
	result := QQMilkyMonitorSettings{Enabled: settings.Enabled, PeerIDs: append([]string{}, settings.PeerIDs...)}
	seen := make(map[string]struct{}, len(result.PeerIDs))
	for _, peer := range result.PeerIDs {
		if !validQQMilkyID(peer) || peer == accountID {
			return QQMilkyMonitorSettings{}, ErrInvalidInput
		}
		if _, ok := seen[peer]; ok {
			return QQMilkyMonitorSettings{}, ErrInvalidInput
		}
		seen[peer] = struct{}{}
	}
	sort.Strings(result.PeerIDs)
	return result, nil
}

func ValidateQQMilkyMonitorSettings(accountID string, settings QQMilkyMonitorSettings) error {
	_, err := normalizeQQMilkyMonitor(accountID, settings)
	return err
}

func (s *Store) LoadQQMilkyMonitor(ctx context.Context, accountID string) (QQMilkyMonitorSettings, error) {
	if !validQQMilkyID(accountID) {
		return QQMilkyMonitorSettings{}, ErrInvalidInput
	}
	if err := s.lockContext(ctx); err != nil {
		return QQMilkyMonitorSettings{}, err
	}
	defer s.mu.Unlock()
	settings := defaultQQMilkyMonitorSettings()
	var enabled int
	var peers string
	err := s.db.QueryRowContext(ctx, `SELECT enabled,peer_ids_json FROM qq_milky_monitor_settings WHERE workspace_id=? AND account_id=?`, workspaceID, accountID).Scan(&enabled, &peers)
	if err == sql.ErrNoRows {
		return settings, nil
	}
	if err != nil {
		return QQMilkyMonitorSettings{}, wechatMonitorDBError(ctx)
	}
	if enabled != 0 && enabled != 1 || len(peers) < 2 || len(peers) > 500 || json.Unmarshal([]byte(peers), &settings.PeerIDs) != nil || settings.PeerIDs == nil {
		return QQMilkyMonitorSettings{}, ErrUnavailable
	}
	settings.Enabled = enabled == 1
	settings, err = normalizeQQMilkyMonitor(accountID, settings)
	if err != nil {
		return QQMilkyMonitorSettings{}, ErrUnavailable
	}
	return settings, nil
}

func (s *Store) SaveQQMilkyMonitor(ctx context.Context, accountID string, settings QQMilkyMonitorSettings) error {
	settings, err := normalizeQQMilkyMonitor(accountID, settings)
	if err != nil {
		return err
	}
	if err = s.lockContext(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	peers, err := json.Marshal(settings.PeerIDs)
	if err != nil || len(peers) > 500 {
		return ErrInvalidInput
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO qq_milky_monitor_settings(workspace_id,account_id,enabled,peer_ids_json,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(workspace_id,account_id) DO UPDATE SET enabled=excluded.enabled,peer_ids_json=excluded.peer_ids_json,updated_at=excluded.updated_at`, workspaceID, accountID, settings.Enabled, string(peers), time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return wechatMonitorDBError(ctx)
	}
	return nil
}

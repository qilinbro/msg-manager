package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

// WechatMonitorSettings is the operator's explicit per-account allowlist. It
// contains no source directory, database key, or permission to send messages.
type WechatMonitorSettings struct {
	Enabled         bool     `json:"enabled"`
	PeerIDs         []string `json:"peer_ids"`
	IntervalSeconds int      `json:"interval_seconds"`
}

func defaultWechatMonitorSettings() WechatMonitorSettings {
	return WechatMonitorSettings{PeerIDs: []string{}, IntervalSeconds: 30}
}

// Match the connector's supported private account identifiers without coupling
// encrypted message storage to a particular source database implementation.
func validWechatMonitorID(id string) bool {
	if len(id) < 2 || len(id) > 128 || !(id[0] >= 'A' && id[0] <= 'Z' || id[0] >= 'a' && id[0] <= 'z') || strings.HasPrefix(id, "gh_") {
		return false
	}
	for _, ch := range id {
		if !(ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-') {
			return false
		}
	}
	switch strings.ToLower(id) {
	case "filehelper", "weixin", "fmessage", "medianote", "newsapp", "notification_messages", "floatbottle", "qqmail", "tmessage", "qmessage", "masssendapp", "weibo", "brandsessionholder", "helper_entry":
		return false
	}
	return true
}

func normalizeWechatMonitor(accountID string, settings WechatMonitorSettings) (WechatMonitorSettings, error) {
	if !validWechatMonitorID(accountID) || settings.IntervalSeconds < 10 || settings.IntervalSeconds > 300 || len(settings.PeerIDs) > 20 || settings.Enabled && len(settings.PeerIDs) == 0 {
		return WechatMonitorSettings{}, ErrInvalidInput
	}
	result := settings
	result.PeerIDs = make([]string, len(settings.PeerIDs))
	seen := make(map[string]bool, len(settings.PeerIDs))
	for i, peer := range settings.PeerIDs {
		if !validWechatMonitorID(peer) || peer == accountID || seen[peer] {
			return WechatMonitorSettings{}, ErrInvalidInput
		}
		result.PeerIDs[i] = peer
		seen[peer] = true
	}
	return result, nil
}

// ValidateWechatMonitorSettings lets the scheduler reject an invalid update
// before stopping its current worker or mutating the persisted configuration.
func ValidateWechatMonitorSettings(accountID string, settings WechatMonitorSettings) error {
	_, err := normalizeWechatMonitor(accountID, settings)
	return err
}

func wechatMonitorDBError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return ErrUnavailable
}

// LoadWechatMonitor is metadata-only: it never opens a WeChat source database.
// A missing record is disabled, with an empty allowlist and a 30-second period.
func (s *Store) LoadWechatMonitor(ctx context.Context, accountID string) (WechatMonitorSettings, error) {
	if !validWechatMonitorID(accountID) {
		return WechatMonitorSettings{}, ErrInvalidInput
	}
	if err := s.lockContext(ctx); err != nil {
		return WechatMonitorSettings{}, err
	}
	defer s.mu.Unlock()
	var enabled int
	var peers string
	settings := defaultWechatMonitorSettings()
	err := s.db.QueryRowContext(ctx, `SELECT enabled,peer_ids_json,interval_seconds FROM wechat_monitor_settings WHERE workspace_id=? AND account_id=?`, workspaceID, accountID).Scan(&enabled, &peers, &settings.IntervalSeconds)
	if err == sql.ErrNoRows {
		return settings, nil
	}
	if err != nil {
		return WechatMonitorSettings{}, wechatMonitorDBError(ctx)
	}
	if enabled != 0 && enabled != 1 || len(peers) > 2700 || json.Unmarshal([]byte(peers), &settings.PeerIDs) != nil || settings.PeerIDs == nil {
		return WechatMonitorSettings{}, ErrUnavailable
	}
	settings.Enabled = enabled == 1
	settings, err = normalizeWechatMonitor(accountID, settings)
	if err != nil {
		return WechatMonitorSettings{}, ErrUnavailable
	}
	return settings, nil
}

// SaveWechatMonitor validates before entering SQL and persists the complete
// allowlist atomically. A successful call does not itself start a worker.
func (s *Store) SaveWechatMonitor(ctx context.Context, accountID string, settings WechatMonitorSettings) error {
	settings, err := normalizeWechatMonitor(accountID, settings)
	if err != nil {
		return err
	}
	if err = s.lockContext(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	peers, err := json.Marshal(settings.PeerIDs)
	if err != nil {
		return ErrInvalidInput
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO wechat_monitor_settings(workspace_id,account_id,enabled,peer_ids_json,interval_seconds,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(workspace_id,account_id) DO UPDATE SET enabled=excluded.enabled,peer_ids_json=excluded.peer_ids_json,interval_seconds=excluded.interval_seconds,updated_at=excluded.updated_at`, workspaceID, accountID, settings.Enabled, string(peers), settings.IntervalSeconds, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return wechatMonitorDBError(ctx)
	}
	return nil
}

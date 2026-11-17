package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// QQMilkyConfig never crosses the HTTP boundary. The token is stored only in
// SQLCipher; response types expose a boolean instead of its value.
type QQMilkyConfig struct {
	URL          string `json:"-"`
	AccountID    string `json:"-"`
	AccessToken  string `json:"-"`
	HistoryLimit int    `json:"-"`
	Revision     int64  `json:"-"`
}

func (QQMilkyConfig) String() string   { return "QQ Milky configuration (redacted)" }
func (QQMilkyConfig) GoString() string { return "QQ Milky configuration (redacted)" }

func validQQMilkyConfig(value QQMilkyConfig) bool {
	return value.URL != "" && profileText(value.URL, 4096, false) && validQQMilkyID(value.AccountID) &&
		len(value.AccessToken) > 0 && len(value.AccessToken) <= 4096 &&
		strings.IndexFunc(value.AccessToken, func(r rune) bool { return r < 33 || r > 126 }) == -1 &&
		value.HistoryLimit >= 1 && value.HistoryLimit <= 30
}

func (s *Store) LoadQQMilkyConfig(ctx context.Context) (*QQMilkyConfig, error) {
	if err := s.lockContext(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	var value QQMilkyConfig
	err := s.db.QueryRowContext(ctx, `SELECT url,account_id,access_token,history_limit,revision FROM qq_milky_connection_config WHERE workspace_id=?`, workspaceID).
		Scan(&value.URL, &value.AccountID, &value.AccessToken, &value.HistoryLimit, &value.Revision)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, wechatMonitorDBError(ctx)
	}
	if !validQQMilkyConfig(value) || value.Revision <= 0 {
		return nil, ErrUnavailable
	}
	return &value, nil
}

// SaveQQMilkyConfig atomically updates credentials and revokes all prior Milky
// receive grants. Selecting A -> B -> A therefore never revives old automation.
// Message history, contacts and other platform permissions are unaffected.
func (s *Store) SaveQQMilkyConfig(ctx context.Context, value QQMilkyConfig, expectedRevision int64) (QQMilkyConfig, error) {
	if expectedRevision < 0 || expectedRevision >= 9007199254740991 || !validQQMilkyConfig(value) {
		return QQMilkyConfig{}, ErrInvalidInput
	}
	if err := s.lockContext(ctx); err != nil {
		return QQMilkyConfig{}, err
	}
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return QQMilkyConfig{}, wechatMonitorDBError(ctx)
	}
	defer tx.Rollback()
	var revision int64
	err = tx.QueryRowContext(ctx, `SELECT revision FROM qq_milky_connection_config WHERE workspace_id=?`, workspaceID).Scan(&revision)
	if err != nil && err != sql.ErrNoRows {
		return QQMilkyConfig{}, wechatMonitorDBError(ctx)
	}
	if revision != expectedRevision {
		return QQMilkyConfig{}, ErrStaleContext
	}
	value.Revision = revision + 1
	_, err = tx.ExecContext(ctx, `INSERT INTO qq_milky_connection_config(workspace_id,url,account_id,access_token,history_limit,revision) VALUES(?,?,?,?,?,?) ON CONFLICT(workspace_id) DO UPDATE SET url=excluded.url,account_id=excluded.account_id,access_token=excluded.access_token,history_limit=excluded.history_limit,revision=excluded.revision`, workspaceID, value.URL, value.AccountID, value.AccessToken, value.HistoryLimit, value.Revision)
	if err != nil {
		return QQMilkyConfig{}, wechatMonitorDBError(ctx)
	}
	_, err = tx.ExecContext(ctx, `UPDATE qq_milky_monitor_settings SET enabled=0,peer_ids_json='[]',updated_at=? WHERE workspace_id=?`, time.Now().UTC().Format(time.RFC3339Nano), workspaceID)
	if err != nil {
		return QQMilkyConfig{}, wechatMonitorDBError(ctx)
	}
	if err := tx.Commit(); err != nil {
		return QQMilkyConfig{}, wechatMonitorDBError(ctx)
	}
	return value, nil
}

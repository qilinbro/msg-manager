package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

func validFeishuID(value, prefix string) bool {
	if !strings.HasPrefix(value, prefix) || len(value) <= len(prefix) || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}
func validFeishuSecret(value string) bool {
	if len(value) < 1 || len(value) > 4096 {
		return false
	}
	for _, r := range value {
		if r < 33 || r > 126 {
			return false
		}
	}
	return true
}
func loadFeishuConfigTx(ctx context.Context, tx *sql.Tx) (FeishuConfig, error) {
	var c FeishuConfig
	err := tx.QueryRowContext(ctx, `SELECT app_id,app_secret,recipient_open_id,revision FROM feishu_configs WHERE workspace_id=?`, workspaceID).Scan(&c.AppID, &c.AppSecret, &c.RecipientOpenID, &c.Revision)
	if err != nil {
		return FeishuConfig{}, wechatMonitorDBError(ctx)
	}
	return c, nil
}
func (s *Store) LoadFeishuConfig(ctx context.Context) (FeishuConfig, error) {
	if err := s.lockContext(ctx); err != nil {
		return FeishuConfig{}, err
	}
	defer s.mu.Unlock()
	var c FeishuConfig
	if err := s.db.QueryRowContext(ctx, `SELECT app_id,app_secret,recipient_open_id,revision FROM feishu_configs WHERE workspace_id=?`, workspaceID).Scan(&c.AppID, &c.AppSecret, &c.RecipientOpenID, &c.Revision); err != nil {
		return FeishuConfig{}, wechatMonitorDBError(ctx)
	}
	return c, nil
}
func revokeFeishuScheduleTx(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `UPDATE feishu_schedules SET enabled=0,revision=revision+1,model_config_id='',model_config_revision=0,config_revision=0,target_binding_id='',next_window_end=NULL,authorized_at=NULL,authorized_by='' WHERE workspace_id=?`, workspaceID)
	if err != nil {
		return wechatMonitorDBError(ctx)
	}
	return nil
}

// SaveFeishuConfig never treats an empty secret as implicit retention. Every save
// including a same-value save revokes standing consent in the same transaction.
func (s *Store) SaveFeishuConfig(ctx context.Context, next FeishuConfig, expected int64, retain bool) (FeishuConfig, error) {
	if expected < 1 || !validFeishuID(next.AppID, "cli_") || !validFeishuID(next.RecipientOpenID, "ou_") || retain && next.AppSecret != "" || !retain && !validFeishuSecret(next.AppSecret) {
		return FeishuConfig{}, ErrInvalidInput
	}
	if err := s.lockContext(ctx); err != nil {
		return FeishuConfig{}, err
	}
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return FeishuConfig{}, wechatMonitorDBError(ctx)
	}
	defer tx.Rollback()
	current, err := loadFeishuConfigTx(ctx, tx)
	if err != nil {
		return FeishuConfig{}, err
	}
	if current.Revision != expected {
		return FeishuConfig{}, ErrStaleContext
	}
	if retain {
		if current.AppID != next.AppID || current.AppSecret == "" {
			return FeishuConfig{}, ErrInvalidInput
		}
		next.AppSecret = current.AppSecret
	}
	next.Revision = current.Revision + 1
	if _, err = tx.ExecContext(ctx, `UPDATE feishu_configs SET app_id=?,app_secret=?,recipient_open_id=?,revision=? WHERE workspace_id=?`, next.AppID, next.AppSecret, next.RecipientOpenID, next.Revision, workspaceID); err != nil {
		return FeishuConfig{}, wechatMonitorDBError(ctx)
	}
	if err = revokeFeishuScheduleTx(ctx, tx); err != nil {
		return FeishuConfig{}, err
	}
	if err = tx.Commit(); err != nil {
		return FeishuConfig{}, wechatMonitorDBError(ctx)
	}
	return next, nil
}
func (s *Store) DeleteFeishuConfig(ctx context.Context, expected int64) error {
	if expected < 1 {
		return ErrInvalidInput
	}
	if err := s.lockContext(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wechatMonitorDBError(ctx)
	}
	defer tx.Rollback()
	c, err := loadFeishuConfigTx(ctx, tx)
	if err != nil {
		return err
	}
	if c.Revision != expected {
		return ErrStaleContext
	}
	if _, err = tx.ExecContext(ctx, `UPDATE feishu_configs SET app_id='',app_secret='',recipient_open_id='',revision=revision+1 WHERE workspace_id=?`, workspaceID); err != nil {
		return wechatMonitorDBError(ctx)
	}
	if err = revokeFeishuScheduleTx(ctx, tx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return wechatMonitorDBError(ctx)
	}
	return nil
}
func checkFeishuBindingTx(ctx context.Context, tx *sql.Tx, b FeishuBinding) error {
	if !b.Valid() {
		return ErrInvalidInput
	}
	c, err := loadFeishuConfigTx(ctx, tx)
	if err != nil {
		return err
	}
	if !c.Configured() || c.Revision != b.ConfigRevision || c.TargetBindingID() != b.TargetBindingID {
		return ErrFeishuConfigChanged
	}
	return nil
}
func scanFeishuSchedule(row interface{ Scan(...any) error }) (FeishuSchedule, error) {
	out := FeishuSchedule{Timezone: FeishuTimezone, Slots: []string{"08:00", "20:00"}}
	err := row.Scan(&out.Enabled, &out.Revision, &out.ModelConfigID, &out.ModelConfigRevision, &out.ConfigRevision, &out.TargetBindingID, &out.NextWindowEnd, &out.LastWindowEnd)
	return out, err
}

const feishuScheduleSelect = `SELECT enabled,revision,model_config_id,model_config_revision,config_revision,target_binding_id,next_window_end,last_window_end FROM feishu_schedules WHERE workspace_id=?`

func (s *Store) LoadFeishuSchedule(ctx context.Context) (FeishuSchedule, error) {
	if err := s.lockContext(ctx); err != nil {
		return FeishuSchedule{}, err
	}
	defer s.mu.Unlock()
	out, err := scanFeishuSchedule(s.db.QueryRowContext(ctx, feishuScheduleSelect, workspaceID))
	if err != nil {
		return FeishuSchedule{}, wechatMonitorDBError(ctx)
	}
	return out, nil
}
func (s *Store) SaveFeishuSchedule(ctx context.Context, next FeishuSchedule, expected int64, now time.Time, owner string) (FeishuSchedule, error) {
	if expected < 1 || now.IsZero() || next.Enabled && (!next.FeishuBinding.Valid() || !feishuText(owner, 256, false)) {
		return FeishuSchedule{}, ErrInvalidInput
	}
	if err := s.lockContext(ctx); err != nil {
		return FeishuSchedule{}, err
	}
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return FeishuSchedule{}, wechatMonitorDBError(ctx)
	}
	defer tx.Rollback()
	current, err := scanFeishuSchedule(tx.QueryRowContext(ctx, feishuScheduleSelect, workspaceID))
	if err != nil {
		return FeishuSchedule{}, wechatMonitorDBError(ctx)
	}
	if current.Revision != expected {
		return FeishuSchedule{}, ErrStaleContext
	}
	next.Revision = current.Revision + 1
	next.LastWindowEnd = current.LastWindowEnd
	next.Timezone = FeishuTimezone
	next.Slots = []string{"08:00", "20:00"}
	var at any
	if next.Enabled {
		if err = checkFeishuBindingTx(ctx, tx, next.FeishuBinding); err != nil {
			return FeishuSchedule{}, err
		}
		end := FeishuTime(FeishuNextWindow(now))
		next.NextWindowEnd = &end
		at = FeishuTime(now)
	} else {
		next.FeishuBinding = FeishuBinding{}
		next.NextWindowEnd = nil
		owner = ""
	}
	if _, err = tx.ExecContext(ctx, `UPDATE feishu_schedules SET enabled=?,revision=?,model_config_id=?,model_config_revision=?,config_revision=?,target_binding_id=?,next_window_end=?,authorized_at=?,authorized_by=? WHERE workspace_id=?`, next.Enabled, next.Revision, next.ModelConfigID, next.ModelConfigRevision, next.ConfigRevision, next.TargetBindingID, next.NextWindowEnd, at, owner, workspaceID); err != nil {
		return FeishuSchedule{}, wechatMonitorDBError(ctx)
	}
	if err = tx.Commit(); err != nil {
		return FeishuSchedule{}, wechatMonitorDBError(ctx)
	}
	return next, nil
}

// RevokeFeishuBinding disables only the currently matching configured target.
// It is used for explicit platform auth/permission/recipient rejections, not for
// ambiguous or transient failures. It never schedules another attempt.
func (s *Store) RevokeFeishuBinding(ctx context.Context, binding FeishuBinding) error {
	if !binding.Valid() {
		return ErrInvalidInput
	}
	if err := s.lockContext(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `UPDATE feishu_schedules SET enabled=0,revision=revision+1,model_config_id='',model_config_revision=0,config_revision=0,target_binding_id='',next_window_end=NULL,authorized_at=NULL,authorized_by='' WHERE workspace_id=? AND enabled=1 AND model_config_id=? AND model_config_revision=? AND config_revision=? AND target_binding_id=?`, workspaceID, binding.ModelConfigID, binding.ModelConfigRevision, binding.ConfigRevision, binding.TargetBindingID)
	if err != nil {
		return wechatMonitorDBError(ctx)
	}
	return nil
}

// SkipFeishuBacklog advances only the timer, never claims a report or sends.
// On startup even a just-missed boundary is not caught up. During normal runtime
// only a narrow grace permits work; waking from sleep skips stale boundaries.
func (s *Store) SkipFeishuBacklog(ctx context.Context, now time.Time, startup bool) error {
	if now.IsZero() {
		return ErrInvalidInput
	}
	if err := s.lockContext(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	schedule, err := scanFeishuSchedule(s.db.QueryRowContext(ctx, feishuScheduleSelect, workspaceID))
	if err != nil {
		return wechatMonitorDBError(ctx)
	}
	if !schedule.Enabled || schedule.NextWindowEnd == nil {
		return nil
	}
	end, err := time.Parse(time.RFC3339Nano, *schedule.NextWindowEnd)
	if err != nil {
		return ErrUnavailable
	}
	if end.After(now) || !startup && now.Sub(end) <= FeishuScheduleGrace {
		return nil
	}
	_, err = s.db.ExecContext(ctx, `UPDATE feishu_schedules SET next_window_end=? WHERE workspace_id=? AND enabled=1 AND revision=?`, FeishuTime(FeishuNextWindow(now)), workspaceID, schedule.Revision)
	if err != nil {
		return wechatMonitorDBError(ctx)
	}
	return nil
}

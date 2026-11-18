package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const feishuDeliverySelect = `SELECT id,report_id,window_start,window_end,state,model_config_id,model_config_revision,config_revision,target_binding_id,content_hash,message_id,error_code,created_at,completed_at FROM feishu_deliveries WHERE workspace_id=?`

func scanFeishuDelivery(row interface{ Scan(...any) error }) (FeishuDelivery, error) {
	var d FeishuDelivery
	err := row.Scan(&d.ID, &d.ReportID, &d.WindowStart, &d.WindowEnd, &d.State, &d.ModelConfigID, &d.ModelConfigRevision, &d.ConfigRevision, &d.TargetBindingID, &d.ContentHash, &d.MessageID, &d.ErrorCode, &d.CreatedAt, &d.CompletedAt)
	return d, err
}
func (s *Store) FeishuDeliveryByID(ctx context.Context, id string) (*FeishuDelivery, error) {
	if !validFeishuLedgerID(id, "fd_") {
		return nil, ErrInvalidInput
	}
	if err := s.lockContext(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	d, err := scanFeishuDelivery(s.db.QueryRowContext(ctx, feishuDeliverySelect+` AND id=?`, workspaceID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, wechatMonitorDBError(ctx)
	}
	return &d, nil
}

// ClaimFeishuDelivery commits BEFORE any token/message HTTP request. Existing
// natural-window rows always win, including failed/unknown; config changes never
// create a second attempt. The API also holds its outbound privacy lease.
func (s *Store) ClaimFeishuDelivery(ctx context.Context, reportID, hash string, b FeishuBinding, now time.Time, scheduled bool) (FeishuDelivery, bool, error) {
	if !validFeishuLedgerID(reportID, "fr_") || !validProfileConfig(hash) || !b.Valid() || now.IsZero() {
		return FeishuDelivery{}, false, ErrInvalidInput
	}
	if err := s.lockContext(ctx); err != nil {
		return FeishuDelivery{}, false, err
	}
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return FeishuDelivery{}, false, wechatMonitorDBError(ctx)
	}
	defer tx.Rollback()
	r, err := scanFeishuReport(tx.QueryRowContext(ctx, feishuReportSelect+` AND id=?`, workspaceID, reportID))
	if errors.Is(err, sql.ErrNoRows) {
		return FeishuDelivery{}, false, ErrNotFound
	}
	if err != nil {
		return FeishuDelivery{}, false, wechatMonitorDBError(ctx)
	}
	d, err := scanFeishuDelivery(tx.QueryRowContext(ctx, feishuDeliverySelect+` AND window_end=?`, workspaceID, r.WindowEnd))
	if err == nil {
		return d, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return FeishuDelivery{}, false, wechatMonitorDBError(ctx)
	}
	if r.State != "succeeded" || r.FeishuBinding != b || r.ContentHash != hash || FeishuContentHash(r) != hash {
		return FeishuDelivery{}, false, ErrStaleContext
	}
	end, e := time.Parse(time.RFC3339Nano, r.WindowEnd)
	if e != nil {
		return FeishuDelivery{}, false, ErrUnavailable
	}
	if now.Before(end) || !now.Before(end.Add(12*time.Hour)) {
		return FeishuDelivery{}, false, ErrFeishuExpired
	}
	if err = checkFeishuBindingTx(ctx, tx, b); err != nil {
		return FeishuDelivery{}, false, err
	}
	if err = loadFeishuReportDetails(ctx, tx, &r); err != nil {
		return FeishuDelivery{}, false, err
	}
	if err = checkFeishuSourcesTx(ctx, tx, r.SourceRefs); err != nil {
		return FeishuDelivery{}, false, err
	}
	if scheduled {
		schedule, e := scanFeishuSchedule(tx.QueryRowContext(ctx, feishuScheduleSelect, workspaceID))
		if e != nil {
			return FeishuDelivery{}, false, wechatMonitorDBError(ctx)
		}
		if r.Trigger != "schedule" || !schedule.Enabled || r.ScheduleRevision != schedule.Revision || r.FeishuBinding != schedule.FeishuBinding || schedule.LastWindowEnd == nil || *schedule.LastWindowEnd != r.WindowEnd {
			return FeishuDelivery{}, false, ErrStaleContext
		}
	}
	d = FeishuDelivery{ID: FeishuDeliveryID(r.WindowEnd), ReportID: r.ID, WindowStart: r.WindowStart, WindowEnd: r.WindowEnd, State: "running", FeishuBinding: r.FeishuBinding, ContentHash: r.ContentHash, CreatedAt: FeishuTime(now)}
	if _, err = tx.ExecContext(ctx, `INSERT INTO feishu_deliveries(id,workspace_id,report_id,window_start,window_end,state,model_config_id,model_config_revision,config_revision,target_binding_id,content_hash,created_at) VALUES(?,?,?,?,?,'running',?,?,?,?,?,?)`, d.ID, workspaceID, d.ReportID, d.WindowStart, d.WindowEnd, d.ModelConfigID, d.ModelConfigRevision, d.ConfigRevision, d.TargetBindingID, d.ContentHash, d.CreatedAt); err != nil {
		return FeishuDelivery{}, false, wechatMonitorDBError(ctx)
	}
	if err = tx.Commit(); err != nil {
		return FeishuDelivery{}, false, wechatMonitorDBError(ctx)
	}
	return d, true, nil
}
func (s *Store) CompleteFeishuDelivery(ctx context.Context, d FeishuDelivery) error {
	completed, timeOK := feishuCanonicalTime(d.CompletedAt)
	created, createdErr := time.Parse(time.RFC3339Nano, d.CreatedAt)
	if d.ID != FeishuDeliveryID(d.WindowEnd) || d.ReportID != FeishuReportID(d.WindowEnd) || !feishuWindowValid(d.WindowStart, d.WindowEnd) || !d.FeishuBinding.Valid() || !feishuTerminal(d.State) || !timeOK || createdErr != nil || completed.Before(created) || !feishuErrorCodeValid(d.ErrorCode) || !feishuText(d.MessageID, 256, true) || d.State == "succeeded" && (d.MessageID == "" || d.ErrorCode != "") || d.State != "succeeded" && d.ErrorCode == "" {
		return ErrInvalidInput
	}
	if err := s.lockContext(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	result, err := s.db.ExecContext(ctx, `UPDATE feishu_deliveries SET state=?,message_id=?,error_code=?,completed_at=? WHERE workspace_id=? AND id=? AND report_id=? AND content_hash=? AND state='running'`, d.State, d.MessageID, d.ErrorCode, d.CompletedAt, workspaceID, d.ID, d.ReportID, d.ContentHash)
	if err != nil {
		return wechatMonitorDBError(ctx)
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return ErrStaleContext
	}
	return nil
}

// Recovery is intentionally non-replaying. We cannot infer whether a model or
// Feishu accepted IO immediately before an abrupt process stop.
func (s *Store) RecoverFeishuRunning(ctx context.Context, now time.Time) error {
	if now.IsZero() {
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
	at := FeishuTime(now)
	if _, err = tx.ExecContext(ctx, `UPDATE feishu_reports SET state='unknown',error_code='FEISHU_INTERRUPTED',generated_at=? WHERE workspace_id=? AND state='running'`, at, workspaceID); err != nil {
		return wechatMonitorDBError(ctx)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE feishu_deliveries SET state='unknown',error_code='FEISHU_INTERRUPTED',completed_at=? WHERE workspace_id=? AND state='running'`, at, workspaceID); err != nil {
		return wechatMonitorDBError(ctx)
	}
	if err = tx.Commit(); err != nil {
		return wechatMonitorDBError(ctx)
	}
	return nil
}

// Each actual per-conversation Analyze consumes one reservation. There is no
// discounted aggregate call, and errors/cancellation do not refund attempts.
func (s *Store) ReserveFeishuCallContext(ctx context.Context, day string, limit int) (string, error) {
	if !validAIDay(day) || limit < 1 || limit > 10000 {
		return "", ErrInvalidInput
	}
	if err := s.lockContext(ctx); err != nil {
		return "", err
	}
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", wechatMonitorDBError(ctx)
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRowContext(ctx, aiCallCountSQL, workspaceID, day, workspaceID, day, workspaceID, day).Scan(&count); err != nil {
		return "", wechatMonitorDBError(ctx)
	}
	if count >= limit {
		return "", ErrQuotaExceeded
	}
	id, err := newAIID()
	if err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO ai_feishu_call_reservations(id,workspace_id,purpose,day,created_at) VALUES(?,?,'feishu_digest',?,?)`, id, workspaceID, day, FeishuTime(time.Now())); err != nil {
		return "", wechatMonitorDBError(ctx)
	}
	if err = tx.Commit(); err != nil {
		return "", wechatMonitorDBError(ctx)
	}
	return id, nil
}

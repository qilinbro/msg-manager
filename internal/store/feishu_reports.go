package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

func (s *Store) LoadFeishuMessages(ctx context.Context, start, end string, limit int) ([]FeishuMessage, error) {
	if !feishuWindowValid(start, end) || limit < 1 || limit > 501 {
		return nil, ErrInvalidInput
	}
	a, _ := time.Parse(time.RFC3339Nano, start)
	b, _ := time.Parse(time.RFC3339Nano, end)
	if err := s.lockContext(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	rows, err := s.db.QueryContext(ctx, `SELECT m.conversation_id,v.title,c.platform,v.conversation_kind,m.id,m.body,m.direction,m.occurred_at,m.is_read,COALESCE(p.revision,0) FROM messages m JOIN conversations v ON v.id=m.conversation_id AND v.workspace_id=m.workspace_id JOIN connections c ON c.id=v.connection_id AND c.workspace_id=v.workspace_id LEFT JOIN ai_preferences p ON p.conversation_id=v.id AND p.workspace_id=v.workspace_id WHERE m.workspace_id=? AND COALESCE(p.mode,'draft_only')<>'paused' AND m.occurred_at>=? AND m.occurred_at<? ORDER BY m.occurred_at DESC,m.seq DESC LIMIT ?`, workspaceID, a.UTC().Format(storedTimeLayout), b.UTC().Format(storedTimeLayout), limit)
	if err != nil {
		return nil, wechatMonitorDBError(ctx)
	}
	defer rows.Close()
	out := []FeishuMessage{}
	for rows.Next() {
		var m FeishuMessage
		var read int
		if err = rows.Scan(&m.ConversationID, &m.Title, &m.Platform, &m.ConversationKind, &m.Message.ID, &m.Message.Body, &m.Message.Direction, &m.Message.OccurredAt, &read, &m.SettingsRevision); err != nil {
			return nil, wechatMonitorDBError(ctx)
		}
		m.Unread = read == 0
		out = append(out, m)
	}
	if rows.Err() != nil {
		return nil, wechatMonitorDBError(ctx)
	}
	return out, nil
}
func checkFeishuSourcesTx(ctx context.Context, tx *sql.Tx, refs []FeishuSourceRef) error {
	if len(refs) > 1024 {
		return ErrInvalidInput
	}
	seen := map[string]bool{}
	for _, ref := range refs {
		if !validResourceID(ref.ConversationID) || ref.SettingsRevision < 0 || seen[ref.ConversationID] {
			return ErrInvalidInput
		}
		seen[ref.ConversationID] = true
		if _, err := aiConversation(tx, ref.ConversationID); err != nil {
			if errors.Is(err, ErrNotFound) {
				return ErrFeishuSourceChanged
			}
			return err
		}
		settings, revision, err := aiSettings(tx, ref.ConversationID)
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrInvalidInput) {
			return ErrFeishuSourceChanged
		}
		if err != nil {
			return err
		}
		if settings.Mode == "paused" || revision != ref.SettingsRevision {
			return ErrFeishuSourceChanged
		}
	}
	return nil
}
func (s *Store) CheckFeishuSourceRefs(ctx context.Context, refs []FeishuSourceRef) error {
	if err := s.lockContext(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wechatMonitorDBError(ctx)
	}
	defer tx.Rollback()
	return checkFeishuSourcesTx(ctx, tx, refs)
}
func (s *Store) CheckFeishuBinding(ctx context.Context, b FeishuBinding) error {
	if err := s.lockContext(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wechatMonitorDBError(ctx)
	}
	defer tx.Rollback()
	return checkFeishuBindingTx(ctx, tx, b)
}

const feishuReportSelect = `SELECT id,window_start,window_end,state,model_config_id,model_config_revision,config_revision,target_binding_id,schedule_revision,trigger,summary,todos_json,excerpts_json,payload_text,content_hash,truncated,error_code,created_at,generated_at FROM feishu_reports WHERE workspace_id=?`

func scanFeishuReport(row interface{ Scan(...any) error }) (FeishuReport, error) {
	r := FeishuReport{Todos: []FeishuTodo{}, Excerpts: []FeishuExcerpt{}, SourceRefs: []FeishuSourceRef{}, ConversationIDs: []string{}}
	var todos, excerpts string
	err := row.Scan(&r.ID, &r.WindowStart, &r.WindowEnd, &r.State, &r.ModelConfigID, &r.ModelConfigRevision, &r.ConfigRevision, &r.TargetBindingID, &r.ScheduleRevision, &r.Trigger, &r.Summary, &todos, &excerpts, &r.Text, &r.ContentHash, &r.Truncated, &r.ErrorCode, &r.CreatedAt, &r.GeneratedAt)
	if err == nil && (json.Unmarshal([]byte(todos), &r.Todos) != nil || json.Unmarshal([]byte(excerpts), &r.Excerpts) != nil) {
		return FeishuReport{}, ErrUnavailable
	}
	return r, err
}
func loadFeishuReportDetails(ctx context.Context, tx *sql.Tx, r *FeishuReport) error {
	rows, err := tx.QueryContext(ctx, `SELECT conversation_id,settings_revision FROM feishu_report_sources WHERE workspace_id=? AND report_id=? ORDER BY conversation_id`, workspaceID, r.ID)
	if err != nil {
		return wechatMonitorDBError(ctx)
	}
	for rows.Next() {
		var ref FeishuSourceRef
		if err = rows.Scan(&ref.ConversationID, &ref.SettingsRevision); err != nil {
			rows.Close()
			return wechatMonitorDBError(ctx)
		}
		r.SourceRefs = append(r.SourceRefs, ref)
		r.ConversationIDs = append(r.ConversationIDs, ref.ConversationID)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return wechatMonitorDBError(ctx)
	}
	r.ConversationCount = len(r.SourceRefs)
	d, err := scanFeishuDelivery(tx.QueryRowContext(ctx, feishuDeliverySelect+` AND report_id=?`, workspaceID, r.ID))
	if err == nil {
		r.Delivery = &d
	} else if !errors.Is(err, sql.ErrNoRows) {
		return wechatMonitorDBError(ctx)
	}
	return nil
}
func (s *Store) feishuReport(ctx context.Context, id string, latest bool) (*FeishuReport, error) {
	if !latest && !validFeishuLedgerID(id, "fr_") {
		return nil, ErrInvalidInput
	}
	if err := s.lockContext(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, wechatMonitorDBError(ctx)
	}
	defer tx.Rollback()
	query := feishuReportSelect + ` AND id=?`
	args := []any{workspaceID, id}
	if latest {
		query = feishuReportSelect + ` ORDER BY window_end DESC LIMIT 1`
		args = []any{workspaceID}
	}
	r, err := scanFeishuReport(tx.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, wechatMonitorDBError(ctx)
	}
	if err = loadFeishuReportDetails(ctx, tx, &r); err != nil {
		return nil, err
	}
	return &r, nil
}
func (s *Store) FeishuReportByID(ctx context.Context, id string) (*FeishuReport, error) {
	return s.feishuReport(ctx, id, false)
}
func (s *Store) LatestFeishuReport(ctx context.Context) (*FeishuReport, error) {
	return s.feishuReport(ctx, "", true)
}

// ClaimFeishuReport claims the natural window, not a model/config revision. A
// failed, unknown or manually previewed window can never be regenerated/reused by
// a later schedule authorization. The schedule cursor advances in the same tx.
func (s *Store) ClaimFeishuReport(ctx context.Context, r FeishuReport, now time.Time) (bool, error) {
	if r.ID != FeishuReportID(r.WindowEnd) || !feishuWindowValid(r.WindowStart, r.WindowEnd) || !r.FeishuBinding.Valid() || len(r.SourceRefs) > 4 || now.IsZero() || (r.Trigger != "manual" && r.Trigger != "schedule") || r.Trigger == "manual" && r.ScheduleRevision != 0 || r.Trigger == "schedule" && r.ScheduleRevision < 1 {
		return false, ErrInvalidInput
	}
	if err := s.lockContext(ctx); err != nil {
		return false, err
	}
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, wechatMonitorDBError(ctx)
	}
	defer tx.Rollback()
	if err = checkFeishuBindingTx(ctx, tx, r.FeishuBinding); err != nil {
		return false, err
	}
	if r.Trigger == "schedule" {
		schedule, e := scanFeishuSchedule(tx.QueryRowContext(ctx, feishuScheduleSelect, workspaceID))
		if e != nil {
			return false, wechatMonitorDBError(ctx)
		}
		end, _ := time.Parse(time.RFC3339Nano, r.WindowEnd)
		if !schedule.Enabled || schedule.Revision != r.ScheduleRevision || schedule.FeishuBinding != r.FeishuBinding || schedule.NextWindowEnd == nil || *schedule.NextWindowEnd != r.WindowEnd || now.Before(end) || now.Sub(end) > FeishuScheduleGrace {
			return false, ErrStaleContext
		}
		if _, err = tx.ExecContext(ctx, `UPDATE feishu_schedules SET next_window_end=?,last_window_end=? WHERE workspace_id=?`, FeishuTime(end.Add(12*time.Hour)), r.WindowEnd, workspaceID); err != nil {
			return false, wechatMonitorDBError(ctx)
		}
	}
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT id FROM feishu_reports WHERE workspace_id=? AND window_end=?`, workspaceID, r.WindowEnd).Scan(&existing)
	if err == nil {
		if err = tx.Commit(); err != nil {
			return false, wechatMonitorDBError(ctx)
		}
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, wechatMonitorDBError(ctx)
	}
	if err = checkFeishuSourcesTx(ctx, tx, r.SourceRefs); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO feishu_reports(id,workspace_id,window_start,window_end,state,model_config_id,model_config_revision,config_revision,target_binding_id,schedule_revision,trigger,truncated,created_at) VALUES(?,?,?,?,'running',?,?,?,?,?,?,?,?)`, r.ID, workspaceID, r.WindowStart, r.WindowEnd, r.ModelConfigID, r.ModelConfigRevision, r.ConfigRevision, r.TargetBindingID, r.ScheduleRevision, r.Trigger, r.Truncated, FeishuTime(now)); err != nil {
		return false, wechatMonitorDBError(ctx)
	}
	for _, ref := range r.SourceRefs {
		if _, err = tx.ExecContext(ctx, `INSERT INTO feishu_report_sources(workspace_id,report_id,conversation_id,settings_revision) VALUES(?,?,?,?)`, workspaceID, r.ID, ref.ConversationID, ref.SettingsRevision); err != nil {
			return false, wechatMonitorDBError(ctx)
		}
	}
	if err = tx.Commit(); err != nil {
		return false, wechatMonitorDBError(ctx)
	}
	return true, nil
}
func (s *Store) CompleteFeishuReport(ctx context.Context, r FeishuReport) error {
	generated, timeOK := feishuCanonicalTime(r.GeneratedAt)
	if r.ID != FeishuReportID(r.WindowEnd) || !feishuWindowValid(r.WindowStart, r.WindowEnd) || !feishuTerminal(r.State) || !timeOK || !feishuErrorCodeValid(r.ErrorCode) || r.State == "succeeded" && r.ErrorCode != "" || r.State != "succeeded" && r.ErrorCode == "" {
		return ErrInvalidInput
	}
	if r.State == "succeeded" && (!feishuText(r.Text, FeishuMaxTextBytes, false) || FeishuSafeText(r.Text) != r.Text || r.ContentHash != FeishuContentHash(r) || !feishuText(r.Summary, 4000, false) || FeishuSafeText(r.Summary) != r.Summary || len(r.Todos) > 4 || len(r.Excerpts) > 3) {
		return ErrInvalidInput
	}
	if r.Todos == nil {
		r.Todos = []FeishuTodo{}
	}
	if r.Excerpts == nil {
		r.Excerpts = []FeishuExcerpt{}
	}
	if r.State != "succeeded" {
		r.Summary = ""
		r.Text = ""
		r.ContentHash = ""
		r.Todos = []FeishuTodo{}
		r.Excerpts = []FeishuExcerpt{}
	}
	todos, _ := json.Marshal(r.Todos)
	excerpts, _ := json.Marshal(r.Excerpts)
	if err := s.lockContext(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wechatMonitorDBError(ctx)
	}
	defer tx.Rollback()
	current, err := scanFeishuReport(tx.QueryRowContext(ctx, feishuReportSelect+` AND id=?`, workspaceID, r.ID))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return wechatMonitorDBError(ctx)
	}
	created, createdErr := time.Parse(time.RFC3339Nano, current.CreatedAt)
	if createdErr != nil || generated.Before(created) {
		return ErrInvalidInput
	}
	if current.State != "running" || current.FeishuBinding != r.FeishuBinding || current.WindowStart != r.WindowStart || current.WindowEnd != r.WindowEnd || current.Trigger != r.Trigger || current.ScheduleRevision != r.ScheduleRevision {
		return ErrStaleContext
	}
	if r.State == "succeeded" {
		if err = checkFeishuBindingTx(ctx, tx, r.FeishuBinding); err != nil {
			return err
		}
		if err = loadFeishuReportDetails(ctx, tx, &current); err != nil {
			return err
		}
		if err = checkFeishuSourcesTx(ctx, tx, current.SourceRefs); err != nil {
			return err
		}
		if err = validateFeishuEvidenceTx(ctx, tx, r, current); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE feishu_reports SET state=?,summary=?,todos_json=?,excerpts_json=?,payload_text=?,content_hash=?,truncated=?,error_code=?,generated_at=? WHERE workspace_id=? AND id=? AND state='running'`, r.State, r.Summary, string(todos), string(excerpts), r.Text, r.ContentHash, r.Truncated, r.ErrorCode, r.GeneratedAt, workspaceID, r.ID)
	if err != nil {
		return wechatMonitorDBError(ctx)
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return ErrStaleContext
	}
	if err = tx.Commit(); err != nil {
		return wechatMonitorDBError(ctx)
	}
	return nil
}

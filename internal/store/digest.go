package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/qilinbro/msg-manager/internal/agent"
)

type DigestSchedule struct {
	Enabled             bool   `json:"enabled"`
	LocalTime           string `json:"local_time"`
	Timezone            string `json:"timezone"`
	Revision            int64  `json:"revision"`
	LastRunDay          string `json:"last_run_day"`
	ModelConfigID       string `json:"model_config_id"`
	ModelConfigRevision int64  `json:"model_config_revision"`
}

type DigestMessage struct {
	ConversationID   string
	Title            string
	Platform         string
	ConversationKind string
	Message          agent.Evidence
	Unread           bool
}

type DigestReminder struct {
	ConversationID   string   `json:"conversation_id"`
	Title            string   `json:"title"`
	Platform         string   `json:"platform"`
	ConversationKind string   `json:"conversation_kind"`
	Priority         string   `json:"priority"`
	Reason           string   `json:"reason"`
	EvidenceIDs      []string `json:"evidence_ids"`
}

type DigestReport struct {
	ID                string           `json:"id"`
	Day               string           `json:"day"`
	State             string           `json:"state"`
	Summary           string           `json:"summary"`
	Reminders         []DigestReminder `json:"reminders"`
	EvidenceIDs       []string         `json:"evidence_ids"`
	ConversationCount int              `json:"conversation_count"`
	ErrorCode         string           `json:"error_code,omitempty"`
	GeneratedAt       *string          `json:"generated_at"`
}

var digestClockPattern = regexp.MustCompile(`^(?:[01][0-9]|2[0-3]):[0-5][0-9]$`)

func digestDayAt(now time.Time) string {
	return now.In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006-01-02")
}

func digestNow() string { return time.Now().UTC().Format(storedTimeLayout) }

func validDigestTime(value string) bool { return digestClockPattern.MatchString(value) }

func scanDigestSchedule(row interface{ Scan(...any) error }) (DigestSchedule, error) {
	var schedule DigestSchedule
	var enabled int
	if err := row.Scan(&enabled, &schedule.LocalTime, &schedule.Timezone, &schedule.Revision, &schedule.LastRunDay, &schedule.ModelConfigID, &schedule.ModelConfigRevision); err != nil {
		return DigestSchedule{}, err
	}
	schedule.Enabled = enabled != 0
	return schedule, nil
}

func (s *Store) LoadDigestSchedule(ctx context.Context) (DigestSchedule, error) {
	if err := s.lockContext(ctx); err != nil {
		return DigestSchedule{}, err
	}
	defer s.mu.Unlock()
	return scanDigestSchedule(s.db.QueryRowContext(ctx, `SELECT enabled,local_time,timezone,revision,last_run_day,model_config_id,model_config_revision FROM digest_schedules WHERE workspace_id=?`, workspaceID))
}

func (s *Store) SaveDigestSchedule(ctx context.Context, next DigestSchedule, expectedRevision int64) (DigestSchedule, error) {
	if expectedRevision < 0 || !validDigestTime(next.LocalTime) || next.Timezone != "Asia/Shanghai" || next.ModelConfigRevision < 0 || next.Enabled && !validProfileConfig(next.ModelConfigID) {
		return DigestSchedule{}, ErrInvalidInput
	}
	if err := s.lockContext(ctx); err != nil {
		return DigestSchedule{}, err
	}
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DigestSchedule{}, ErrUnavailable
	}
	defer tx.Rollback()
	var current int64
	if err = tx.QueryRowContext(ctx, `SELECT revision,last_run_day FROM digest_schedules WHERE workspace_id=?`, workspaceID).Scan(&current, &next.LastRunDay); err != nil {
		return DigestSchedule{}, ErrUnavailable
	}
	if current != expectedRevision {
		return DigestSchedule{}, ErrStaleContext
	}
	if !next.Enabled {
		next.ModelConfigID, next.ModelConfigRevision = "", 0
	}
	next.Revision = current + 1
	if _, err = tx.ExecContext(ctx, `UPDATE digest_schedules SET enabled=?,local_time=?,timezone=?,revision=?,model_config_id=?,model_config_revision=? WHERE workspace_id=?`, next.Enabled, next.LocalTime, next.Timezone, next.Revision, next.ModelConfigID, next.ModelConfigRevision, workspaceID); err != nil {
		return DigestSchedule{}, ErrUnavailable
	}
	if err = tx.Commit(); err != nil {
		return DigestSchedule{}, ErrUnavailable
	}
	return next, nil
}

// DigestReportID is the one canonical identifier used for both claim and finish.
func DigestReportID(day string) string { return stableID("digest-report", day) }

// ClaimDigestRun makes the date idempotent before any model call. Force bypasses
// scheduling, never an existing report (even failed/unknown). It cannot start a
// second model run while the first request is in flight or its result is lost.
// The report remains visibly running after an abrupt process stop.
func (s *Store) ClaimDigestRun(ctx context.Context, day string, force bool) (bool, error) {
	if !validAIDay(day) {
		return false, ErrInvalidInput
	}
	if err := s.lockContext(ctx); err != nil {
		return false, err
	}
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, ErrUnavailable
	}
	defer tx.Rollback()
	var enabled int
	var last string
	if err = tx.QueryRowContext(ctx, `SELECT enabled,last_run_day FROM digest_schedules WHERE workspace_id=?`, workspaceID).Scan(&enabled, &last); err != nil {
		return false, ErrUnavailable
	}
	if !force && (enabled == 0 || last == day) {
		return false, nil
	}
	var state string
	err = tx.QueryRowContext(ctx, `SELECT state FROM digest_reports WHERE workspace_id=? AND day=?`, workspaceID, day).Scan(&state)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, wechatMonitorDBError(ctx)
	}
	now := digestNow()
	id := DigestReportID(day)
	if _, err = tx.ExecContext(ctx, `INSERT INTO digest_reports(id,workspace_id,day,state,created_at) VALUES(?,?,?,'running',?)`, id, workspaceID, day, now); err != nil {
		return false, ErrUnavailable
	}
	if _, err = tx.ExecContext(ctx, `UPDATE digest_schedules SET last_run_day=? WHERE workspace_id=?`, day, workspaceID); err != nil {
		return false, ErrUnavailable
	}
	if err = tx.Commit(); err != nil {
		return false, ErrUnavailable
	}
	return true, nil
}

func (s *Store) LoadDigestMessages(ctx context.Context, start, end string, limit int) ([]DigestMessage, error) {
	if limit < 1 || limit > 1000 {
		return nil, ErrInvalidInput
	}
	startAt, startErr := time.Parse(time.RFC3339Nano, start)
	endAt, endErr := time.Parse(time.RFC3339Nano, end)
	if startErr != nil || endErr != nil || !startAt.Before(endAt) {
		return nil, ErrInvalidInput
	}
	if err := s.lockContext(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	rows, err := s.db.QueryContext(ctx, `SELECT m.conversation_id,v.title,c.platform,v.conversation_kind,m.id,m.body,m.direction,m.occurred_at,m.is_read FROM messages m JOIN conversations v ON v.id=m.conversation_id AND v.workspace_id=m.workspace_id JOIN connections c ON c.id=v.connection_id AND c.workspace_id=v.workspace_id LEFT JOIN ai_preferences p ON p.conversation_id=v.id AND p.workspace_id=v.workspace_id WHERE m.workspace_id=? AND COALESCE(p.mode,'draft_only')<>'paused' AND m.occurred_at>=? AND m.occurred_at<? ORDER BY m.occurred_at DESC,m.seq DESC LIMIT ?`, workspaceID, startAt.UTC().Format(storedTimeLayout), endAt.UTC().Format(storedTimeLayout), limit)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer rows.Close()
	out := []DigestMessage{}
	for rows.Next() {
		var item DigestMessage
		var read int
		if err := rows.Scan(&item.ConversationID, &item.Title, &item.Platform, &item.ConversationKind, &item.Message.ID, &item.Message.Body, &item.Message.Direction, &item.Message.OccurredAt, &read); err != nil {
			return nil, ErrUnavailable
		}
		item.Unread = read == 0
		out = append(out, item)
	}
	if rows.Err() != nil {
		return nil, ErrUnavailable
	}
	return out, nil
}

func (s *Store) CompleteDigestReport(ctx context.Context, report DigestReport) error {
	if report.ID == "" || report.Day == "" || (report.State != "succeeded" && report.State != "failed" && report.State != "unknown") {
		return ErrInvalidInput
	}
	reminders, err := json.Marshal(report.Reminders)
	if err != nil {
		return ErrInvalidInput
	}
	evidence, err := json.Marshal(report.EvidenceIDs)
	if err != nil {
		return ErrInvalidInput
	}
	if err := s.lockContext(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	result, err := s.db.ExecContext(ctx, `UPDATE digest_reports SET state=?,summary=?,reminders_json=?,evidence_json=?,conversation_count=?,error_code=?,generated_at=? WHERE workspace_id=? AND id=? AND day=? AND state='running'`, report.State, report.Summary, string(reminders), string(evidence), report.ConversationCount, nullString(report.ErrorCode), report.GeneratedAt, workspaceID, report.ID, report.Day)
	if err != nil {
		return wechatMonitorDBError(ctx)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return ErrUnavailable
	}
	if changed != 1 {
		return ErrStaleContext
	}
	return nil
}

func scanDigestReport(row interface{ Scan(...any) error }) (DigestReport, error) {
	var report DigestReport
	var reminders, evidence, errorCode, generated sql.NullString
	if err := row.Scan(&report.ID, &report.Day, &report.State, &report.Summary, &reminders, &evidence, &report.ConversationCount, &errorCode, &generated); err != nil {
		return DigestReport{}, err
	}
	if reminders.Valid && json.Unmarshal([]byte(reminders.String), &report.Reminders) != nil {
		return DigestReport{}, ErrUnavailable
	}
	if evidence.Valid && json.Unmarshal([]byte(evidence.String), &report.EvidenceIDs) != nil {
		return DigestReport{}, ErrUnavailable
	}
	if errorCode.Valid {
		report.ErrorCode = errorCode.String
	}
	if generated.Valid {
		report.GeneratedAt = &generated.String
	}
	if report.Reminders == nil {
		report.Reminders = []DigestReminder{}
	}
	if report.EvidenceIDs == nil {
		report.EvidenceIDs = []string{}
	}
	return report, nil
}

func (s *Store) DigestReportForDay(ctx context.Context, day string) (*DigestReport, error) {
	if !validAIDay(day) {
		return nil, ErrInvalidInput
	}
	if err := s.lockContext(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	report, err := scanDigestReport(s.db.QueryRowContext(ctx, `SELECT id,day,state,summary,reminders_json,evidence_json,conversation_count,error_code,generated_at FROM digest_reports WHERE workspace_id=? AND day=?`, workspaceID, day))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, wechatMonitorDBError(ctx)
	}
	return &report, nil
}

func (s *Store) LatestDigestReport(ctx context.Context) (*DigestReport, error) {
	if err := s.lockContext(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	report, err := scanDigestReport(s.db.QueryRowContext(ctx, `SELECT id,day,state,summary,reminders_json,evidence_json,conversation_count,error_code,generated_at FROM digest_reports WHERE workspace_id=? ORDER BY day DESC LIMIT 1`, workspaceID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, ErrUnavailable
	}
	return &report, nil
}

func nullString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

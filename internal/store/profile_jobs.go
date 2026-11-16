package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var ErrProfileInputTooLarge = errors.New("a profile message exceeds the serialized input budget")

const profileJobColumns = `id,conversation_id,kind,state,attempts,next_attempt_at,lease_until,lease_token,fence,model_config_id,settings_revision,max_seq,last_error_code,updated_at,input_hash,manual`

type profileJobRecord struct {
	ProfileJob
	inputHash string
	manual    bool
}
type profileScanner interface{ Scan(...any) error }

func scanProfileJob(row profileScanner) (profileJobRecord, error) {
	var j profileJobRecord
	var next, lease, code sql.NullString
	err := row.Scan(&j.ID, &j.ConversationID, &j.Kind, &j.State, &j.Attempts, &next, &lease, &j.LeaseToken, &j.Fence, &j.ModelConfigID, &j.SettingsRevision, &j.MaxSeq, &code, &j.UpdatedAt, &j.inputHash, &j.manual)
	if err != nil {
		return j, err
	}
	j.ProfileJob.Manual = j.manual
	if next.Valid {
		j.NextAttemptAt = &next.String
	}
	if lease.Valid {
		j.LeaseUntil = &lease.String
	}
	if code.Valid {
		j.LastErrorCode = &code.String
	}
	return j, nil
}
func profileJobs(ctx context.Context, tx *sql.Tx, id string) ([]ProfileJob, error) {
	rows, e := tx.QueryContext(ctx, `SELECT `+profileJobColumns+` FROM profile_jobs WHERE workspace_id=? AND conversation_id=? ORDER BY kind`, workspaceID, id)
	if e != nil {
		return nil, ErrUnavailable
	}
	defer rows.Close()
	out := []ProfileJob{}
	for rows.Next() {
		j, e := scanProfileJob(rows)
		if e != nil {
			return nil, ErrUnavailable
		}
		out = append(out, j.ProfileJob)
	}
	if rows.Err() != nil {
		return nil, ErrUnavailable
	}
	return out, nil
}

func enqueueProfile(ctx context.Context, tx *sql.Tx, p profileRecord, kind, cfg string, manual bool) (ProfileJob, error) {
	if (kind != "organize" && kind != "enrich") || !validProfileConfig(cfg) {
		return ProfileJob{}, ErrInvalidInput
	}
	if p.AIPaused {
		return ProfileJob{}, ErrStaleContext
	}
	if err := ensureProfile(ctx, tx, p); err != nil {
		return ProfileJob{}, err
	}
	if kind == "enrich" {
		if !p.PublicSearchEnabled || p.PublicName == "" || p.ModelConfigID != cfg {
			return ProfileJob{}, ErrStaleContext
		}
	} else if p.MaxSeq == 0 {
		return ProfileJob{}, ErrInvalidInput
	}
	if p.ModelConfigID != cfg {
		if !manual {
			return ProfileJob{}, ErrStaleContext
		}
		if !p.AutoOrganize && !p.PublicSearchEnabled {
			if _, err := tx.ExecContext(ctx, `UPDATE contact_profiles SET model_config_id=?,revision=revision+1,updated_at=? WHERE workspace_id=? AND conversation_id=?`, cfg, profileNow(), workspaceID, p.ConversationID); err != nil {
				return ProfileJob{}, ErrUnavailable
			}
			p.ModelConfigID = cfg
			p.Revision++
			if err := cancelProfileJobs(ctx, tx, p.ConversationID, "", "MODEL_CHANGED"); err != nil {
				return ProfileJob{}, err
			}
		}
	}
	existing, err := scanProfileJob(tx.QueryRowContext(ctx, `SELECT `+profileJobColumns+` FROM profile_jobs WHERE workspace_id=? AND conversation_id=? AND kind=?`, workspaceID, p.ConversationID, kind))
	if err != nil && err != sql.ErrNoRows {
		return ProfileJob{}, ErrUnavailable
	}
	if err == nil && existing.ModelConfigID == cfg && existing.SettingsRevision == p.Revision && (existing.State == "queued" || existing.State == "running") {
		return existing.ProfileJob, nil
	}
	if kind == "enrich" && p.lastEnrichDay == profileDay() {
		if err == nil && !manual {
			return existing.ProfileJob, nil
		}
		return ProfileJob{}, ErrConflict
	}
	if !manual && err == nil && p.ProcessedSeq >= p.MaxSeq && existing.State != "cancelled" && existing.ModelConfigID == cfg && existing.SettingsRevision == p.Revision && existing.MaxSeq >= p.MaxSeq {
		return existing.ProfileJob, nil
	}
	now := profileNow()
	id := stableID("profile-job", p.ConversationID, kind)
	_, err = tx.ExecContext(ctx, `INSERT INTO profile_jobs(id,workspace_id,conversation_id,kind,state,attempts,next_attempt_at,model_config_id,settings_revision,max_seq,updated_at,manual)VALUES(?,?,?,?,'queued',0,?,?,?,?,?,?) ON CONFLICT(workspace_id,conversation_id,kind) DO UPDATE SET state='queued',attempts=0,next_attempt_at=excluded.next_attempt_at,lease_until=NULL,lease_token='',fence=profile_jobs.fence+1,model_config_id=excluded.model_config_id,settings_revision=excluded.settings_revision,max_seq=excluded.max_seq,input_hash='',last_error_code=NULL,updated_at=excluded.updated_at,manual=excluded.manual`, id, workspaceID, p.ConversationID, kind, now, cfg, p.Revision, p.MaxSeq, now, manual)
	if err != nil {
		return ProfileJob{}, ErrUnavailable
	}
	if kind == "enrich" {
		if _, err = tx.ExecContext(ctx, `UPDATE contact_profiles SET last_enrich_day=? WHERE workspace_id=? AND conversation_id=?`, profileDay(), workspaceID, p.ConversationID); err != nil {
			return ProfileJob{}, ErrUnavailable
		}
	}
	j, err := scanProfileJob(tx.QueryRowContext(ctx, `SELECT `+profileJobColumns+` FROM profile_jobs WHERE workspace_id=? AND id=?`, workspaceID, id))
	if err != nil {
		return ProfileJob{}, ErrUnavailable
	}
	return j.ProfileJob, nil
}

func (s *Store) EnqueueProfileJob(ctx context.Context, id, kind, cfg string, expectedRevision ...int64) (ProfileJob, error) {
	if len(expectedRevision) > 1 || len(expectedRevision) == 1 && expectedRevision[0] < 0 {
		return ProfileJob{}, ErrInvalidInput
	}
	if err := s.lockContext(ctx); err != nil {
		return ProfileJob{}, err
	}
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ProfileJob{}, ErrUnavailable
	}
	defer tx.Rollback()
	p, err := profileRow(ctx, tx, id)
	if err != nil {
		return ProfileJob{}, err
	}
	if len(expectedRevision) == 1 && p.Revision != expectedRevision[0] {
		return ProfileJob{}, ErrStaleContext
	}
	j, err := enqueueProfile(ctx, tx, p, kind, cfg, true)
	if err != nil {
		return ProfileJob{}, err
	}
	if tx.Commit() != nil {
		return ProfileJob{}, ErrUnavailable
	}
	return j, nil
}

func (s *Store) ScheduleProfileJobs(ctx context.Context, cfg string) (int, error) {
	if !validProfileConfig(cfg) {
		return 0, ErrInvalidInput
	}
	if err := s.lockContext(ctx); err != nil {
		return 0, err
	}
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, ErrUnavailable
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE profile_jobs SET state='cancelled',lease_token='',lease_until=NULL,next_attempt_at=NULL,fence=fence+1,last_error_code='MODEL_CHANGED',updated_at=? WHERE workspace_id=? AND model_config_id<>? AND state IN ('queued','running')`, profileNow(), workspaceID, cfg); err != nil {
		return 0, ErrUnavailable
	}
	rows, err := tx.QueryContext(ctx, `SELECT p.conversation_id FROM contact_profiles p LEFT JOIN ai_preferences a ON a.workspace_id=p.workspace_id AND a.conversation_id=p.conversation_id WHERE p.workspace_id=? AND p.model_config_id=? AND coalesce(a.mode,'draft_only')<>'paused' AND (
 (p.auto_organize=1 AND EXISTS(SELECT 1 FROM messages m WHERE m.workspace_id=p.workspace_id AND m.conversation_id=p.conversation_id AND m.seq>p.processed_seq)
 AND NOT EXISTS(SELECT 1 FROM profile_jobs j WHERE j.workspace_id=p.workspace_id AND j.conversation_id=p.conversation_id AND j.kind='organize' AND j.model_config_id=p.model_config_id AND j.settings_revision=p.revision AND (j.state IN ('queued','running') OR (j.state='failed' AND j.max_seq>=(SELECT coalesce(max(seq),0) FROM messages m WHERE m.workspace_id=p.workspace_id AND m.conversation_id=p.conversation_id)))))
 OR (p.public_search_enabled=1 AND p.public_name<>'' AND p.last_enrich_day<>? AND NOT EXISTS(SELECT 1 FROM profile_jobs j WHERE j.workspace_id=p.workspace_id AND j.conversation_id=p.conversation_id AND j.kind='enrich' AND j.state IN ('queued','running')))) ORDER BY p.conversation_id LIMIT 1001`, workspaceID, cfg, profileDay())
	if err != nil {
		return 0, ErrUnavailable
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) != nil {
			rows.Close()
			return 0, ErrUnavailable
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, ErrUnavailable
	}
	if len(ids) > 1000 {
		ids = ids[:1000]
	}
	count := 0
	for _, id := range ids {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		p, e := profileRow(ctx, tx, id)
		if e != nil {
			return 0, e
		}
		for _, kind := range []string{"organize", "enrich"} {
			if kind == "organize" && (!p.AutoOrganize || p.MaxSeq <= p.ProcessedSeq) || kind == "enrich" && (!p.PublicSearchEnabled || p.PublicName == "") {
				continue
			}
			job, e := enqueueProfile(ctx, tx, p, kind, cfg, false)
			if e != nil {
				if e == ErrConflict || e == ErrInvalidInput {
					continue
				}
				return 0, e
			}
			if job.State == "queued" {
				count++
			}
		}
	}
	if tx.Commit() != nil {
		return 0, ErrUnavailable
	}
	return count, nil
}

func (s *Store) ClaimProfileJob(ctx context.Context, cfg string, leaseDuration time.Duration) (*ProfileJob, error) {
	if !validProfileConfig(cfg) || leaseDuration <= 0 || leaseDuration > 5*time.Minute {
		return nil, ErrInvalidInput
	}
	if err := s.lockContext(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer tx.Rollback()
	now := profileNow()
	if _, err = tx.ExecContext(ctx, `UPDATE profile_jobs SET state='failed',lease_token='',lease_until=NULL,last_error_code='LEASE_EXHAUSTED',updated_at=? WHERE workspace_id=? AND state='running' AND lease_until<=? AND attempts>=3`, now, workspaceID, now); err != nil {
		return nil, ErrUnavailable
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+profileJobColumns+` FROM profile_jobs WHERE workspace_id=? AND model_config_id=? AND attempts<3 AND ((state='queued' AND (next_attempt_at IS NULL OR next_attempt_at<=?)) OR (state='running' AND lease_until<=?)) AND NOT EXISTS(SELECT 1 FROM profile_jobs other WHERE other.workspace_id=profile_jobs.workspace_id AND other.conversation_id=profile_jobs.conversation_id AND other.state='running' AND other.lease_until>?) ORDER BY updated_at,id LIMIT 100`, workspaceID, cfg, now, now, now)
	if err != nil {
		return nil, ErrUnavailable
	}
	list := []profileJobRecord{}
	for rows.Next() {
		j, e := scanProfileJob(rows)
		if e != nil {
			rows.Close()
			return nil, ErrUnavailable
		}
		list = append(list, j)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, ErrUnavailable
	}
	for _, candidate := range list {
		p, e := profileRow(ctx, tx, candidate.ConversationID)
		if e != nil {
			return nil, e
		}
		if p.AIPaused || p.Revision != candidate.SettingsRevision || p.ModelConfigID != cfg && !(candidate.manual && candidate.Kind == "organize") || candidate.Kind == "enrich" && (!p.PublicSearchEnabled || p.PublicName == "") {
			if e = cancelProfileJobs(ctx, tx, p.ConversationID, candidate.Kind, "SETTINGS_CHANGED"); e != nil {
				return nil, e
			}
			continue
		}
		token, e := newAIID()
		if e != nil {
			return nil, e
		}
		until := time.Now().UTC().Add(leaseDuration).Format(storedTimeLayout)
		result, e := tx.ExecContext(ctx, `UPDATE profile_jobs SET state='running',attempts=attempts+1,lease_token=?,lease_until=?,fence=fence+1,next_attempt_at=NULL,updated_at=? WHERE workspace_id=? AND id=? AND fence=? AND state=?`, token, until, now, workspaceID, candidate.ID, candidate.Fence, candidate.State)
		if e != nil {
			return nil, ErrUnavailable
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			continue
		}
		j, e := scanProfileJob(tx.QueryRowContext(ctx, `SELECT `+profileJobColumns+` FROM profile_jobs WHERE workspace_id=? AND id=?`, workspaceID, candidate.ID))
		if e != nil {
			return nil, ErrUnavailable
		}
		if tx.Commit() != nil {
			return nil, ErrUnavailable
		}
		return &j.ProfileJob, nil
	}
	if tx.Commit() != nil {
		return nil, ErrUnavailable
	}
	return nil, nil
}

func validateProfileLease(ctx context.Context, tx *sql.Tx, claim ProfileJob) (profileJobRecord, profileRecord, error) {
	if !validResourceID(claim.ID) || !validResourceID(claim.ConversationID) || claim.LeaseToken == "" {
		return profileJobRecord{}, profileRecord{}, ErrInvalidInput
	}
	j, e := scanProfileJob(tx.QueryRowContext(ctx, `SELECT `+profileJobColumns+` FROM profile_jobs WHERE workspace_id=? AND conversation_id=? AND id=?`, workspaceID, claim.ConversationID, claim.ID))
	if e == sql.ErrNoRows {
		return j, profileRecord{}, ErrNotFound
	}
	if e != nil {
		return j, profileRecord{}, ErrUnavailable
	}
	if j.State != "running" || j.LeaseToken != claim.LeaseToken || j.Fence != claim.Fence || j.ModelConfigID != claim.ModelConfigID || j.SettingsRevision != claim.SettingsRevision || j.MaxSeq != claim.MaxSeq || j.LeaseUntil == nil || *j.LeaseUntil <= profileNow() {
		return j, profileRecord{}, ErrStaleContext
	}
	p, e := profileRow(ctx, tx, j.ConversationID)
	if e != nil {
		return j, p, e
	}
	if p.AIPaused || p.Revision != j.SettingsRevision || p.ModelConfigID != j.ModelConfigID && !(j.manual && j.Kind == "organize") || j.Kind == "enrich" && (!p.PublicSearchEnabled || p.PublicName == "") {
		return j, p, ErrStaleContext
	}
	return j, p, nil
}

func (s *Store) CheckProfileLease(ctx context.Context, claim ProfileJob) error {
	if err := s.lockContext(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ErrUnavailable
	}
	defer tx.Rollback()
	if _, _, err = validateProfileLease(ctx, tx, claim); err != nil {
		return err
	}
	if tx.Commit() != nil {
		return ErrUnavailable
	}
	return nil
}

func profileInput(ctx context.Context, tx *sql.Tx, j profileJobRecord, p profileRecord) (ProfileJobInput, error) {
	profile, e := loadProfile(ctx, tx, p.ConversationID)
	if e != nil {
		return ProfileJobInput{}, e
	}
	input := ProfileJobInput{Profile: profile, Messages: []AIMessage{}}
	if j.Kind == "organize" {
		var rows *sql.Rows
		if j.Manual {
			// A recent-window manual preview must never claim older history
			// has been processed by the automatic full-history pipeline.
			input.ProcessedThroughSeq = p.ProcessedSeq
			rows, e = tx.QueryContext(ctx, `SELECT seq,id,body,direction,occurred_at FROM messages WHERE workspace_id=? AND conversation_id=? AND seq<=? ORDER BY seq DESC LIMIT 21`, workspaceID, p.ConversationID, j.MaxSeq)
		} else {
			rows, e = tx.QueryContext(ctx, `SELECT seq,id,body,direction,occurred_at FROM messages WHERE workspace_id=? AND conversation_id=? AND seq>? AND seq<=? ORDER BY seq ASC LIMIT 21`, workspaceID, p.ConversationID, p.ProcessedSeq, j.MaxSeq)
		}
		if e != nil {
			return input, ErrUnavailable
		}
		used := 2
		for rows.Next() {
			var m AIMessage
			if rows.Scan(&m.Seq, &m.ID, &m.Body, &m.Direction, &m.OccurredAt) != nil {
				rows.Close()
				return input, ErrUnavailable
			}
			encodedMessage, encodeErr := json.Marshal(m)
			if encodeErr != nil {
				rows.Close()
				return input, ErrUnavailable
			}
			if len(input.Messages) >= 20 || used+len(encodedMessage)+1 > 20000 {
				if len(input.Messages) == 0 {
					rows.Close()
					return input, ErrProfileInputTooLarge
				}
				input.Truncated = true
				break
			}
			input.Messages = append(input.Messages, m)
			used += len(encodedMessage) + 1
			if !j.Manual {
				input.ProcessedThroughSeq = m.Seq
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return input, ErrUnavailable
		}
		if len(input.Messages) == 0 {
			return input, ErrInvalidInput
		}
		if j.Manual {
			for l, r := 0, len(input.Messages)-1; l < r; l, r = l+1, r-1 {
				input.Messages[l], input.Messages[r] = input.Messages[r], input.Messages[l]
			}
		}
	}
	encoded, e := json.Marshal(struct {
		ConversationID   string
		Revision, MaxSeq int64
		Kind, Config     string
		Messages         []AIMessage
	}{p.ConversationID, p.Revision, j.MaxSeq, j.Kind, j.ModelConfigID, input.Messages})
	if e != nil {
		return input, ErrUnavailable
	}
	input.ContextHash = fingerprint("profile-input-v1", string(encoded))
	return input, nil
}

func (s *Store) LoadProfileJobInput(ctx context.Context, claim ProfileJob) (ProfileJobInput, error) {
	if err := s.lockContext(ctx); err != nil {
		return ProfileJobInput{}, err
	}
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ProfileJobInput{}, ErrUnavailable
	}
	defer tx.Rollback()
	j, p, err := validateProfileLease(ctx, tx, claim)
	if err != nil {
		return ProfileJobInput{}, err
	}
	input, err := profileInput(ctx, tx, j, p)
	if err != nil {
		return ProfileJobInput{}, err
	}
	if j.inputHash != "" && j.inputHash != input.ContextHash {
		return ProfileJobInput{}, ErrStaleContext
	}
	if _, err = tx.ExecContext(ctx, `UPDATE profile_jobs SET input_hash=? WHERE workspace_id=? AND id=? AND lease_token=? AND fence=?`, input.ContextHash, workspaceID, j.ID, j.LeaseToken, j.Fence); err != nil {
		return ProfileJobInput{}, ErrUnavailable
	}
	if tx.Commit() != nil {
		return ProfileJobInput{}, ErrUnavailable
	}
	return input, nil
}

func (s *Store) FailProfileJob(ctx context.Context, claim ProfileJob, code string, retryable bool) error {
	if len(code) < 1 || len(code) > 64 {
		return ErrInvalidInput
	}
	for _, c := range code {
		if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			return ErrInvalidInput
		}
	}
	if err := s.lockContext(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ErrUnavailable
	}
	defer tx.Rollback()
	j, _, err := validateProfileLease(ctx, tx, claim)
	if err != nil {
		return err
	}
	state := "failed"
	var next any
	attempts := j.Attempts
	if code == "PROFILE_CANCELLED" {
		state = "queued"
		next = profileNow()
		if attempts > 0 {
			attempts--
		}
	} else if code == "AI_QUOTA_EXCEEDED" || code == "SEARCH_QUOTA_EXCEEDED" {
		state = "queued"
		zone := time.FixedZone("Asia/Shanghai", 8*3600)
		now := time.Now().In(zone)
		next = time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, zone).UTC().Format(storedTimeLayout)
		if attempts > 0 {
			attempts--
		}
	} else if retryable && j.Attempts < 3 {
		state = "queued"
		next = time.Now().UTC().Add(time.Duration(j.Attempts*j.Attempts) * 30 * time.Second).Format(storedTimeLayout)
	}
	_, err = tx.ExecContext(ctx, `UPDATE profile_jobs SET state=?,attempts=?,next_attempt_at=?,lease_until=NULL,lease_token='',fence=fence+1,last_error_code=?,updated_at=? WHERE workspace_id=? AND id=? AND lease_token=? AND fence=?`, state, attempts, next, code, profileNow(), workspaceID, j.ID, j.LeaseToken, j.Fence)
	if err != nil {
		return ErrUnavailable
	}
	if tx.Commit() != nil {
		return ErrUnavailable
	}
	return nil
}

// Legacy AI pause applies to every profile model/search operation as well.
func cancelPausedProfileJobs(ctx context.Context, tx *sql.Tx, id string) error {
	return cancelProfileJobs(ctx, tx, id, "", "AI_PAUSED")
}

func normalizeProfileEvidence(ids []string, known map[string]bool, required bool) ([]string, error) {
	if len(ids) > 20 || required && len(ids) == 0 {
		return nil, ErrInvalidInput
	}
	out := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if !validResourceID(id) || seen[id] || !known[id] {
			return nil, ErrInvalidInput
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

func profileSafeCode(value string) bool {
	return strings.Trim(value, "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_") == "" && len(value) > 0 && len(value) <= 64
}

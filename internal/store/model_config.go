package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/url"
)

// ModelConfig is never serialized to an HTTP response. APIKey is write-only at
// the API boundary and the whole record is persisted only in SQLCipher.
type ModelConfig struct {
	BaseURL    string `json:"-"`
	Model      string `json:"-"`
	APIKey     string `json:"-"`
	DailyLimit int    `json:"-"`
	Revision   int64  `json:"-"`
}

func (ModelConfig) String() string   { return "model configuration (redacted)" }
func (ModelConfig) GoString() string { return "model configuration (redacted)" }

func (s *Store) LoadModelConfig(ctx context.Context) (*ModelConfig, error) {
	if err := s.lockContext(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	var result ModelConfig
	err := s.db.QueryRowContext(ctx, `SELECT base_url,model,api_key,daily_limit,revision FROM ai_model_config WHERE workspace_id=?`, workspaceID).Scan(&result.BaseURL, &result.Model, &result.APIKey, &result.DailyLimit, &result.Revision)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, wechatMonitorDBError(ctx)
	}
	return &result, nil
}

// SaveModelConfig uses a monotonic revision so a stale browser cannot silently
// replace a newly saved model or key. The caller validates transport policy.
func (s *Store) SaveModelConfig(ctx context.Context, input ModelConfig, expectedRevision int64, previousTarget ...string) (ModelConfig, error) {
	if len(previousTarget) > 1 || len(previousTarget) == 1 && previousTarget[0] != "" && !validProfileConfig(previousTarget[0]) || expectedRevision < 0 || !profileText(input.BaseURL, 2048, false) || input.BaseURL == "" || !profileText(input.Model, 128, false) || input.Model == "" || !profileText(input.APIKey, 4096, false) || input.DailyLimit < 1 || input.DailyLimit > 1000 {
		return ModelConfig{}, ErrInvalidInput
	}
	if err := s.lockContext(ctx); err != nil {
		return ModelConfig{}, err
	}
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ModelConfig{}, wechatMonitorDBError(ctx)
	}
	defer tx.Rollback()
	var revision int64
	var previousURL, previousModel string
	err = tx.QueryRowContext(ctx, `SELECT revision,base_url,model FROM ai_model_config WHERE workspace_id=?`, workspaceID).Scan(&revision, &previousURL, &previousModel)
	if err != nil && err != sql.ErrNoRows {
		return ModelConfig{}, wechatMonitorDBError(ctx)
	}
	if revision != expectedRevision {
		return ModelConfig{}, ErrStaleContext
	}
	targetChanged := err == sql.ErrNoRows || previousURL != input.BaseURL || previousModel != input.Model
	if err == sql.ErrNoRows && len(previousTarget) == 1 {
		// The first saved selection may replace an environment-configured model.
		// Compare the live destination too; absence of a durable row is not an
		// authorization to revive grants for a previously used destination.
		targetChanged = previousTarget[0] != modelConfigTarget(input)
	}
	input.Revision = revision + 1
	_, err = tx.ExecContext(ctx, `INSERT INTO ai_model_config(workspace_id,base_url,model,api_key,daily_limit,revision) VALUES(?,?,?,?,?,?) ON CONFLICT(workspace_id) DO UPDATE SET base_url=excluded.base_url,model=excluded.model,api_key=excluded.api_key,daily_limit=excluded.daily_limit,revision=excluded.revision`, workspaceID, input.BaseURL, input.Model, input.APIKey, input.DailyLimit, input.Revision)
	if err != nil {
		return ModelConfig{}, wechatMonitorDBError(ctx)
	}
	if targetChanged {
		// Revoke persistently, rather than merely ignoring a nonmatching hash.
		// Otherwise selecting A, then B, then A could restore old grants without
		// another user confirmation. Preserve all user notes and derived facts.
		now := profileNow()
		profiles, e := tx.ExecContext(ctx, `UPDATE contact_profiles SET auto_organize=0,public_search_enabled=0,model_config_id='',revision=revision+1,updated_at=? WHERE workspace_id=? AND (auto_organize<>0 OR public_search_enabled<>0 OR model_config_id<>'')`, now, workspaceID)
		if e != nil {
			return ModelConfig{}, wechatMonitorDBError(ctx)
		}
		jobs, e := tx.ExecContext(ctx, `UPDATE profile_jobs SET state='cancelled',lease_token='',lease_until=NULL,next_attempt_at=NULL,fence=fence+1,last_error_code='MODEL_CHANGED',updated_at=? WHERE workspace_id=? AND state IN ('queued','running')`, now, workspaceID)
		if e != nil {
			return ModelConfig{}, wechatMonitorDBError(ctx)
		}
		profilesChanged, _ := profiles.RowsAffected()
		jobsChanged, _ := jobs.RowsAffected()
		if profilesChanged > 0 || jobsChanged > 0 {
			if _, e = tx.ExecContext(ctx, `UPDATE workspaces SET revision=revision+1 WHERE id=?`, workspaceID); e != nil {
				return ModelConfig{}, wechatMonitorDBError(ctx)
			}
		}
		// Manual AI previews and saved drafts bind to the conversation's
		// preference revision. Bumping it here prevents selecting A, then B,
		// then A from reviving an old A confirmation even when the destination
		// fingerprint is identical again. Existing tone/mode values remain.
		if _, e = tx.ExecContext(ctx, `INSERT INTO ai_preferences(conversation_id,workspace_id,connection_id,contact_id,mode,tone,revision)
SELECT id,workspace_id,connection_id,contact_id,'draft_only','natural',1 FROM conversations WHERE workspace_id=?
ON CONFLICT(conversation_id) DO UPDATE SET revision=ai_preferences.revision+1`, workspaceID); e != nil {
			return ModelConfig{}, wechatMonitorDBError(ctx)
		}
	}
	// Recurring digest consent binds to the exact model configuration revision,
	// including key-only or quota-only saves. A→B→A must not revive an old grant.
	if _, err = tx.ExecContext(ctx, `UPDATE digest_schedules SET enabled=0,model_config_id='',model_config_revision=0,revision=revision+1 WHERE workspace_id=? AND (enabled<>0 OR model_config_id<>'' OR model_config_revision<>0)`, workspaceID); err != nil {
		return ModelConfig{}, wechatMonitorDBError(ctx)
	}
	if err = tx.Commit(); err != nil {
		return ModelConfig{}, wechatMonitorDBError(ctx)
	}
	return input, nil
}

func modelConfigTarget(input ModelConfig) string {
	base := input.BaseURL
	if parsed, err := url.Parse(base); err == nil {
		base = parsed.String()
	}
	encoded, _ := json.Marshal([]string{base, input.Model})
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:])
}

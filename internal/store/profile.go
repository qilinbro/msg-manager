package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/qilinbro/msg-manager/internal/publicinfo"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var profileCategories = map[string]bool{"unclassified": true, "friend": true, "family": true, "colleague": true, "client": true, "partner": true, "other": true}
var profileFields = map[string]bool{"name": true, "company": true, "role": true, "interest": true, "project": true, "preference": true, "public_bio": true}

type profileRecord struct {
	Profile
	connectionID      string
	documentTruncated bool
	lastEnrichDay     string
}

func profileNow() string { return time.Now().UTC().Format(storedTimeLayout) }
func profileDay() string {
	return time.Now().In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("2006-01-02")
}
func validProfileConfig(value string) bool {
	b, e := hex.DecodeString(value)
	return e == nil && len(value) == 64 && len(b) == 32
}
func profileText(value string, limit int, multiline bool) bool {
	if !utf8.ValidString(value) || len(value) > limit {
		return false
	}
	for _, c := range value {
		if unicode.IsControl(c) && !(multiline && (c == '\n' || c == '\r' || c == '\t')) {
			return false
		}
	}
	return true
}

func profileRow(ctx context.Context, tx *sql.Tx, id string) (profileRecord, error) {
	c, err := aiConversation(tx, id)
	if err != nil {
		return profileRecord{}, err
	}
	p := profileRecord{Profile: Profile{ConversationID: id, ContactID: c.ContactID, DisplayName: c.Title, Platform: c.Platform, Category: "unclassified", AICategory: "unclassified", Tags: []string{}, EvidenceIDs: []string{}, Facts: []ProfileFact{}, Sources: []ProfileSource{}, Jobs: []ProfileJob{}}, connectionID: c.ConnectionID}
	if tx.QueryRowContext(ctx, `SELECT peer_id FROM conversations WHERE workspace_id=? AND connection_id=? AND contact_id=? AND id=?`, workspaceID, c.ConnectionID, c.ContactID, id).Scan(&p.PeerID) != nil {
		return profileRecord{}, ErrUnavailable
	}
	var tags, evidence string
	var auto, search, truncated int
	var updated string
	err = tx.QueryRowContext(ctx, `SELECT public_name,company,role,manual_notes,category_override,auto_organize,public_search_enabled,model_config_id,revision,processed_seq,ai_category,tags_json,summary,evidence_json,document_truncated,last_enrich_day,updated_at FROM contact_profiles WHERE workspace_id=? AND connection_id=? AND contact_id=? AND conversation_id=?`, workspaceID, c.ConnectionID, c.ContactID, id).Scan(&p.PublicName, &p.Company, &p.Role, &p.ManualNotes, &p.CategoryOverride, &auto, &search, &p.ModelConfigID, &p.Revision, &p.ProcessedSeq, &p.AICategory, &tags, &p.Summary, &evidence, &truncated, &p.lastEnrichDay, &updated)
	if err != nil && err != sql.ErrNoRows {
		return profileRecord{}, ErrUnavailable
	}
	if err == nil {
		if json.Unmarshal([]byte(tags), &p.Tags) != nil || json.Unmarshal([]byte(evidence), &p.EvidenceIDs) != nil || p.Tags == nil || p.EvidenceIDs == nil {
			return profileRecord{}, ErrUnavailable
		}
		p.AutoOrganize = auto == 1
		p.PublicSearchEnabled = search == 1
		p.documentTruncated = truncated == 1
		p.UpdatedAt = &updated
	}
	if tx.QueryRowContext(ctx, `SELECT coalesce(max(seq),0),count(*),coalesce(sum(CASE WHEN seq<=? THEN 1 ELSE 0 END),0) FROM messages WHERE workspace_id=? AND conversation_id=?`, p.ProcessedSeq, workspaceID, id).Scan(&p.MaxSeq, &p.AnalysisTotal, &p.AnalysisProcessed) != nil {
		return profileRecord{}, ErrUnavailable
	}
	settings, _, err := aiSettings(tx, id)
	if err != nil {
		return profileRecord{}, err
	}
	p.AIPaused = settings.Mode == "paused"
	p.Category = p.AICategory
	if p.CategoryOverride != "" {
		p.Category = p.CategoryOverride
	}
	p.Truncated = p.documentTruncated
	return p, nil
}

func ensureProfile(ctx context.Context, tx *sql.Tx, p profileRecord) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO contact_profiles(conversation_id,workspace_id,connection_id,contact_id,updated_at)VALUES(?,?,?,?,?) ON CONFLICT(conversation_id) DO NOTHING`, p.ConversationID, workspaceID, p.connectionID, p.ContactID, profileNow())
	if err != nil {
		return ErrUnavailable
	}
	return nil
}

func profileStrings(ctx context.Context, tx *sql.Tx, table, column, conversation, fact string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+column+` FROM `+table+` WHERE workspace_id=? AND conversation_id=? AND fact_id=? ORDER BY `+column, workspaceID, conversation, fact)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var v string
		if rows.Scan(&v) != nil {
			return nil, ErrUnavailable
		}
		out = append(out, v)
	}
	if rows.Err() != nil {
		return nil, ErrUnavailable
	}
	return out, nil
}

func loadProfile(ctx context.Context, tx *sql.Tx, id string) (Profile, error) {
	p, err := profileRow(ctx, tx, id)
	if err != nil {
		return Profile{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,field,value,state,origin FROM profile_facts WHERE workspace_id=? AND conversation_id=? ORDER BY CASE state WHEN 'confirmed' THEN 0 WHEN 'unverified' THEN 1 ELSE 2 END,updated_at DESC,id LIMIT 101`, workspaceID, id)
	if err != nil {
		return Profile{}, ErrUnavailable
	}
	for rows.Next() {
		var f ProfileFact
		if rows.Scan(&f.ID, &f.Field, &f.Value, &f.State, &f.Origin) != nil {
			rows.Close()
			return Profile{}, ErrUnavailable
		}
		p.Facts = append(p.Facts, f)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return Profile{}, ErrUnavailable
	}
	if len(p.Facts) > 100 {
		p.Facts = p.Facts[:100]
		p.Truncated = true
	}
	for i := range p.Facts {
		p.Facts[i].MessageIDs, err = profileStrings(ctx, tx, "profile_fact_messages", "message_id", id, p.Facts[i].ID)
		if err != nil {
			return Profile{}, err
		}
		p.Facts[i].SourceIDs, err = profileStrings(ctx, tx, "profile_fact_sources", "source_id", id, p.Facts[i].ID)
		if err != nil {
			return Profile{}, err
		}
	}
	rows, err = tx.QueryContext(ctx, `SELECT id,url,title,snippet,content_hash,retrieved_at FROM profile_sources WHERE workspace_id=? AND conversation_id=? ORDER BY retrieved_at DESC,id LIMIT 201`, workspaceID, id)
	if err != nil {
		return Profile{}, ErrUnavailable
	}
	for rows.Next() {
		var v ProfileSource
		if rows.Scan(&v.ID, &v.URL, &v.Title, &v.Snippet, &v.ContentHash, &v.RetrievedAt) != nil {
			rows.Close()
			return Profile{}, ErrUnavailable
		}
		p.Sources = append(p.Sources, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return Profile{}, ErrUnavailable
	}
	if len(p.Sources) > 200 {
		p.Sources = p.Sources[:200]
		p.Truncated = true
	}
	p.Jobs, err = profileJobs(ctx, tx, id)
	if err != nil {
		return Profile{}, err
	}
	p.DocumentMarkdown = profileMarkdown(p.Profile)
	if strings.HasPrefix(p.DocumentMarkdown, "当前文档已截断") {
		p.Truncated = true
	}
	return p.Profile, nil
}

func (s *Store) GetProfile(ctx context.Context, id string) (Profile, error) {
	if err := s.lockContext(ctx); err != nil {
		return Profile{}, err
	}
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Profile{}, ErrUnavailable
	}
	defer tx.Rollback()
	p, err := loadProfile(ctx, tx, id)
	if err != nil {
		return Profile{}, err
	}
	if tx.Commit() != nil {
		return Profile{}, ErrUnavailable
	}
	return p, nil
}

func cancelProfileJobs(ctx context.Context, tx *sql.Tx, id, kind, code string) error {
	query := `UPDATE profile_jobs SET state='cancelled',lease_token='',lease_until=NULL,next_attempt_at=NULL,fence=fence+1,last_error_code=?,updated_at=? WHERE workspace_id=? AND conversation_id=? AND state IN ('queued','running')`
	args := []any{code, profileNow(), workspaceID, id}
	if kind != "" {
		query += ` AND kind=?`
		args = append(args, kind)
	}
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return ErrUnavailable
	}
	return nil
}

func (s *Store) UpdateProfile(ctx context.Context, id string, patch ProfilePatch) (Profile, error) {
	if patch.ExpectedRevision == nil || *patch.ExpectedRevision < 0 {
		return Profile{}, ErrInvalidInput
	}
	for _, field := range []struct {
		value *string
		limit int
		multi bool
	}{{patch.PublicName, 256, false}, {patch.Company, 256, false}, {patch.Role, 256, false}, {patch.ManualNotes, 8000, true}} {
		if field.value != nil && !profileText(*field.value, field.limit, field.multi) {
			return Profile{}, ErrInvalidInput
		}
	}
	if patch.CategoryOverride != nil && *patch.CategoryOverride != "" && !profileCategories[*patch.CategoryOverride] {
		return Profile{}, ErrInvalidInput
	}
	if patch.ModelConfigID != nil && *patch.ModelConfigID != "" && !validProfileConfig(*patch.ModelConfigID) {
		return Profile{}, ErrInvalidInput
	}
	if err := s.lockContext(ctx); err != nil {
		return Profile{}, err
	}
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Profile{}, ErrUnavailable
	}
	defer tx.Rollback()
	p, err := profileRow(ctx, tx, id)
	if err != nil {
		return Profile{}, err
	}
	if p.Revision != *patch.ExpectedRevision {
		return Profile{}, ErrStaleContext
	}
	if err = ensureProfile(ctx, tx, p); err != nil {
		return Profile{}, err
	}
	before := fmt.Sprintf("%q", []any{p.PublicName, p.Company, p.Role, p.ManualNotes, p.CategoryOverride, p.AutoOrganize, p.PublicSearchEnabled, p.ModelConfigID})
	wasAuto, wasSearch := p.AutoOrganize, p.PublicSearchEnabled
	if patch.PublicName != nil {
		p.PublicName = strings.TrimSpace(*patch.PublicName)
	}
	if patch.Company != nil {
		p.Company = strings.TrimSpace(*patch.Company)
	}
	if patch.Role != nil {
		p.Role = strings.TrimSpace(*patch.Role)
	}
	if patch.ManualNotes != nil {
		p.ManualNotes = *patch.ManualNotes
	}
	if patch.CategoryOverride != nil {
		p.CategoryOverride = *patch.CategoryOverride
	}
	if patch.AutoOrganize != nil {
		p.AutoOrganize = *patch.AutoOrganize
	}
	if patch.PublicSearchEnabled != nil {
		p.PublicSearchEnabled = *patch.PublicSearchEnabled
	}
	if patch.ModelConfigID != nil {
		p.ModelConfigID = *patch.ModelConfigID
	}
	if (p.AutoOrganize || p.PublicSearchEnabled) && !validProfileConfig(p.ModelConfigID) || p.AIPaused && ((p.AutoOrganize && !wasAuto) || (p.PublicSearchEnabled && !wasSearch)) || p.PublicSearchEnabled && p.PublicName == "" {
		return Profile{}, ErrStaleContext
	}
	after := fmt.Sprintf("%q", []any{p.PublicName, p.Company, p.Role, p.ManualNotes, p.CategoryOverride, p.AutoOrganize, p.PublicSearchEnabled, p.ModelConfigID})
	if before != after {
		_, err = tx.ExecContext(ctx, `UPDATE contact_profiles SET public_name=?,company=?,role=?,manual_notes=?,category_override=?,auto_organize=?,public_search_enabled=?,model_config_id=?,revision=revision+1,updated_at=? WHERE workspace_id=? AND conversation_id=?`, p.PublicName, p.Company, p.Role, p.ManualNotes, p.CategoryOverride, p.AutoOrganize, p.PublicSearchEnabled, p.ModelConfigID, profileNow(), workspaceID, id)
		if err != nil {
			return Profile{}, ErrUnavailable
		}
		if err = cancelProfileJobs(ctx, tx, id, "", "SETTINGS_CHANGED"); err != nil {
			return Profile{}, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE workspaces SET revision=revision+1 WHERE id=?`, workspaceID); err != nil {
			return Profile{}, ErrUnavailable
		}
	}
	result, err := loadProfile(ctx, tx, id)
	if err != nil {
		return Profile{}, err
	}
	if tx.Commit() != nil {
		return Profile{}, ErrUnavailable
	}
	return result, nil
}

func (s *Store) SetProfileFact(ctx context.Context, id, factID, state string, expectedRev int64) (Profile, error) {
	if !validResourceID(factID) || expectedRev < 0 || (state != "confirmed" && state != "rejected") {
		return Profile{}, ErrInvalidInput
	}
	if err := s.lockContext(ctx); err != nil {
		return Profile{}, err
	}
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Profile{}, ErrUnavailable
	}
	defer tx.Rollback()
	p, err := profileRow(ctx, tx, id)
	if err != nil {
		return Profile{}, err
	}
	if p.Revision != expectedRev {
		return Profile{}, ErrStaleContext
	}
	var current string
	err = tx.QueryRowContext(ctx, `SELECT state FROM profile_facts WHERE workspace_id=? AND conversation_id=? AND id=?`, workspaceID, id, factID).Scan(&current)
	if err == sql.ErrNoRows {
		return Profile{}, ErrNotFound
	}
	if err != nil {
		return Profile{}, ErrUnavailable
	}
	if current != state {
		if _, err = tx.ExecContext(ctx, `UPDATE profile_facts SET state=?,updated_at=? WHERE workspace_id=? AND conversation_id=? AND id=?`, state, profileNow(), workspaceID, id, factID); err != nil {
			return Profile{}, ErrUnavailable
		}
		if _, err = tx.ExecContext(ctx, `UPDATE contact_profiles SET revision=revision+1,updated_at=? WHERE workspace_id=? AND conversation_id=?`, profileNow(), workspaceID, id); err != nil {
			return Profile{}, ErrUnavailable
		}
		if err = cancelProfileJobs(ctx, tx, id, "", "FACT_REVIEWED"); err != nil {
			return Profile{}, err
		}
	}
	result, err := loadProfile(ctx, tx, id)
	if err != nil {
		return Profile{}, err
	}
	if tx.Commit() != nil {
		return Profile{}, ErrUnavailable
	}
	return result, nil
}

func ProfileSourceID(conversationID, sourceURL, contentHash string) string {
	return stableID("profile-source", conversationID, sourceURL, contentHash)
}

func safeProfileURL(value string) bool {
	return len(value) <= 2048 && publicinfo.SafeURL(value)
}

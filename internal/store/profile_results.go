package store

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qilinbro/msg-manager/internal/publicinfo"
)

func normalizeProfileResult(conversation string, j profileJobRecord, input ProfileJobInput, r ProfileJobResult) (ProfileJobResult, error) {
	if len(r.Facts) > 12 || len(r.Sources) > 5 || len(r.Tags) > 8 || !profileText(r.Summary, 3000, true) {
		return r, ErrInvalidInput
	}
	messages := map[string]bool{}
	for _, m := range input.Messages {
		messages[m.ID] = true
	}
	sources := map[string]bool{}
	for _, s := range input.Profile.Sources {
		sources[s.ID] = true
	}
	seenSources := map[string]bool{}
	for i, s := range r.Sources {
		at, e := time.Parse(time.RFC3339Nano, s.RetrievedAt)
		if !safeProfileURL(s.URL) || !profileText(s.Title, 512, false) || strings.TrimSpace(s.Title) == "" || !profileText(s.Snippet, 4000, true) || !validProfileConfig(s.ContentHash) || e != nil || at.After(time.Now().Add(24*time.Hour)) || s.ID != ProfileSourceID(conversation, s.URL, s.ContentHash) || seenSources[s.ID] || publicinfo.SensitiveText(s.Title) || publicinfo.SensitiveText(s.Snippet) {
			return r, ErrInvalidInput
		}
		s.RetrievedAt = at.UTC().Format(storedTimeLayout)
		r.Sources[i] = s
		sources[s.ID] = true
		seenSources[s.ID] = true
	}
	var err error
	if j.Kind == "organize" {
		if !profileCategories[r.Category] || strings.TrimSpace(r.Summary) == "" || len(r.Sources) != 0 || len(r.SourceIDs) != 0 {
			return r, ErrInvalidInput
		}
		r.EvidenceIDs, err = normalizeProfileEvidence(r.EvidenceIDs, messages, true)
		if err != nil {
			return r, err
		}
	} else {
		if len(r.EvidenceIDs) != 0 {
			return r, ErrInvalidInput
		}
		r.SourceIDs, err = normalizeProfileEvidence(r.SourceIDs, sources, r.Summary != "")
		if err != nil {
			return r, err
		}
		if publicinfo.SensitiveText(r.Summary) {
			return r, ErrInvalidInput
		}
	}
	seenTags := map[string]bool{}
	for _, tag := range r.Tags {
		if strings.TrimSpace(tag) == "" || !profileText(tag, 100, false) || seenTags[tag] {
			return r, ErrInvalidInput
		}
		seenTags[tag] = true
	}
	if r.Tags == nil {
		r.Tags = []string{}
	}
	for i, f := range r.Facts {
		if !profileFields[f.Field] || !profileText(f.Value, 1000, true) || strings.TrimSpace(f.Value) == "" {
			return r, ErrInvalidInput
		}
		if j.Kind == "organize" {
			if len(f.SourceIDs) != 0 {
				return r, ErrInvalidInput
			}
			f.MessageIDs, err = normalizeProfileEvidence(f.MessageIDs, messages, true)
		} else {
			if len(f.MessageIDs) != 0 || publicinfo.SensitiveText(f.Value) {
				return r, ErrInvalidInput
			}
			f.SourceIDs, err = normalizeProfileEvidence(f.SourceIDs, sources, true)
		}
		if err != nil {
			return r, err
		}
		if f.MessageIDs == nil {
			f.MessageIDs = []string{}
		}
		if f.SourceIDs == nil {
			f.SourceIDs = []string{}
		}
		sort.Strings(f.MessageIDs)
		sort.Strings(f.SourceIDs)
		r.Facts[i] = f
	}
	return r, nil
}

func (s *Store) CompleteProfileJob(ctx context.Context, claim ProfileJob, result ProfileJobResult) error {
	if err := s.lockContext(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ErrUnavailable
	}
	defer tx.Rollback()
	j, p, err := validateProfileLease(ctx, tx, claim)
	if err != nil {
		return err
	}
	input, err := profileInput(ctx, tx, j, p)
	if err != nil {
		return err
	}
	if j.inputHash == "" || j.inputHash != input.ContextHash {
		return ErrStaleContext
	}
	result, err = normalizeProfileResult(p.ConversationID, j, input, result)
	if err != nil {
		return err
	}
	now := profileNow()
	for _, source := range result.Sources {
		_, err = tx.ExecContext(ctx, `INSERT INTO profile_sources(id,workspace_id,conversation_id,url,title,snippet,content_hash,retrieved_at)VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, source.ID, workspaceID, p.ConversationID, source.URL, source.Title, source.Snippet, source.ContentHash, source.RetrievedAt)
		if err != nil {
			return ErrUnavailable
		}
	}
	publicSummaryTruncated := j.Kind == "enrich" && len(result.Summary) > 1000
	if j.Kind == "enrich" && result.Summary != "" {
		value := result.Summary
		if len(value) > 1000 {
			value = value[:1000]
			for !utf8.ValidString(value) {
				value = value[:len(value)-1]
			}
		}
		result.Facts = append(result.Facts, ProfileFactInput{Field: "public_bio", Value: value, MessageIDs: []string{}, SourceIDs: result.SourceIDs})
	}
	origin := "chat"
	if j.Kind == "enrich" {
		origin = "public"
	}
	for _, f := range result.Facts {
		sort.Strings(f.MessageIDs)
		sort.Strings(f.SourceIDs)
		id := stableID("profile-fact", p.ConversationID, origin, f.Field, f.Value, strings.Join(f.MessageIDs, "\x00"), strings.Join(f.SourceIDs, "\x00"))
		_, err = tx.ExecContext(ctx, `INSERT INTO profile_facts(id,workspace_id,conversation_id,field,value,state,origin,created_at,updated_at)VALUES(?,?,?,?,?,'unverified',?,?,?) ON CONFLICT(id) DO NOTHING`, id, workspaceID, p.ConversationID, f.Field, f.Value, origin, now, now)
		if err != nil {
			return ErrUnavailable
		}
		for _, message := range f.MessageIDs {
			if _, err = tx.ExecContext(ctx, `INSERT INTO profile_fact_messages(workspace_id,conversation_id,fact_id,message_id)VALUES(?,?,?,?) ON CONFLICT(fact_id,message_id) DO NOTHING`, workspaceID, p.ConversationID, id, message); err != nil {
				return ErrUnavailable
			}
		}
		for _, source := range f.SourceIDs {
			if _, err = tx.ExecContext(ctx, `INSERT INTO profile_fact_sources(workspace_id,conversation_id,fact_id,source_id)VALUES(?,?,?,?) ON CONFLICT(fact_id,source_id) DO NOTHING`, workspaceID, p.ConversationID, id, source); err != nil {
				return ErrUnavailable
			}
		}
	}
	if j.Kind == "organize" {
		tags, _ := json.Marshal(result.Tags)
		evidence, _ := json.Marshal(result.EvidenceIDs)
		_, err = tx.ExecContext(ctx, `UPDATE contact_profiles SET ai_category=?,tags_json=?,summary=?,evidence_json=?,processed_seq=MAX(processed_seq,?),document_truncated=MAX(document_truncated,?),updated_at=? WHERE workspace_id=? AND conversation_id=? AND revision=?`, result.Category, string(tags), result.Summary, string(evidence), j.MaxSeq, input.Truncated, now, workspaceID, p.ConversationID, p.Revision)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE contact_profiles SET document_truncated=MAX(document_truncated,?),updated_at=? WHERE workspace_id=? AND conversation_id=? AND revision=?`, publicSummaryTruncated, now, workspaceID, p.ConversationID, p.Revision)
	}
	if err != nil {
		return ErrUnavailable
	}
	resultSQL, err := tx.ExecContext(ctx, `UPDATE profile_jobs SET state='succeeded',lease_token='',lease_until=NULL,next_attempt_at=NULL,last_error_code=NULL,updated_at=? WHERE workspace_id=? AND id=? AND state='running' AND lease_token=? AND fence=?`, now, workspaceID, j.ID, j.LeaseToken, j.Fence)
	if err != nil {
		return ErrUnavailable
	}
	n, _ := resultSQL.RowsAffected()
	if n != 1 {
		return ErrStaleContext
	}
	if _, err = tx.ExecContext(ctx, `UPDATE workspaces SET revision=revision+1 WHERE id=?`, workspaceID); err != nil {
		return ErrUnavailable
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if tx.Commit() != nil {
		return ErrUnavailable
	}
	return nil
}

func markdownText(value string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "`", "\\`", "*", "\\*", "_", "\\_", "[", "\\[", "]", "\\]", "<", "&lt;", ">", "&gt;", "#", "\\#", "!", "\\!")
	return replacer.Replace(value)
}
func profileMarkdown(p Profile) string {
	var b strings.Builder
	b.WriteString("# " + markdownText(p.DisplayName) + "\n\n")
	b.WriteString("分类：" + markdownText(p.Category) + "\n\n")
	if p.Truncated {
		b.WriteString("> 当前文档基于有限消息和资料窗口，部分内容未显示，不代表完整历史。\n\n")
	}
	b.WriteString("## 人工资料\n\n")
	for _, v := range []struct{ label, value string }{{"公开姓名", p.PublicName}, {"公司", p.Company}, {"角色", p.Role}, {"备注", p.ManualNotes}} {
		if v.value != "" {
			b.WriteString("- " + v.label + "：" + markdownText(v.value) + "\n")
		}
	}
	b.WriteString("\n## AI整理建议\n\n")
	if p.Summary != "" {
		b.WriteString(markdownText(p.Summary) + "\n\n")
	}
	if len(p.Tags) > 0 {
		b.WriteString("标签：" + markdownText(strings.Join(p.Tags, "、")) + "\n\n")
	}
	if len(p.EvidenceIDs) > 0 {
		b.WriteString("消息证据：" + strings.Join(p.EvidenceIDs, ", ") + "\n\n")
	}
	for _, section := range []struct{ state, title string }{{"confirmed", "已确认资料"}, {"unverified", "待核实候选"}, {"rejected", "已拒绝候选"}} {
		b.WriteString("## " + section.title + "\n\n")
		for _, f := range p.Facts {
			if f.State != section.state {
				continue
			}
			b.WriteString("- " + markdownText(f.Field) + "：" + markdownText(f.Value) + "（" + f.Origin + "；证据 " + strings.Join(append(append([]string{}, f.MessageIDs...), f.SourceIDs...), ", ") + "）\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("## 公开来源\n\n")
	for _, source := range p.Sources {
		safeURL := strings.NewReplacer("<", "%3C", ">", "%3E").Replace(source.URL)
		b.WriteString("- " + markdownText(source.Title) + " — <" + safeURL + "> · " + source.RetrievedAt + " · " + source.ID + "\n")
	}
	value := b.String()
	if len(value) > 180000 {
		value = value[:179700]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
		value = "当前文档已截断，请在档案页查看可见资料窗口。\n\n" + value + "\n\n（文档达到长度上限）\n"
	}
	return value
}

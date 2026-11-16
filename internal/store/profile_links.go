package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
	"unicode/utf8"
)

const maxProfileLinks = 20
const maxLinkedDocumentBytes = 180000

// ProfileLinkIdentity names a source conversation. Matching metadata is a
// suggestion only; it does not grant permission to read a different identity's
// messages through the AI context of the current conversation.
type ProfileLinkIdentity struct {
	ConversationID string `json:"conversation_id"`
	DisplayName    string `json:"display_name"`
	Platform       string `json:"platform"`
	PeerID         string `json:"peer_id"`
	Reason         string `json:"reason"`
}

type ProfileLinks struct {
	ConversationID   string                `json:"conversation_id"`
	Candidates       []ProfileLinkIdentity `json:"candidates"`
	Linked           []ProfileLinkIdentity `json:"linked"`
	DocumentMarkdown string                `json:"document_markdown"`
	Truncated        bool                  `json:"truncated"`
}

func profileLinkPair(a, b string) (string, string) {
	if a < b {
		return a, b
	}
	return b, a
}

func readProfileLinkRows(rows *sql.Rows) ([]ProfileLinkIdentity, error) {
	defer rows.Close()
	out := []ProfileLinkIdentity{}
	for rows.Next() {
		var identity ProfileLinkIdentity
		if rows.Scan(&identity.ConversationID, &identity.DisplayName, &identity.Platform, &identity.PeerID, &identity.Reason) != nil {
			return nil, ErrUnavailable
		}
		out = append(out, identity)
	}
	if rows.Err() != nil {
		return nil, ErrUnavailable
	}
	return out, nil
}

func loadProfileLinks(ctx context.Context, tx *sql.Tx, id string) (ProfileLinks, error) {
	profile, err := profileRow(ctx, tx, id)
	if err != nil {
		return ProfileLinks{}, err
	}
	out := ProfileLinks{ConversationID: id, Candidates: []ProfileLinkIdentity{}, Linked: []ProfileLinkIdentity{}}
	rows, err := tx.QueryContext(ctx, `SELECT v.id,v.title,c.platform,v.peer_id,'confirmed'
 FROM profile_links l JOIN conversations v ON v.workspace_id=l.workspace_id AND v.id=CASE WHEN l.left_conversation_id=? THEN l.right_conversation_id ELSE l.left_conversation_id END
 JOIN connections c ON c.id=v.connection_id AND c.workspace_id=v.workspace_id
 WHERE l.workspace_id=? AND (l.left_conversation_id=? OR l.right_conversation_id=?) ORDER BY v.title,v.id LIMIT 21`, id, workspaceID, id, id)
	if err != nil {
		return out, ErrUnavailable
	}
	out.Linked, err = readProfileLinkRows(rows)
	if err != nil {
		return out, err
	}
	if len(out.Linked) > maxProfileLinks {
		out.Linked = out.Linked[:maxProfileLinks]
		out.Truncated = true
	}
	var sourcePeer string
	if tx.QueryRowContext(ctx, `SELECT peer_id FROM conversations WHERE workspace_id=? AND id=?`, workspaceID, id).Scan(&sourcePeer) != nil {
		return out, ErrUnavailable
	}
	// Same-name suggestions are deliberately conservative. Account IDs are
	// platform-scoped; identical digits across platforms are not identity proof.
	rows, err = tx.QueryContext(ctx, `SELECT v.id,v.title,c.platform,v.peer_id,
 CASE WHEN c.platform=? AND v.peer_id=? THEN 'same_platform_id'
 WHEN trim(?)<>'' AND lower(trim(p.public_name))=lower(trim(?)) THEN 'same_public_name' ELSE 'same_nickname' END
 FROM conversations v JOIN connections c ON c.id=v.connection_id AND c.workspace_id=v.workspace_id
 LEFT JOIN contact_profiles p ON p.workspace_id=v.workspace_id AND p.conversation_id=v.id
 WHERE v.workspace_id=? AND v.id<>? AND (
  (c.platform=? AND v.peer_id=?) OR
  (trim(?)<>'' AND lower(trim(p.public_name))=lower(trim(?))) OR
  (trim(?)<>'' AND ?<>? AND lower(trim(v.title))=lower(trim(?)) AND v.title<>v.peer_id))
 AND NOT EXISTS(SELECT 1 FROM profile_links l WHERE l.workspace_id=v.workspace_id AND ((l.left_conversation_id=? AND l.right_conversation_id=v.id) OR (l.right_conversation_id=? AND l.left_conversation_id=v.id)))
 ORDER BY v.title,v.id LIMIT 20`, profile.Platform, sourcePeer, profile.PublicName, profile.PublicName, workspaceID, id, profile.Platform, sourcePeer, profile.PublicName, profile.PublicName, profile.DisplayName, profile.DisplayName, sourcePeer, profile.DisplayName, id, id)
	if err != nil {
		return out, ErrUnavailable
	}
	out.Candidates, err = readProfileLinkRows(rows)
	if err != nil {
		return out, err
	}
	var document strings.Builder
	document.WriteString("# 联系人联合档案\n\n以下会话由用户逐个确认关联。原聊天、事实证据和AI权限各自保留；同名建议未经确认不会加入。\n\n")
	ids := []string{id}
	for _, linked := range out.Linked {
		ids = append(ids, linked.ConversationID)
	}
	for _, sourceID := range ids {
		p, err := loadProfile(ctx, tx, sourceID)
		if err != nil {
			return out, err
		}
		part := "\n---\n\n## 来源会话 " + markdownText(p.DisplayName) + "\n\n平台：" + markdownText(p.Platform) + " · 会话：" + p.ConversationID + "\n\n" + p.DocumentMarkdown
		if document.Len()+len(part) > maxLinkedDocumentBytes {
			remaining := maxLinkedDocumentBytes - document.Len() - 200
			if remaining > 0 {
				part = part[:min(remaining, len(part))]
				for !utf8.ValidString(part) {
					part = part[:len(part)-1]
				}
				document.WriteString(part)
			}
			document.WriteString("\n\n（联合文档达到显示上限，请打开各来源档案查看。）\n")
			out.Truncated = true
			break
		}
		document.WriteString(part)
		out.Truncated = out.Truncated || p.Truncated
	}
	out.DocumentMarkdown = document.String()
	return out, nil
}

func (s *Store) GetProfileLinks(ctx context.Context, id string) (ProfileLinks, error) {
	if err := s.lockContext(ctx); err != nil {
		return ProfileLinks{}, err
	}
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ProfileLinks{}, ErrUnavailable
	}
	defer tx.Rollback()
	out, err := loadProfileLinks(ctx, tx, id)
	if err != nil {
		return out, err
	}
	if tx.Commit() != nil {
		return ProfileLinks{}, ErrUnavailable
	}
	return out, nil
}

// SetProfileLink confirms or removes a single symmetric association. Each pair
// is idempotent and reversible; no message, fact, or permission is copied.
func (s *Store) SetProfileLink(ctx context.Context, id, target string, linked bool) (ProfileLinks, error) {
	if !validResourceID(id) || !validResourceID(target) || id == target {
		return ProfileLinks{}, ErrInvalidInput
	}
	if err := s.lockContext(ctx); err != nil {
		return ProfileLinks{}, err
	}
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ProfileLinks{}, ErrUnavailable
	}
	defer tx.Rollback()
	for _, conversation := range []string{id, target} {
		p, err := profileRow(ctx, tx, conversation)
		if err != nil {
			return ProfileLinks{}, err
		}
		if linked {
			if err := ensureProfile(ctx, tx, p); err != nil {
				return ProfileLinks{}, err
			}
		}
	}
	left, right := profileLinkPair(id, target)
	if linked {
		var exists int
		if tx.QueryRowContext(ctx, `SELECT count(*) FROM profile_links WHERE workspace_id=? AND left_conversation_id=? AND right_conversation_id=?`, workspaceID, left, right).Scan(&exists) != nil {
			return ProfileLinks{}, ErrUnavailable
		}
		if exists == 0 {
			for _, conversation := range []string{id, target} {
				var count int
				if tx.QueryRowContext(ctx, `SELECT count(*) FROM profile_links WHERE workspace_id=? AND (left_conversation_id=? OR right_conversation_id=?)`, workspaceID, conversation, conversation).Scan(&count) != nil {
					return ProfileLinks{}, ErrUnavailable
				}
				if count >= maxProfileLinks {
					return ProfileLinks{}, ErrConflict
				}
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO profile_links(workspace_id,left_conversation_id,right_conversation_id,created_at)VALUES(?,?,?,?) ON CONFLICT DO NOTHING`, workspaceID, left, right, time.Now().UTC().Format(storedTimeLayout))
	} else {
		_, err = tx.ExecContext(ctx, `DELETE FROM profile_links WHERE workspace_id=? AND left_conversation_id=? AND right_conversation_id=?`, workspaceID, left, right)
	}
	if err != nil {
		return ProfileLinks{}, ErrUnavailable
	}
	out, err := loadProfileLinks(ctx, tx, id)
	if err != nil {
		return out, err
	}
	if tx.Commit() != nil {
		return ProfileLinks{}, ErrUnavailable
	}
	return out, nil
}

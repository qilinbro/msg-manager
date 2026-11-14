package store

import (
	"database/sql"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/qilinbro/msg-manager/migrations"
)

func TestV1MigrationPreservesMessagesManagementAndIndexes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1.db")
	u := url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(path)}
	q := url.Values{}
	q.Set("_pragma_key", "x'"+testKey+"'")
	u.RawQuery = q.Encode()
	db, err := sql.Open("social-sqlcipher", u.String())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.Files.ReadFile("0001_messages.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(raw)); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,applied_at TEXT NOT NULL);INSERT INTO schema_migrations VALUES (1,'2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	conn, contact, conversation, message := stableID("old-connection"), stableID("old-contact"), stableID("old-conversation"), stableID("old-message")
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO connections VALUES (?,'local-default','qq','old-account','合成旧账号','import')`, []any{conn}},
		{`INSERT INTO contacts VALUES (?,'local-default',?,'peer','同名')`, []any{contact, conn}},
		{`INSERT INTO conversations VALUES (?,'local-default',?,?,'peer','原会话',1,1)`, []any{conversation, conn, contact}},
		{`INSERT INTO messages(seq,id,workspace_id,conversation_id,external_id,direction,body,occurred_at,is_read) VALUES (17,?,'local-default',?,'old-message','incoming','保留旧正文','2026-01-01T00:00:00.000000000Z',1)`, []any{message, conversation}},
	}
	for _, statement := range statements {
		if _, err = db.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path, testKey)
	if err != nil {
		t.Fatal("v1->v2 migration", err)
	}
	defer s.Close()
	page, err := s.Conversations(Filter{Archived: true, Pinned: true})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != conversation || page.Items[0].UnreadCount != 0 {
		t.Fatal("old management state lost", err, page)
	}
	messages, err := s.Messages(conversation, "", 50)
	if err != nil || len(messages.Items) != 1 || messages.Items[0].ID != message || messages.Items[0].Body != "保留旧正文" || messages.Items[0].Source != "import" || messages.Items[0].Email != nil {
		t.Fatal("old messages lost", err)
	}
	rows, err := s.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Fatal("foreign key broken during migration")
	}
	_ = rows.Close()
	var found, version int
	if s.db.QueryRow(`SELECT count(*) FROM messages_fts WHERE messages_fts MATCH '保留旧正文'`).Scan(&found) != nil || found != 1 {
		t.Fatal("FTS rebuild lost evidence")
	}
	if s.db.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&version) != nil || version != 4 {
		t.Fatal("schema version not recorded")
	}
	result, err := s.ImportIMAP(emailBatch("imap-account", "INBOX", 1, "邮件主题", false))
	if err != nil || result.Inserted != 1 {
		t.Fatal("email after migration", err)
	}
	var seq int
	if s.db.QueryRow(`SELECT max(seq) FROM messages`).Scan(&seq) != nil || seq <= 17 {
		t.Fatal("message sequence reset")
	}
}

func emailBatch(account, folder string, uid int, subject string, read bool) ImportRequest {
	return ImportRequest{Platform: "email", AccountID: account, AccountName: "合成邮箱", Messages: []ImportMessage{{ExternalID: stableID(folder, string(rune(uid))), PeerID: stableID("mail", folder, string(rune(uid))), PeerName: subject, Direction: "incoming", Body: "合成邮件正文", OccurredAt: "2026-01-01T00:00:00Z", Read: &read, Email: &EmailHeaders{Subject: subject, FromName: "合成发件人", FromAddress: "sender@example.test", To: []string{"receiver@example.test"}, Folder: folder}}}}
}

func TestEmailDedupPreservesLocalStateAndScopes(t *testing.T) {
	s := testStore(t)
	input := emailBatch("first", "INBOX", 1, "合成主题", true)
	result, err := s.ImportIMAP(input)
	if err != nil || result.Inserted != 1 {
		t.Fatal(err)
	}
	convs := mustConversations(t, s, Filter{Platform: "email", Q: "sender@example.test"})
	if len(convs.Items) != 1 || convs.Items[0].UnreadCount != 0 || convs.Items[0].AccountName != "合成邮箱" {
		t.Fatal("email metadata search or seen state failed")
	}
	id := convs.Items[0].ID
	unread := false
	if err = s.UpdateConversation(id, ConversationPatch{Read: &unread}); err != nil {
		t.Fatal(err)
	}
	result, err = s.ImportIMAP(input)
	if err != nil || result.Duplicates != 1 {
		t.Fatal("email duplicate", err)
	}
	convs = mustConversations(t, s, Filter{Platform: "email", Unread: true})
	if len(convs.Items) != 1 {
		t.Fatal("duplicate sync overwrote local read flag")
	}
	page, err := s.Messages(id, "", 50)
	if err != nil || page.Items[0].Email == nil || page.Items[0].Email.Subject != "合成主题" || page.Items[0].Source != "imap" {
		t.Fatal("mail metadata lost", err)
	}
	for _, pair := range [][2]string{{"second", "INBOX"}, {"first", "OtherFolder"}} {
		if _, err = s.ImportIMAP(emailBatch(pair[0], pair[1], 1, "合成主题", false)); err != nil {
			t.Fatal(err)
		}
	}
	if len(mustConversations(t, s, Filter{Platform: "email"}).Items) != 3 {
		t.Fatal("accounts or folders merged")
	}
	input.Messages[0].Body = "同UID变更正文"
	if _, err = s.ImportIMAP(input); err != ErrConflict {
		t.Fatal("conflicting UID silently overwritten", err)
	}
}

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/qilinbro/msg-manager/migrations"
)

func wechatBatch(account string) ImportRequest {
	return ImportRequest{Platform: "weixin", AccountID: account, Messages: []ImportMessage{
		importMessage("wechat-message", "wechat-peer", "合成微信正文", "2026-01-01T04:00:00Z"),
	}}
}

type observedImportContext struct {
	context.Context
	checked chan struct{}
	once    sync.Once
}

func (c *observedImportContext) Err() error {
	err := c.Context.Err()
	c.once.Do(func() { close(c.checked) })
	return err
}

func TestWeChatCanceledWhileWaitingForStoreCannotWrite(t *testing.T) {
	s := testStore(t)
	mustImport(t, s, wechatBatch("existing-account"))
	before := snapshotV3Data(t, s.db)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	observed := &observedImportContext{Context: ctx, checked: make(chan struct{})}
	s.mu.Lock()
	done := make(chan error, 1)
	go func() {
		_, err := s.ImportWeChatContext(observed, wechatBatch("canceled-account"))
		done <- err
	}()
	// The initial check has captured nil while the store remains locked. Any
	// cancellation from this point must be checked again before beginning SQL.
	<-observed.checked
	cancel()
	s.mu.Unlock()
	if err := <-done; err != context.Canceled {
		t.Fatal("canceled waiting import did not stop", err)
	}
	if !reflect.DeepEqual(before, snapshotV3Data(t, s.db)) {
		t.Fatal("canceled import wrote after waiting for the store lock")
	}
	connections, err := s.Connections()
	if err != nil || len(connections) != 1 || connections[0].Source != "import" {
		t.Fatal("canceled import changed connector provenance", err)
	}
}

func TestWeChatImportProvenanceDedupManagementAndAccountIsolation(t *testing.T) {
	s := testStore(t)
	input := wechatBatch("first-account")
	result, err := s.ImportWeChat(input)
	if err != nil || result.Inserted != 1 || result.Conversations != 1 {
		t.Fatal("wechat import failed", result, err)
	}
	id := mustConversations(t, s, Filter{Platform: "weixin"}).Items[0].ID
	if err = s.UpdateConversation(id, ConversationPatch{Read: boolValue(true), Pinned: boolValue(true), Archived: boolValue(true)}); err != nil {
		t.Fatal(err)
	}
	result, err = s.ImportWeChat(input)
	if err != nil || result.Inserted != 0 || result.Duplicates != 1 {
		t.Fatal("wechat replay did not deduplicate", result, err)
	}
	page := mustConversations(t, s, Filter{Platform: "weixin", Pinned: true, Archived: true})
	if len(page.Items) != 1 || page.Items[0].ID != id || page.Items[0].UnreadCount != 0 {
		t.Fatal("wechat replay changed local management flags")
	}
	messages, err := s.Messages(id, "", 50)
	if err != nil || len(messages.Items) != 1 || messages.Items[0].Source != "wechatdb" || !messages.Items[0].Read || messages.Items[0].Email != nil {
		t.Fatal("wechat message provenance missing", err)
	}
	firstMessageID := messages.Items[0].ID
	input.AccountID = "second-account"
	if _, err = s.ImportWeChat(input); err != nil {
		t.Fatal(err)
	}
	other := mustConversations(t, s, Filter{Platform: "weixin"})
	if len(other.Items) != 1 || other.Items[0].ID == id || other.Items[0].UnreadCount != 1 {
		t.Fatal("wechat account isolation failed")
	}
	messages, err = s.Messages(other.Items[0].ID, "", 50)
	if err != nil || messages.Items[0].ID == firstMessageID {
		t.Fatal("message identity crossed account scope", err)
	}
	connections, err := s.Connections()
	if err != nil || len(connections) != 2 {
		t.Fatal("connections missing", err)
	}
	for _, connection := range connections {
		if connection.Source != "wechatdb" || connection.MessageCount != 1 {
			t.Fatal("wechat connection provenance differs")
		}
	}
}

func TestWeChatConflictRollsBackRowsProvenanceAndRevision(t *testing.T) {
	s := testStore(t)
	base := wechatBatch("self")
	mustImport(t, s, base)
	before := snapshotV3Data(t, s.db)
	conflict := wechatBatch("self")
	conflict.Messages = append([]ImportMessage{importMessage("new-before-conflict", "new-peer", "应整体回滚", "2026-01-01T05:00:00Z")}, conflict.Messages...)
	conflict.Messages[1].Body = "相同消息ID不同正文"
	if _, err := s.ImportWeChat(conflict); err != ErrConflict {
		t.Fatal("conflicting identity was not rejected", err)
	}
	if !reflect.DeepEqual(before, snapshotV3Data(t, s.db)) {
		t.Fatal("failed batch changed persisted rows or revision")
	}
	var provenance int
	if s.db.QueryRow(`SELECT count(*) FROM connections WHERE connector_source IS NOT NULL`).Scan(&provenance) != nil || provenance != 0 {
		t.Fatal("failed batch promoted original import provenance")
	}
	var found int
	if s.db.QueryRow(`SELECT count(*) FROM messages_fts WHERE messages_fts MATCH '应整体回滚'`).Scan(&found) != nil || found != 0 {
		t.Fatal("failed batch leaked rows into FTS")
	}
}

func TestWeChatSourceRequiresServerEntryPointAndRejectsEmail(t *testing.T) {
	s := testStore(t)
	for _, platform := range []string{"qq", "telegram", "email", "shanliao", ""} {
		input := wechatBatch("self")
		input.Platform = platform
		if _, err := s.ImportWeChat(input); err != ErrInvalidInput {
			t.Fatal("wechat entry point accepted another platform", platform, err)
		}
	}
	input := wechatBatch("self")
	input.Messages[0].Email = emailBatch("mail", "INBOX", 1, "subject", false).Messages[0].Email
	if _, err := s.ImportWeChat(input); err != ErrInvalidInput {
		t.Fatal("wechat entry point accepted email headers", err)
	}
	// ImportRequest exposes no source field. Even permissive JSON decoding
	// cannot select connector provenance; the browser path always calls Import.
	raw, _ := json.Marshal(wechatBatch("browser"))
	var payload map[string]any
	_ = json.Unmarshal(raw, &payload)
	payload["source"] = "wechatdb"
	payload["connector_source"] = "wechatdb"
	payload["messages"].([]any)[0].(map[string]any)["source"] = "wechatdb"
	raw, _ = json.Marshal(payload)
	var browser ImportRequest
	if err := json.Unmarshal(raw, &browser); err != nil {
		t.Fatal(err)
	}
	mustImport(t, s, browser)
	id := mustConversations(t, s, Filter{}).Items[0].ID
	page, err := s.Messages(id, "", 50)
	if err != nil || len(page.Items) != 1 || page.Items[0].Source != "import" {
		t.Fatal("browser supplied source was trusted", err)
	}
	connections, err := s.Connections()
	if err != nil || len(connections) != 1 || connections[0].Source != "import" {
		t.Fatal("browser supplied connection provenance was trusted", err)
	}
	// A real connector replay does not relabel the already imported message.
	if _, err = s.ImportWeChat(browser); err != nil {
		t.Fatal(err)
	}
	connections, _ = s.Connections()
	page, _ = s.Messages(id, "", 50)
	if connections[0].Source != "mixed" || page.Items[0].Source != "import" {
		t.Fatal("cross-source replay overwrote original message provenance")
	}
	next := wechatBatch("browser")
	next.Messages[0].ExternalID = "second-message"
	if _, err = s.ImportWeChat(next); err != nil {
		t.Fatal(err)
	}
	page, _ = s.Messages(id, "", 50)
	if len(page.Items) != 2 || page.Items[1].Source != "wechatdb" {
		t.Fatal("mixed account lost per-message provenance")
	}
}

func legacyV3Store(t *testing.T, path string) *Store {
	t.Helper()
	uriPath := filepath.ToSlash(path)
	if runtime.GOOS == "windows" {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	q := url.Values{}
	q.Set("_pragma_key", "x'"+testKey+"'")
	u.RawQuery = q.Encode()
	db, err := sql.Open("social-sqlcipher", u.String())
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	for _, name := range []string{"0001_messages.sql", "0002_email.sql", "0003_ai.sql"} {
		raw, err := migrations.Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(string(raw)); err != nil {
			t.Fatal("legacy v3 fixture migration", err)
		}
	}
	if _, err = db.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,applied_at TEXT NOT NULL);INSERT INTO schema_migrations VALUES(1,'2026-01-01T00:00:00Z'),(2,'2026-01-01T00:00:00Z'),(3,'2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	return &Store{db: db}
}

// Snapshot every pre-v4 data table, including both AI evidence graphs and both
// AUTOINCREMENT high-water marks. Explicit base columns exclude additive v4
// metadata so any other data change is caught byte for byte.
func snapshotV3Data(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	queries := map[string]string{
		"workspaces":           `SELECT * FROM workspaces ORDER BY id`,
		"connections":          `SELECT id,workspace_id,platform,account_id,display_name,source FROM connections ORDER BY id`,
		"contacts":             `SELECT * FROM contacts ORDER BY id`,
		"conversations":        `SELECT * FROM conversations ORDER BY id`,
		"messages":             `SELECT seq,id,workspace_id,conversation_id,external_id,direction,body,occurred_at,is_read,source,email_json FROM messages ORDER BY seq`,
		"ai_preferences":       `SELECT * FROM ai_preferences ORDER BY conversation_id`,
		"ai_memories":          `SELECT * FROM ai_memories ORDER BY id`,
		"ai_memory_evidence":   `SELECT * FROM ai_memory_evidence ORDER BY memory_id,message_id`,
		"ai_analyses":          `SELECT * FROM ai_analyses ORDER BY seq`,
		"ai_analysis_evidence": `SELECT * FROM ai_analysis_evidence ORDER BY analysis_id,message_id`,
		"ai_call_reservations": `SELECT * FROM ai_call_reservations ORDER BY id`,
		"sqlite_sequence":      `SELECT * FROM sqlite_sequence ORDER BY name`,
		"fts":                  `SELECT rowid,body FROM messages_fts ORDER BY rowid`,
	}
	snapshot := map[string]string{}
	for table, query := range queries {
		rows, err := db.Query(query)
		if err != nil {
			t.Fatal(table, err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(table, err)
		}
		values := [][]any{}
		for rows.Next() {
			row := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range row {
				pointers[i] = &row[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(table, err)
			}
			values = append(values, row)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(table, err)
		}
		_ = rows.Close()
		raw, err := json.Marshal(values)
		if err != nil {
			t.Fatal(table, err)
		}
		snapshot[table] = string(raw)
	}
	return snapshot
}

func TestV3ToV4PreservesAIDataForeignKeysAndMessageIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-v3.db")
	legacy := legacyV3Store(t, path)
	connection := stableID("connection", "weixin", "legacy-account")
	contact := stableID("contact", connection, "legacy-peer")
	conversation := stableID("conversation", connection, "legacy-peer")
	message := stableID("message", conversation, "legacy-evidence")
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO connections VALUES(?,'local-default','weixin','legacy-account','旧账号','import')`, []any{connection}},
		{`INSERT INTO contacts VALUES(?,'local-default',?,'legacy-peer','旧联系人')`, []any{contact, connection}},
		{`INSERT INTO conversations VALUES(?,'local-default',?,?,'legacy-peer','旧会话',1,1)`, []any{conversation, connection, contact}},
		{`INSERT INTO messages(seq,id,workspace_id,conversation_id,external_id,direction,body,occurred_at,is_read,source) VALUES(29,?,'local-default',?,'legacy-evidence','incoming','合成迁移证据','2026-01-01T00:00:00.000000000Z',1,'import')`, []any{message, conversation}},
	}
	for _, statement := range statements {
		if _, err := legacy.db.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	context, err := legacy.AIContext(conversation)
	if err != nil {
		t.Fatal(err)
	}
	result := aiTestResult(context)
	result.MemoryCandidates = []AICandidate{{Content: "迁移保留的已确认记忆", EvidenceIDs: []string{message}}}
	if _, err = legacy.SaveAIAnalysis(conversation, context.ContextHash, result); err != nil {
		t.Fatal(err)
	}
	memories, err := legacy.ListAIMemories(conversation)
	if err != nil || len(memories) != 1 {
		t.Fatal("seed memory", err)
	}
	if err = legacy.UpdateAIMemory(conversation, memories[0].ID, "confirmed"); err != nil {
		t.Fatal(err)
	}
	if err = legacy.UpdateAISettings(conversation, "", "professional"); err != nil {
		t.Fatal(err)
	}
	context, _ = legacy.AIContext(conversation)
	result = aiTestResult(context)
	result.ConfigID = strings.Repeat("ab", 32)
	analysis, err := legacy.SaveAIAnalysis(conversation, context.ContextHash, result)
	if err != nil {
		t.Fatal(err)
	}
	if err = legacy.UpdateAIDraft(conversation, analysis.ID, "迁移保留的编辑草稿", result.ConfigID); err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.ReserveAICall("2026-11-14", conversation, 50); err != nil {
		t.Fatal(err)
	}
	before := snapshotV3Data(t, legacy.db)
	if err = legacy.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path, testKey)
	if err != nil {
		t.Fatal("v3 to v4 upgrade", err)
	}
	defer s.Close()
	if after := snapshotV3Data(t, s.db); !reflect.DeepEqual(before, after) {
		for table, data := range before {
			if data != after[table] {
				t.Errorf("migration changed %s", table)
			}
		}
	}
	var version, oldVersions int
	if s.db.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&version) != nil || version != latestSchemaVersion {
		t.Fatal("current schema version missing")
	}
	if s.db.QueryRow(`SELECT count(*) FROM schema_migrations WHERE version<=3 AND applied_at='2026-01-01T00:00:00Z'`).Scan(&oldVersions) != nil || oldVersions != 3 {
		t.Fatal("old migration records changed")
	}
	rows, err := s.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Fatal("v4 foreign key check failed")
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	_ = rows.Close()
	var found int
	if s.db.QueryRow(`SELECT count(*) FROM messages_fts WHERE messages_fts MATCH '合成迁移证据'`).Scan(&found) != nil || found != 1 {
		t.Fatal("v4 FTS index lost evidence")
	}
	afterContext, err := s.AIContext(conversation)
	if err != nil || !reflect.DeepEqual(context, afterContext) {
		t.Fatal("migration changed AI context", err)
	}
	latest, err := s.LastAIAnalysis(conversation)
	if err != nil || latest == nil || latest.ID != analysis.ID || latest.Draft != "迁移保留的编辑草稿" || !latest.DraftEdited || latest.Stale {
		t.Fatal("migration lost analysis or draft", err)
	}
	page := mustConversations(t, s, Filter{Pinned: true, Archived: true})
	if len(page.Items) != 1 || page.Items[0].ID != conversation || page.Items[0].UnreadCount != 0 {
		t.Fatal("migration changed conversation management")
	}
	input := wechatBatch("legacy-account")
	input.Messages[0].PeerID = "legacy-peer"
	if _, err = s.ImportWeChat(input); err != nil {
		t.Fatal("wechat import after v4 upgrade", err)
	}
	var seq int
	if s.db.QueryRow(`SELECT max(seq) FROM messages`).Scan(&seq) != nil || seq <= 29 {
		t.Fatal("v4 reset message AUTOINCREMENT")
	}
	if s.db.QueryRow(`SELECT count(*) FROM messages_fts WHERE messages_fts MATCH '合成微信正文'`).Scan(&found) != nil || found != 1 {
		t.Fatal("v4 FTS insert trigger failed")
	}
}

func TestV4MigrationFailureRollsBackAddedColumnsAndVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conflicting-v3.db")
	legacy := legacyV3Store(t, path)
	// Force failure after the first v4 ALTER succeeds. Its column and the
	// migration record must both disappear when migrate rolls back.
	if _, err := legacy.db.Exec(`ALTER TABLE messages ADD COLUMN connector_source TEXT`); err != nil {
		t.Fatal(err)
	}
	before := snapshotV3Data(t, legacy.db)
	if err := migrate(legacy.db); err != ErrUnavailable {
		t.Fatal("conflicting migration unexpectedly succeeded", err)
	}
	if !reflect.DeepEqual(before, snapshotV3Data(t, legacy.db)) {
		t.Fatal("failed migration changed v3 data")
	}
	var version int
	if legacy.db.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&version) != nil || version != 3 {
		t.Fatal("failed v4 migration recorded completion")
	}
	if _, err := legacy.db.Exec(`SELECT connector_source FROM connections`); err == nil {
		t.Fatal("first added column survived rollback")
	}
}

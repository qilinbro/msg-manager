package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/qilinbro/msg-manager/migrations"
)

var shoppingPlatforms = []string{"xianyu", "taobao", "jd", "pinduoduo"}

func TestShoppingImportsSeparatePlatformAccountPeerAndPreserveReplayState(t *testing.T) {
	s := testStore(t)
	for _, platform := range shoppingPlatforms {
		if !ValidPlatform(platform) || !IsShoppingPlatform(platform) {
			t.Fatal("missing shopping platform", platform)
		}
		for _, account := range []string{"synthetic-owner-a", "synthetic-owner-b"} {
			req := ImportRequest{Platform: platform, AccountID: account, Messages: []ImportMessage{importMessage("same-external-id", "same-peer", "合成购物私聊 约定发货", "2026-01-01T00:00:00Z")}}
			first, err := s.Import(req)
			if err != nil || first.Inserted != 1 {
				t.Fatal(platform, err)
			}
			page := mustConversations(t, s, Filter{Platform: platform, Q: account})
			if len(page.Items) != 1 {
				t.Fatal("account filter scope", page)
			}
			id := page.Items[0].ID
			if err := s.UpdateConversation(id, ConversationPatch{Read: boolValue(true), Pinned: boolValue(true)}); err != nil {
				t.Fatal(err)
			}
			replay, err := s.Import(req)
			if err != nil || replay.Inserted != 0 || replay.Duplicates != 1 || replay.Conversations != 1 {
				t.Fatal("replay not deduplicated", err)
			}
			messages, err := s.Messages(id, "", 50)
			if err != nil || len(messages.Items) != 1 || messages.Items[0].Source != "import" || !messages.Items[0].Read || messages.Items[0].Email != nil {
				t.Fatal("replay changed source/read", err)
			}
			profile := mustProfile(t, s, id)
			if profile.AutoOrganize || profile.PublicSearchEnabled || profile.AIPaused {
				t.Fatal("import changed AI authorization")
			}
			req.Messages[0].Body = "conflicting body"
			if _, err := s.Import(req); !errors.Is(err, ErrConflict) {
				t.Fatal("conflicting identity accepted", err)
			}
		}
		page := mustConversations(t, s, Filter{Platform: platform, Q: "约定发货", Pinned: true})
		if len(page.Items) != 2 {
			t.Fatal("platform text search leaked or lost records", platform, page)
		}
	}
	all := mustConversations(t, s, Filter{})
	if len(all.Items) != 8 {
		t.Fatal("platform/account identity collapsed")
	}
	ids := map[string]bool{}
	for _, c := range all.Items {
		if ids[c.ID] || c.ConversationKind != "private" {
			t.Fatal("identity/kind collision")
		}
		ids[c.ID] = true
	}
	if page := mustConversations(t, s, Filter{Unread: true}); len(page.Items) != 0 {
		t.Fatal("reimport reset local read")
	}
}

func TestShoppingImportsRejectGroupsRichMailAndProtocolPromotionAtomically(t *testing.T) {
	s := testStore(t)
	for _, platform := range shoppingPlatforms {
		base := ImportRequest{Platform: platform, AccountID: "synthetic-owner", Messages: []ImportMessage{importMessage("one", "one", "private text", "2026-01-01T00:00:00Z")}}
		for _, mutate := range []func(*ImportRequest){
			func(r *ImportRequest) { r.Messages[0].ConversationKind = "group" },
			func(r *ImportRequest) { r.Messages[0].Email = &EmailHeaders{Subject: "not a chat"} },
			func(r *ImportRequest) { r.Messages[0].Body = "" },
		} {
			req := base
			req.Messages = append([]ImportMessage(nil), base.Messages...)
			mutate(&req)
			req.Messages = append([]ImportMessage{importMessage("before-invalid", "valid-peer", "must rollback", "2026-01-01T00:00:00Z")}, req.Messages...)
			if _, err := s.Import(req); !errors.Is(err, ErrInvalidInput) {
				t.Fatal("invalid shopping batch accepted", err)
			}
		}
		if _, err := s.importBatch(base, "imap"); !errors.Is(err, ErrInvalidInput) {
			t.Fatal("shopping source promoted")
		}
		if _, err := s.ImportIMAP(base); !errors.Is(err, ErrInvalidInput) {
			t.Fatal("shopping passed IMAP adapter")
		}
		if _, err := s.ImportWeChat(base); !errors.Is(err, ErrInvalidInput) {
			t.Fatal("shopping passed database connector")
		}
	}
	if got := mustConversations(t, s, Filter{}); len(got.Items) != 0 {
		t.Fatal("invalid import partially persisted")
	}
	if ValidPlatform("other-shop") || IsShoppingPlatform("qq") {
		t.Fatal("platform validation widened unexpectedly")
	}
}

// Build an actual v13 database with the original constrained connections table,
// rather than merely removing a version row from an already upgraded database.
func shoppingLegacyV13(t *testing.T, path string) *Store {
	t.Helper()
	s := localDevicesLegacyV11(t, path)
	for i, name := range []string{"0012_local_devices.sql", "0013_milky_connection_config.sql"} {
		raw, err := migrations.Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.db.Exec(string(raw)); err != nil {
			t.Fatal(err)
		}
		if _, err = s.db.Exec(`INSERT INTO schema_migrations VALUES(?,?)`, 12+i, "2026-01-01T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func shoppingDataSnapshot(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name!='receive_monitors' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if rows.Scan(&name) != nil {
			t.Fatal("snapshot table")
		}
		tables = append(tables, name)
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	_ = rows.Close()
	out := map[string]string{}
	for _, table := range tables {
		query := `SELECT * FROM "` + strings.ReplaceAll(table, `"`, `""`) + `"`
		if table == "schema_migrations" {
			query += ` WHERE version<=13`
		}
		rows, err := db.Query(query)
		if err != nil {
			t.Fatal("snapshot", table, err)
		}
		cols, _ := rows.Columns()
		var records []string
		for rows.Next() {
			values := make([]any, len(cols))
			pointers := make([]any, len(cols))
			for i := range values {
				pointers[i] = &values[i]
			}
			if rows.Scan(pointers...) != nil {
				t.Fatal("snapshot row")
			}
			b, _ := json.Marshal(values)
			records = append(records, string(b))
		}
		if rows.Err() != nil {
			t.Fatal(rows.Err())
		}
		_ = rows.Close()
		sort.Strings(records)
		b, _ := json.Marshal(records)
		out[table] = string(b)
	}
	return out
}

func assertShoppingFKEnabled(t *testing.T, s *Store) {
	t.Helper()
	var enabled int
	if s.db.QueryRow(`PRAGMA foreign_keys`).Scan(&enabled) != nil || enabled != 1 {
		t.Fatal("foreign keys left disabled")
	}
	rows, err := s.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() || rows.Err() != nil {
		t.Fatal("migration broke foreign keys")
	}
}

func TestShoppingV14UpgradePreservesEntireGraphFTSSettingsAndHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "actual-v13.db")
	s := shoppingLegacyV13(t, path)
	id := aiTestConversation(t, s, "qq", "legacy-owner", "legacy-peer")
	contextBefore, err := s.AIContext(id)
	if err != nil {
		t.Fatal(err)
	}
	analysis := aiTestResult(contextBefore)
	analysis.MemoryCandidates = []AICandidate{{Content: "keep confirmed memory", EvidenceIDs: []string{contextBefore.Messages[0].ID}}}
	if _, err = s.SaveAIAnalysis(id, contextBefore.ContextHash, analysis); err != nil {
		t.Fatal(err)
	}
	memories, err := s.ListAIMemories(id)
	if err != nil || len(memories) != 1 {
		t.Fatal(err)
	}
	if err = s.UpdateAIMemory(id, memories[0].ID, "confirmed"); err != nil {
		t.Fatal(err)
	}
	patchProfile(t, s, id, ProfilePatch{ExpectedRevision: profilePointer(int64(0)), ManualNotes: profilePointer("keep profile notes")})
	other := aiTestConversation(t, s, "telegram", "legacy-owner", "legacy-other")
	if _, err = s.SetProfileLink(context.Background(), id, other, true); err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateConversation(id, ConversationPatch{Read: boolValue(true), Pinned: boolValue(true), Archived: boolValue(true)}); err != nil {
		t.Fatal(err)
	}
	device := localDeviceTestValue(t)
	mustCreateLocalDevice(t, s, device)
	if _, err = s.SaveQQMilkyConfig(context.Background(), QQMilkyConfig{URL: "http://127.0.0.1:3000", AccountID: "123456", AccessToken: "synthetic-migration-only", HistoryLimit: 20}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`CREATE INDEX connections_display_name_test ON connections(display_name); CREATE TRIGGER connection_update_test AFTER UPDATE ON connections BEGIN SELECT 1; END`); err != nil {
		t.Fatal(err)
	}
	before := shoppingDataSnapshot(t, s.db)
	var sequenceBefore int64
	if s.db.QueryRow(`SELECT seq FROM sqlite_sequence WHERE name='messages'`).Scan(&sequenceBefore) != nil {
		t.Fatal("sequence")
	}
	if err = migrate(s.db); err != nil {
		t.Fatal("v13 to v14 failed", err)
	}
	if after := shoppingDataSnapshot(t, s.db); !reflect.DeepEqual(before, after) {
		t.Fatal("v14 changed existing table data")
	}
	assertShoppingFKEnabled(t, s)
	var version, objects int
	if s.db.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&version) != nil || version != currentSchemaVersion {
		t.Fatal("latest migration not recorded")
	}
	if s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name IN ('connections_display_name_test','connection_update_test')`).Scan(&objects) != nil || objects != 2 {
		t.Fatal("existing indexes/triggers lost")
	}
	if err = migrate(s.db); err != nil {
		t.Fatal("v14 rerun", err)
	}
	if !reflect.DeepEqual(before, shoppingDataSnapshot(t, s.db)) {
		t.Fatal("v14 rerun changed data")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path, testKey)
	if err != nil {
		t.Fatal("encrypted v14 reopen", err)
	}
	defer s.Close()
	assertShoppingFKEnabled(t, s)
	if got := mustConversations(t, s, Filter{Archived: true, Pinned: true}); len(got.Items) != 1 || got.Items[0].ID != id || got.Items[0].UnreadCount != 0 {
		t.Fatal("management flags lost")
	}
	if profile := mustProfile(t, s, id); profile.ManualNotes != "keep profile notes" || profile.AutoOrganize || profile.PublicSearchEnabled {
		t.Fatal("profile data or authorization changed")
	}
	if links, err := s.GetProfileLinks(context.Background(), id); err != nil || len(links.Linked) != 1 || links.Linked[0].ConversationID != other {
		t.Fatal("profile links lost", err)
	}
	var matches int
	if s.db.QueryRow(`SELECT count(*) FROM messages_fts WHERE messages_fts MATCH '合成事实'`).Scan(&matches) != nil || matches != 2 {
		t.Fatal("FTS evidence lost")
	}
	if _, err = s.Import(ImportRequest{Platform: "xianyu", AccountID: "shopping-owner", Messages: []ImportMessage{importMessage("new", "new-peer", "new shopping message", "2026-01-02T00:00:00Z")}}); err != nil {
		t.Fatal("shopping after upgrade", err)
	}
	var sequenceAfter int64
	if s.db.QueryRow(`SELECT seq FROM sqlite_sequence WHERE name='messages'`).Scan(&sequenceAfter) != nil || sequenceAfter <= sequenceBefore {
		t.Fatal("message sequence reset")
	}
}

func TestShoppingV14FailureRollsBackAndRestoresForeignKeys(t *testing.T) {
	s := shoppingLegacyV13(t, filepath.Join(t.TempDir(), "rollback-v13.db"))
	aiTestConversation(t, s, "weixin", "legacy-owner", "legacy-peer")
	before := shoppingDataSnapshot(t, s.db)
	if _, err := s.db.Exec(`CREATE TRIGGER reject_shopping_migration BEFORE INSERT ON schema_migrations WHEN NEW.version=14 BEGIN SELECT RAISE(ABORT,'synthetic-v14-failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(s.db); !errors.Is(err, ErrUnavailable) {
		t.Fatal("failed migration accepted", err)
	}
	assertShoppingFKEnabled(t, s)
	if !reflect.DeepEqual(before, shoppingDataSnapshot(t, s.db)) {
		t.Fatal("failed v14 changed rows")
	}
	var schema string
	if s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='connections'`).Scan(&schema) != nil || strings.Contains(schema, "xianyu") {
		t.Fatal("failed v14 changed constraint")
	}
	var remnants int
	if s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='shopping_v14_connections'`).Scan(&remnants) != nil || remnants != 0 {
		t.Fatal("partial migration table left behind")
	}
	if _, err := s.db.Exec(`DROP TRIGGER reject_shopping_migration`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(s.db); err != nil {
		t.Fatal("retry failed", err)
	}
	assertShoppingFKEnabled(t, s)
}

func TestShoppingV14RejectsExistingForeignKeyViolationBeforeCommit(t *testing.T) {
	s := shoppingLegacyV13(t, filepath.Join(t.TempDir(), "invalid-fk-v13.db"))
	// Simulate pre-existing corruption only in this encrypted temporary fixture.
	if _, err := s.db.Exec(`PRAGMA foreign_keys=OFF; INSERT INTO contacts(id,workspace_id,connection_id,peer_id,display_name) VALUES('orphan','local-default','missing-connection','peer','synthetic'); PRAGMA foreign_keys=ON;`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(s.db); !errors.Is(err, ErrUnavailable) {
		t.Fatal("foreign_key_check did not reject invalid graph", err)
	}
	var version, enabled int
	if s.db.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&version) != nil || version != 13 {
		t.Fatal("invalid graph recorded migration")
	}
	if s.db.QueryRow(`PRAGMA foreign_keys`).Scan(&enabled) != nil || enabled != 1 {
		t.Fatal("failed graph check left foreign keys disabled")
	}
	if _, err := s.db.Exec(`INSERT INTO contacts(id,workspace_id,connection_id,peer_id,display_name) VALUES('another-orphan','local-default','missing-connection','peer2','synthetic')`); err == nil {
		t.Fatal("failed migration disabled later FK enforcement")
	}
}

func TestShoppingV14RejectsUnknownV13ColumnsWithoutDiscardingData(t *testing.T) {
	s := shoppingLegacyV13(t, filepath.Join(t.TempDir(), "forked-v13.db"))
	aiTestConversation(t, s, "qq", "legacy-owner", "legacy-peer")
	if _, err := s.db.Exec(`ALTER TABLE connections ADD COLUMN custom_data TEXT; UPDATE connections SET custom_data='must preserve unknown column';`); err != nil {
		t.Fatal(err)
	}
	before := shoppingDataSnapshot(t, s.db)
	var schemaBefore, schemaAfter string
	if s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='connections'`).Scan(&schemaBefore) != nil {
		t.Fatal("schema fixture")
	}
	if err := migrate(s.db); !errors.Is(err, ErrUnavailable) {
		t.Fatal("unknown data column was discarded", err)
	}
	assertShoppingFKEnabled(t, s)
	if s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='connections'`).Scan(&schemaAfter) != nil || schemaAfter != schemaBefore {
		t.Fatal("unknown table schema changed")
	}
	if !reflect.DeepEqual(before, shoppingDataSnapshot(t, s.db)) {
		t.Fatal("unknown data changed on rejected migration")
	}
	var version int
	if s.db.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&version) != nil || version != 13 {
		t.Fatal("unknown shape marked upgraded")
	}
}

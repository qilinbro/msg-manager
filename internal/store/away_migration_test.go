package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/qilinbro/msg-manager/migrations"
)

// Before the branches converged, the away preview and upstream WeChat both
// used migration 4. Upgrade a real preview layout without losing its work.
func TestAwayPreviewV4UpgradePreservesTasksAndAddsWeChat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "away-v4.db")
	legacy := legacyV3Store(t, path)
	raw, err := migrations.Files.ReadFile("0006_away.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.db.Exec(string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.db.Exec(`INSERT INTO schema_migrations VALUES(4,'2026-11-12T00:00:00Z');
 INSERT INTO connections VALUES('c','local-default','email','fixture','fixture','imap');
 INSERT INTO contacts VALUES('p','local-default','c','sender','fixture');
 INSERT INTO conversations(id,workspace_id,connection_id,contact_id,peer_id,title) VALUES('v','local-default','c','p','sender','fixture');
 INSERT INTO messages(id,workspace_id,conversation_id,external_id,direction,body,occurred_at,is_read,source,email_json) VALUES('m','local-default','v','mail','incoming','Send the public demo by 18:00 Beijing time today.','2026-11-12T06:00:00Z',0,'imap','{"subject":"Public demo","from_address":"sender@example.test","folder":"INBOX"}')`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	session := AwaySession{ID: "legacy", StartedAt: now, EndsAt: now.Add(time.Hour), Sender: "sender@example.test", AccountKey: "fixture", ModelKey: "fixture", UIDValidity: 7, NextUID: 42}
	if err := legacy.StartAway(session); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.db.Exec(`INSERT INTO away_tasks(id,session_id,received_ms,title,requirements,deadline,evidence,draft,state,done) VALUES('m','legacy',?,'Public demo','["60 second video"]','2026-11-12T18:00:00+08:00','Send the public demo by 18:00 Beijing time today.','Saved edited reply','ready',1)`, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	before, err := legacy.AwaySnapshot()
	if err != nil {
		t.Fatal(err)
	}
	graph := snapshotV3Data(t, legacy.db)
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(path, testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	after, err := upgraded.AwaySnapshot()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("away task, evidence, draft, events or cursor changed", err)
	}
	if !reflect.DeepEqual(graph, snapshotV3Data(t, upgraded.db)) {
		t.Fatal("message or AI graph changed")
	}
	var version int
	if err := upgraded.db.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&version); err != nil || version != latestSchemaVersion {
		t.Fatal("schema not upgraded", err)
	}
	if _, err := upgraded.ImportWeChat(wechatBatch("wxid_self")); err != nil {
		t.Fatal("wechat provenance unavailable", err)
	}
	if err := upgraded.SaveWechatMonitor(context.Background(), "wxid_self", WechatMonitorSettings{Enabled: true, PeerIDs: []string{"wxid_peer"}, IntervalSeconds: 30}); err != nil {
		t.Fatal(err)
	}
	if err := migrate(upgraded.db); err != nil {
		t.Fatal("repeat open must be idempotent", err)
	}
}

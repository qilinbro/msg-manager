package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMilkyConnectionConfigCASReopenAndGrantRevocation(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "milky-settings.db")
	s, err := Open(databasePath, testKey)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if saved, err := s.LoadQQMilkyConfig(ctx); err != nil || saved != nil {
		t.Fatal("new store has unexpected configuration", err)
	}
	oldGrant := QQMilkyMonitorSettings{Enabled: true, PeerIDs: []string{"987654321"}}
	for _, account := range []string{"123456789", "123456788"} {
		if err := s.SaveQQMilkyMonitor(ctx, account, oldGrant); err != nil {
			t.Fatal(err)
		}
	}
	input := QQMilkyConfig{URL: "http://127.0.0.1:3010", AccountID: "123456789", AccessToken: "synthetic-milky-config-secret-never-plaintext", HistoryLimit: 20}
	saved, err := s.SaveQQMilkyConfig(ctx, input, 0)
	if err != nil || saved.Revision != 1 {
		t.Fatal("first save failed", err)
	}
	for _, account := range []string{"123456789", "123456788"} {
		grant, err := s.LoadQQMilkyMonitor(ctx, account)
		if err != nil || grant.Enabled || len(grant.PeerIDs) != 0 {
			t.Fatal("configuration save did not revoke prior receive grants", err)
		}
	}
	// A stale writer cannot revoke a receive grant or replace the new token.
	if err := s.SaveQQMilkyMonitor(ctx, input.AccountID, oldGrant); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveQQMilkyConfig(ctx, input, 0); !errors.Is(err, ErrStaleContext) {
		t.Fatal("stale revision accepted", err)
	}
	if grant, err := s.LoadQQMilkyMonitor(ctx, input.AccountID); err != nil || !grant.Enabled {
		t.Fatal("stale save changed authorization", err)
	}
	input.AccountID = "123456788"
	saved, err = s.SaveQQMilkyConfig(ctx, input, 1)
	if err != nil || saved.Revision != 2 {
		t.Fatal(err)
	}
	input.AccountID = "123456789"
	saved, err = s.SaveQQMilkyConfig(ctx, input, 2)
	if err != nil || saved.Revision != 3 {
		t.Fatal(err)
	}
	if grant, err := s.LoadQQMilkyMonitor(ctx, input.AccountID); err != nil || grant.Enabled || len(grant.PeerIDs) != 0 {
		t.Fatal("A -> B -> A revived authorization", err)
	}
	if body, err := json.Marshal(saved); err != nil || string(body) != "{}" {
		t.Fatal("secret configuration is serializable")
	}
	for _, format := range []string{"%v", "%+v", "%#v"} {
		if strings.Contains(fmt.Sprintf(format, saved), input.AccessToken) {
			t.Fatal("configuration formatting leaks a credential")
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(databasePath); err != nil || bytes.Contains(raw, []byte(input.AccessToken)) {
		t.Fatal("credential appeared in plaintext or database could not be inspected", err)
	}
	s, err = Open(databasePath, testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	loaded, err := s.LoadQQMilkyConfig(ctx)
	if err != nil || !reflect.DeepEqual(loaded, &saved) {
		t.Fatal("configuration did not survive encrypted reopen", err)
	}
	if _, err := s.db.Exec(`UPDATE qq_milky_connection_config SET access_token=? WHERE workspace_id=?`, "bad\ncredential", workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadQQMilkyConfig(ctx); !errors.Is(err, ErrUnavailable) {
		t.Fatal("corrupted credential accepted", err)
	}
}

func TestMilkyConfigMigrationRollbackAndCancellation(t *testing.T) {
	s := testStore(t)
	if _, err := s.db.Exec(`DROP TABLE qq_milky_connection_config; DROP TABLE receive_monitors; DELETE FROM schema_migrations WHERE version>=13; CREATE TRIGGER reject_milky_config_migration BEFORE INSERT ON schema_migrations WHEN NEW.version=13 BEGIN SELECT RAISE(ABORT,'synthetic-migration-failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(s.db); !errors.Is(err, ErrUnavailable) {
		t.Fatal("failed migration succeeded", err)
	}
	var version, tables int
	if s.db.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&version) != nil || version != 12 {
		t.Fatal("failed migration changed previous version")
	}
	if s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='qq_milky_connection_config'`).Scan(&tables) != nil || tables != 0 {
		t.Fatal("failed migration left a partial credential table")
	}
	if _, err := s.db.Exec(`DROP TRIGGER reject_milky_config_migration`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(s.db); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.SaveQQMilkyConfig(ctx, QQMilkyConfig{URL: "http://127.0.0.1:3010", AccountID: "123456789", AccessToken: "synthetic", HistoryLimit: 20}, 0); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled configuration was saved", err)
	}
	if loaded, err := s.LoadQQMilkyConfig(context.Background()); err != nil || loaded != nil {
		t.Fatal("cancelled save changed credential row", err)
	}
}

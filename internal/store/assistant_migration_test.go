package store

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/qilinbro/msg-manager/migrations"
)

func TestAssistantV12MigrationIsAtomicPreservesDataAndRequiresDigestReconsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "actual-v12.db")
	legacy := localDevicesLegacyV11(t, path)
	raw, err := migrations.Files.ReadFile("0012_local_devices.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.db.Exec(string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.db.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(12,'2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	id := aiTestConversation(t, legacy, "qq", "assistant-legacy", "peer")
	if _, err := legacy.db.Exec(`INSERT INTO ai_call_reservations(id,workspace_id,conversation_id,day,created_at) VALUES('synthetic-existing-call',?,?,'2026-11-17','2026-11-17T01:00:00Z')`, workspaceID, id); err != nil {
		t.Fatal(err)
	}
	device := localDeviceTestValue(t)
	mustCreateLocalDevice(t, legacy, device)
	if _, err := legacy.db.Exec(`UPDATE digest_schedules SET enabled=1,local_time='09:30',revision=7`); err != nil {
		t.Fatal(err)
	}
	before := snapshotV3Data(t, legacy.db)
	if _, err := legacy.db.Exec(`CREATE TRIGGER reject_assistant_migration BEFORE INSERT ON schema_migrations WHEN NEW.version=13 BEGIN SELECT RAISE(ABORT,'synthetic-v13-failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(legacy.db); err != ErrUnavailable {
		t.Fatal("partial migration was accepted", err)
	}
	var version, ledger, consentColumns, enabled, revision int
	if err := legacy.db.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&version); err != nil || version != 12 {
		t.Fatal("failed migration advanced version", version, err)
	}
	if err := legacy.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name IN ('ai_assistant_call_reservations','ai_assistant_daily_call_quota')`).Scan(&ledger); err != nil || ledger != 0 {
		t.Fatal("partial ledger/index committed", ledger, err)
	}
	if err := legacy.db.QueryRow(`SELECT count(*) FROM pragma_table_info('digest_schedules') WHERE name IN ('model_config_id','model_config_revision')`).Scan(&consentColumns); err != nil || consentColumns != 0 {
		t.Fatal("partial consent schema committed", consentColumns, err)
	}
	if err := legacy.db.QueryRow(`SELECT enabled,revision FROM digest_schedules`).Scan(&enabled, &revision); err != nil || enabled != 1 || revision != 7 {
		t.Fatal("failed migration changed schedule", enabled, revision, err)
	}
	if !reflect.DeepEqual(before, snapshotV3Data(t, legacy.db)) {
		t.Fatal("failed migration changed message graph")
	}
	if _, err := legacy.db.Exec(`DROP TRIGGER reject_assistant_migration`); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path, testKey)
	if err != nil {
		t.Fatal("v12 upgrade", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if !reflect.DeepEqual(before, snapshotV3Data(t, s.db)) {
		t.Fatal("v13 changed existing messages, evidence or reservations")
	}
	if got, err := s.LoadLocalDevice(context.Background(), device.ID); err != nil || !reflect.DeepEqual(got, device) {
		t.Fatal("v13 lost device authorization", err)
	}
	schedule, err := s.LoadDigestSchedule(context.Background())
	if err != nil || schedule.Enabled || schedule.LocalTime != "09:30" || schedule.Revision != 8 || schedule.ModelConfigID != "" || schedule.ModelConfigRevision != 0 {
		t.Fatal("legacy model use was not safely paused", schedule, err)
	}
	if count, err := s.CountAICalls("2026-11-17"); err != nil || count != 1 {
		t.Fatal("legacy quota lost", count, err)
	}
	if _, err := s.ReserveAssistantCallContext(context.Background(), "2026-11-17", 1); err != ErrQuotaExceeded {
		t.Fatal("new ledger bypassed existing quota", err)
	}
	schedule.Enabled, schedule.ModelConfigID, schedule.ModelConfigRevision = true, strings.Repeat("a", 64), 3
	schedule, err = s.SaveDigestSchedule(context.Background(), schedule, schedule.Revision)
	if err != nil {
		t.Fatal("reconsent", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path, testKey)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadDigestSchedule(context.Background())
	if err != nil || !reflect.DeepEqual(got, schedule) {
		t.Fatal("reopen reran consent revocation", got, err)
	}
}

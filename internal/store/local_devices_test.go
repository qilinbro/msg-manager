package store

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qilinbro/msg-manager/migrations"
)

const localDeviceFixtureMarker = "synthetic-local-device-fixture-not-a-real-grant"

func localDeviceTestID(t *testing.T) string {
	t.Helper()
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:])
}

func localDeviceTestIssuer(seed string) string {
	hash := sha256.Sum256([]byte("sociald-local-device-issuer-v1\x00" + seed))
	return hex.EncodeToString(hash[:])
}

func localDeviceTestDER(t *testing.T, key any) []byte {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func localDeviceTestValue(t *testing.T) LocalDevice {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	return LocalDevice{
		ID:             localDeviceTestID(t),
		PublicKey:      localDeviceTestDER(t, &key.PublicKey),
		ServiceOrigin:  "http://127.0.0.1:18763",
		FrontendOrigin: "http://localhost:1420",
		IssuerHash:     localDeviceTestIssuer(localDeviceFixtureMarker),
		CreatedAt:      now.Add(-time.Minute),
		ExpiresAt:      now.Add(24 * time.Hour),
	}
}

func localDeviceTestClone(t *testing.T, base LocalDevice) LocalDevice {
	t.Helper()
	base.ID = localDeviceTestID(t)
	base.PublicKey = bytes.Clone(base.PublicKey)
	return base
}

func mustCreateLocalDevice(t *testing.T, s *Store, device LocalDevice) {
	t.Helper()
	if err := s.CreateLocalDevice(context.Background(), device); err != nil {
		t.Fatalf("create synthetic local device: %v", err)
	}
}

func assertLocalDeviceMissing(t *testing.T, s *Store, id string) {
	t.Helper()
	got, err := s.LoadLocalDevice(context.Background(), id)
	if !errors.Is(err, ErrNotFound) || !reflect.DeepEqual(got, LocalDevice{}) {
		t.Fatalf("missing/revoked device returned a grant: err=%v", err)
	}
}

func localDeviceTestCount(t *testing.T, s *Store, workspace string) int {
	t.Helper()
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM local_devices WHERE workspace_id=?`, workspace).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func assertLocalDeviceEncryptedFiles(t *testing.T, dir string, markers [][]byte) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		t.Fatal("missing encrypted test files", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, marker := range markers {
			if bytes.Contains(raw, marker) {
				t.Fatalf("encrypted device test file exposed plaintext: %s", entry.Name())
			}
		}
	}
}

func TestLocalDevicesEmptyDatabaseEncryptedReopenAndPublicOnlySchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "devices.db")
	s, err := Open(path, testKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	var version int
	if err := s.db.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&version); err != nil || version != currentSchemaVersion {
		t.Fatal("empty database did not migrate to v12", version, err)
	}
	rows, err := s.db.Query(`PRAGMA table_info(local_devices)`)
	if err != nil {
		t.Fatal(err)
	}
	columns := map[string]string{}
	for rows.Next() {
		var index, notNull, primaryKey int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&index, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		columns[name] = kind
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	wantColumns := map[string]string{"workspace_id": "TEXT", "id": "TEXT", "public_key": "BLOB", "service_origin": "TEXT", "frontend_origin": "TEXT", "issuer_hash": "TEXT", "created_at": "INTEGER", "expires_at": "INTEGER"}
	if !reflect.DeepEqual(columns, wantColumns) {
		t.Fatal("local devices schema is not public-key-and-metadata only", columns)
	}
	device := localDeviceTestValue(t)
	assertLocalDeviceMissing(t, s, device.ID)
	mustCreateLocalDevice(t, s, device)
	var createdKind, expiresKind string
	var createdAt, expiresAt int64
	if err := s.db.QueryRow(`SELECT typeof(created_at),typeof(expires_at),created_at,expires_at FROM local_devices WHERE workspace_id=? AND id=?`, workspaceID, device.ID).Scan(&createdKind, &expiresKind, &createdAt, &expiresAt); err != nil || createdKind != "integer" || expiresKind != "integer" || createdAt != device.CreatedAt.Unix() || expiresAt != device.ExpiresAt.Unix() {
		t.Fatal("device times were not stored as integer Unix seconds", err)
	}
	markers := [][]byte{[]byte(localDeviceFixtureMarker), []byte(device.ID), device.PublicKey, []byte(device.IssuerHash), []byte(device.ServiceOrigin), []byte(device.FrontendOrigin), []byte("local_devices"), []byte("SQLite format 3"), []byte(testKey)}
	for _, file := range []string{path, path + "-wal"} {
		if raw, err := os.ReadFile(file); err != nil || len(raw) == 0 {
			t.Fatal("encrypted database/WAL missing", err)
		}
	}
	assertLocalDeviceEncryptedFiles(t, dir, markers)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	assertLocalDeviceEncryptedFiles(t, dir, markers)
	s, err = Open(path, testKey)
	if err != nil {
		t.Fatal("encrypted device reopen", err)
	}
	if got, err := s.LoadLocalDevice(context.Background(), device.ID); err != nil || !reflect.DeepEqual(got, device) {
		t.Fatal("grant did not survive encrypted reopen", err)
	}
	if _, err := Open(path, strings.Repeat("f", 64)); !errors.Is(err, ErrInvalidKey) {
		t.Fatal("wrong database key was accepted", err)
	}
	if err := s.RevokeLocalDevice(context.Background(), device.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path, testKey)
	if err != nil {
		t.Fatal(err)
	}
	assertLocalDeviceMissing(t, s, device.ID)
	assertLocalDeviceEncryptedFiles(t, dir, markers)
}

func localDevicesLegacyV11(t *testing.T, path string) *Store {
	t.Helper()
	uriPath := filepath.ToSlash(path)
	if runtime.GOOS == "windows" {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	query := url.Values{}
	query.Set("_pragma_key", "x'"+testKey+"'")
	u.RawQuery = query.Encode()
	db, err := sql.Open("social-sqlcipher", u.String())
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for index, name := range []string{"0001_messages.sql", "0002_email.sql", "0003_ai.sql", "0004_wechat_source.sql", "0005_wechat_monitor.sql", "0006_contact_profiles.sql", "0007_qq_milky_monitor.sql", "0008_ai_model_config.sql", "0009_profile_links.sql", "0010_daily_digest.sql", "0011_wechat_group_conversations.sql"} {
		raw, err := migrations.Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(raw)); err != nil {
			t.Fatal("build actual v11 encrypted fixture", name, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)`, index+1, "2026-01-01T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	return &Store{db: db}
}

func TestLocalDevicesV11MigrationPreservesDataHistoryAndSyntheticOldFixtures(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v11.db")
	legacy := localDevicesLegacyV11(t, path)
	id := aiTestConversation(t, legacy, "qq", "local-device-legacy-account", "legacy-peer")
	aiContext, err := legacy.AIContext(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.SaveAIAnalysis(id, aiContext.ContextHash, aiTestResult(aiContext)); err != nil {
		t.Fatal(err)
	}
	if err := legacy.UpdateConversation(id, ConversationPatch{Read: boolValue(true), Pinned: boolValue(true)}); err != nil {
		t.Fatal(err)
	}
	before := snapshotV3Data(t, legacy.db)
	var oldVersion, existingTable int
	if err := legacy.db.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&oldVersion); err != nil || oldVersion != 11 {
		t.Fatal("fixture is not actual v11", oldVersion, err)
	}
	if err := legacy.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='local_devices'`).Scan(&existingTable); err != nil || existingTable != 0 {
		t.Fatal("v12 table already existed in v11 fixture", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path, testKey)
	if err != nil {
		t.Fatal("v11 to v12 migration", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if !reflect.DeepEqual(before, snapshotV3Data(t, s.db)) {
		t.Fatal("v12 changed existing message/AI graph or sequence watermarks")
	}
	var version, historical, current int
	if err := s.db.QueryRow(`SELECT max(version),count(CASE WHEN version<=11 AND applied_at='2026-01-01T00:00:00Z' THEN 1 END),count(CASE WHEN version=12 THEN 1 END) FROM schema_migrations`).Scan(&version, &historical, &current); err != nil || version != currentSchemaVersion || historical != 11 || current != 1 {
		t.Fatal("migration history changed", version, historical, current, err)
	}
	if count := localDeviceTestCount(t, s, workspaceID); count != 0 {
		t.Fatal("migration created an authorization without user confirmation")
	}
	rows, err := s.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("v12 foreign key check failed")
	}
	_ = rows.Close()
	device := localDeviceTestValue(t)
	mustCreateLocalDevice(t, s, device)
	// Existing legacy tests synthesize older versions by removing migration
	// records without dropping additive tables. v12 must safely tolerate that.
	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version>=12`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path, testKey)
	if err != nil {
		t.Fatal("idempotent schema migration for synthetic old fixture", err)
	}
	if got, err := s.LoadLocalDevice(context.Background(), device.ID); err != nil || !reflect.DeepEqual(got, device) {
		t.Fatal("IF NOT EXISTS migration lost an existing grant", err)
	}
	if !reflect.DeepEqual(before, snapshotV3Data(t, s.db)) {
		t.Fatal("idempotent v12 migration changed existing data")
	}
	if err := s.db.QueryRow(`SELECT max(version),count(*) FROM schema_migrations`).Scan(&version, &current); err != nil || version != currentSchemaVersion || current != currentSchemaVersion {
		t.Fatal("v12 was not recorded exactly once", err)
	}
}

func TestLocalDevicesMigrationRollsBackAndRejectsFutureSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "migration-rollback.db")
	legacy := localDevicesLegacyV11(t, path)
	before := snapshotV3Data(t, legacy.db)
	if _, err := legacy.db.Exec(`CREATE TRIGGER reject_local_devices_migration BEFORE INSERT ON schema_migrations WHEN NEW.version=12 BEGIN SELECT RAISE(ABORT,'synthetic-v12-migration-failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(legacy.db); err != ErrUnavailable {
		t.Fatal("failed migration error was not sanitized", err)
	}
	var version, tableCount int
	if err := legacy.db.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&version); err != nil || version != 11 {
		t.Fatal("failed v12 migration recorded completion", version, err)
	}
	if err := legacy.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name IN ('local_devices','local_devices_expiry')`).Scan(&tableCount); err != nil || tableCount != 0 {
		t.Fatal("failed v12 migration left an authorization table or index", err)
	}
	if !reflect.DeepEqual(before, snapshotV3Data(t, legacy.db)) {
		t.Fatal("failed v12 migration changed existing data")
	}
	if _, err := legacy.db.Exec(`DROP TRIGGER reject_local_devices_migration`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(legacy.db); err != nil {
		t.Fatal("v12 migration retry failed", err)
	}
	if _, err := legacy.db.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(15,'2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	if opened, err := Open(path, testKey); err != ErrUnavailable {
		if opened != nil {
			_ = opened.Close()
		}
		t.Fatal("unknown future schema was accepted", err)
	}
}

func TestLocalDevicesCRUDOriginsCanonicalTimeAndIdempotentRevoke(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	base := localDeviceTestValue(t)
	base.ExpiresAt = base.CreatedAt.Add(90 * 24 * time.Hour)
	for index, origin := range []string{"http://127.0.0.1:1420", "http://localhost:1420", "tauri://localhost", "http://tauri.localhost", "https://tauri.localhost"} {
		device := localDeviceTestClone(t, base)
		device.FrontendOrigin = origin
		device.ServiceOrigin = "http://127.0.0.1:1"
		if index%2 == 0 {
			device.ServiceOrigin = "http://127.0.0.1:65535"
		}
		if index == 0 {
			// Equivalent whole-second instants need no lossy conversion; reads
			// always return UTC, independent of the caller's location object.
			device.CreatedAt = device.CreatedAt.In(time.FixedZone("test-offset", 8*60*60))
			device.ExpiresAt = device.ExpiresAt.In(time.FixedZone("test-offset", 8*60*60))
		}
		mustCreateLocalDevice(t, s, device)
		want := device
		want.CreatedAt, want.ExpiresAt = device.CreatedAt.UTC(), device.ExpiresAt.UTC()
		loaded, err := s.LoadLocalDevice(ctx, device.ID)
		if err != nil || !reflect.DeepEqual(loaded, want) {
			t.Fatal("public grant did not round-trip", err)
		}
		loaded.PublicKey[0] ^= 0xff
		if got, err := s.LoadLocalDevice(ctx, device.ID); err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("caller mutated the persisted public key", err)
		}
		duplicate := localDeviceTestValue(t)
		duplicate.ID = device.ID
		if err := s.CreateLocalDevice(ctx, duplicate); !errors.Is(err, ErrConflict) {
			t.Fatal("existing ID was overwritten", err)
		}
		if got, err := s.LoadLocalDevice(ctx, device.ID); err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("conflicting registration changed existing grant", err)
		}
		for attempt := 0; attempt < 2; attempt++ {
			if err := s.RevokeLocalDevice(ctx, device.ID); err != nil {
				t.Fatal("revoke was not idempotent", err)
			}
		}
		assertLocalDeviceMissing(t, s, device.ID)
	}
	if err := s.RevokeLocalDevice(ctx, localDeviceTestID(t)); err != nil {
		t.Fatal("revoking an unknown canonical ID failed", err)
	}
	if count := localDeviceTestCount(t, s, workspaceID); count != 0 {
		t.Fatal("revoked grant remained on disk")
	}
}

func TestLocalDevicesPruneExpiryIssuerAndServiceCannotReviveGrants(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prune.db")
	s, err := Open(path, testKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	base := localDeviceTestValue(t)
	clock := time.Now().UTC().Truncate(time.Second).Add(time.Hour)
	base.ExpiresAt = clock.Add(time.Second)
	expired := localDeviceTestClone(t, base)
	expired.ExpiresAt = clock
	otherIssuer := localDeviceTestClone(t, base)
	otherIssuer.IssuerHash = localDeviceTestIssuer("rotated-issuer")
	otherService := localDeviceTestClone(t, base)
	otherService.ServiceOrigin = "http://127.0.0.1:18764"
	bothChanged := localDeviceTestClone(t, otherIssuer)
	bothChanged.ServiceOrigin = otherService.ServiceOrigin
	for _, device := range []LocalDevice{base, expired, otherIssuer, otherService, bothChanged} {
		mustCreateLocalDevice(t, s, device)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := s.PruneLocalDevices(ctx, base.IssuerHash, base.ServiceOrigin, clock.Add(999*time.Millisecond)); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := s.LoadLocalDevice(ctx, base.ID); err != nil || !reflect.DeepEqual(got, base) {
		t.Fatal("expiry boundary removed the still-valid next-second grant", err)
	}
	for _, device := range []LocalDevice{expired, otherIssuer, otherService, bothChanged} {
		assertLocalDeviceMissing(t, s, device.ID)
	}
	if count := localDeviceTestCount(t, s, workspaceID); count != 1 {
		t.Fatal("prune ignored stale grants instead of deleting them")
	}
	issuerB := localDeviceTestClone(t, base)
	issuerB.IssuerHash = otherIssuer.IssuerHash
	mustCreateLocalDevice(t, s, issuerB)
	if err := s.PruneLocalDevices(ctx, issuerB.IssuerHash, base.ServiceOrigin, clock); err != nil {
		t.Fatal(err)
	}
	assertLocalDeviceMissing(t, s, base.ID)
	if _, err := s.LoadLocalDevice(ctx, issuerB.ID); err != nil {
		t.Fatal("new issuer's current grant was pruned", err)
	}
	if err := s.PruneLocalDevices(ctx, base.IssuerHash, base.ServiceOrigin, clock); err != nil {
		t.Fatal(err)
	}
	assertLocalDeviceMissing(t, s, base.ID)
	assertLocalDeviceMissing(t, s, issuerB.ID)
	serviceA := localDeviceTestClone(t, base)
	serviceB := localDeviceTestClone(t, otherService)
	mustCreateLocalDevice(t, s, serviceA)
	mustCreateLocalDevice(t, s, serviceB)
	if err := s.PruneLocalDevices(ctx, base.IssuerHash, serviceB.ServiceOrigin, clock); err != nil {
		t.Fatal(err)
	}
	assertLocalDeviceMissing(t, s, serviceA.ID)
	if _, err := s.LoadLocalDevice(ctx, serviceB.ID); err != nil {
		t.Fatal("new service origin's current grant was pruned", err)
	}
	if err := s.PruneLocalDevices(ctx, base.IssuerHash, serviceA.ServiceOrigin, clock); err != nil {
		t.Fatal(err)
	}
	if count := localDeviceTestCount(t, s, workspaceID); count != 0 {
		t.Fatal("A-B-A issuer/service rotation retained old grants")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path, testKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, device := range []LocalDevice{base, expired, otherIssuer, otherService, bothChanged, issuerB, serviceA, serviceB} {
		assertLocalDeviceMissing(t, s, device.ID)
	}
}

func TestLocalDevicesRejectInvalidMetadataKeysAndIdentifiers(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	base := localDeviceTestValue(t)
	mustCreateLocalDevice(t, s, base)
	assertUnchanged := func(t *testing.T) {
		t.Helper()
		got, err := s.LoadLocalDevice(ctx, base.ID)
		if err != nil || !reflect.DeepEqual(got, base) || localDeviceTestCount(t, s, workspaceID) != 1 {
			t.Fatal("invalid input changed an existing grant", err)
		}
	}
	for index, id := range []string{"", "a", base.ID + "A", base.ID + "==", base.ID[:21] + "B", "+" + base.ID[1:], "/" + base.ID[1:], " " + base.ID[1:], "\n" + base.ID[1:], "中" + base.ID[3:]} {
		t.Run(fmt.Sprintf("id-%d", index), func(t *testing.T) {
			device := base
			device.ID = id
			if err := s.CreateLocalDevice(ctx, device); !errors.Is(err, ErrInvalidInput) {
				t.Fatal("noncanonical ID accepted for create", err)
			}
			if got, err := s.LoadLocalDevice(ctx, id); !errors.Is(err, ErrInvalidInput) || !reflect.DeepEqual(got, LocalDevice{}) {
				t.Fatal("noncanonical ID accepted for load", err)
			}
			if err := s.RevokeLocalDevice(ctx, id); !errors.Is(err, ErrInvalidInput) {
				t.Fatal("noncanonical ID accepted for revoke", err)
			}
			assertUnchanged(t)
		})
	}
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	privateSEC1, err := x509.MarshalECPrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	privatePKCS8, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	invalidPoint := bytes.Clone(base.PublicKey)
	clear(invalidPoint[len(invalidPoint)-64:])
	keys := map[string][]byte{
		"nil": nil, "empty": {}, "garbage": []byte("not-a-key"), "oversized": bytes.Repeat([]byte{0x42}, 4096),
		"private-sec1": privateSEC1, "private-pkcs8": privatePKCS8, "p384": localDeviceTestDER(t, &p384.PublicKey),
		"raw-point": elliptic.Marshal(elliptic.P256(), private.X, private.Y), "raw-private-scalar": private.D.Bytes(),
		"ed25519":       localDeviceTestDER(t, ed25519.PublicKey(bytes.Repeat([]byte{0x42}, ed25519.PublicKeySize))),
		"invalid-point": invalidPoint, "trailing-data": append(bytes.Clone(base.PublicKey), 0),
		"pem-not-der": pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: base.PublicKey}),
	}
	for name, key := range keys {
		t.Run("key-"+name, func(t *testing.T) {
			device := localDeviceTestClone(t, base)
			device.PublicKey = key
			if err := s.CreateLocalDevice(ctx, device); !errors.Is(err, ErrInvalidInput) {
				t.Fatal("non-public-P256 SPKI accepted", err)
			}
			assertUnchanged(t)
		})
	}
	for index, origin := range []string{"", "http://127.0.0.1", "http://localhost:18763", "https://127.0.0.1:18763", "http://127.0.0.2:18763", "http://[::1]:18763", "http://[::ffff:127.0.0.1]:18763", "http://127.0.0.1:0", "http://127.0.0.1:65536", "http://127.0.0.1:018763", "http://127.0.0.1:01", "http://127.0.0.1:+1", "http://127.0.0.1:1e3", "http://127.0.0.1:18763/", "http://127.0.0.1:18763/path", "http://127.0.0.1:18763?x", "http://127.0.0.1:18763#x", "http://user@127.0.0.1:18763", "HTTP://127.0.0.1:18763", " http://127.0.0.1:18763", "http://127.0.0.1:18763\n"} {
		t.Run(fmt.Sprintf("service-origin-%d", index), func(t *testing.T) {
			device := localDeviceTestClone(t, base)
			device.ServiceOrigin = origin
			if err := s.CreateLocalDevice(ctx, device); !errors.Is(err, ErrInvalidInput) {
				t.Fatal("noncanonical service origin accepted", err)
			}
			if err := s.PruneLocalDevices(ctx, base.IssuerHash, origin, time.Now()); !errors.Is(err, ErrInvalidInput) {
				t.Fatal("invalid prune origin accepted", err)
			}
			assertUnchanged(t)
		})
	}
	for index, origin := range []string{"", "null", "http://localhost", "http://localhost:01420", "http://localhost:1420/", "http://127.0.0.1:1420/", "https://localhost:1420", "http://localhost:1420@evil.example", "tauri://LOCALHOST", "tauri://localhost/", "https://tauri.localhost/", "file://localhost", "https://evil.example"} {
		t.Run(fmt.Sprintf("frontend-origin-%d", index), func(t *testing.T) {
			device := localDeviceTestClone(t, base)
			device.FrontendOrigin = origin
			if err := s.CreateLocalDevice(ctx, device); !errors.Is(err, ErrInvalidInput) {
				t.Fatal("frontend origin outside allowlist accepted", err)
			}
			assertUnchanged(t)
		})
	}
	for index, issuer := range []string{"", strings.ToUpper(base.IssuerHash), base.IssuerHash[:63], base.IssuerHash + "0", strings.Repeat("g", 64), "0x" + base.IssuerHash[2:], "\n" + base.IssuerHash[1:]} {
		t.Run(fmt.Sprintf("issuer-%d", index), func(t *testing.T) {
			device := localDeviceTestClone(t, base)
			device.IssuerHash = issuer
			if err := s.CreateLocalDevice(ctx, device); !errors.Is(err, ErrInvalidInput) {
				t.Fatal("noncanonical issuer hash accepted", err)
			}
			if err := s.PruneLocalDevices(ctx, issuer, base.ServiceOrigin, time.Now()); !errors.Is(err, ErrInvalidInput) {
				t.Fatal("invalid prune issuer accepted", err)
			}
			assertUnchanged(t)
		})
	}
	invalidTimes := map[string]func(*LocalDevice){
		"created-zero":           func(d *LocalDevice) { d.CreatedAt = time.Time{} },
		"expires-zero":           func(d *LocalDevice) { d.ExpiresAt = time.Time{} },
		"equal":                  func(d *LocalDevice) { d.ExpiresAt = d.CreatedAt },
		"reversed":               func(d *LocalDevice) { d.ExpiresAt = d.CreatedAt.Add(-time.Second) },
		"too-long":               func(d *LocalDevice) { d.ExpiresAt = d.CreatedAt.Add(90*24*time.Hour + time.Second) },
		"created-fraction":       func(d *LocalDevice) { d.CreatedAt = d.CreatedAt.Add(time.Nanosecond) },
		"expires-fraction":       func(d *LocalDevice) { d.ExpiresAt = d.ExpiresAt.Add(time.Nanosecond) },
		"already-expired":        func(d *LocalDevice) { d.ExpiresAt = d.CreatedAt.Add(time.Second) },
		"future-created":         func(d *LocalDevice) { d.CreatedAt = time.Now().UTC().Truncate(time.Second).Add(time.Hour) },
		"negative-epoch":         func(d *LocalDevice) { d.CreatedAt = time.Unix(-1, 0).UTC() },
		"zero-epoch":             func(d *LocalDevice) { d.CreatedAt = time.Unix(0, 0).UTC() },
		"beyond-supported-range": func(d *LocalDevice) { d.ExpiresAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) },
		"extreme-epoch":          func(d *LocalDevice) { d.ExpiresAt = time.Unix(1<<62, 0).UTC() },
	}
	for name, mutate := range invalidTimes {
		t.Run("time-"+name, func(t *testing.T) {
			device := localDeviceTestClone(t, base)
			mutate(&device)
			if err := s.CreateLocalDevice(ctx, device); !errors.Is(err, ErrInvalidInput) {
				t.Fatal("unreasonable timestamp accepted", err)
			}
			assertUnchanged(t)
		})
	}
	for _, now := range []time.Time{{}, time.Unix(0, 0), time.Unix(-1, 0), time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)} {
		if err := s.PruneLocalDevices(ctx, base.IssuerHash, base.ServiceOrigin, now); !errors.Is(err, ErrInvalidInput) {
			t.Fatal("invalid prune clock accepted", err)
		}
		assertUnchanged(t)
	}
}

func TestLocalDevicesLoadCorruptedRowsFailsClosedAndExpiredIsAbsent(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	base := localDeviceTestValue(t)
	invalidPoint := bytes.Clone(base.PublicKey)
	clear(invalidPoint[len(invalidPoint)-64:])
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, assignment string
		value            any
	}{
		{"invalid-point", "public_key=?", invalidPoint},
		{"private-key", "public_key=?", privateDER},
		{"text-key", "public_key=CAST(? AS TEXT)", base.PublicKey},
		{"empty-key", "public_key=?", []byte{}},
		{"issuer", "issuer_hash=?", strings.ToUpper(base.IssuerHash)},
		{"blob-issuer", "issuer_hash=CAST(? AS BLOB)", base.IssuerHash},
		{"service-origin", "service_origin=?", "http://localhost:18763"},
		{"blob-service-origin", "service_origin=CAST(? AS BLOB)", base.ServiceOrigin},
		{"frontend-origin", "frontend_origin=?", "http://evil.example"},
		{"blob-frontend-origin", "frontend_origin=CAST(? AS BLOB)", base.FrontendOrigin},
		{"created-zero", "created_at=?", int64(0)},
		{"future-created", "created_at=?", time.Now().Add(time.Hour).Unix()},
		{"created-text", "created_at=?", base.CreatedAt.Format(time.RFC3339Nano)},
		{"created-real", "created_at=?", 1.5},
		{"expires-zero", "expires_at=?", int64(0)},
		{"expires-reversed", "expires_at=?", base.CreatedAt.Add(-time.Second).Unix()},
		{"expires-equal", "expires_at=?", base.CreatedAt.Unix()},
		{"expires-too-long", "expires_at=?", base.CreatedAt.Add(90*24*time.Hour + time.Second).Unix()},
		{"expires-text", "expires_at=?", base.ExpiresAt.Format(time.RFC3339Nano)},
		{"expires-real", "expires_at=?", 1.5},
		{"expires-extreme", "expires_at=?", int64(1 << 62)},
	}
	for _, candidate := range cases {
		t.Run(candidate.name, func(t *testing.T) {
			mustCreateLocalDevice(t, s, base)
			// Simulate an on-disk damaged/legacy row, not a supported write path.
			if _, err := s.db.Exec(`PRAGMA ignore_check_constraints=ON`); err != nil {
				t.Fatal(err)
			}
			_, updateErr := s.db.Exec(`UPDATE local_devices SET `+candidate.assignment+` WHERE workspace_id=? AND id=?`, candidate.value, workspaceID, base.ID)
			if _, err := s.db.Exec(`PRAGMA ignore_check_constraints=OFF`); err != nil {
				t.Fatal(err)
			}
			if updateErr != nil {
				t.Fatal("could not seed synthetic damaged row", updateErr)
			}
			got, err := s.LoadLocalDevice(ctx, base.ID)
			if !errors.Is(err, ErrUnavailable) || !reflect.DeepEqual(got, LocalDevice{}) {
				t.Fatal("damaged row returned a partial or valid grant", err)
			}
			if err := s.RevokeLocalDevice(ctx, base.ID); err != nil {
				t.Fatal("could not revoke damaged grant", err)
			}
		})
	}
	mustCreateLocalDevice(t, s, base)
	if _, err := s.db.Exec(`UPDATE local_devices SET expires_at=? WHERE workspace_id=? AND id=?`, time.Now().Add(-time.Second).Unix(), workspaceID, base.ID); err != nil {
		t.Fatal(err)
	}
	assertLocalDeviceMissing(t, s, base.ID)
	if err := s.PruneLocalDevices(ctx, base.IssuerHash, base.ServiceOrigin, time.Now()); err != nil || localDeviceTestCount(t, s, workspaceID) != 0 {
		t.Fatal("expired row was not physically purged", err)
	}
}

func TestLocalDevicesCapacityExpiredCleanupAndConflict(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	base := localDeviceTestValue(t)
	var devices []LocalDevice
	for index := 0; index < MaxLocalDevices; index++ {
		device := localDeviceTestClone(t, base)
		// The cap is for the whole workspace, not per key, issuer or origin.
		if index%2 == 0 {
			device.IssuerHash = localDeviceTestIssuer("another-live-issuer")
			device.ServiceOrigin = "http://127.0.0.1:18764"
			device.FrontendOrigin = "tauri://localhost"
		}
		mustCreateLocalDevice(t, s, device)
		devices = append(devices, device)
	}
	extra := localDeviceTestClone(t, base)
	if err := s.CreateLocalDevice(ctx, extra); !errors.Is(err, ErrConflict) {
		t.Fatal("workspace exceeded the 32-device cap", err)
	}
	assertLocalDeviceMissing(t, s, extra.ID)
	if err := s.CreateLocalDevice(ctx, devices[0]); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate ID at capacity was not a conflict", err)
	}
	if _, err := s.db.Exec(`UPDATE local_devices SET expires_at=? WHERE workspace_id=? AND id=?`, time.Now().Add(-time.Second).Unix(), workspaceID, devices[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateLocalDevice(ctx, devices[0]); !errors.Is(err, ErrConflict) {
		t.Fatal("ID collision with an expired row revived its grant", err)
	}
	mustCreateLocalDevice(t, s, extra)
	assertLocalDeviceMissing(t, s, devices[0].ID)
	if count := localDeviceTestCount(t, s, workspaceID); count != MaxLocalDevices {
		t.Fatal("expired grant was not reclaimed atomically", count)
	}
	if err := s.RevokeLocalDevice(ctx, extra.ID); err != nil {
		t.Fatal(err)
	}
	mustCreateLocalDevice(t, s, localDeviceTestClone(t, base))
	if count := localDeviceTestCount(t, s, workspaceID); count != MaxLocalDevices {
		t.Fatal("revocation did not release the device slot", count)
	}
}

func TestLocalDevicesConcurrentCapacityAcrossStoreHandles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent.db")
	first, err := Open(path, testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(path, testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	base := localDeviceTestValue(t)
	var devices []LocalDevice
	for index := 0; index < MaxLocalDevices+24; index++ {
		devices = append(devices, localDeviceTestClone(t, base))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	type outcome struct {
		id  string
		err error
	}
	results := make(chan outcome, len(devices))
	start := make(chan struct{})
	var workers sync.WaitGroup
	for index, device := range devices {
		s := []*Store{first, second}[index%2]
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			results <- outcome{device.ID, s.CreateLocalDevice(ctx, device)}
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	succeeded, conflicted := 0, 0
	for result := range results {
		switch {
		case result.err == nil:
			succeeded++
			if _, err := second.LoadLocalDevice(ctx, result.id); err != nil {
				t.Fatal("committed grant missing from second handle", err)
			}
		case errors.Is(result.err, ErrConflict):
			conflicted++
			assertLocalDeviceMissing(t, first, result.id)
		default:
			t.Fatalf("unexpected concurrent registration error: %v", result.err)
		}
	}
	if succeeded != MaxLocalDevices || conflicted != len(devices)-MaxLocalDevices || localDeviceTestCount(t, first, workspaceID) != MaxLocalDevices {
		t.Fatal("concurrent creates bypassed the cap", succeeded, conflicted)
	}
}

func TestLocalDevicesCancellationDoesNotMutateOrLeaveMutexWaiters(t *testing.T) {
	s := testStore(t)
	base := localDeviceTestValue(t)
	mustCreateLocalDevice(t, s, base)
	candidate := localDeviceTestClone(t, base)
	operations := []struct {
		name string
		run  func(context.Context) error
	}{
		{"create", func(ctx context.Context) error { return s.CreateLocalDevice(ctx, candidate) }},
		{"load", func(ctx context.Context) error { _, err := s.LoadLocalDevice(ctx, base.ID); return err }},
		{"revoke", func(ctx context.Context) error { return s.RevokeLocalDevice(ctx, base.ID) }},
		{"prune", func(ctx context.Context) error {
			return s.PruneLocalDevices(ctx, localDeviceTestIssuer("cancelled-new-issuer"), base.ServiceOrigin, time.Now())
		}},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			if err := operation.run(nil); !errors.Is(err, ErrInvalidInput) {
				t.Fatal("nil context was accepted", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := operation.run(ctx); !errors.Is(err, context.Canceled) {
				t.Fatal("pre-cancelled operation was not cancelled", err)
			}
			func() {
				s.mu.Lock()
				defer s.mu.Unlock()
				ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
				defer cancel()
				done := make(chan error, 1)
				go func() { done <- operation.run(ctx) }()
				select {
				case err := <-done:
					if !errors.Is(err, context.DeadlineExceeded) {
						t.Fatal("mutex wait was not cancelled", err)
					}
				case <-time.After(time.Second):
					t.Fatal("cancelled operation remained blocked on the mutex")
				}
			}()
			func() {
				// With the only SQL connection reserved, Context SQL must
				// cancel pool waits too, rather than use non-context methods.
				conn, err := s.db.Conn(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
				defer cancel()
				done := make(chan error, 1)
				go func() { done <- operation.run(ctx) }()
				select {
				case err := <-done:
					if !errors.Is(err, context.DeadlineExceeded) {
						t.Fatal("SQL connection wait was not cancelled", err)
					}
				case <-time.After(time.Second):
					t.Fatal("cancelled operation remained blocked on SQL")
				}
			}()
			if got, err := s.LoadLocalDevice(context.Background(), base.ID); err != nil || !reflect.DeepEqual(got, base) || localDeviceTestCount(t, s, workspaceID) != 1 {
				t.Fatal("cancelled operation changed grants or left a lock waiter", err)
			}
			assertLocalDeviceMissing(t, s, candidate.ID)
		})
	}
	mustCreateLocalDevice(t, s, candidate)
}

func TestLocalDevicesCreateRollsBackCleanupAndSanitizesDatabaseErrors(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	expired := localDeviceTestValue(t)
	mustCreateLocalDevice(t, s, expired)
	if _, err := s.db.Exec(`UPDATE local_devices SET expires_at=? WHERE workspace_id=? AND id=?`, time.Now().Add(-time.Second).Unix(), workspaceID, expired.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TEMP TRIGGER reject_local_device BEFORE INSERT ON local_devices BEGIN SELECT RAISE(ABORT,'synthetic-sensitive-database-detail'); END`); err != nil {
		t.Fatal(err)
	}
	candidate := localDeviceTestClone(t, expired)
	if err := s.CreateLocalDevice(ctx, candidate); err != ErrUnavailable {
		t.Fatal("database failure was not sanitized", err)
	}
	var expiredCount int
	if err := s.db.QueryRow(`SELECT count(*) FROM local_devices WHERE workspace_id=? AND id=?`, workspaceID, expired.ID).Scan(&expiredCount); err != nil || expiredCount != 1 || localDeviceTestCount(t, s, workspaceID) != 1 {
		t.Fatal("failed create partially committed expired cleanup", err)
	}
	assertLocalDeviceMissing(t, s, candidate.ID)
	if _, err := s.db.Exec(`DROP TRIGGER reject_local_device`); err != nil {
		t.Fatal(err)
	}
	mustCreateLocalDevice(t, s, candidate)
	if count := localDeviceTestCount(t, s, workspaceID); count != 1 {
		t.Fatal("successful retry did not atomically clean expired grant")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateLocalDevice(ctx, localDeviceTestClone(t, candidate)); err != ErrUnavailable {
		t.Fatal("closed database create error leaked", err)
	}
	if got, err := s.LoadLocalDevice(ctx, candidate.ID); err != ErrUnavailable || !reflect.DeepEqual(got, LocalDevice{}) {
		t.Fatal("closed database load returned a grant or leaked an error", err)
	}
	if err := s.RevokeLocalDevice(ctx, candidate.ID); err != ErrUnavailable {
		t.Fatal("closed database revoke error leaked", err)
	}
	if err := s.PruneLocalDevices(ctx, candidate.IssuerHash, candidate.ServiceOrigin, time.Now()); err != ErrUnavailable {
		t.Fatal("closed database prune error leaked", err)
	}
}

func TestLocalDevicesWorkspaceIsolationForEveryCRUDAndCapacity(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	base := localDeviceTestValue(t)
	const otherWorkspace = "synthetic-other-workspace"
	if _, err := s.db.Exec(`INSERT INTO workspaces(id) VALUES(?)`, otherWorkspace); err == nil {
		t.Fatal("single-workspace schema guard was bypassed")
	}
	// Simulate foreign rows in a damaged/future multi-workspace database. CRUD
	// predicates must remain scoped even when a schema guard was bypassed.
	if _, err := s.db.Exec(`PRAGMA ignore_check_constraints=ON`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO workspaces(id) VALUES(?)`, otherWorkspace); err != nil {
		t.Fatal(err)
	}
	var foreign []LocalDevice
	for index := 0; index < MaxLocalDevices; index++ {
		device := localDeviceTestClone(t, base)
		if index == 0 {
			device.ID = base.ID
		}
		device.IssuerHash = localDeviceTestIssuer("foreign-workspace-issuer")
		device.ServiceOrigin = "http://127.0.0.1:18764"
		device.ExpiresAt = time.Now().UTC().Truncate(time.Second).Add(-time.Second)
		if _, err := s.db.Exec(`INSERT INTO local_devices(workspace_id,id,public_key,service_origin,frontend_origin,issuer_hash,created_at,expires_at) VALUES(?,?,?,?,?,?,?,?)`, otherWorkspace, device.ID, device.PublicKey, device.ServiceOrigin, device.FrontendOrigin, device.IssuerHash, device.CreatedAt.Unix(), device.ExpiresAt.Unix()); err != nil {
			t.Fatal(err)
		}
		foreign = append(foreign, device)
	}
	if _, err := s.db.Exec(`PRAGMA ignore_check_constraints=OFF`); err != nil {
		t.Fatal(err)
	}
	for _, device := range foreign {
		assertLocalDeviceMissing(t, s, device.ID)
		if err := s.RevokeLocalDevice(ctx, device.ID); err != nil {
			t.Fatal(err)
		}
	}
	if count := localDeviceTestCount(t, s, otherWorkspace); count != MaxLocalDevices {
		t.Fatal("revoke crossed workspace boundary", count)
	}
	mustCreateLocalDevice(t, s, base)
	if got, err := s.LoadLocalDevice(ctx, base.ID); err != nil || !reflect.DeepEqual(got, base) {
		t.Fatal("foreign same-ID row shadowed the current workspace grant", err)
	}
	for index := 1; index < MaxLocalDevices; index++ {
		mustCreateLocalDevice(t, s, localDeviceTestClone(t, base))
	}
	if err := s.CreateLocalDevice(ctx, localDeviceTestClone(t, base)); !errors.Is(err, ErrConflict) {
		t.Fatal("current-workspace cap was not enforced", err)
	}
	if count := localDeviceTestCount(t, s, otherWorkspace); count != MaxLocalDevices {
		t.Fatal("create cleanup removed foreign expired grants", count)
	}
	if err := s.PruneLocalDevices(ctx, localDeviceTestIssuer("new-current-issuer"), base.ServiceOrigin, time.Now()); err != nil {
		t.Fatal(err)
	}
	if localDeviceTestCount(t, s, workspaceID) != 0 || localDeviceTestCount(t, s, otherWorkspace) != MaxLocalDevices {
		t.Fatal("prune crossed workspace boundary")
	}
	assertLocalDeviceMissing(t, s, base.ID)
	mustCreateLocalDevice(t, s, base)
	if err := s.RevokeLocalDevice(ctx, base.ID); err != nil {
		t.Fatal(err)
	}
	assertLocalDeviceMissing(t, s, base.ID)
	if localDeviceTestCount(t, s, workspaceID) != 0 || localDeviceTestCount(t, s, otherWorkspace) != MaxLocalDevices {
		t.Fatal("same-ID revocation removed the foreign workspace grant")
	}
}

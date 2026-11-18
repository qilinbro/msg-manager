package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"time"

	"github.com/qilinbro/msg-manager/migrations"
)

const currentSchemaVersion = 14

func migrate(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return ErrUnavailable
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return ErrUnavailable
	}
	var version int
	if tx.QueryRow(`SELECT coalesce(max(version),0) FROM schema_migrations`).Scan(&version) != nil {
		return ErrUnavailable
	}
	if version > currentSchemaVersion {
		return ErrUnavailable
	}
	if version == 0 {
		raw, err := migrations.Files.ReadFile("0001_messages.sql")
		if err != nil {
			return ErrUnavailable
		}
		if _, err := tx.Exec(string(raw)); err != nil {
			return ErrUnavailable
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES (1,?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return ErrUnavailable
		}
	}
	if version < 2 {
		raw, err := migrations.Files.ReadFile("0002_email.sql")
		if err != nil {
			return ErrUnavailable
		}
		if _, err = tx.Exec(string(raw)); err != nil {
			return ErrUnavailable
		}
		if _, err = tx.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES (2,?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return ErrUnavailable
		}
	}
	if version < 3 {
		raw, err := migrations.Files.ReadFile("0003_ai.sql")
		if err != nil {
			return ErrUnavailable
		}
		if _, err = tx.Exec(string(raw)); err != nil {
			return ErrUnavailable
		}
		if _, err = tx.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(3,?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return ErrUnavailable
		}
	}
	if version < 4 {
		raw, err := migrations.Files.ReadFile("0004_wechat_source.sql")
		if err != nil {
			return ErrUnavailable
		}
		if _, err = tx.Exec(string(raw)); err != nil {
			return ErrUnavailable
		}
		if _, err = tx.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(4,?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return ErrUnavailable
		}
	}
	if version < 5 {
		raw, err := migrations.Files.ReadFile("0005_wechat_monitor.sql")
		if err != nil {
			return ErrUnavailable
		}
		if _, err = tx.Exec(string(raw)); err != nil {
			return ErrUnavailable
		}
		if _, err = tx.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(5,?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return ErrUnavailable
		}
	}
	if version < 6 {
		raw, err := migrations.Files.ReadFile("0006_contact_profiles.sql")
		if err != nil {
			return ErrUnavailable
		}
		if _, err = tx.Exec(string(raw)); err != nil {
			return ErrUnavailable
		}
		if _, err = tx.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(6,?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return ErrUnavailable
		}
	}
	if version < 7 {
		raw, err := migrations.Files.ReadFile("0007_qq_milky_monitor.sql")
		if err != nil {
			return ErrUnavailable
		}
		if _, err = tx.Exec(string(raw)); err != nil {
			return ErrUnavailable
		}
		if _, err = tx.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(7,?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return ErrUnavailable
		}
	}
	if version < 8 {
		raw, err := migrations.Files.ReadFile("0008_ai_model_config.sql")
		if err != nil {
			return ErrUnavailable
		}
		if _, err = tx.Exec(string(raw)); err != nil {
			return ErrUnavailable
		}
		if _, err = tx.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(8,?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return ErrUnavailable
		}
	}
	if version < 9 {
		raw, err := migrations.Files.ReadFile("0009_profile_links.sql")
		if err != nil {
			return ErrUnavailable
		}
		if _, err = tx.Exec(string(raw)); err != nil {
			return ErrUnavailable
		}
		if _, err = tx.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(9,?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return ErrUnavailable
		}
	}
	if version < 10 {
		raw, err := migrations.Files.ReadFile("0010_daily_digest.sql")
		if err != nil {
			return ErrUnavailable
		}
		if _, err = tx.Exec(string(raw)); err != nil {
			return ErrUnavailable
		}
		if _, err = tx.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(10,?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return ErrUnavailable
		}
	}
	if version < 11 {
		raw, err := migrations.Files.ReadFile("0011_wechat_group_conversations.sql")
		if err != nil {
			return ErrUnavailable
		}
		var hasKind int
		if err = tx.QueryRow(`SELECT count(*) FROM pragma_table_info('conversations') WHERE name='conversation_kind'`).Scan(&hasKind); err != nil {
			return ErrUnavailable
		}
		if hasKind == 0 {
			if _, err = tx.Exec(string(raw)); err != nil {
				return ErrUnavailable
			}
		}
		if _, err = tx.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(11,?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return ErrUnavailable
		}
	}
	if version < 12 {
		raw, err := migrations.Files.ReadFile("0012_local_devices.sql")
		if err != nil {
			return ErrUnavailable
		}
		if _, err = tx.Exec(string(raw)); err != nil {
			return ErrUnavailable
		}
		if _, err = tx.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(12,?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return ErrUnavailable
		}
	}
	if version < 13 {
		raw, err := migrations.Files.ReadFile("0013_milky_connection_config.sql")
		if err != nil {
			return ErrUnavailable
		}
		if _, err = tx.Exec(string(raw)); err != nil {
			return ErrUnavailable
		}
		if _, err = tx.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(13,?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return ErrUnavailable
		}
	}
	if tx.Commit() != nil {
		return ErrUnavailable
	}
	return migrateShoppingConnections(db)
}

// v14 changes a parent table's CHECK constraint. SQLite requires disabling
// foreign keys before BEGIN, then checking the full graph before COMMIT. A
// pinned connection prevents the disabled setting leaking to other operations.
// Earlier versioned SQL remains unchanged and commits before this phase.
func migrateShoppingConnections(db *sql.DB) (result error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		return ErrUnavailable
	}
	defer func() {
		// Use a fresh context even when migration timed out. A connection whose
		// FK enforcement cannot be restored must never return to the pool.
		restoreCtx, restoreCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer restoreCancel()
		_, restoreErr := conn.ExecContext(restoreCtx, `PRAGMA foreign_keys=ON`)
		var enabled int
		if restoreErr != nil || conn.QueryRowContext(restoreCtx, `PRAGMA foreign_keys`).Scan(&enabled) != nil || enabled != 1 {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			result = ErrUnavailable
		}
		_ = conn.Close()
	}()
	var version int
	if conn.QueryRowContext(ctx, `SELECT coalesce(max(version),0) FROM schema_migrations`).Scan(&version) != nil {
		return ErrUnavailable
	}
	if version == currentSchemaVersion {
		return nil
	}
	if version != 13 {
		return ErrUnavailable
	}
	if _, err = conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		return ErrUnavailable
	}
	var enabled int
	if conn.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&enabled) != nil || enabled != 0 {
		return ErrUnavailable
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return ErrUnavailable
	}
	defer tx.Rollback()
	// Recheck inside the write transaction so a second startup can only see a
	// completed v14, or rebuild a still-current v13. Never overwrite new data.
	if tx.QueryRowContext(ctx, `SELECT coalesce(max(version),0) FROM schema_migrations`).Scan(&version) != nil {
		return ErrUnavailable
	}
	if version == currentSchemaVersion {
		if tx.Commit() != nil {
			return ErrUnavailable
		}
		return nil
	}
	if version != 13 {
		return ErrUnavailable
	}
	if !knownV13Connections(ctx, tx) {
		return ErrUnavailable
	}
	rows, err := tx.QueryContext(ctx, `SELECT sql FROM sqlite_master WHERE tbl_name='connections' AND type IN ('index','trigger') AND sql IS NOT NULL ORDER BY type,name`)
	if err != nil {
		return ErrUnavailable
	}
	var dependentSQL []string
	for rows.Next() {
		var statement string
		if rows.Scan(&statement) != nil {
			_ = rows.Close()
			return ErrUnavailable
		}
		dependentSQL = append(dependentSQL, statement)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return ErrUnavailable
	}
	raw, err := migrations.Files.ReadFile("0014_shopping_imports.sql")
	if err != nil {
		return ErrUnavailable
	}
	if _, err = tx.ExecContext(ctx, string(raw)); err != nil {
		return ErrUnavailable
	}
	for _, statement := range dependentSQL {
		if _, err = tx.ExecContext(ctx, statement); err != nil {
			return ErrUnavailable
		}
	}
	rows, err = tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return ErrUnavailable
	}
	broken := rows.Next()
	rowsErr := rows.Err()
	_ = rows.Close()
	if broken || rowsErr != nil {
		return ErrUnavailable
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO schema_migrations(version,applied_at) VALUES(14,?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return ErrUnavailable
	}
	if tx.Commit() != nil {
		return ErrUnavailable
	}
	return nil
}

// A fork or externally edited database can report version 13 while carrying
// additional data columns. Never silently discard such data with a fixed copy.
// table_xinfo also exposes generated/hidden columns omitted by table_info.
func knownV13Connections(ctx context.Context, tx *sql.Tx) bool {
	rows, err := tx.QueryContext(ctx, `PRAGMA table_xinfo('connections')`)
	if err != nil {
		return false
	}
	defer rows.Close()
	want := []string{"id", "workspace_id", "platform", "account_id", "display_name", "source", "connector_source"}
	count := 0
	for rows.Next() {
		var index, notNull, primary, hidden int
		var name, kind string
		var defaultValue any
		if rows.Scan(&index, &name, &kind, &notNull, &defaultValue, &primary, &hidden) != nil || count >= len(want) || index != count || name != want[count] || kind != "TEXT" || hidden != 0 || defaultValue != nil {
			return false
		}
		wantPrimary, wantNotNull := 0, 1
		if count == 0 {
			wantPrimary, wantNotNull = 1, 0
		}
		if count == 6 {
			wantNotNull = 0
		}
		if primary != wantPrimary || notNull != wantNotNull {
			return false
		}
		count++
	}
	return rows.Err() == nil && count == len(want)
}

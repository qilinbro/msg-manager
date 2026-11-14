package store

import (
	"database/sql"
	"time"

	"github.com/qilinbro/msg-manager/migrations"
)

const latestSchemaVersion = 6

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
	if version > latestSchemaVersion {
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
	var hasConnectorSource int
	if err := tx.QueryRow(`SELECT count(*) FROM pragma_table_info('messages') WHERE name='connector_source'`).Scan(&hasConnectorSource); err != nil {
		return ErrUnavailable
	}
	// The early away preview also used version 4. Its existing rows must survive.
	if version < 4 || (version == 4 && hasConnectorSource == 0) {
		raw, err := migrations.Files.ReadFile("0004_wechat_source.sql")
		if err != nil {
			return ErrUnavailable
		}
		if _, err = tx.Exec(string(raw)); err != nil {
			return ErrUnavailable
		}
		if _, err = tx.Exec(`INSERT OR IGNORE INTO schema_migrations(version,applied_at) VALUES(4,?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
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
		var hasAway int
		if err := tx.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='away_sessions'`).Scan(&hasAway); err != nil {
			return ErrUnavailable
		}
		if hasAway == 0 {
			raw, err := migrations.Files.ReadFile("0006_away.sql")
			if err != nil {
				return ErrUnavailable
			}
			if _, err := tx.Exec(string(raw)); err != nil {
				return ErrUnavailable
			}
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(6,?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return ErrUnavailable
		}
	}
	if tx.Commit() != nil {
		return ErrUnavailable
	}
	return nil
}

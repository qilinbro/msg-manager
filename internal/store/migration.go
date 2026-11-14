package store

import (
	"database/sql"
	"time"

	"github.com/qilinbro/msg-manager/migrations"
)

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
	if version > 4 {
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
	if tx.Commit() != nil {
		return ErrUnavailable
	}
	return nil
}

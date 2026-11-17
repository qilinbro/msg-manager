package store

import (
	"database/sql"
	"time"

	"github.com/qilinbro/msg-manager/migrations"
)

const currentSchemaVersion = 13

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
		raw, err := migrations.Files.ReadFile("0013_assistant_quota.sql")
		if err != nil {
			return ErrUnavailable
		}
		if _, err = tx.Exec(string(raw)); err != nil {
			return ErrUnavailable
		}
		if err := migrateDigestConsent(tx); err != nil {
			return ErrUnavailable
		}
		if _, err = tx.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(13,?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return ErrUnavailable
		}
	}
	if tx.Commit() != nil {
		return ErrUnavailable
	}
	return nil
}

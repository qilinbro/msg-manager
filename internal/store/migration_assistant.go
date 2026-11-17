package store

import "database/sql"

// migrateDigestConsent binds recurring model use to the selection explicitly
// shown to the human. Older enabled schedules had no destination consent and
// must be disabled once, without losing the chosen time or any message data.
// The guards also tolerate legacy test fixtures that remove only migration rows.
func migrateDigestConsent(tx *sql.Tx) error {
	rows, err := tx.Query(`PRAGMA table_info(digest_schedules)`)
	if err != nil {
		return ErrUnavailable
	}
	hasID, hasRevision := false, false
	for rows.Next() {
		var index, notNull, primaryKey int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&index, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return ErrUnavailable
		}
		hasID = hasID || name == "model_config_id"
		hasRevision = hasRevision || name == "model_config_revision"
	}
	readErr := rows.Err()
	closeErr := rows.Close()
	if readErr != nil || closeErr != nil {
		return ErrUnavailable
	}
	if hasID && hasRevision {
		return nil
	}
	if !hasID {
		if _, err := tx.Exec(`ALTER TABLE digest_schedules ADD COLUMN model_config_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return ErrUnavailable
		}
	}
	if !hasRevision {
		if _, err := tx.Exec(`ALTER TABLE digest_schedules ADD COLUMN model_config_revision INTEGER NOT NULL DEFAULT 0 CHECK(model_config_revision>=0)`); err != nil {
			return ErrUnavailable
		}
	}
	if _, err := tx.Exec(`UPDATE digest_schedules SET enabled=0,model_config_id='',model_config_revision=0,revision=revision+1 WHERE enabled<>0 OR model_config_id<>'' OR model_config_revision<>0`); err != nil {
		return ErrUnavailable
	}
	return nil
}

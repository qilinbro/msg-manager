// Package store owns the single local workspace's encrypted message database.
// Building requires CGO and the sqlite_fts5 tag. Missing encryption or FTS5 fails
// closed; there is no plaintext or in-memory persistence fallback.
package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	sqlcipher "github.com/mutecomm/go-sqlcipher/v4"
)

var (
	ErrInvalidInput  = errors.New("invalid message management input")
	ErrInvalidCursor = errors.New("invalid or expired page cursor; reload the list")
	ErrNotFound      = errors.New("message resource not found")
	ErrConflict      = errors.New("import conflicts with an existing message identity")
	ErrUnavailable   = errors.New("encrypted storage is unavailable")
	ErrInvalidKey    = errors.New("encrypted storage key or database format is invalid")
)

const workspaceID = "local-default"

type Store struct {
	db *sql.DB
	mu sync.Mutex
}

func init() {
	sql.Register("social-sqlcipher", &sqlcipher.SQLiteDriver{ConnectHook: func(conn *sqlcipher.SQLiteConn) error {
		// ConnectHook executes after the driver applies the validated raw hex key.
		for _, statement := range []string{"PRAGMA temp_store=MEMORY", "PRAGMA cipher_memory_security=ON", "PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=5000", "PRAGMA synchronous=FULL"} {
			if _, err := conn.Exec(statement, nil); err != nil {
				return ErrUnavailable
			}
		}
		return nil
	}})
}

func Open(path, key string) (*Store, error) {
	keyBytes, err := hex.DecodeString(key)
	if err != nil || len(keyBytes) != 32 || len(key) != 64 {
		return nil, ErrInvalidKey
	}
	for i := range keyBytes {
		keyBytes[i] = 0
	}
	if path == "" || strings.HasPrefix(path, "file:") || path == ":memory:" || strings.HasPrefix(path, `\\`) || strings.HasPrefix(path, "//") {
		return nil, ErrInvalidInput
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, ErrInvalidInput
	}
	if !localDatabasePath(absolute) {
		return nil, ErrInvalidInput
	}
	if info, err := os.Lstat(absolute); err == nil && (info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return nil, ErrInvalidInput
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0700); err != nil {
		return nil, ErrUnavailable
	}
	uriPath := filepath.ToSlash(absolute)
	if runtime.GOOS == "windows" {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	query := url.Values{}
	// Only exactly 64 validated hex digits are interpolated into SQLCipher's
	// raw-key syntax; paths and user passphrases cannot enter a PRAGMA statement.
	query.Set("_pragma_key", "x'"+key+"'")
	query.Set("_foreign_keys", "on")
	query.Set("_busy_timeout", "5000")
	query.Set("_txlock", "immediate")
	u.RawQuery = query.Encode()
	db, err := sql.Open("social-sqlcipher", u.String())
	if err != nil {
		return nil, ErrUnavailable
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var count int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master").Scan(&count); err != nil {
		_ = db.Close()
		return nil, ErrInvalidKey
	}
	var cipherVersion, journalMode string
	if db.QueryRowContext(ctx, "PRAGMA cipher_version").Scan(&cipherVersion) != nil || cipherVersion == "" {
		_ = db.Close()
		return nil, ErrUnavailable
	}
	if db.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&journalMode) != nil || strings.ToLower(journalMode) != "wal" {
		_ = db.Close()
		return nil, ErrUnavailable
	}
	// This creates only an in-memory temporary table; the production schema is
	// migrated separately after encryption and compile features are verified.
	if _, err := db.ExecContext(ctx, "CREATE VIRTUAL TABLE temp.fts_probe USING fts5(body); DROP TABLE temp.fts_probe;"); err != nil {
		_ = db.Close()
		return nil, ErrUnavailable
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	if s.db.Close() != nil {
		return ErrUnavailable
	}
	return nil
}

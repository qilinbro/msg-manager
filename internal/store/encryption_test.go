package store

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestSQLCipherEncryptionWALFTSAndWrongKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "messages.db")
	s, err := Open(path, testKey)
	if err != nil {
		t.Fatalf("encrypted store open: %v", err)
	}
	defer s.Close()
	const sentinel = "synthetic-secret-正文-71948a40"
	if _, err := s.db.Exec("CREATE TABLE prototype(body TEXT); CREATE VIRTUAL TABLE prototype_fts USING fts5(body);"); err != nil {
		t.Fatal("SQLCipher schema/FTS5 failed")
	}
	if _, err := s.db.Exec("INSERT INTO prototype(body) VALUES (?);", sentinel); err != nil {
		t.Fatal("encrypted insert failed")
	}
	if _, err := s.db.Exec("INSERT INTO prototype_fts(body) VALUES (?)", sentinel); err != nil {
		t.Fatal("encrypted FTS insert failed")
	}
	for _, check := range []struct {
		pragma string
		want   int
	}{{"PRAGMA foreign_keys", 1}, {"PRAGMA temp_store", 2}, {"PRAGMA cipher_memory_security", 1}} {
		var value int
		if s.db.QueryRow(check.pragma).Scan(&value) != nil || value != check.want {
			t.Fatalf("security pragma %s not enabled", check.pragma)
		}
	}
	var found string
	if s.db.QueryRow("SELECT body FROM prototype").Scan(&found) != nil || found != sentinel {
		t.Fatal("encrypted text not readable with correct key")
	}
	for _, name := range []string{path, path + "-wal"} {
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal("encrypted database/WAL missing")
		}
		if bytes.Contains(raw, []byte(sentinel)) || bytes.Contains(raw, []byte("SQLite format 3")) {
			t.Fatal("database or WAL exposed plaintext")
		}
	}
	if _, err := Open(path, strings.Repeat("f", 64)); err != ErrInvalidKey {
		t.Fatal("wrong key was accepted or error was not sanitized")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, testKey)
	if err != nil {
		t.Fatal("correct-key reopen failed")
	}
	defer reopened.Close()
	if reopened.db.QueryRow("SELECT body FROM prototype").Scan(&found) != nil || found != sentinel {
		t.Fatal("data was lost on reopen")
	}
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if !entry.IsDir() {
			raw, _ := os.ReadFile(filepath.Join(dir, entry.Name()))
			if bytes.Contains(raw, []byte(sentinel)) {
				t.Fatal("temporary file exposed plaintext")
			}
		}
	}
}

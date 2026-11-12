//go:build windows

package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDatabaseRejectsNetworkAndLinkedParent(t *testing.T) {
	if localDatabasePath(`\\server\share\messages.db`) {
		t.Fatal("network path accepted")
	}
	dir := t.TempDir()
	if !localDatabasePath(filepath.Join(dir, "fresh", "messages.db")) {
		t.Fatal("local nonexisting database rejected")
	}
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "linked")
	if err := os.Symlink(target, link); err != nil {
		t.Log("Windows symlink creation unavailable; ancestor link rejection not exercised")
		return
	}
	if localDatabasePath(filepath.Join(link, "messages.db")) {
		t.Fatal("linked parent accepted")
	}
}

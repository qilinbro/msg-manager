package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestReceiveMonitorSettingsAreScopedAndRestored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receive-monitor.db")
	inbox, err := Open(path, testKey)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	enabled := json.RawMessage(`{"enabled":true,"cursor":427}`)
	paused := json.RawMessage(`{"enabled":false,"cursor":427}`)
	if err := inbox.SaveReceiveMonitor(ctx, "email", "account-folder", enabled); err != nil {
		t.Fatal(err)
	}
	for _, key := range [][2]string{{"email", "other-account"}, {"telegram", "account-folder"}} {
		state, err := inbox.LoadReceiveMonitor(ctx, key[0], key[1])
		if err != nil || state != nil {
			t.Fatal("authorization crossed channel or account", key, err)
		}
	}
	for _, invalid := range []json.RawMessage{nil, []byte("null"), []byte("[]"), []byte("{"), []byte(`{"key":"` + strings.Repeat("x", 16384) + `"}`)} {
		if err := inbox.SaveReceiveMonitor(ctx, "email", "account-folder", invalid); !errors.Is(err, ErrInvalidInput) {
			t.Fatal("invalid monitor state accepted", err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := inbox.SaveReceiveMonitor(canceled, "email", "account-folder", paused); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled settings write was accepted", err)
	}
	if actual, err := inbox.LoadReceiveMonitor(ctx, "email", "account-folder"); err != nil || string(actual) != string(enabled) {
		t.Fatal("invalid or interrupted write replaced valid settings", err)
	}
	if err := inbox.SaveReceiveMonitor(ctx, "email", "account-folder", paused); err != nil {
		t.Fatal(err)
	}
	if err := inbox.Close(); err != nil {
		t.Fatal(err)
	}
	inbox, err = Open(path, testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer inbox.Close()
	if actual, err := inbox.LoadReceiveMonitor(ctx, "email", "account-folder"); err != nil || string(actual) != string(paused) {
		t.Fatal("pause was lost on restart", err)
	}
}

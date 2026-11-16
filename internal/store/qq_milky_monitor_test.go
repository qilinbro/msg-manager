package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

func TestQQMilkyMonitorDefaultsIsolationAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "qq-milky.db")
	s, err := Open(path, testKey)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	wantDefault := QQMilkyMonitorSettings{PeerIDs: []string{}}
	if got, err := s.LoadQQMilkyMonitor(ctx, "123456789"); err != nil || !reflect.DeepEqual(got, wantDefault) {
		t.Fatal("new account was not disabled", got, err)
	}
	want := QQMilkyMonitorSettings{Enabled: true, PeerIDs: []string{"987654321", "234567890"}}
	if err := s.SaveQQMilkyMonitor(ctx, "123456789", want); err != nil {
		t.Fatal(err)
	}
	// IDs are stored in canonical order, so a replay of the same selection has
	// stable output and cannot alter the authorized set.
	want.PeerIDs = []string{"234567890", "987654321"}
	if got, err := s.LoadQQMilkyMonitor(ctx, "123456789"); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("saved allowlist differs", got, err)
	}
	if got, err := s.LoadQQMilkyMonitor(ctx, "123456788"); err != nil || !reflect.DeepEqual(got, wantDefault) {
		t.Fatal("allowlist crossed account scope", got, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path, testKey)
	if err != nil {
		t.Fatal("reopen encrypted store", err)
	}
	defer s.Close()
	if got, err := s.LoadQQMilkyMonitor(ctx, "123456789"); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("allowlist did not survive reopen", got, err)
	}
	if err := s.SaveQQMilkyMonitor(ctx, "123456789", QQMilkyMonitorSettings{}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.LoadQQMilkyMonitor(ctx, "123456789"); err != nil || got.Enabled || len(got.PeerIDs) != 0 {
		t.Fatal("disabled allowlist was not cleared", got, err)
	}
}

func TestQQMilkyMonitorRejectsInvalidUpdatesWithoutChangingExisting(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	baseline := QQMilkyMonitorSettings{Enabled: true, PeerIDs: []string{"987654321"}}
	if err := s.SaveQQMilkyMonitor(ctx, "123456789", baseline); err != nil {
		t.Fatal(err)
	}
	invalid := []QQMilkyMonitorSettings{
		{Enabled: true},
		{Enabled: false, PeerIDs: []string{"123456789"}},
		{Enabled: false, PeerIDs: []string{"987654321", "987654321"}},
		{Enabled: false, PeerIDs: []string{"0"}},
		{Enabled: false, PeerIDs: []string{"not-a-qq"}},
	}
	for _, candidate := range invalid {
		if err := s.SaveQQMilkyMonitor(ctx, "123456789", candidate); err != ErrInvalidInput {
			t.Fatalf("invalid update accepted: %+v, err=%v", candidate, err)
		}
		if got, err := s.LoadQQMilkyMonitor(ctx, "123456789"); err != nil || !reflect.DeepEqual(got, baseline) {
			t.Fatal("invalid update changed saved allowlist", got, err)
		}
	}
	for _, account := range []string{"", "1", "01", "abc", "123456789012345678901"} {
		if err := ValidateQQMilkyMonitorSettings(account, QQMilkyMonitorSettings{}); err != ErrInvalidInput {
			t.Fatalf("invalid account accepted: %q, err=%v", account, err)
		}
	}
}

package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestAwaySessionRestoresAfterRestartAndRejectsLateWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "away.db")
	first, err := Open(path, testKey)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	session := AwaySession{ID: "restart-session", StartedAt: now, EndsAt: now.Add(time.Hour), Sender: "sender@example.test", AccountKey: "fixture-mailbox", ModelKey: "fixture-model", UIDValidity: 4, NextUID: 18}
	if err := first.StartAway(session); err != nil {
		t.Fatal(err)
	}
	req := ImportRequest{Platform: "email", AccountID: "away-account", AccountName: "demo mailbox", Messages: []ImportMessage{{
		ExternalID: "fixture-mail", PeerID: "fixture-mail", PeerName: "Makers Week", Direction: "incoming", Body: "Public agenda is due tomorrow.", OccurredAt: now.Format(time.RFC3339Nano), Email: &EmailHeaders{Subject: "Makers Week", FromAddress: session.Sender, To: []string{"receiver@example.test"}, Folder: "INBOX"},
	}}}
	if err := first.QueueAwayMail(session.ID, req, now); err != nil {
		t.Fatal(err)
	}
	if err := first.AdvanceAway(session.ID, 19); err != nil {
		t.Fatal(err)
	}
	inFlight, err := first.TakeAwayTask(session.ID)
	if err != nil || inFlight == nil {
		t.Fatal("no queued task")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path, testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	active, err := reopened.RunningAway()
	if err != nil || active == nil || active.NextUID != 19 || active.ModelKey != session.ModelKey {
		t.Fatal("session and checkpoint not durable")
	}
	if err := reopened.RecoverAwayTask(active.ID); err != nil {
		t.Fatal(err)
	}
	snapshot, err := reopened.AwaySnapshot()
	if err != nil || len(snapshot.Tasks) != 1 || !snapshot.Tasks[0].DraftFailed {
		t.Fatal("interrupted draft was not kept for review")
	}
	if err := reopened.FinishAway(active.ID, "stopped", ""); err != nil {
		t.Fatal(err)
	}
	if err := reopened.QueueAwayMail(active.ID, req, now); !errors.Is(err, ErrAwayInactive) {
		t.Fatalf("stopped session queued mail: %v", err)
	}
	if err := reopened.CompleteAwayTask(active.ID, *inFlight, ""); !errors.Is(err, ErrAwayInactive) {
		t.Fatalf("stopped session accepted late draft: %v", err)
	}
}

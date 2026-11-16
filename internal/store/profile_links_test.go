package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfileLinksRequireExplicitAssociationPreserveSourcesAndCanUnlink(t *testing.T) {
	s := testStore(t)
	a := aiTestConversation(t, s, "qq", "source-account", "peer-a")
	b := aiTestConversation(t, s, "weixin", "target-account", "peer-b")
	pa := mustProfile(t, s, a)
	pb := patchProfile(t, s, b, ProfilePatch{ExpectedRevision: profilePointer(int64(0)), ManualNotes: profilePointer("target-source-note-remains-separate")})
	ctx := context.Background()
	before, err := s.GetProfileLinks(ctx, a)
	if err != nil || len(before.Linked) != 0 || len(before.Candidates) != 1 || before.Candidates[0].ConversationID != b || before.Candidates[0].Reason != "same_nickname" || strings.Contains(before.DocumentMarkdown, "target-source-note-remains-separate") {
		t.Fatal("same nickname was merged without confirmation", before, err)
	}
	linked, err := s.SetProfileLink(ctx, a, b, true)
	if err != nil || len(linked.Linked) != 1 || len(linked.Candidates) != 0 || !strings.Contains(linked.DocumentMarkdown, "target-source-note-remains-separate") || !strings.Contains(linked.DocumentMarkdown, a) || !strings.Contains(linked.DocumentMarkdown, b) {
		t.Fatal("linked document lost source distinction", linked, err)
	}
	if _, err := s.SetProfileLink(ctx, b, a, true); err != nil {
		t.Fatal("reversed association is not idempotent", err)
	}
	otherView, err := s.GetProfileLinks(ctx, b)
	if err != nil || len(otherView.Linked) != 1 || otherView.Linked[0].ConversationID != a {
		t.Fatal("symmetric identity association missing", otherView, err)
	}
	afterA, afterB := mustProfile(t, s, a), mustProfile(t, s, b)
	if afterA.Revision != pa.Revision || afterB.Revision != pb.Revision || afterA.AutoOrganize || afterB.AutoOrganize || afterA.ManualNotes != "" || afterB.ManualNotes != pb.ManualNotes {
		t.Fatal("association copied permissions or modified original profiles")
	}
	for _, pair := range [][2]string{{a, a}, {a, "../foreign"}, {a, "doesnotexist"}} {
		if _, err := s.SetProfileLink(ctx, pair[0], pair[1], true); err == nil {
			t.Fatal("invalid/foreign identity accepted", pair)
		}
	}
	unlinked, err := s.SetProfileLink(ctx, b, a, false)
	if err != nil || len(unlinked.Linked) != 0 || len(unlinked.Candidates) != 1 {
		t.Fatal("association did not unlink", unlinked, err)
	}
	if got := mustProfile(t, s, b); got.ManualNotes != pb.ManualNotes {
		t.Fatal("unlink deleted original profile data")
	}
}

func TestProfileLinksPersistAcrossReopenAndHonorCancellation(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "profile-links.db")
	s, err := Open(dbPath, testKey)
	if err != nil {
		t.Fatal(err)
	}
	a := aiTestConversation(t, s, "qq", "first", "peer")
	b := aiTestConversation(t, s, "telegram", "second", "peer")
	if _, err := s.SetProfileLink(context.Background(), a, b, true); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dbPath, testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.GetProfileLinks(context.Background(), a)
	if err != nil || len(got.Linked) != 1 || got.Linked[0].ConversationID != b {
		t.Fatal("encrypted association missing after reopen", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.SetProfileLink(ctx, a, b, false); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled unlink changed association", err)
	}
	got, err = s.GetProfileLinks(context.Background(), a)
	if err != nil || len(got.Linked) != 1 {
		t.Fatal("cancelled operation mutated durable links")
	}
	if _, err := s.db.Exec(`INSERT INTO profile_links(workspace_id,left_conversation_id,right_conversation_id,created_at)VALUES('foreign',?,?,?)`, a, b, profileNow()); err == nil {
		t.Fatal("foreign workspace inserted link")
	}
}

func TestProfileLinksBoundCandidatesAndConfirmedAssociations(t *testing.T) {
	s := testStore(t)
	a := aiTestConversation(t, s, "qq", "root", "peer")
	ids := []string{}
	for i := 0; i < 21; i++ {
		ids = append(ids, aiTestConversation(t, s, "telegram", fmt.Sprintf("scope-%d", i), "peer"))
	}
	list, err := s.GetProfileLinks(context.Background(), a)
	if err != nil || len(list.Candidates) != 20 || len(list.Linked) != 0 {
		t.Fatal("candidate listing is not bounded", err)
	}
	for _, id := range ids[:20] {
		if _, err := s.SetProfileLink(context.Background(), a, id, true); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.SetProfileLink(context.Background(), a, ids[20], true); !errors.Is(err, ErrConflict) {
		t.Fatal("unbounded linked document permitted", err)
	}
	if _, err := s.SetProfileLink(context.Background(), a, ids[0], true); err != nil {
		t.Fatal("idempotent replay refused at link limit", err)
	}
}

func TestProfileLinksMatchStableIDsOnlyInsideTheSamePlatform(t *testing.T) {
	s := testStore(t)
	ids := []string{}
	for i, platform := range []string{"qq", "qq", "weixin"} {
		r, err := s.Import(ImportRequest{Platform: platform, AccountID: fmt.Sprintf("account-%d", i), Messages: []ImportMessage{{ExternalID: "fixture", PeerID: "shared-id", PeerName: fmt.Sprintf("独立昵称%d", i), Direction: "incoming", Body: "合成记录", OccurredAt: "2026-01-01T00:00:00Z"}}})
		if err != nil || r.Conversations != 1 {
			t.Fatal(err)
		}
		connection := stableID("connection", platform, fmt.Sprintf("account-%d", i))
		ids = append(ids, stableID("conversation", connection, "shared-id"))
	}
	got, err := s.GetProfileLinks(context.Background(), ids[0])
	if err != nil || len(got.Candidates) != 1 || got.Candidates[0].ConversationID != ids[1] || got.Candidates[0].Reason != "same_platform_id" {
		t.Fatal("stable ID matching crossed platform scope", got, err)
	}
}

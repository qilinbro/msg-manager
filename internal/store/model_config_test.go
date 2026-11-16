package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestModelConfigEncryptedPersistenceCASAndRedaction(t *testing.T) {
	p := filepath.Join(t.TempDir(), "model.db")
	s, err := Open(p, testKey)
	if err != nil {
		t.Fatal(err)
	}
	input := ModelConfig{BaseURL: "https://api.deepseek.com", Model: "deepseek-chat", APIKey: "synthetic-model-key-should-stay-encrypted", DailyLimit: 23}
	if saved, e := s.LoadModelConfig(context.Background()); e != nil || saved != nil {
		t.Fatal("unexpected default", e)
	}
	first, err := s.SaveModelConfig(context.Background(), input, 0)
	if err != nil || first.Revision != 1 {
		t.Fatal("save", err)
	}
	input.Model = "another-model"
	if _, err = s.SaveModelConfig(context.Background(), input, 0); !errors.Is(err, ErrStaleContext) {
		t.Fatal("stale save accepted", err)
	}
	for _, file := range []string{p, p + "-wal"} {
		if raw, e := os.ReadFile(file); e == nil && strings.Contains(string(raw), first.APIKey) {
			t.Fatal("plaintext model credential in database")
		}
	}
	if strings.Contains(fmt.Sprintf("%+v %#v", first, first), first.APIKey) {
		t.Fatal("formatted key leaked")
	}
	encoded, _ := json.Marshal(first)
	if strings.Contains(string(encoded), first.APIKey) {
		t.Fatal("JSON key leaked")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(p, testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	saved, err := s.LoadModelConfig(context.Background())
	if err != nil || saved == nil || saved.APIKey != first.APIKey || saved.Model != first.Model || saved.Revision != 1 {
		t.Fatal("configuration did not survive reopen", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = s.SaveModelConfig(ctx, input, 1); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled write accepted", err)
	}
}

func TestModelConfigDestinationChangePersistentlyRevokesProfileGrants(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := ModelConfig{BaseURL: "https://api.deepseek.com", Model: "model-a", APIKey: "synthetic-key-a", DailyLimit: 10}
	saved, err := s.SaveModelConfig(ctx, a, 0)
	if err != nil {
		t.Fatal(err)
	}
	aTarget := modelConfigTarget(a)
	id := aiTestConversation(t, s, "qq", "revoke-config-account", "revoke-config-peer")
	p := patchProfile(t, s, id, ProfilePatch{ExpectedRevision: profilePointer(int64(0)), PublicName: profilePointer("合成公开名称"), ManualNotes: profilePointer("保留人工资料"), AutoOrganize: profilePointer(true), PublicSearchEnabled: profilePointer(true), ModelConfigID: profilePointer(aTarget)})
	for _, kind := range []string{"organize", "enrich"} {
		if _, err = s.EnqueueProfileJob(ctx, id, kind, aTarget, p.Revision); err != nil {
			t.Fatal(err)
		}
	}
	if claim, err := s.ClaimProfileJob(ctx, aTarget, time.Minute); err != nil || claim == nil {
		t.Fatal("claim before switch", err)
	}
	// Editing only credentials or quota must not silently remove valid grants.
	a.APIKey, a.DailyLimit = "synthetic-replaced-key", 11
	saved, err = s.SaveModelConfig(ctx, a, saved.Revision, aTarget)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustProfile(t, s, id); !got.AutoOrganize || !got.PublicSearchEnabled || got.Revision != p.Revision {
		t.Fatal("same-target edit changed authorization")
	}
	b := a
	b.Model = "model-b"
	if _, err := s.SaveModelConfig(ctx, b, saved.Revision-1, aTarget); !errors.Is(err, ErrStaleContext) {
		t.Fatal("stale save unexpectedly succeeded", err)
	}
	if got := mustProfile(t, s, id); !got.AutoOrganize || got.Revision != p.Revision {
		t.Fatal("stale save partially revoked authorization")
	}
	saved, err = s.SaveModelConfig(ctx, b, saved.Revision, aTarget)
	if err != nil {
		t.Fatal(err)
	}
	revoked := mustProfile(t, s, id)
	if revoked.AutoOrganize || revoked.PublicSearchEnabled || revoked.ModelConfigID != "" || revoked.Revision != p.Revision+1 || revoked.ManualNotes != "保留人工资料" {
		t.Fatal("target switch did not persistently revoke grants")
	}
	for _, job := range revoked.Jobs {
		if job.State != "cancelled" || job.LastErrorCode == nil || *job.LastErrorCode != "MODEL_CHANGED" || job.LeaseToken != "" {
			t.Fatal("old queued/running job survived switch")
		}
	}
	if _, err = s.SaveModelConfig(ctx, a, saved.Revision, modelConfigTarget(b)); err != nil {
		t.Fatal(err)
	}
	if count, err := s.ScheduleProfileJobs(ctx, aTarget); err != nil || count != 0 {
		t.Fatal("A-B-A restored old automatic grants", count, err)
	}
	if claim, err := s.ClaimProfileJob(ctx, aTarget, time.Minute); err != nil || claim != nil {
		t.Fatal("A-B-A restored cancelled job", err)
	}
}

func TestModelConfigFirstSaveHonorsEnvironmentTarget(t *testing.T) {
	for _, sameTarget := range []bool{true, false} {
		t.Run(fmt.Sprintf("same-target-%t", sameTarget), func(t *testing.T) {
			s := testStore(t)
			ctx := context.Background()
			a := ModelConfig{BaseURL: "https://api.deepseek.com", Model: "environment-model", APIKey: "synthetic-env-key", DailyLimit: 10}
			oldTarget := modelConfigTarget(a)
			id := aiTestConversation(t, s, "qq", "environment-config-account", "environment-config-peer")
			p := patchProfile(t, s, id, ProfilePatch{ExpectedRevision: profilePointer(int64(0)), AutoOrganize: profilePointer(true), ModelConfigID: profilePointer(oldTarget)})
			if !sameTarget {
				a.Model = "new-model"
			}
			if _, err := s.SaveModelConfig(ctx, a, 0, oldTarget); err != nil {
				t.Fatal(err)
			}
			got := mustProfile(t, s, id)
			if sameTarget {
				if !got.AutoOrganize || got.ModelConfigID != oldTarget || got.Revision != p.Revision {
					t.Fatal("first same-target save removed grants")
				}
			} else if got.AutoOrganize || got.ModelConfigID != "" || got.Revision != p.Revision+1 {
				t.Fatal("first destination change retained environment grants")
			}
		})
	}
}

func TestModelConfigDestinationChangeStalesManualAIContext(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := ModelConfig{BaseURL: "https://api.deepseek.com", Model: "manual-a", APIKey: "synthetic-manual-a", DailyLimit: 10}
	saved, err := s.SaveModelConfig(ctx, a, 0)
	if err != nil {
		t.Fatal(err)
	}
	id := aiTestConversation(t, s, "qq", "manual-config-account", "manual-config-peer")
	before, err := s.AIContext(id)
	if err != nil {
		t.Fatal(err)
	}
	b := a
	b.Model = "manual-b"
	saved, err = s.SaveModelConfig(ctx, b, saved.Revision, modelConfigTarget(a))
	if err != nil {
		t.Fatal(err)
	}
	afterB, err := s.AIContext(id)
	if err != nil || afterB.ContextHash == before.ContextHash {
		t.Fatal("destination change did not stale manual context", err)
	}
	if _, err = s.SaveModelConfig(ctx, a, saved.Revision, modelConfigTarget(b)); err != nil {
		t.Fatal(err)
	}
	afterA, err := s.AIContext(id)
	if err != nil || afterA.ContextHash == afterB.ContextHash || afterA.ContextHash == before.ContextHash {
		t.Fatal("A-B-A restored an old manual context", err)
	}
}

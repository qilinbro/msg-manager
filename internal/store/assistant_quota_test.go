package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAssistantQuotaSharedAtomicPersistentAndNoSyntheticConversation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "assistant-quota.db")
	s, err := Open(path, testKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	conversationID := aiTestConversation(t, s, "qq", "assistant-account", "assistant-peer")
	var accepted, rejected atomic.Int32
	var wg sync.WaitGroup
	for i := range 30 {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			var err error
			if index%2 == 0 {
				_, err = s.ReserveAssistantCallContext(context.Background(), "2026-11-17", 7)
			} else {
				_, err = s.ReserveAICallContext(context.Background(), "2026-11-17", conversationID, 7)
			}
			if err == nil {
				accepted.Add(1)
			} else if errors.Is(err, ErrQuotaExceeded) {
				rejected.Add(1)
			} else {
				t.Errorf("shared reservation failed: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if accepted.Load() != 7 || rejected.Load() != 23 {
		t.Fatal("mixed calls exceeded common budget", accepted.Load(), rejected.Load())
	}
	if count, err := s.CountAICallsContext(context.Background(), "2026-11-17"); err != nil || count != 7 {
		t.Fatal("shared count", count, err)
	}
	var conversations, secretColumns int
	if err := s.db.QueryRow(`SELECT count(*) FROM conversations`).Scan(&conversations); err != nil || conversations != 1 {
		t.Fatal("assistant quota created a synthetic conversation", conversations, err)
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM pragma_table_info('ai_assistant_call_reservations') WHERE name IN ('conversation_id','message','content','api_key','bearer','token','history')`).Scan(&secretColumns); err != nil || secretColumns != 0 {
		t.Fatal("assistant ledger stores data/credentials", secretColumns, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path, testKey)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := s.CountAICalls("2026-11-17"); err != nil || count != 7 {
		t.Fatal("quota lost on reopen", count, err)
	}
	if _, err := s.ReserveAssistantCallContext(context.Background(), "2026-11-17", 7); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatal("reopen reset assistant quota", err)
	}
	if _, err := s.ReserveAICallContext(context.Background(), "2026-11-17", conversationID, 7); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatal("assistant reservations invisible to conversation calls", err)
	}
	if id, err := s.ReserveAssistantCallContext(context.Background(), "2026-11-18", 7); err != nil || id == "" {
		t.Fatal("new day quota", err)
	}
	if _, err := s.ReserveAssistantCallContext(context.Background(), "2026-11-33", 7); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("invalid day", err)
	}
	if _, err := s.ReserveAssistantCallContext(context.Background(), "2026-11-18", 0); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("invalid limit", err)
	}
}

func TestAssistantQuotaAndAISettingsRespectCanceledLockWait(t *testing.T) {
	s := testStore(t)
	conversationID := aiTestConversation(t, s, "qq", "assistant-cancel-account", "assistant-cancel-peer")
	s.mu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := s.ReserveAssistantCallContext(ctx, "2026-11-17", 2); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("quota lock wait", err)
		}
	case <-time.After(time.Second):
		s.mu.Unlock()
		t.Fatal("assistant quota ignores cancellation")
	}
	if _, err := s.AISettingsContext(ctx, conversationID); !errors.Is(err, context.DeadlineExceeded) {
		s.mu.Unlock()
		t.Fatal("settings lock wait", err)
	}
	s.mu.Unlock()
	if count, err := s.CountAICalls("2026-11-17"); err != nil || count != 0 {
		t.Fatal("cancelled call consumed quota", count, err)
	}
}

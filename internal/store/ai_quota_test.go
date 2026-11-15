package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCountAICallsContextPreservesQuotaAndCancelsMutexWait(t *testing.T) {
	s := testStore(t)
	id := aiTestConversation(t, s, "qq", "quota-context-account", "quota-context-peer")
	const day = "2026-11-15"
	if _, err := s.ReserveAICall(day, id, 5); err != nil {
		t.Fatal(err)
	}
	if n, err := s.CountAICallsContext(context.Background(), day); err != nil || n != 1 {
		t.Fatal("context count lost shared ledger", n, err)
	}
	if n, err := s.CountAICalls(day); err != nil || n != 1 {
		t.Fatal("legacy wrapper changed", n, err)
	}
	if _, err := s.CountAICallsContext(nil, day); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("nil context accepted", err)
	}
	if _, err := s.CountAICallsContext(context.Background(), "invalid-day"); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("invalid day accepted", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := s.CountAICallsContext(ctx, day)
		finished <- err
	}()
	select {
	case err := <-finished:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("mutex wait did not preserve cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled quota count waited for storage mutex release")
	}
	// Still owning the mutex proves cancellation joined the operation without
	// leaving a detached waiter to resume after shutdown or settings changes.
}

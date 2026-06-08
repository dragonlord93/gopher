package memory_test

import (
	"context"
	"errors"
	"testing"

	"github.com/shivam/abnormal/ratelimit"
	"github.com/shivam/abnormal/ratelimit/memory"
)

func TestStore_MutateCreatesAndUpdatesState(t *testing.T) {
	store := memory.NewStore()
	ctx := context.Background()

	result, err := store.Mutate(ctx, "key-1", func(current []byte) ([]byte, ratelimit.Result, error) {
		if current != nil {
			t.Fatalf("expected nil current state, got %q", current)
		}
		return []byte("v1"), ratelimit.Result{Allowed: true, Remaining: 1}, nil
	})
	if err != nil {
		t.Fatalf("mutate: %v", err)
	}
	if !result.Allowed || result.Remaining != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}

	result, err = store.Mutate(ctx, "key-1", func(current []byte) ([]byte, ratelimit.Result, error) {
		if string(current) != "v1" {
			t.Fatalf("expected current state v1, got %q", current)
		}
		return []byte("v2"), ratelimit.Result{Allowed: false, Remaining: 0}, nil
	})
	if err != nil {
		t.Fatalf("mutate: %v", err)
	}
	if result.Allowed {
		t.Fatal("expected denied result on second mutate")
	}
}

func TestStore_MutateDoesNotPersistOnMutatorError(t *testing.T) {
	store := memory.NewStore()
	ctx := context.Background()
	mutatorErr := errors.New("mutator failed")

	_, err := store.Mutate(ctx, "key-1", func(current []byte) ([]byte, ratelimit.Result, error) {
		return []byte("v1"), ratelimit.Result{}, mutatorErr
	})
	if !errors.Is(err, mutatorErr) {
		t.Fatalf("expected mutator error, got %v", err)
	}

	result, err := store.Mutate(ctx, "key-1", func(current []byte) ([]byte, ratelimit.Result, error) {
		if current != nil {
			t.Fatalf("expected state not to persist after error, got %q", current)
		}
		return []byte("v1"), ratelimit.Result{Allowed: true}, nil
	})
	if err != nil {
		t.Fatalf("mutate: %v", err)
	}
	if !result.Allowed {
		t.Fatal("expected allowed result")
	}
}

func TestStore_DeleteRemovesState(t *testing.T) {
	store := memory.NewStore()
	ctx := context.Background()

	_, err := store.Mutate(ctx, "key-1", func(current []byte) ([]byte, ratelimit.Result, error) {
		return []byte("v1"), ratelimit.Result{Allowed: true}, nil
	})
	if err != nil {
		t.Fatalf("mutate: %v", err)
	}

	if err := store.Delete(ctx, "key-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	_, err = store.Mutate(ctx, "key-1", func(current []byte) ([]byte, ratelimit.Result, error) {
		if current != nil {
			t.Fatalf("expected deleted state, got %q", current)
		}
		return []byte("v2"), ratelimit.Result{Allowed: true}, nil
	})
	if err != nil {
		t.Fatalf("mutate after delete: %v", err)
	}
}

func TestStore_Validation(t *testing.T) {
	store := memory.NewStore()
	ctx := context.Background()

	_, err := store.Mutate(ctx, "", func(current []byte) ([]byte, ratelimit.Result, error) {
		return nil, ratelimit.Result{}, nil
	})
	if err == nil {
		t.Fatal("expected error for empty state key")
	}

	_, err = store.Mutate(ctx, "key-1", nil)
	if err == nil {
		t.Fatal("expected error for nil mutator")
	}

	if err := store.Delete(ctx, ""); err == nil {
		t.Fatal("expected error for empty delete key")
	}
}

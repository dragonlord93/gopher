package ratelimiter

import (
	"context"
	"testing"
	"time"
)

// --- LocalStore Tests ---

func TestLocalStore_BasicIncrementAndExpiry(t *testing.T) {
	store := NewLocalStore(time.Minute)
	defer store.Close()

	ctx := context.Background()

	// First increment creates the bucket.
	res, err := store.Increment(ctx, "key1", 1, time.Second*10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Count != 1 {
		t.Fatalf("expected count=1, got %d", res.Count)
	}

	// Second increment within same window adds to it.
	res, err = store.Increment(ctx, "key1", 1, time.Second*10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Count != 2 {
		t.Fatalf("expected count=2, got %d", res.Count)
	}
}

func TestLocalStore_ExpiredWindowResets(t *testing.T) {
	store := NewLocalStore(time.Minute)
	defer store.Close()

	// Inject a controllable clock.
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	store.clock = func() time.Time { return now }

	ctx := context.Background()

	// Fill up a 1-second window.
	store.Increment(ctx, "key1", 5, time.Second)

	// Advance time past the window.
	now = now.Add(2 * time.Second)

	// Should start a new window with count=1, not 6.
	res, _ := store.Increment(ctx, "key1", 1, time.Second)
	if res.Count != 1 {
		t.Fatalf("expected count to reset to 1 after expiry, got %d", res.Count)
	}
}

func TestLocalStore_RespectsContextCancellation(t *testing.T) {
	store := NewLocalStore(time.Minute)
	defer store.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := store.Increment(ctx, "key1", 1, time.Second)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

// --- FixedWindowLimiter Tests ---

func TestFixedWindow_AllowsUpToLimit(t *testing.T) {
	store := NewLocalStore(time.Minute)
	defer store.Close()

	limiter, err := NewFixedWindowLimiter(FixedWindowConfig{
		MaxRequests: 3,
		Window:      time.Minute,
	}, store)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := context.Background()
	req := Request{
		EntityType: User,
		EntityID:   "user-42",
		Resource:   "/api/orders",
	}

	// First 3 should be allowed.
	for i := 0; i < 3; i++ {
		dec, err := limiter.Allow(ctx, req)
		if err != nil {
			t.Fatalf("request %d: unexpected error: %v", i, err)
		}
		if !dec.Allowed {
			t.Fatalf("request %d: should be allowed", i)
		}
		if dec.Remaining != 2-i {
			t.Fatalf("request %d: expected remaining=%d, got %d", i, 2-i, dec.Remaining)
		}
	}

	// 4th should be denied.
	dec, err := limiter.Allow(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Allowed {
		t.Fatal("4th request should be denied")
	}
	if dec.Remaining != 0 {
		t.Fatalf("expected remaining=0, got %d", dec.Remaining)
	}
	if dec.RetryAfter <= 0 {
		t.Fatal("RetryAfter should be positive when denied")
	}
}

func TestFixedWindow_DifferentEntitiesAreIndependent(t *testing.T) {
	store := NewLocalStore(time.Minute)
	defer store.Close()

	limiter, _ := NewFixedWindowLimiter(FixedWindowConfig{
		MaxRequests: 1,
		Window:      time.Minute,
	}, store)

	ctx := context.Background()

	// User A uses their one allowed request.
	dec, _ := limiter.Allow(ctx, Request{EntityType: User, EntityID: "A", Resource: "/api"})
	if !dec.Allowed {
		t.Fatal("user A first request should be allowed")
	}

	// User B should still be allowed — independent counter.
	dec, _ = limiter.Allow(ctx, Request{EntityType: User, EntityID: "B", Resource: "/api"})
	if !dec.Allowed {
		t.Fatal("user B first request should be allowed")
	}

	// User A is now denied.
	dec, _ = limiter.Allow(ctx, Request{EntityType: User, EntityID: "A", Resource: "/api"})
	if dec.Allowed {
		t.Fatal("user A second request should be denied")
	}
}

func TestFixedWindow_CostConsumesMultipleTokens(t *testing.T) {
	store := NewLocalStore(time.Minute)
	defer store.Close()

	limiter, _ := NewFixedWindowLimiter(FixedWindowConfig{
		MaxRequests: 10,
		Window:      time.Minute,
	}, store)

	ctx := context.Background()

	// A bulk request that costs 8 tokens.
	dec, _ := limiter.Allow(ctx, Request{
		EntityType: IP,
		EntityID:   "10.0.0.1",
		Resource:   "/api/export",
		Cost:       8,
	})
	if !dec.Allowed {
		t.Fatal("should be allowed")
	}
	if dec.Remaining != 2 {
		t.Fatalf("expected 2 remaining, got %d", dec.Remaining)
	}

	// Another request costing 3 exceeds the limit.
	dec, _ = limiter.Allow(ctx, Request{
		EntityType: IP,
		EntityID:   "10.0.0.1",
		Resource:   "/api/export",
		Cost:       3,
	})
	if dec.Allowed {
		t.Fatal("should be denied — 8 + 3 > 10")
	}
}

func TestFixedWindow_InvalidConfigReturnsError(t *testing.T) {
	store := NewLocalStore(time.Minute)
	defer store.Close()

	_, err := NewFixedWindowLimiter(FixedWindowConfig{MaxRequests: 0, Window: time.Minute}, store)
	if err == nil {
		t.Fatal("expected error for MaxRequests=0")
	}

	_, err = NewFixedWindowLimiter(FixedWindowConfig{MaxRequests: 10, Window: 0}, store)
	if err == nil {
		t.Fatal("expected error for Window=0")
	}
}

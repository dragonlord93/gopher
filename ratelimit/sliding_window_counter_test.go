package ratelimit_test

import (
	"context"
	"testing"
	"time"

	"github.com/shivam/abnormal/ratelimit"
	"github.com/shivam/abnormal/ratelimit/memory"
)

func TestSlidingWindowCounterLimiter_FirstRequestAllowed(t *testing.T) {
	clock := ratelimit.NewFakeClock(time.Unix(1_700_000_000, 0).UTC())
	limiter := newSlidingWindowLimiter(t, clock, ratelimit.SlidingWindowCounterPolicy{
		Limit:  10,
		Window: time.Minute,
	})

	result, err := limiter.Allow(context.Background(), ratelimit.Request{
		Key:  ratelimit.LimitKey{Namespace: "api", Subject: "user-1"},
		Cost: 3,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Allowed || result.Remaining != 7 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestSlidingWindowCounterLimiter_ExhaustThenDeny(t *testing.T) {
	clock := ratelimit.NewFakeClock(time.Unix(1_700_000_000, 0).UTC())
	limiter := newSlidingWindowLimiter(t, clock, ratelimit.SlidingWindowCounterPolicy{
		Limit:  5,
		Window: time.Minute,
	})
	req := ratelimit.Request{
		Key:  ratelimit.LimitKey{Namespace: "api", Subject: "user-1"},
		Cost: 1,
	}

	for i := 0; i < 5; i++ {
		result, err := limiter.Allow(context.Background(), req)
		if err != nil || !result.Allowed {
			t.Fatalf("request %d: expected allow, err=%v result=%+v", i, err, result)
		}
	}

	result, err := limiter.Allow(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Allowed {
		t.Fatal("expected deny after limit exhausted")
	}
	if result.RetryAfter <= 0 {
		t.Fatalf("expected positive retry after, got %v", result.RetryAfter)
	}
}

func TestSlidingWindowCounterLimiter_PreviousWindowInfluencesCurrent(t *testing.T) {
	clock := ratelimit.NewFakeClock(time.Unix(1_700_000_000, 0).UTC())
	window := time.Minute
	limiter := newSlidingWindowLimiter(t, clock, ratelimit.SlidingWindowCounterPolicy{
		Limit:  100,
		Window: window,
	})
	key := ratelimit.LimitKey{Namespace: "api", Subject: "user-1"}

	base := time.Unix(1_700_000_000, 0).UTC()
	// Fill first window near its end (10s before boundary).
	clock.Set(base.Add(50 * time.Second))
	_, err := limiter.Allow(context.Background(), ratelimit.Request{Key: key, Cost: 80})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Advance to 5s into the next aligned window; previous window still weighs heavily.
	clock.Set(base.Add(70 * time.Second))

	result, err := limiter.Allow(context.Background(), ratelimit.Request{Key: key, Cost: 30})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// estimated ≈ 80 * (55/60) + 0 ≈ 73.3 → 30 should be denied (73+30 > 100)
	if result.Allowed {
		t.Fatal("expected deny from weighted previous window count")
	}
}

func TestSlidingWindowCounterLimiter_IdleResetsCounts(t *testing.T) {
	clock := ratelimit.NewFakeClock(time.Unix(1_700_000_000, 0).UTC())
	window := time.Minute
	limiter := newSlidingWindowLimiter(t, clock, ratelimit.SlidingWindowCounterPolicy{
		Limit:  5,
		Window: window,
	})
	key := ratelimit.LimitKey{Namespace: "api", Subject: "user-1"}

	_, err := limiter.Allow(context.Background(), ratelimit.Request{Key: key, Cost: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	clock.Advance(3 * window)

	result, err := limiter.Allow(context.Background(), ratelimit.Request{Key: key, Cost: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Allowed {
		t.Fatal("expected allow after long idle reset windows")
	}
}

func TestSlidingWindowCounterLimiter_VariableCost(t *testing.T) {
	clock := ratelimit.NewFakeClock(time.Unix(1_700_000_000, 0).UTC())
	limiter := newSlidingWindowLimiter(t, clock, ratelimit.SlidingWindowCounterPolicy{
		Limit:  20,
		Window: time.Minute,
	})
	key := ratelimit.LimitKey{Namespace: "api", Subject: "user-1"}

	result, err := limiter.Allow(context.Background(), ratelimit.Request{Key: key, Cost: 15})
	if err != nil || !result.Allowed {
		t.Fatalf("unexpected first result: err=%v result=%+v", err, result)
	}

	result, err = limiter.Allow(context.Background(), ratelimit.Request{Key: key, Cost: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Allowed {
		t.Fatal("expected deny when variable cost exceeds remaining capacity")
	}
}

func TestSlidingWindowCounterLimiter_KeysAreIsolated(t *testing.T) {
	clock := ratelimit.NewFakeClock(time.Unix(1_700_000_000, 0).UTC())
	limiter := newSlidingWindowLimiter(t, clock, ratelimit.SlidingWindowCounterPolicy{
		Limit:  2,
		Window: time.Minute,
	})

	_, _ = limiter.Allow(context.Background(), ratelimit.Request{
		Key: ratelimit.LimitKey{Namespace: "api", Subject: "user-1"}, Cost: 2,
	})

	denied, err := limiter.Allow(context.Background(), ratelimit.Request{
		Key: ratelimit.LimitKey{Namespace: "api", Subject: "user-1"}, Cost: 1,
	})
	if err != nil || denied.Allowed {
		t.Fatalf("expected user-1 denied, err=%v result=%+v", err, denied)
	}

	allowed, err := limiter.Allow(context.Background(), ratelimit.Request{
		Key: ratelimit.LimitKey{Namespace: "api", Subject: "user-2"}, Cost: 1,
	})
	if err != nil || !allowed.Allowed {
		t.Fatalf("expected user-2 allowed, err=%v result=%+v", err, allowed)
	}
}

func TestSlidingWindowCounterLimiter_ValidationFailClosed(t *testing.T) {
	clock := ratelimit.NewFakeClock(time.Unix(1_700_000_000, 0).UTC())
	limiter := newSlidingWindowLimiter(t, clock, ratelimit.SlidingWindowCounterPolicy{
		Limit:  1,
		Window: time.Minute,
	})

	_, err := limiter.Allow(context.Background(), ratelimit.Request{
		Key: ratelimit.LimitKey{Namespace: "", Subject: "user-1"}, Cost: 1,
	})
	if err == nil {
		t.Fatal("expected namespace validation error")
	}

	_, err = limiter.Allow(context.Background(), ratelimit.Request{
		Key: ratelimit.LimitKey{Namespace: "api", Subject: "user-1"}, Cost: 0,
	})
	if err == nil {
		t.Fatal("expected cost validation error")
	}
}

func TestNewSlidingWindowCounterLimiter_Validation(t *testing.T) {
	store := memory.NewStore()
	clock := ratelimit.NewFakeClock(time.Now())

	_, err := ratelimit.NewSlidingWindowCounterLimiter(nil, clock, ratelimit.SlidingWindowCounterPolicy{Limit: 1, Window: time.Minute})
	if err == nil {
		t.Fatal("expected error for nil store")
	}

	_, err = ratelimit.NewSlidingWindowCounterLimiter(store, nil, ratelimit.SlidingWindowCounterPolicy{Limit: 1, Window: time.Minute})
	if err == nil {
		t.Fatal("expected error for nil clock")
	}

	_, err = ratelimit.NewSlidingWindowCounterLimiter(store, clock, ratelimit.SlidingWindowCounterPolicy{Limit: 0, Window: time.Minute})
	if err == nil {
		t.Fatal("expected error for invalid limit")
	}

	_, err = ratelimit.NewSlidingWindowCounterLimiter(store, clock, ratelimit.SlidingWindowCounterPolicy{Limit: 1, Window: 0})
	if err == nil {
		t.Fatal("expected error for invalid window")
	}
}

func TestSlidingWindowCounterLimiter_ConcurrentAccessDoesNotOvercommit(t *testing.T) {
	clock := ratelimit.NewFakeClock(time.Unix(1_700_000_000, 0).UTC())
	limiter := newSlidingWindowLimiter(t, clock, ratelimit.SlidingWindowCounterPolicy{
		Limit:  10,
		Window: time.Minute,
	})
	req := ratelimit.Request{
		Key:  ratelimit.LimitKey{Namespace: "api", Subject: "user-1"},
		Cost: 1,
	}

	allowed := make(chan bool, 20)
	for i := 0; i < 20; i++ {
		go func() {
			result, err := limiter.Allow(context.Background(), req)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				allowed <- false
				return
			}
			allowed <- result.Allowed
		}()
	}

	allowedCount := 0
	for i := 0; i < 20; i++ {
		if <-allowed {
			allowedCount++
		}
	}

	if allowedCount != 10 {
		t.Fatalf("allowed count = %d, want 10", allowedCount)
	}
}

func newSlidingWindowLimiter(t *testing.T, clock ratelimit.Clock, policy ratelimit.SlidingWindowCounterPolicy) *ratelimit.SlidingWindowCounterLimiter {
	t.Helper()

	limiter, err := ratelimit.NewSlidingWindowCounterLimiter(memory.NewStore(), clock, policy)
	if err != nil {
		t.Fatalf("new limiter: %v", err)
	}
	return limiter
}

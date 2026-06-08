package ratelimit_test

import (
	"context"
	"testing"
	"time"

	"github.com/shivam/abnormal/ratelimit"
	"github.com/shivam/abnormal/ratelimit/memory"
)

func TestTokenBucketLimiter_FirstRequestUsesFullBurst(t *testing.T) {
	clock := ratelimit.NewFakeClock(time.Unix(1_700_000_000, 0).UTC())
	limiter := newTestLimiter(t, clock, ratelimit.TokenBucketPolicy{RefillRate: 1, Burst: 5})

	result, err := limiter.Allow(context.Background(), ratelimit.Request{
		Key:  ratelimit.LimitKey{Namespace: "api", Subject: "user-1"},
		Cost: 2,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Allowed {
		t.Fatal("expected first request to be allowed")
	}
	if result.Remaining != 3 {
		t.Fatalf("remaining = %d, want 3", result.Remaining)
	}
}

func TestTokenBucketLimiter_ExhaustThenDeny(t *testing.T) {
	clock := ratelimit.NewFakeClock(time.Unix(1_700_000_000, 0).UTC())
	limiter := newTestLimiter(t, clock, ratelimit.TokenBucketPolicy{RefillRate: 1, Burst: 3})
	req := ratelimit.Request{Key: ratelimit.LimitKey{Namespace: "api", Subject: "user-1"}, Cost: 1}

	for i := 0; i < 3; i++ {
		result, err := limiter.Allow(context.Background(), req)
		if err != nil {
			t.Fatalf("request %d: unexpected error: %v", i, err)
		}
		if !result.Allowed {
			t.Fatalf("request %d: expected allow", i)
		}
	}

	result, err := limiter.Allow(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Allowed {
		t.Fatal("expected deny after bucket exhausted")
	}
	if result.Remaining != 0 {
		t.Fatalf("remaining = %d, want 0", result.Remaining)
	}
	if result.RetryAfter != 1*time.Second {
		t.Fatalf("retry after = %v, want 1s", result.RetryAfter)
	}
}

func TestTokenBucketLimiter_RefillAfterIdle(t *testing.T) {
	clock := ratelimit.NewFakeClock(time.Unix(1_700_000_000, 0).UTC())
	limiter := newTestLimiter(t, clock, ratelimit.TokenBucketPolicy{RefillRate: 2, Burst: 4})
	req := ratelimit.Request{Key: ratelimit.LimitKey{Namespace: "api", Subject: "user-1"}, Cost: 1}

	_, err := limiter.Allow(context.Background(), ratelimit.Request{Key: req.Key, Cost: 4})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	denied, err := limiter.Allow(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if denied.Allowed {
		t.Fatal("expected bucket to be empty")
	}

	clock.Advance(1 * time.Second)

	allowed, err := limiter.Allow(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed.Allowed {
		t.Fatal("expected refill to allow one token")
	}
	if allowed.Remaining != 1 {
		t.Fatalf("remaining = %d, want 1", allowed.Remaining)
	}
}

func TestTokenBucketLimiter_VariableCost(t *testing.T) {
	clock := ratelimit.NewFakeClock(time.Unix(1_700_000_000, 0).UTC())
	limiter := newTestLimiter(t, clock, ratelimit.TokenBucketPolicy{RefillRate: 1, Burst: 10})
	key := ratelimit.LimitKey{Namespace: "api", Subject: "user-1"}

	result, err := limiter.Allow(context.Background(), ratelimit.Request{Key: key, Cost: 7})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Allowed || result.Remaining != 3 {
		t.Fatalf("unexpected first result: %+v", result)
	}

	result, err = limiter.Allow(context.Background(), ratelimit.Request{Key: key, Cost: 4})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Allowed {
		t.Fatal("expected deny when cost exceeds remaining tokens")
	}
	if result.RetryAfter != 1*time.Second {
		t.Fatalf("retry after = %v, want 1s", result.RetryAfter)
	}
}

func TestTokenBucketLimiter_KeysAreIsolated(t *testing.T) {
	clock := ratelimit.NewFakeClock(time.Unix(1_700_000_000, 0).UTC())
	limiter := newTestLimiter(t, clock, ratelimit.TokenBucketPolicy{RefillRate: 1, Burst: 2})

	_, err := limiter.Allow(context.Background(), ratelimit.Request{
		Key: ratelimit.LimitKey{Namespace: "api", Subject: "user-1"}, Cost: 2,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	denied, err := limiter.Allow(context.Background(), ratelimit.Request{
		Key: ratelimit.LimitKey{Namespace: "api", Subject: "user-1"}, Cost: 1,
	})
	if err != nil || denied.Allowed {
		t.Fatalf("expected user-1 to be denied, err=%v result=%+v", err, denied)
	}

	allowed, err := limiter.Allow(context.Background(), ratelimit.Request{
		Key: ratelimit.LimitKey{Namespace: "api", Subject: "user-2"}, Cost: 1,
	})
	if err != nil || !allowed.Allowed {
		t.Fatalf("expected user-2 to be allowed, err=%v result=%+v", err, allowed)
	}
}

func TestTokenBucketLimiter_ValidationFailClosed(t *testing.T) {
	clock := ratelimit.NewFakeClock(time.Unix(1_700_000_000, 0).UTC())
	limiter := newTestLimiter(t, clock, ratelimit.TokenBucketPolicy{RefillRate: 1, Burst: 1})

	_, err := limiter.Allow(context.Background(), ratelimit.Request{
		Key: ratelimit.LimitKey{Namespace: "", Subject: "user-1"}, Cost: 1,
	})
	if err == nil {
		t.Fatal("expected namespace validation error")
	}

	_, err = limiter.Allow(context.Background(), ratelimit.Request{
		Key: ratelimit.LimitKey{Namespace: "api", Subject: ""}, Cost: 1,
	})
	if err == nil {
		t.Fatal("expected subject validation error")
	}

	_, err = limiter.Allow(context.Background(), ratelimit.Request{
		Key: ratelimit.LimitKey{Namespace: "api", Subject: "user-1"}, Cost: 0,
	})
	if err == nil {
		t.Fatal("expected cost validation error")
	}
}

func TestNewTokenBucketLimiter_Validation(t *testing.T) {
	store := memory.NewStore()
	clock := ratelimit.NewFakeClock(time.Now())

	_, err := ratelimit.NewTokenBucketLimiter(nil, clock, ratelimit.TokenBucketPolicy{RefillRate: 1, Burst: 1})
	if err == nil {
		t.Fatal("expected error for nil store")
	}

	_, err = ratelimit.NewTokenBucketLimiter(store, nil, ratelimit.TokenBucketPolicy{RefillRate: 1, Burst: 1})
	if err == nil {
		t.Fatal("expected error for nil clock")
	}

	_, err = ratelimit.NewTokenBucketLimiter(store, clock, ratelimit.TokenBucketPolicy{RefillRate: 0, Burst: 1})
	if err == nil {
		t.Fatal("expected error for invalid refill rate")
	}

	_, err = ratelimit.NewTokenBucketLimiter(store, clock, ratelimit.TokenBucketPolicy{RefillRate: 1, Burst: 0})
	if err == nil {
		t.Fatal("expected error for invalid burst")
	}
}

func TestTokenBucketLimiter_ConcurrentAccessDoesNotOvercommit(t *testing.T) {
	clock := ratelimit.NewFakeClock(time.Unix(1_700_000_000, 0).UTC())
	limiter := newTestLimiter(t, clock, ratelimit.TokenBucketPolicy{RefillRate: 1, Burst: 10})
	req := ratelimit.Request{Key: ratelimit.LimitKey{Namespace: "api", Subject: "user-1"}, Cost: 1}

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

func newTestLimiter(t *testing.T, clock ratelimit.Clock, policy ratelimit.TokenBucketPolicy) *ratelimit.TokenBucketLimiter {
	t.Helper()

	limiter, err := ratelimit.NewTokenBucketLimiter(memory.NewStore(), clock, policy)
	if err != nil {
		t.Fatalf("new limiter: %v", err)
	}
	return limiter
}

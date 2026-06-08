package ratelimit

import (
	"testing"
	"time"
)

func TestTransitionTokenBucket_InitialAllow(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	policy := TokenBucketPolicy{RefillRate: 1, Burst: 10}

	state, result := transitionTokenBucket(tokenBucketState{
		Tokens:       10,
		LastRefillAt: now,
	}, now, 3, policy)

	if !result.Allowed {
		t.Fatal("expected request to be allowed")
	}
	if result.Remaining != 7 {
		t.Fatalf("remaining = %d, want 7", result.Remaining)
	}
	if result.Limit != 10 {
		t.Fatalf("limit = %d, want 10", result.Limit)
	}
	if state.Tokens != 7 {
		t.Fatalf("state tokens = %v, want 7", state.Tokens)
	}
}

func TestTransitionTokenBucket_DenyWhenInsufficientTokens(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	policy := TokenBucketPolicy{RefillRate: 2, Burst: 10}

	_, result := transitionTokenBucket(tokenBucketState{
		Tokens:       1,
		LastRefillAt: now,
	}, now, 3, policy)

	if result.Allowed {
		t.Fatal("expected request to be denied")
	}
	if result.Remaining != 1 {
		t.Fatalf("remaining = %d, want 1", result.Remaining)
	}
	if result.RetryAfter != 1*time.Second {
		t.Fatalf("retry after = %v, want 1s", result.RetryAfter)
	}
}

func TestTransitionTokenBucket_LazyRefillAllowsAfterElapsedTime(t *testing.T) {
	start := time.Unix(1_700_000_000, 0).UTC()
	policy := TokenBucketPolicy{RefillRate: 2, Burst: 10}
	now := start.Add(2 * time.Second)

	state, result := transitionTokenBucket(tokenBucketState{
		Tokens:       0,
		LastRefillAt: start,
	}, now, 3, policy)

	if !result.Allowed {
		t.Fatal("expected refill to provide enough tokens")
	}
	if state.Tokens != 1 {
		t.Fatalf("state tokens = %v, want 1", state.Tokens)
	}
	if result.Remaining != 1 {
		t.Fatalf("remaining = %d, want 1", result.Remaining)
	}
}

func TestTransitionTokenBucket_RefillCapsAtBurst(t *testing.T) {
	start := time.Unix(1_700_000_000, 0).UTC()
	policy := TokenBucketPolicy{RefillRate: 5, Burst: 10}
	now := start.Add(1 * time.Hour)

	state, result := transitionTokenBucket(tokenBucketState{
		Tokens:       0,
		LastRefillAt: start,
	}, now, 1, policy)

	if !result.Allowed {
		t.Fatal("expected request to be allowed after long idle period")
	}
	if state.Tokens != 9 {
		t.Fatalf("state tokens = %v, want 9", state.Tokens)
	}
	if result.Remaining != 9 {
		t.Fatalf("remaining = %d, want 9", result.Remaining)
	}
}

func TestTransitionTokenBucket_CostGreaterThanBurstDenied(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	policy := TokenBucketPolicy{RefillRate: 1, Burst: 5}

	_, result := transitionTokenBucket(tokenBucketState{
		Tokens:       5,
		LastRefillAt: now,
	}, now, 6, policy)

	if result.Allowed {
		t.Fatal("expected request with cost above burst to be denied")
	}
	if result.RetryAfter != 1*time.Second {
		t.Fatalf("retry after = %v, want 1s", result.RetryAfter)
	}
}

func TestTransitionTokenBucket_NoRefillWhenElapsedIsZero(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	policy := TokenBucketPolicy{RefillRate: 100, Burst: 10}

	state, result := transitionTokenBucket(tokenBucketState{
		Tokens:       2,
		LastRefillAt: now,
	}, now, 3, policy)

	if result.Allowed {
		t.Fatal("expected deny without elapsed refill time")
	}
	if state.Tokens != 2 {
		t.Fatalf("state tokens = %v, want 2", state.Tokens)
	}
	if state.LastRefillAt != now {
		t.Fatalf("last refill at changed without elapsed time: %v", state.LastRefillAt)
	}
}

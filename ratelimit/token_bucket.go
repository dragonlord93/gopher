package ratelimit

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"
)

const tokenBucketAlgorithm = "token_bucket"

type tokenBucketState struct {
	Tokens       float64   `json:"tokens"`
	LastRefillAt time.Time `json:"last_refill_at"`
}

// TokenBucketLimiter applies lazy-refill token bucket rate limiting.
type TokenBucketLimiter struct {
	store  RateLimitStore
	clock  Clock
	policy TokenBucketPolicy
}

func NewTokenBucketLimiter(store RateLimitStore, clock Clock, policy TokenBucketPolicy) (*TokenBucketLimiter, error) {
	if store == nil {
		return nil, fmt.Errorf("ratelimit: store is required")
	}
	if clock == nil {
		return nil, fmt.Errorf("ratelimit: clock is required")
	}
	if err := policy.validate(); err != nil {
		return nil, err
	}
	return &TokenBucketLimiter{
		store:  store,
		clock:  clock,
		policy: policy,
	}, nil
}

func (l *TokenBucketLimiter) Allow(ctx context.Context, req Request) (Result, error) {
	if err := req.validate(); err != nil {
		return Result{}, err
	}

	now := l.clock.Now()
	cost := float64(req.Cost)
	stateKey := req.Key.StateKey(tokenBucketAlgorithm)

	return l.store.Mutate(ctx, stateKey, func(current []byte) ([]byte, Result, error) {
		state, err := decodeTokenBucketState(current, now, float64(l.policy.Burst))
		if err != nil {
			return nil, Result{}, err
		}

		newState, result := transitionTokenBucket(state, now, cost, l.policy)
		encoded, err := json.Marshal(newState)
		if err != nil {
			return nil, Result{}, fmt.Errorf("ratelimit: encode token bucket state: %w", err)
		}
		return encoded, result, nil
	})
}

func decodeTokenBucketState(current []byte, now time.Time, burst float64) (tokenBucketState, error) {
	if len(current) == 0 {
		return tokenBucketState{
			Tokens:       burst,
			LastRefillAt: now,
		}, nil
	}

	var state tokenBucketState
	if err := json.Unmarshal(current, &state); err != nil {
		return tokenBucketState{}, fmt.Errorf("ratelimit: decode token bucket state: %w", err)
	}
	return state, nil
}

func transitionTokenBucket(state tokenBucketState, now time.Time, cost float64, policy TokenBucketPolicy) (tokenBucketState, Result) {
	burst := float64(policy.Burst)

	elapsed := now.Sub(state.LastRefillAt).Seconds()
	if elapsed > 0 {
		state.Tokens = math.Min(burst, state.Tokens+elapsed*policy.RefillRate)
		state.LastRefillAt = now
	}

	result := Result{
		Limit: policy.Burst,
	}

	if state.Tokens >= cost {
		state.Tokens -= cost
		result.Allowed = true
	} else {
		result.Allowed = false
		deficit := cost - state.Tokens
		if policy.RefillRate > 0 {
			result.RetryAfter = time.Duration(deficit / policy.RefillRate * float64(time.Second))
		}
	}

	result.Remaining = int(math.Floor(state.Tokens))
	if result.Remaining < 0 {
		result.Remaining = 0
	}

	if policy.RefillRate > 0 {
		tokensToFull := burst - state.Tokens
		if tokensToFull > 0 {
			result.ResetAt = now.Add(time.Duration(tokensToFull / policy.RefillRate * float64(time.Second)))
		} else {
			result.ResetAt = now
		}
	}

	return state, result
}

package ratelimiter

import (
	"context"
	"fmt"
	"time"
)

// FixedWindowConfig holds the settings for a fixed-window counter algorithm.
type FixedWindowConfig struct {
	MaxRequests int64         // max allowed requests per window
	Window      time.Duration // e.g. 1 * time.Minute
}

// FixedWindowLimiter implements RateLimiter using a fixed-window counter.
//
// Simple and fast. The known tradeoff: a burst of requests at the boundary
// of two windows can allow up to 2x the limit. If that matters, use
// sliding window instead.
type FixedWindowLimiter struct {
	config FixedWindowConfig
	store  Store
}

func NewFixedWindowLimiter(config FixedWindowConfig, store Store) (*FixedWindowLimiter, error) {
	if config.MaxRequests <= 0 {
		return nil, fmt.Errorf("MaxRequests must be > 0, got %d", config.MaxRequests)
	}
	if config.Window <= 0 {
		return nil, fmt.Errorf("Window must be > 0, got %v", config.Window)
	}
	return &FixedWindowLimiter{
		config: config,
		store:  store,
	}, nil
}

func (f *FixedWindowLimiter) Allow(ctx context.Context, req Request) (Decision, error) {
	cost := int64(req.Cost)
	if cost <= 0 {
		cost = 1
	}

	// Build a key that's unique per entity + resource.
	// e.g. "rl:0:user-123:/api/orders"
	key := fmt.Sprintf("rl:%d:%s:%s", req.EntityType, req.EntityID, req.Resource)

	result, err := f.store.Increment(ctx, key, cost, f.config.Window)
	if err != nil {
		return Decision{}, fmt.Errorf("store.Increment failed: %w", err)
	}

	remaining := f.config.MaxRequests - result.Count
	if remaining < 0 {
		remaining = 0
	}

	if result.Count > f.config.MaxRequests {
		return Decision{
			Allowed:    false,
			Remaining:  int(remaining),
			ResetAt:    result.ExpiresAt,
			RetryAfter: time.Until(result.ExpiresAt),
		}, nil
	}

	return Decision{
		Allowed:   true,
		Remaining: int(remaining),
		ResetAt:   result.ExpiresAt,
	}, nil
}

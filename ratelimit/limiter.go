package ratelimit

import "context"

// RateLimiter evaluates whether a request should be allowed.
type RateLimiter interface {
	Allow(ctx context.Context, req Request) (Result, error)
}

package ratelimiter

import (
	"context"
	"time"
)

// RequesterType identifies how we're keying the rate limit.
type RequesterType int

const (
	User RequesterType = iota
	IP
	DeviceFingerprint
)

// Request captures everything needed to make a rate-limit decision.
type Request struct {
	EntityType RequesterType
	EntityID   string // "user-123", "10.0.0.1", etc.
	Resource   string // API path or operation name
	Cost       int    // how many tokens this request consumes (default: 1)
}

// Decision is what the rate limiter returns to the caller.
type Decision struct {
	Allowed    bool
	Remaining  int
	ResetAt    time.Time
	RetryAfter time.Duration
}

// RateLimiter is the core interface — algorithm-agnostic.
type RateLimiter interface {
	Allow(ctx context.Context, req Request) (Decision, error)
}

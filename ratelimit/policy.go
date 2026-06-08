package ratelimit

import (
	"fmt"
	"time"
)

// TokenBucketPolicy configures a token bucket limiter.
// RefillRate is tokens added per second via lazy refill.
type TokenBucketPolicy struct {
	RefillRate float64
	Burst      int
}

func (p TokenBucketPolicy) validate() error {
	if p.RefillRate <= 0 {
		return fmt.Errorf("ratelimit: refill rate must be positive")
	}
	if p.Burst <= 0 {
		return fmt.Errorf("ratelimit: burst must be positive")
	}
	return nil
}

// SlidingWindowCounterPolicy configures a sliding window counter limiter.
// It blends the previous and current fixed-window counts, weighted by elapsed
// time in the current window, to approximate a sliding window.
type SlidingWindowCounterPolicy struct {
	Limit  int
	Window time.Duration
}

func (p SlidingWindowCounterPolicy) validate() error {
	if p.Limit <= 0 {
		return fmt.Errorf("ratelimit: limit must be positive")
	}
	if p.Window <= 0 {
		return fmt.Errorf("ratelimit: window must be positive")
	}
	return nil
}

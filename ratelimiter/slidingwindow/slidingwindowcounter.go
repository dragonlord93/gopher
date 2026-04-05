package slidingwindow

import (
	"context"
	"time"

	"github.com/dragonlord93/gopher/ratelimiter"
)

type SlidingWindowIncrementer interface {
	Increment(ctx context.Context)
}

type SlidingWindowCounterConfig struct {
	Window      time.Duration
	MaxRequests int64
}

type SlidingWindowCounterLimiter struct {
	config      *SlidingWindowCounterConfig
	incrementer SlidingWindowIncrementer
}

func NewSlidingWindowCounterLimiter(config *SlidingWindowCounterConfig, store Store) RateLimiter {
	return &SlidingWindowCounterLimiter{
		config: config,
		store:  store,
	}
}

func (s *SlidingWindowCounterLimiter) Close() {

}

func (s *SlidingWindowCounterLimiter) Allow(ctx context.Context, req Request) (Decision, error) {
	return Decision{}, nil
}

var _ ratelimiter.RateLimiter = (*SlidingWindowCounterLimiter)(nil)

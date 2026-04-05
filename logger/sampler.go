// logger/sampler.go
package logger

import (
	"sync"
	"sync/atomic"
	"time"
)

// Sampler decides whether a log entry should be written
// Interface allows different sampling strategies
type Sampler interface {
	Sample(level Level, msg string) bool
}

// CountSampler logs first N entries, then every Mth entry
// Useful for high-frequency debug logs
type CountSampler struct {
	Initial    int      // log first N
	Thereafter int      // then every Mth
	counters   sync.Map // msg -> *counter
}

type counter struct {
	count atomic.Uint64
}

func NewCountSampler(initial, thereafter int) *CountSampler {
	return &CountSampler{
		Initial:    initial,
		Thereafter: thereafter,
	}
}

func (s *CountSampler) Sample(level Level, msg string) bool {
	// Always log errors and above
	if level >= ErrorLevel {
		return true
	}

	// Get or create counter for this message
	val, _ := s.counters.LoadOrStore(msg, &counter{})
	c := val.(*counter)

	n := c.count.Add(1)
	if int(n) <= s.Initial {
		return true
	}
	return (int(n)-s.Initial)%s.Thereafter == 0
}

// RateSampler limits logs per second per message
type RateSampler struct {
	Limit   int // max logs per interval per message
	Window  time.Duration
	buckets sync.Map // msg -> *rateBucket
}

type rateBucket struct {
	count     atomic.Int32
	resetTime atomic.Int64
}

func NewRateSampler(limit int, window time.Duration) *RateSampler {
	return &RateSampler{
		Limit:  limit,
		Window: window,
	}
}

func (s *RateSampler) Sample(level Level, msg string) bool {
	// Always log errors and above
	if level >= ErrorLevel {
		return true
	}

	now := time.Now().UnixNano()

	val, _ := s.buckets.LoadOrStore(msg, &rateBucket{})
	bucket := val.(*rateBucket)

	resetTime := bucket.resetTime.Load()
	windowNanos := s.Window.Nanoseconds()

	// Check if we need to reset the bucket
	if now-resetTime >= windowNanos {
		// Try to reset - CAS to avoid race
		if bucket.resetTime.CompareAndSwap(resetTime, now) {
			bucket.count.Store(1)
			return true
		}
	}

	// Increment and check limit
	count := bucket.count.Add(1)
	return int(count) <= s.Limit
}

// NoopSampler always allows logging
type NoopSampler struct{}

func (s *NoopSampler) Sample(level Level, msg string) bool {
	return true
}

package ratelimiter

import (
	"context"
	"time"
)

// CounterResult is what most algorithms need back from storage
type CounterResult struct {
	Count     int64
	ExpiresAt time.Time
}

type Store interface {
	// Increment atomically increments a key's counter and sets TTL if new.
	// This is the core operation — maps to INCR+EXPIRE in Redis,
	// atomic add in memory.
	Increment(ctx context.Context, key string, delta int64, window time.Duration) (CounterResult, error)
	// Close releases resources (connections, goroutines, etc.)
	Close() error
}

type WindowLogStore interface {
	Store
	AddAndCount(ctx context.Context, key string, now time.Time, window time.Duration) (int64, error)
}

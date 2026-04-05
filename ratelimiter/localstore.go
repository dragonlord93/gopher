package ratelimiter

import (
	"context"
	"sync"
	"time"
)

// bucket holds the counter state for a single key.
type bucket struct {
	count     int64
	expiresAt time.Time
}

// LocalStore is a concurrency-safe, in-memory Store.
// Expired entries are cleaned up by a background goroutine.
type LocalStore struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	clock   func() time.Time // injectable for testing

	stopCleanup chan struct{}
	wg          sync.WaitGroup
}

// NewLocalStore creates a ready-to-use in-memory store.
// cleanupInterval controls how often expired entries are swept.
// Pass 0 to use the default (1 minute).
func NewLocalStore(cleanupInterval time.Duration) *LocalStore {
	if cleanupInterval <= 0 {
		cleanupInterval = time.Minute
	}
	s := &LocalStore{
		buckets:     make(map[string]*bucket),
		clock:       time.Now,
		stopCleanup: make(chan struct{}),
	}
	s.wg.Add(1)
	go s.cleanupLoop(cleanupInterval)
	return s
}

// Increment atomically increments the counter for key by delta.
// If the key doesn't exist or has expired, a new window starts.
func (s *LocalStore) Increment(ctx context.Context, key string, delta int64, window time.Duration) (CounterResult, error) {
	// Respect context cancellation before acquiring the lock.
	select {
	case <-ctx.Done():
		return CounterResult{}, ctx.Err()
	default:
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.clock()
	b, exists := s.buckets[key]

	if !exists || now.After(b.expiresAt) {
		// New window — either first request or previous window expired.
		b = &bucket{
			count:     delta,
			expiresAt: now.Add(window),
		}
		s.buckets[key] = b
	} else {
		b.count += delta
	}

	return CounterResult{
		Count:     b.count,
		ExpiresAt: b.expiresAt,
	}, nil
}

// Close stops the cleanup goroutine and releases resources.
func (s *LocalStore) Close() error {
	close(s.stopCleanup)
	s.wg.Wait()
	return nil
}

// cleanupLoop periodically removes expired entries to prevent memory leaks.
func (s *LocalStore) cleanupLoop(interval time.Duration) {
	defer s.wg.Done()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCleanup:
			return
		case <-ticker.C:
			s.evictExpired()
		}
	}
}

func (s *LocalStore) evictExpired() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.clock()
	for key, b := range s.buckets {
		if now.After(b.expiresAt) {
			delete(s.buckets, key)
		}
	}
}

package ratelimit

import "context"

// StateMutator runs atomically for a single state key.
// A nil current state means the key has not been used before.
type StateMutator func(current []byte) (new []byte, result Result, err error)

// RateLimitStore provides isolated, atomic state mutation per key.
type RateLimitStore interface {
	Mutate(ctx context.Context, stateKey string, fn StateMutator) (Result, error)
	Delete(ctx context.Context, stateKey string) error
}

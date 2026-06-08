package memory

import (
	"context"
	"fmt"
	"hash/fnv"
	"sync"

	"github.com/shivam/abnormal/ratelimit"
)

const shardCount = 32

type shard struct {
	mu    sync.Mutex
	state map[string][]byte
}

// Store is an in-memory RateLimitStore with per-key isolated mutation.
type Store struct {
	shards [shardCount]shard
}

func NewStore() *Store {
	s := &Store{}
	for i := range s.shards {
		s.shards[i].state = make(map[string][]byte)
	}
	return s
}

func (s *Store) Mutate(_ context.Context, stateKey string, fn ratelimit.StateMutator) (ratelimit.Result, error) {
	if stateKey == "" {
		return ratelimit.Result{}, fmt.Errorf("ratelimit/memory: state key is required")
	}
	if fn == nil {
		return ratelimit.Result{}, fmt.Errorf("ratelimit/memory: mutator is required")
	}

	sh := s.shardFor(stateKey)
	sh.mu.Lock()
	defer sh.mu.Unlock()

	current := sh.state[stateKey]
	updated, result, err := fn(current)
	if err != nil {
		return ratelimit.Result{}, err
	}

	sh.state[stateKey] = updated
	return result, nil
}

func (s *Store) Delete(_ context.Context, stateKey string) error {
	if stateKey == "" {
		return fmt.Errorf("ratelimit/memory: state key is required")
	}

	sh := s.shardFor(stateKey)
	sh.mu.Lock()
	defer sh.mu.Unlock()

	delete(sh.state, stateKey)
	return nil
}

func (s *Store) shardFor(stateKey string) *shard {
	h := fnv.New32a()
	_, _ = h.Write([]byte(stateKey))
	return &s.shards[h.Sum32()%shardCount]
}

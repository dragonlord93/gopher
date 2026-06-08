package ratelimit

import (
	"fmt"
	"strings"
	"time"
)

// LimitKey identifies who or what is being rate limited.
type LimitKey struct {
	Namespace string
	Subject   string
}

// StateKey returns a stable store key for the given algorithm identifier.
func (k LimitKey) StateKey(algorithm string) string {
	return algorithm + ":" + k.Namespace + ":" + k.Subject
}

func (k LimitKey) validate() error {
	if strings.TrimSpace(k.Namespace) == "" {
		return fmt.Errorf("ratelimit: namespace is required")
	}
	if strings.TrimSpace(k.Subject) == "" {
		return fmt.Errorf("ratelimit: subject is required")
	}
	return nil
}

// Request is a single rate limit check.
type Request struct {
	Key  LimitKey
	Cost int
}

func (r Request) validate() error {
	if err := r.Key.validate(); err != nil {
		return err
	}
	if r.Cost <= 0 {
		return fmt.Errorf("ratelimit: cost must be positive")
	}
	return nil
}

// Result is the outcome of a rate limit check.
type Result struct {
	Allowed    bool
	Limit      int
	Remaining  int
	ResetAt    time.Time
	RetryAfter time.Duration
}

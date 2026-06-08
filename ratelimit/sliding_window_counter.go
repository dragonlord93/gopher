package ratelimit

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"
)

const slidingWindowCounterAlgorithm = "sliding_window_counter"

type slidingWindowCounterState struct {
	WindowStart   time.Time `json:"window_start"`
	CurrentCount  int       `json:"current_count"`
	PreviousCount int       `json:"previous_count"`
}

// SlidingWindowCounterLimiter applies sliding window counter rate limiting.
type SlidingWindowCounterLimiter struct {
	store  RateLimitStore
	clock  Clock
	policy SlidingWindowCounterPolicy
}

func NewSlidingWindowCounterLimiter(store RateLimitStore, clock Clock, policy SlidingWindowCounterPolicy) (*SlidingWindowCounterLimiter, error) {
	if store == nil {
		return nil, fmt.Errorf("ratelimit: store is required")
	}
	if clock == nil {
		return nil, fmt.Errorf("ratelimit: clock is required")
	}
	if err := policy.validate(); err != nil {
		return nil, err
	}
	return &SlidingWindowCounterLimiter{
		store:  store,
		clock:  clock,
		policy: policy,
	}, nil
}

func (l *SlidingWindowCounterLimiter) Allow(ctx context.Context, req Request) (Result, error) {
	if err := req.validate(); err != nil {
		return Result{}, err
	}

	now := l.clock.Now()
	stateKey := req.Key.StateKey(slidingWindowCounterAlgorithm)

	return l.store.Mutate(ctx, stateKey, func(current []byte) ([]byte, Result, error) {
		state, err := decodeSlidingWindowCounterState(current, now, l.policy.Window)
		if err != nil {
			return nil, Result{}, err
		}

		newState, result := transitionSlidingWindowCounter(state, now, req.Cost, l.policy)
		encoded, err := json.Marshal(newState)
		if err != nil {
			return nil, Result{}, fmt.Errorf("ratelimit: encode sliding window counter state: %w", err)
		}
		return encoded, result, nil
	})
}

func decodeSlidingWindowCounterState(current []byte, now time.Time, window time.Duration) (slidingWindowCounterState, error) {
	if len(current) == 0 {
		return slidingWindowCounterState{
			WindowStart: windowStart(now, window),
		}, nil
	}

	var state slidingWindowCounterState
	if err := json.Unmarshal(current, &state); err != nil {
		return slidingWindowCounterState{}, fmt.Errorf("ratelimit: decode sliding window counter state: %w", err)
	}
	rollSlidingWindow(&state, now, window)
	return state, nil
}

func transitionSlidingWindowCounter(state slidingWindowCounterState, now time.Time, cost int, policy SlidingWindowCounterPolicy) (slidingWindowCounterState, Result) {
	rollSlidingWindow(&state, now, policy.Window)

	estimated := estimatedSlidingWindowCount(state, now, policy.Window)
	costF := float64(cost)
	limitF := float64(policy.Limit)

	result := Result{Limit: policy.Limit}

	if estimated+costF <= limitF {
		state.CurrentCount += cost
		result.Allowed = true
		estimatedAfter := estimated + costF
		result.Remaining = remainingFromEstimate(limitF, estimatedAfter)
		result.ResetAt = windowStart(now, policy.Window).Add(policy.Window)
		return state, result
	}

	overflow := estimated + costF - limitF
	result.Allowed = false
	result.Remaining = remainingFromEstimate(limitF, estimated)
	result.RetryAfter = retryAfterForOverflow(overflow, limitF, policy.Window)
	result.ResetAt = now.Add(result.RetryAfter)
	return state, result
}

func rollSlidingWindow(state *slidingWindowCounterState, now time.Time, window time.Duration) {
	currentStart := windowStart(now, window)
	if state.WindowStart.IsZero() {
		state.WindowStart = currentStart
		return
	}
	if !currentStart.After(state.WindowStart) {
		return
	}

	windowsElapsed := int(currentStart.Sub(state.WindowStart) / window)
	switch {
	case windowsElapsed >= 2:
		state.PreviousCount = 0
		state.CurrentCount = 0
	case windowsElapsed == 1:
		state.PreviousCount = state.CurrentCount
		state.CurrentCount = 0
	}
	state.WindowStart = currentStart
}

func windowStart(now time.Time, window time.Duration) time.Time {
	windowNanos := window.Nanoseconds()
	if windowNanos <= 0 {
		return now
	}
	aligned := (now.UnixNano() / windowNanos) * windowNanos
	return time.Unix(0, aligned).In(now.Location())
}

func estimatedSlidingWindowCount(state slidingWindowCounterState, now time.Time, window time.Duration) float64 {
	elapsed := now.Sub(state.WindowStart)
	if elapsed < 0 {
		elapsed = 0
	}
	if elapsed > window {
		elapsed = window
	}

	previousWeight := float64(window-elapsed) / float64(window)
	return float64(state.PreviousCount)*previousWeight + float64(state.CurrentCount)
}

func remainingFromEstimate(limit, estimated float64) int {
	remaining := int(math.Floor(limit - estimated))
	if remaining < 0 {
		return 0
	}
	return remaining
}

func retryAfterForOverflow(overflow, limit float64, window time.Duration) time.Duration {
	if overflow <= 0 || limit <= 0 {
		return 0
	}
	seconds := overflow / limit * window.Seconds()
	return time.Duration(seconds * float64(time.Second))
}

package ratelimit

import (
	"testing"
	"time"
)

func TestTransitionSlidingWindowCounter_AllowsWithinLimit(t *testing.T) {
	window := 60 * time.Second
	now := windowStart(time.Unix(1_700_000_000, 0).UTC(), window)
	policy := SlidingWindowCounterPolicy{Limit: 100, Window: window}
	start := now

	state, result := transitionSlidingWindowCounter(slidingWindowCounterState{
		WindowStart:  start,
		CurrentCount: 40,
	}, now, 10, policy)

	if !result.Allowed {
		t.Fatal("expected allow within limit")
	}
	if state.CurrentCount != 50 {
		t.Fatalf("current count = %d, want 50", state.CurrentCount)
	}
	if result.Remaining != 50 {
		t.Fatalf("remaining = %d, want 50", result.Remaining)
	}
}

func TestTransitionSlidingWindowCounter_DeniesWhenOverLimit(t *testing.T) {
	window := 60 * time.Second
	now := windowStart(time.Unix(1_700_000_000, 0).UTC(), window)
	policy := SlidingWindowCounterPolicy{Limit: 100, Window: window}
	start := now

	_, result := transitionSlidingWindowCounter(slidingWindowCounterState{
		WindowStart:  start,
		CurrentCount: 95,
	}, now, 10, policy)

	if result.Allowed {
		t.Fatal("expected deny when estimated count exceeds limit")
	}
	if result.Remaining != 5 {
		t.Fatalf("remaining = %d, want 5", result.Remaining)
	}
	if result.RetryAfter <= 0 {
		t.Fatalf("expected positive retry after, got %v", result.RetryAfter)
	}
}

func TestTransitionSlidingWindowCounter_WeightsPreviousWindow(t *testing.T) {
	window := 60 * time.Second
	start := windowStart(time.Unix(1_700_000_000, 0).UTC(), window)
	now := start.Add(30 * time.Second) // halfway through window
	policy := SlidingWindowCounterPolicy{Limit: 100, Window: window}

	// estimated = 80 * 0.5 + 10 = 50
	state, result := transitionSlidingWindowCounter(slidingWindowCounterState{
		WindowStart:   start,
		PreviousCount: 80,
		CurrentCount:  10,
	}, now, 40, policy)

	if !result.Allowed {
		t.Fatal("expected allow with weighted previous window contribution")
	}
	if state.CurrentCount != 50 {
		t.Fatalf("current count = %d, want 50", state.CurrentCount)
	}
}

func TestTransitionSlidingWindowCounter_PreviousWindowFadesAtBoundary(t *testing.T) {
	window := 60 * time.Second
	base := time.Unix(1_700_000_000, 0).UTC()
	start := windowStart(base, window)
	now := start.Add(window) // new window, previous weight = 0
	policy := SlidingWindowCounterPolicy{Limit: 10, Window: window}

	state, result := transitionSlidingWindowCounter(slidingWindowCounterState{
		WindowStart:   start,
		PreviousCount: 100,
		CurrentCount:  0,
	}, now, 5, policy)

	if !result.Allowed {
		t.Fatal("expected allow after previous window fully expired")
	}
	if state.PreviousCount != 0 {
		t.Fatalf("previous count = %d, want 0 after roll", state.PreviousCount)
	}
	if state.CurrentCount != 5 {
		t.Fatalf("current count = %d, want 5", state.CurrentCount)
	}
	if !result.Allowed {
		t.Fatal("expected allow")
	}
}

func TestRollSlidingWindow_SkipsMultipleWindows(t *testing.T) {
	window := 60 * time.Second
	base := time.Unix(1_700_000_000, 0).UTC()
	start := windowStart(base, window)
	now := start.Add(3 * window)

	state := slidingWindowCounterState{
		WindowStart:   start,
		PreviousCount: 10,
		CurrentCount:  20,
	}
	rollSlidingWindow(&state, now, window)

	if state.PreviousCount != 0 || state.CurrentCount != 0 {
		t.Fatalf("expected counts reset after idle, got previous=%d current=%d", state.PreviousCount, state.CurrentCount)
	}
	if !state.WindowStart.Equal(windowStart(now, window)) {
		t.Fatalf("window start = %v, want %v", state.WindowStart, windowStart(now, window))
	}
}

func TestRollSlidingWindow_AdvancesOneWindow(t *testing.T) {
	window := 60 * time.Second
	base := time.Unix(1_700_000_000, 0).UTC()
	start := windowStart(base, window)
	now := start.Add(window)

	state := slidingWindowCounterState{
		WindowStart:   start,
		PreviousCount: 5,
		CurrentCount:  42,
	}
	rollSlidingWindow(&state, now, window)

	if state.PreviousCount != 42 {
		t.Fatalf("previous count = %d, want 42", state.PreviousCount)
	}
	if state.CurrentCount != 0 {
		t.Fatalf("current count = %d, want 0", state.CurrentCount)
	}
}

func TestEstimatedSlidingWindowCount_EdgeWeights(t *testing.T) {
	window := 10 * time.Second
	start := windowStart(time.Unix(1_700_000_000, 0).UTC(), window)

	atStart := estimatedSlidingWindowCount(slidingWindowCounterState{
		WindowStart: start, PreviousCount: 100, CurrentCount: 0,
	}, start, window)
	if atStart != 100 {
		t.Fatalf("at window start estimated = %v, want 100", atStart)
	}

	atEnd := estimatedSlidingWindowCount(slidingWindowCounterState{
		WindowStart: start, PreviousCount: 100, CurrentCount: 0,
	}, start.Add(window), window)
	if atEnd != 0 {
		t.Fatalf("at window end estimated = %v, want 0", atEnd)
	}
}

func TestTransitionSlidingWindowCounter_CostGreaterThanLimitDenied(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	policy := SlidingWindowCounterPolicy{Limit: 5, Window: time.Minute}

	_, result := transitionSlidingWindowCounter(slidingWindowCounterState{
		WindowStart: windowStart(now, policy.Window),
	}, now, 6, policy)

	if result.Allowed {
		t.Fatal("expected deny when cost exceeds limit")
	}
}

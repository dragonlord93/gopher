package ratelimit

import "time"

// Clock provides the current time. Inject a fake implementation in tests.
type Clock interface {
	Now() time.Time
}

// RealClock uses time.Now.
type RealClock struct{}

func (RealClock) Now() time.Time {
	return time.Now()
}

// FakeClock is a mutable clock for tests.
type FakeClock struct {
	current time.Time
}

func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{current: start}
}

func (c *FakeClock) Now() time.Time {
	return c.current
}

func (c *FakeClock) Advance(d time.Duration) {
	c.current = c.current.Add(d)
}

func (c *FakeClock) Set(t time.Time) {
	c.current = t
}

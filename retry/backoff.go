package retry

import (
	"math/rand"
	"time"
)

type Backoff interface {
	GetBase() time.Duration
	ComputeDelay() time.Duration
}

type LinearBackoff struct {
	Base    time.Duration
	Cap     time.Duration
	Delay   time.Duration
	MeanVar float64
}

func (l *LinearBackoff) GetBase() time.Duration {
	return l.Base
}

func (l *LinearBackoff) ComputeDelay() time.Duration {
	delay := min(l.Cap, l.Delay+l.Base)
	jitter := rand.Int63n(int64(float64(delay) * l.MeanVar))
	return l.Delay + time.Duration(jitter)*time.Millisecond
}

var _ Backoff = (*LinearBackoff)(nil)

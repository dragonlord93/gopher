package ratelimiter2

import (
	"sync"
	"time"
)

type counter struct {
	currentWindowStart time.Time
	previousCnt        int
	currentCnt         int
}

type SlidingWindowLimiter struct {
	mu         *sync.Mutex
	maxRequest int
	windowSize time.Duration
	users      map[string]*counter
}

func NewSlidingWindowLimiter(maxRequest int, windowSize time.Duration) *SlidingWindowLimiter {
	return &SlidingWindowLimiter{
		maxRequest: maxRequest,
		windowSize: windowSize,
		users:      make(map[string]*counter),
	}
}

func (s *SlidingWindowLimiter) Allow(key string) bool {

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	w, ok := s.users[key]
	if !ok {
		w = &counter{
			currentWindowStart: now,
			currentCnt:         0,
			previousCnt:        0,
		}
		s.users[key] = w
	}

	elapsed := now.Sub(w.currentWindowStart)

	switch {
	case elapsed >= 2*s.windowSize:
		w.currentCnt = 0
		w.previousCnt = 0
		w.currentWindowStart = now
	case elapsed >= s.windowSize:
		w.previousCnt = w.currentCnt
		w.previousCnt = 0
		w.currentWindowStart = w.currentWindowStart.Add(s.windowSize)
	}

	timeIntoCurrent := now.Sub(w.currentWindowStart)
	prevWeight := 1.0 - float64(timeIntoCurrent)/float64(s.windowSize)
	estimated := prevWeight*float64(w.previousCnt) + float64(w.currentCnt)

	if estimated < float64(s.maxRequest) {
		w.currentCnt++
		return true
	}

	return false
}

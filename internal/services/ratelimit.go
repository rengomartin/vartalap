package services

import (
	"sync"
	"time"
)

type bucket struct {
	tokens float64
	last   time.Time
}

// Limiter is a small in-memory token bucket limiter keyed by string (e.g. device ID).
// Anonymous IDs are not authentication; this only slows accidental or deliberate spam.
type Limiter struct {
	mu          sync.Mutex
	buckets     map[string]*bucket
	rate        float64
	burst       float64
	lastCleanup time.Time
}

// NewLimiter creates a limiter allowing rate tokens per second with the given burst.
func NewLimiter(rate float64, burst int) *Limiter {
	return &Limiter{
		buckets:     make(map[string]*bucket),
		rate:        rate,
		burst:       float64(burst),
		lastCleanup: time.Now(),
	}
}

// Allow reports whether one token is available for key at the supplied time.
func (l *Limiter) Allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	state, ok := l.buckets[key]
	if !ok {
		state = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = state
	}
	if elapsed := now.Sub(state.last).Seconds(); elapsed > 0 {
		state.tokens += elapsed * l.rate
		if state.tokens > l.burst {
			state.tokens = l.burst
		}
		state.last = now
	}

	allowed := state.tokens >= 1
	if allowed {
		state.tokens--
	}
	if now.Sub(l.lastCleanup) > time.Minute {
		for id, candidate := range l.buckets {
			if now.Sub(candidate.last) > 10*time.Minute {
				delete(l.buckets, id)
			}
		}
		l.lastCleanup = now
	}
	return allowed
}

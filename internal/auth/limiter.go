package auth

import (
	"sync"

	"golang.org/x/time/rate"
)

// Limiter is a keyed, in-memory token-bucket rate limiter. Good enough for a
// single-instance deployment; not distributed (fine — one binary, one host).
type Limiter struct {
	mu      sync.Mutex
	r       rate.Limit
	burst   int
	entries map[string]*rate.Limiter
}

// NewLimiter builds a limiter allowing perMinute events per key on average,
// with an immediate burst.
func NewLimiter(perMinute float64, burst int) *Limiter {
	return &Limiter{
		r:       rate.Limit(perMinute / 60.0),
		burst:   burst,
		entries: make(map[string]*rate.Limiter),
	}
}

// Allow reports whether one event for key is permitted.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	rl, ok := l.entries[key]
	if !ok {
		if len(l.entries) > 10_000 { // bound memory under key spray
			l.entries = make(map[string]*rate.Limiter)
		}
		rl = rate.NewLimiter(l.r, l.burst)
		l.entries[key] = rl
	}
	return rl.Allow()
}

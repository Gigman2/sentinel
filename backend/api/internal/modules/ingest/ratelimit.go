package ingest

import (
	"sync"

	"golang.org/x/time/rate"
)

// Limiter rate-limits ingest per integration, in memory. Each integration
// has its own token bucket, so one noisy source cannot starve another.
// (Per-organization limiting is deferred; see the ADR.)
type Limiter struct {
	rate  rate.Limit
	burst int

	mu      sync.Mutex
	buckets map[string]*rate.Limiter
}

// NewLimiter allows r events per second per integration, with bursts of up to burst.
func NewLimiter(r rate.Limit, burst int) *Limiter {
	return &Limiter{rate: r, burst: burst, buckets: map[string]*rate.Limiter{}}
}

// Allow reports whether integrationID may send one more event now.
func (l *Limiter) Allow(integrationID string) bool {
	l.mu.Lock()
	b, ok := l.buckets[integrationID]
	if !ok {
		b = rate.NewLimiter(l.rate, l.burst)
		l.buckets[integrationID] = b
	}
	l.mu.Unlock()
	return b.Allow()
}

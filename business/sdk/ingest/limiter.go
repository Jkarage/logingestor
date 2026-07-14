package ingest

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// bucket is a single source's token bucket.
type bucket struct {
	tokens float64
	last   time.Time
}

// Limiter is an in-process per-source token-bucket rate limiter. It is safe for
// concurrent use. Buckets are created lazily and keyed by source ID; the rate
// and burst are supplied per call so a source's configured limits are honored
// without re-registration.
type Limiter struct {
	mu      sync.Mutex
	buckets map[uuid.UUID]*bucket
}

// NewLimiter constructs an empty limiter.
func NewLimiter() *Limiter {
	return &Limiter{buckets: make(map[uuid.UUID]*bucket)}
}

// AllowN reports whether n events are permitted for the source right now given
// ratePerSec/burst, refilling the bucket from elapsed time. When denied it
// returns the duration the caller should wait before retrying (Retry-After).
func (l *Limiter) AllowN(id uuid.UUID, ratePerSec, burst, n int, now time.Time) (bool, time.Duration) {
	if ratePerSec <= 0 || burst <= 0 {
		return true, 0 // unlimited / misconfigured -> do not throttle
	}

	rate := float64(ratePerSec)
	cap := float64(burst)
	need := float64(n)

	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[id]
	if !ok {
		b = &bucket{tokens: cap, last: now}
		l.buckets[id] = b
	}

	// Refill based on elapsed time, clamped to burst.
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = min(cap, b.tokens+elapsed*rate)
		b.last = now
	}

	if b.tokens >= need {
		b.tokens -= need
		return true, 0
	}

	deficit := need - b.tokens
	wait := max(time.Duration(deficit/rate*float64(time.Second)), time.Second)
	return false, wait
}

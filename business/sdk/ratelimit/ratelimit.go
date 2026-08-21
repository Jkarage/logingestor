// Package ratelimit provides an in-process, keyed token bucket.
//
// It is in-process on purpose. A limiter that needs a network round trip to
// decide is part of the load it is supposed to shed, and this service runs as a
// single instance — the moment that stops being true, the per-instance limit
// becomes per-instance rather than global, which is a real thing to fix then and
// not a reason to add Redis now.
package ratelimit

import (
	"sync"
	"time"
)

// Policy is what a caller is allowed. Rate is per minute rather than per second
// because that is how an API limit is written down, and because an integer
// per-second rate cannot express "thirty requests a minute" at all.
type Policy struct {
	// PerMinute is the sustained rate. Zero or negative means unlimited.
	PerMinute float64

	// Burst is how much can be spent at once. Zero falls back to one minute's
	// worth, which keeps a policy usable when only the rate is configured.
	Burst float64
}

// withDefaults fills in a burst so a policy that names only a rate still allows
// a sensible clump of requests rather than exactly one per interval.
func (p Policy) withDefaults() Policy {
	if p.Burst <= 0 {
		p.Burst = p.PerMinute
	}

	return p
}

// Decision is the outcome of one check.
type Decision struct {
	// Allowed is whether the request may proceed.
	Allowed bool

	// Remaining is how much of the burst is left after this request, rounded
	// down. It is reported so a caller can slow down before being refused.
	Remaining int

	// RetryAfter is how long to wait, set only when the request was refused.
	RetryAfter time.Duration

	// ResetAfter is how long until the bucket is full again.
	ResetAfter time.Duration
}

type bucket struct {
	tokens float64
	last   time.Time
}

// Limiter holds one bucket per key. It is safe for concurrent use.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket

	// idle is how long a key may go unused before its bucket is dropped, so the
	// map cannot grow without bound on a long-lived process. A bucket may
	// survive up to twice this, since the sweep itself only runs that often.
	idle time.Duration

	// swept is when the last sweep ran, on the caller's clock. It starts at the
	// zero time so the first call sweeps — every other decision here is made
	// from the now the caller passes, and seeding this from time.Now() would
	// make the limiter behave differently under a test clock than in production.
	swept time.Time
}

// DefaultIdle is how long an unused bucket is kept.
const DefaultIdle = 30 * time.Minute

// New constructs a limiter.
func New() *Limiter {
	return &Limiter{buckets: make(map[string]*bucket), idle: DefaultIdle}
}

// Allow charges cost against key's bucket under policy and reports what to do.
//
// cost lets one request weigh more than another, which matters when the same
// credential can ask for a page of twenty rows or an export of a hundred
// thousand: a flat per-request limit would leave the expensive call unbounded.
func (l *Limiter) Allow(key string, policy Policy, cost float64, now time.Time) Decision {
	policy = policy.withDefaults()

	if policy.PerMinute <= 0 {
		return Decision{Allowed: true, Remaining: -1}
	}
	if cost <= 0 {
		cost = 1
	}

	perSecond := policy.PerMinute / 60

	l.mu.Lock()
	defer l.mu.Unlock()

	l.sweep(now)

	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: policy.Burst, last: now}
		l.buckets[key] = b
	}

	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = min(policy.Burst, b.tokens+elapsed*perSecond)
		b.last = now
	}

	// A cost above the whole burst could never be paid, and refusing it forever
	// would be worse than serving it once: the burst is the cap on concurrency,
	// not a statement that the request is too big to exist.
	if cost > policy.Burst {
		cost = policy.Burst
	}

	full := time.Duration((policy.Burst - b.tokens) / perSecond * float64(time.Second))

	if b.tokens < cost {
		deficit := cost - b.tokens

		return Decision{
			Allowed:    false,
			Remaining:  0,
			RetryAfter: max(time.Duration(deficit/perSecond*float64(time.Second)), time.Second),
			ResetAfter: full,
		}
	}

	b.tokens -= cost

	return Decision{
		Allowed:    true,
		Remaining:  int(b.tokens),
		ResetAfter: time.Duration((policy.Burst - b.tokens) / perSecond * float64(time.Second)),
	}
}

// sweep drops buckets nobody has touched recently. The caller holds the lock.
func (l *Limiter) sweep(now time.Time) {
	if now.Sub(l.swept) < l.idle {
		return
	}

	for k, b := range l.buckets {
		if now.Sub(b.last) > l.idle {
			delete(l.buckets, k)
		}
	}
	l.swept = now
}

// Len reports how many buckets are held. It exists for tests and for a metric.
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return len(l.buckets)
}

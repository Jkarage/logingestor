package ratelimit

import (
	"sync"
	"testing"
	"time"
)

func Test_Allow_SpendsTheBurstThenRefills(t *testing.T) {
	l := New()
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	policy := Policy{PerMinute: 60, Burst: 10}

	// The burst is spendable immediately, which is the point of a burst.
	for i := 0; i < 10; i++ {
		d := l.Allow("k", policy, 1, now)
		if !d.Allowed {
			t.Fatalf("request %d was refused inside the burst", i)
		}
		if want := 10 - i - 1; d.Remaining != want {
			t.Errorf("request %d: remaining = %d, want %d", i, d.Remaining, want)
		}
	}

	// The eleventh is not, and it says how long to wait.
	d := l.Allow("k", policy, 1, now)
	if d.Allowed {
		t.Fatalf("the burst did not run out")
	}
	if d.RetryAfter < time.Second {
		t.Errorf("retryAfter = %v, want at least a second", d.RetryAfter)
	}
	if d.Remaining != 0 {
		t.Errorf("remaining = %d, want 0", d.Remaining)
	}

	// One token a second at 60/min.
	if d := l.Allow("k", policy, 1, now.Add(time.Second)); !d.Allowed {
		t.Errorf("a token did not refill after a second")
	}

	// Refill is capped at the burst however long it idles.
	d = l.Allow("k", policy, 10, now.Add(time.Hour))
	if !d.Allowed {
		t.Errorf("a full burst was refused after an hour idle")
	}
	if d := l.Allow("k", policy, 1, now.Add(time.Hour)); d.Allowed {
		t.Errorf("the bucket refilled past its burst")
	}
}

// A per-minute rate below sixty is the case an integer per-second limiter cannot
// express at all — it truncates to zero and silently allows everything.
func Test_Allow_HandlesSubSecondRates(t *testing.T) {
	l := New()
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	policy := Policy{PerMinute: 30, Burst: 2}

	if d := l.Allow("k", policy, 1, now); !d.Allowed {
		t.Fatalf("first request refused")
	}
	if d := l.Allow("k", policy, 1, now); !d.Allowed {
		t.Fatalf("second request refused")
	}
	if d := l.Allow("k", policy, 1, now); d.Allowed {
		t.Fatalf("30/min with a burst of 2 allowed a third immediate request")
	}

	// Half a token a second: one second is not enough, two is.
	if d := l.Allow("k", policy, 1, now.Add(time.Second)); d.Allowed {
		t.Errorf("a token appeared after one second at 30/min")
	}
	if d := l.Allow("k", policy, 1, now.Add(2*time.Second)); !d.Allowed {
		t.Errorf("no token after two seconds at 30/min")
	}
}

// Cost is what keeps an expensive call from hiding behind a per-request limit.
func Test_Allow_ChargesByCost(t *testing.T) {
	l := New()
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	policy := Policy{PerMinute: 60, Burst: 30}

	if d := l.Allow("k", policy, 20, now); !d.Allowed || d.Remaining != 10 {
		t.Fatalf("an expensive call was refused or mispriced: %+v", d)
	}
	if d := l.Allow("k", policy, 20, now); d.Allowed {
		t.Errorf("two expensive calls fitted in a burst of 30")
	}
	if d := l.Allow("k", policy, 1, now); !d.Allowed {
		t.Errorf("a cheap call was refused with ten tokens left")
	}

	// A cost larger than the whole burst is clamped rather than refused forever:
	// the burst caps concurrency, it does not declare a request too big to exist.
	fresh := New()
	if d := fresh.Allow("k", policy, 1000, now); !d.Allowed {
		t.Errorf("a cost above the burst was refused outright")
	}
}

// Unlimited has to mean unlimited, and it must not be reachable by accident from
// a fractional rate.
func Test_Allow_Unlimited(t *testing.T) {
	l := New()
	now := time.Now()

	for i := 0; i < 100; i++ {
		d := l.Allow("k", Policy{PerMinute: 0}, 50, now)
		if !d.Allowed {
			t.Fatalf("a zero rate throttled request %d", i)
		}
		if d.Remaining != -1 {
			t.Errorf("remaining = %d, want -1 to mean unlimited", d.Remaining)
		}
	}

	if l.Len() != 0 {
		t.Errorf("an unlimited policy allocated %d buckets", l.Len())
	}
}

// One noisy key must not throttle another.
func Test_Allow_KeysAreIndependent(t *testing.T) {
	l := New()
	now := time.Now()
	policy := Policy{PerMinute: 60, Burst: 2}

	l.Allow("loud", policy, 2, now)
	if d := l.Allow("loud", policy, 1, now); d.Allowed {
		t.Fatalf("the loud key was not throttled")
	}
	if d := l.Allow("quiet", policy, 1, now); !d.Allowed {
		t.Errorf("a second key was throttled by the first's traffic")
	}
}

// A policy naming only a rate still has to be usable.
func Test_Policy_BurstDefaultsToOneMinute(t *testing.T) {
	l := New()
	now := time.Now()

	d := l.Allow("k", Policy{PerMinute: 5}, 1, now)
	if !d.Allowed {
		t.Fatalf("a rate-only policy refused the first request")
	}
	if d.Remaining != 4 {
		t.Errorf("remaining = %d, want a burst of one minute's worth", d.Remaining)
	}
}

// The map must not grow without bound on a long-lived process.
func Test_Limiter_SweepsIdleBuckets(t *testing.T) {
	l := New()
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	policy := Policy{PerMinute: 60}

	for i := 0; i < 200; i++ {
		l.Allow(string(rune('a'+i%26))+string(rune('a'+i/26)), policy, 1, now)
	}
	before := l.Len()
	if before < 100 {
		t.Fatalf("only %d buckets were created", before)
	}

	l.Allow("active", policy, 1, now.Add(DefaultIdle+time.Minute))
	if l.Len() != 1 {
		t.Errorf("buckets = %d after the sweep, want only the active one", l.Len())
	}
}

// Concurrent callers must not corrupt a bucket or lose a refusal.
func Test_Allow_IsConcurrencySafe(t *testing.T) {
	l := New()
	now := time.Now()
	policy := Policy{PerMinute: 60, Burst: 50}

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		allowed int
	)

	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			if l.Allow("shared", policy, 1, now).Allowed {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	// Every caller passes the same instant, so exactly the burst should get
	// through: more would mean tokens were double spent.
	if allowed != 50 {
		t.Errorf("allowed %d of 200 concurrent requests, want exactly the burst of 50", allowed)
	}
}

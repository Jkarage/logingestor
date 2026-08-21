package mid

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/business/sdk/ratelimit"
	"github.com/jkarage/logingestor/foundation/web"
)

// QueryAPICost is what each read costs against a key's budget.
//
// A flat per-request limit would leave the expensive call unbounded: the same
// credential can ask for a page of twenty rows or stream an export of a hundred
// thousand. Pricing the export higher is the difference between a limit that
// protects the database and one that only looks like it does.
const (
	CostRead   = 1
	CostExport = 20
)

// RateLimitAPIKey throttles the query API per key.
//
// It runs after AuthenticateAPIKey and reads the key from the context, so the
// budget belongs to the credential rather than to an address — a customer's CI
// runner has no stable address, and an address shared by a NAT would punish
// tenants for each other's traffic.
//
// Every response carries the budget headers, not just the refusals, so a script
// can slow down before it is refused rather than after.
func RateLimitAPIKey(limiter *ratelimit.Limiter, defaultPerMin, defaultBurst int) web.MidFunc {
	m := func(next web.HandlerFunc) web.HandlerFunc {
		h := func(ctx context.Context, r *http.Request) web.Encoder {
			key, err := GetAPIKey(ctx)
			if err != nil {
				// Unreachable behind the authenticator, and a limiter that cannot
				// identify the caller must refuse rather than wave everyone through.
				return errs.New(errs.Unauthenticated, err)
			}

			policy := ratelimit.Policy{
				PerMinute: float64(defaultPerMin),
				Burst:     float64(defaultBurst),
			}
			if key.RateLimitPerMin > 0 {
				policy.PerMinute = float64(key.RateLimitPerMin)
				policy.Burst = 0
			}
			if key.RateLimitBurst > 0 {
				policy.Burst = float64(key.RateLimitBurst)
			}

			decision := limiter.Allow(key.ID.String(), policy, float64(queryCost(r)), time.Now())

			if w := web.GetWriter(ctx); w != nil {
				writeBudget(w, policy, decision)
			}

			if !decision.Allowed {
				if w := web.GetWriter(ctx); w != nil {
					w.Header().Set("Retry-After", strconv.Itoa(max(int(decision.RetryAfter.Seconds()), 1)))
				}

				return errs.New(errs.TooManyRequests, errors.New("query API rate limit exceeded"))
			}

			return next(ctx, r)
		}

		return h
	}

	return m
}

// queryCost prices one request. Export is the only call that can return an
// unbounded amount of work, so it is the only one priced above a read.
func queryCost(r *http.Request) int {
	if strings.HasSuffix(r.URL.Path, "/export") {
		return CostExport
	}

	return CostRead
}

// writeBudget reports the limit on every response. The names are the widely
// implemented X-RateLimit-* set rather than the IETF draft, because that is what
// HTTP clients and their retry helpers already understand.
func writeBudget(w http.ResponseWriter, policy ratelimit.Policy, d ratelimit.Decision) {
	if d.Remaining < 0 {
		// Unlimited. Saying nothing is clearer than reporting a limit of zero.
		return
	}

	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(int(policy.PerMinute)))
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(d.Remaining))
	w.Header().Set("X-RateLimit-Reset", strconv.Itoa(max(int(d.ResetAfter.Seconds()), 0)))
}

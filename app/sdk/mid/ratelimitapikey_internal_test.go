package mid

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/business/domain/apikeybus"
	"github.com/jkarage/logingestor/business/sdk/ratelimit"
	"github.com/jkarage/logingestor/foundation/web"
)

// serve builds an app with the throttle installed behind a stub that puts a key
// in the context, so the middleware is exercised through a real router — the
// headers and the status are the contract, and calling it directly would test
// neither.
func serveThrottled(t *testing.T, key apikeybus.APIKey, perMin, burst int) func(path string) *httptest.ResponseRecorder {
	t.Helper()

	app := web.NewApp(func(context.Context, string, ...any) {}, nil)

	// Stand in for AuthenticateAPIKey, which is the only thing the throttle needs
	// from the request.
	authenticate := func(next web.HandlerFunc) web.HandlerFunc {
		return func(ctx context.Context, r *http.Request) web.Encoder {
			return next(setAPIKey(ctx, key), r)
		}
	}

	throttle := RateLimitAPIKey(ratelimit.New(), perMin, burst)

	handler := func(context.Context, *http.Request) web.Encoder { return nil }

	app.HandlerFunc(http.MethodGet, "v1", "/query/logs", handler, authenticate, throttle)
	app.HandlerFunc(http.MethodGet, "v1", "/query/logs/export", handler, authenticate, throttle)

	return func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))

		return w
	}
}

func aKey() apikeybus.APIKey {
	return apikeybus.APIKey{ID: uuid.New(), OrgID: uuid.New(), IsActive: true}
}

// The budget is spendable, then it runs out with a 429 and a Retry-After — an
// unbounded read path is how a script in a retry loop takes the app down with it.
func Test_RateLimitAPIKey_RefusesOverBudget(t *testing.T) {
	get := serveThrottled(t, aKey(), 60, 5)

	for i := 0; i < 5; i++ {
		if w := get("/v1/query/logs"); w.Code != http.StatusNoContent {
			t.Fatalf("request %d: status = %d, want 204", i, w.Code)
		}
	}

	w := get("/v1/query/logs")
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Errorf("no Retry-After on a 429")
	}
	if got := w.Header().Get("X-RateLimit-Remaining"); got != "0" {
		t.Errorf("remaining = %q, want 0", got)
	}
}

// The headers are on every response, not only the refusals, so a caller can slow
// down before it is refused rather than after.
func Test_RateLimitAPIKey_ReportsTheBudgetOnSuccess(t *testing.T) {
	get := serveThrottled(t, aKey(), 120, 10)

	w := get("/v1/query/logs")
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d", w.Code)
	}

	if got := w.Header().Get("X-RateLimit-Limit"); got != "120" {
		t.Errorf("limit = %q, want 120", got)
	}
	remaining, err := strconv.Atoi(w.Header().Get("X-RateLimit-Remaining"))
	if err != nil || remaining != 9 {
		t.Errorf("remaining = %q, want 9", w.Header().Get("X-RateLimit-Remaining"))
	}
	if w.Header().Get("X-RateLimit-Reset") == "" {
		t.Errorf("no reset hint")
	}
}

// Export can stream a hundred thousand rows, so it cannot cost the same as a
// page of twenty. Without weighting, the one call worth limiting is the one a
// per-request limit does not bound.
func Test_RateLimitAPIKey_ExportCostsMore(t *testing.T) {
	get := serveThrottled(t, aKey(), 600, CostExport)

	// The whole burst is exactly one export.
	if w := get("/v1/query/logs/export"); w.Code != http.StatusNoContent {
		t.Fatalf("the first export was refused: %d", w.Code)
	}
	if w := get("/v1/query/logs/export"); w.Code != http.StatusTooManyRequests {
		t.Errorf("a second export fitted in a budget of one: %d", w.Code)
	}

	// A budget that allows one export allows many reads.
	get = serveThrottled(t, aKey(), 600, CostExport)
	for i := 0; i < CostExport; i++ {
		if w := get("/v1/query/logs"); w.Code != http.StatusNoContent {
			t.Fatalf("read %d was refused inside a budget of %d: %d", i, CostExport, w.Code)
		}
	}
	if w := get("/v1/query/logs"); w.Code != http.StatusTooManyRequests {
		t.Errorf("reads were not counted at all: %d", w.Code)
	}
}

// A key's own limit overrides the default, which is how one customer is raised
// or throttled without a deploy.
func Test_RateLimitAPIKey_PerKeyOverride(t *testing.T) {
	key := aKey()
	key.RateLimitPerMin = 60
	key.RateLimitBurst = 2

	get := serveThrottled(t, key, 120, 240)

	if w := get("/v1/query/logs"); w.Code != http.StatusNoContent {
		t.Fatalf("first request refused")
	}
	if w := get("/v1/query/logs"); w.Code != http.StatusNoContent {
		t.Fatalf("second request refused")
	}

	w := get("/v1/query/logs")
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("the key's own burst of 2 was ignored: %d", w.Code)
	}
	if got := w.Header().Get("X-RateLimit-Limit"); got != "60" {
		t.Errorf("limit = %q, want the key's own 60", got)
	}
}

// A key naming only a rate must get a burst from that rate, not from the
// service default it is overriding — otherwise a key asking for 60/min would
// silently keep a burst of 240.
func Test_RateLimitAPIKey_OverrideDerivesItsOwnBurst(t *testing.T) {
	key := aKey()
	key.RateLimitPerMin = 3

	get := serveThrottled(t, key, 120, 240)

	for i := 0; i < 3; i++ {
		if w := get("/v1/query/logs"); w.Code != http.StatusNoContent {
			t.Fatalf("request %d refused inside a burst of 3", i)
		}
	}
	if w := get("/v1/query/logs"); w.Code != http.StatusTooManyRequests {
		t.Errorf("a 3/min key inherited the default burst: %d", w.Code)
	}
}

// A zero default means unlimited, and it must not report a limit of zero — which
// a client would read as "you may make no requests".
func Test_RateLimitAPIKey_UnlimitedSaysNothing(t *testing.T) {
	get := serveThrottled(t, aKey(), 0, 0)

	for i := 0; i < 50; i++ {
		if w := get("/v1/query/logs/export"); w.Code != http.StatusNoContent {
			t.Fatalf("request %d refused under an unlimited policy: %d", i, w.Code)
		}
	}

	w := get("/v1/query/logs")
	if got := w.Header().Get("X-RateLimit-Limit"); got != "" {
		t.Errorf("an unlimited policy reported a limit of %q", got)
	}
}

// One noisy key must not throttle another tenant's script.
func Test_RateLimitAPIKey_KeysAreIndependent(t *testing.T) {
	limiter := ratelimit.New()
	throttle := RateLimitAPIKey(limiter, 60, 2)

	serve := func(key apikeybus.APIKey) *httptest.ResponseRecorder {
		app := web.NewApp(func(context.Context, string, ...any) {}, nil)

		authenticate := func(next web.HandlerFunc) web.HandlerFunc {
			return func(ctx context.Context, r *http.Request) web.Encoder {
				return next(setAPIKey(ctx, key), r)
			}
		}
		app.HandlerFunc(http.MethodGet, "v1", "/query/logs", func(context.Context, *http.Request) web.Encoder { return nil }, authenticate, throttle)

		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/query/logs", nil))

		return w
	}

	loud, quiet := aKey(), aKey()

	serve(loud)
	serve(loud)
	if w := serve(loud); w.Code != http.StatusTooManyRequests {
		t.Fatalf("the loud key was not throttled: %d", w.Code)
	}
	if w := serve(quiet); w.Code != http.StatusNoContent {
		t.Errorf("a second key was throttled by the first's traffic: %d", w.Code)
	}
}

// A limiter that cannot identify the caller must refuse rather than wave
// everyone through. Unreachable behind the authenticator, which is the point.
func Test_RateLimitAPIKey_RefusesWithoutAKey(t *testing.T) {
	throttle := RateLimitAPIKey(ratelimit.New(), 60, 60)

	handler := throttle(func(context.Context, *http.Request) web.Encoder { return nil })

	resp := handler(context.Background(), httptest.NewRequest(http.MethodGet, "/v1/query/logs", nil))

	e, ok := resp.(*errs.Error)
	if !ok {
		t.Fatalf("expected an error, got %T", resp)
	}
	if e.HTTPStatus() != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", e.HTTPStatus())
	}
}

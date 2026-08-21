package clienterrorapp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/business/domain/clienterrorbus"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jkarage/logingestor/foundation/web"
)

// The ingest endpoint is the only unauthenticated write in the product, so the
// controls that keep it from being a liability are tested through a real
// router — the status code and the Retry-After header are part of the contract,
// and calling the handler directly would not exercise either.

// fakeStorer records what reached the store and implements nothing else.
type fakeStorer struct {
	mu     sync.Mutex
	events []clienterrorbus.Event
	clienterrorbus.Storer
}

func (f *fakeStorer) Ingest(_ context.Context, events []clienterrorbus.Event) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.events = append(f.events, events...)

	return len(events), nil
}

func (f *fakeStorer) stored() []clienterrorbus.Event {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]clienterrorbus.Event(nil), f.events...)
}

// serve builds an app with the given origins and returns a function that posts a
// body and hands back the response.
func serve(t *testing.T, origins []string) (*fakeStorer, func(body string, headers map[string]string, remote string) *httptest.ResponseRecorder) {
	t.Helper()

	store := &fakeStorer{}
	log := logger.New(io.Discard, logger.LevelError, "test", func(context.Context) string { return "" })

	api := newApp(Config{
		Log:            log,
		ClientErrorBus: clienterrorbus.NewBusiness(log, store, nil),
		AllowedOrigins: origins,
	})

	app := web.NewApp(func(context.Context, string, ...any) {}, nil)
	app.HandlerFunc(http.MethodPost, "v1", "/client-errors", api.ingest)

	post := func(body string, headers map[string]string, remote string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/v1/client-errors", strings.NewReader(body))
		r.RemoteAddr = remote
		for k, v := range headers {
			r.Header.Set(k, v)
		}

		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)

		return w
	}

	return store, post
}

func batch(n int) string {
	var b strings.Builder
	b.WriteString(`{"events":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"eventId":"` + uuid.NewString() + `","level":"error","kind":"react","name":"TypeError","message":"boom"}`)
	}
	b.WriteString(`]}`)

	return b.String()
}

// errorCode reads the machine code out of an error envelope.
func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()

	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not an error envelope: %s", w.Body.String())
	}

	return body.Error
}

// A report is accepted with 202 and a count: the browser must never wait on
// grouping.
func Test_ingest_Accepts(t *testing.T) {
	store, post := serve(t, []string{"*"})

	w := post(batch(3), map[string]string{"Origin": "https://streamlogia.com"}, "203.0.113.10:1")

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", w.Code, w.Body.String())
	}

	var body Accepted
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not the accepted shape: %s", w.Body.String())
	}
	if body.Accepted != 3 {
		t.Errorf("accepted = %d, want 3", body.Accepted)
	}

	stored := store.stored()
	if len(stored) != 3 {
		t.Fatalf("stored %d events, want 3", len(stored))
	}

	// No token was presented, so the report is anonymous whatever it claimed.
	for _, e := range stored {
		if e.OrgID != nil || e.UserID != nil {
			t.Errorf("an anonymous report was attributed: org %v user %v", e.OrgID, e.UserID)
		}
	}
}

// A body over the cap is refused before it is parsed. Without this an anonymous
// caller can make the API buffer whatever it likes.
func Test_ingest_RejectsOversizeBody(t *testing.T) {
	store, post := serve(t, []string{"*"})

	huge := `{"events":[{"message":"` + strings.Repeat("x", 200_000) + `"}]}`
	w := post(huge, nil, "203.0.113.10:1")

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", w.Code)
	}
	if got := errorCode(t, w); got != "payload_too_large" {
		t.Errorf("error = %q, want payload_too_large", got)
	}
	if len(store.stored()) != 0 {
		t.Errorf("an oversize body reached the store")
	}
}

// A batch over the cap is refused whole rather than partly stored.
func Test_ingest_RejectsOversizeBatch(t *testing.T) {
	store, post := serve(t, []string{"*"})

	w := post(batch(clienterrorbus.MaxBatchEvents+1), nil, "203.0.113.10:1")

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
	if len(store.stored()) != 0 {
		t.Errorf("part of an oversize batch was stored")
	}
}

// Never a 500 for bad input: the browser retries these, and a 500 would look
// like our fault and be retried harder.
func Test_ingest_RejectsEmptyAndMalformed(t *testing.T) {
	_, post := serve(t, []string{"*"})

	for _, body := range []string{`{"events":[]}`, `{}`, `not json`, ``} {
		w := post(body, nil, "203.0.113.10:1")

		if w.Code != http.StatusBadRequest {
			t.Errorf("body %q: status = %d, want 400", body, w.Code)
		}
	}
}

// A report from somebody else's site is refused. It is not a security boundary —
// a header is forgeable — but it keeps other sites' scripts and scanners out.
func Test_ingest_OriginAllowlist(t *testing.T) {
	store, post := serve(t, []string{"https://streamlogia.com", "https://preview.streamlogia.com"})

	w := post(batch(1), map[string]string{"Origin": "https://evil.example"}, "203.0.113.10:1")
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
	if got := errorCode(t, w); got != "permission_denied" {
		t.Errorf("error = %q", got)
	}

	if w := post(batch(1), map[string]string{"Origin": "https://preview.streamlogia.com"}, "203.0.113.10:1"); w.Code != http.StatusAccepted {
		t.Errorf("an allowed origin got %d: %s", w.Code, w.Body.String())
	}

	// A beacon fired during unload sends no Origin at all, and those are the
	// reports we most want to keep.
	if w := post(batch(1), nil, "203.0.113.10:1"); w.Code != http.StatusAccepted {
		t.Errorf("a request with no Origin got %d", w.Code)
	}

	if len(store.stored()) != 2 {
		t.Errorf("stored %d events, want the two allowed ones", len(store.stored()))
	}
}

// The rate limit counts events rather than requests, or a client batches its way
// around it. When it trips, the response has to say how long to wait.
func Test_ingest_RateLimitsByEventCount(t *testing.T) {
	_, post := serve(t, []string{"*"})

	const client = "203.0.113.10:1"

	// The burst is 240 events, so four batches of fifty stay under it and the
	// fifth crosses.
	for i := 0; i < 4; i++ {
		if w := post(batch(50), nil, client); w.Code != http.StatusAccepted {
			t.Fatalf("batch %d was refused early with %d", i, w.Code)
		}
	}

	w := post(batch(50), nil, client)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Errorf("no Retry-After header on a 429")
	}
	if got := errorCode(t, w); got != "too_many_requests" {
		t.Errorf("error = %q", got)
	}

	// A different address is unaffected: one noisy client must not silence
	// everyone else's reports.
	if w := post(batch(1), nil, "198.51.100.7:1"); w.Code != http.StatusAccepted {
		t.Errorf("a second address was limited by the first's traffic: %d", w.Code)
	}
}

// Everything the payload says about identity is a hint, and a request with no
// token must not be attributed however hard it insists.
func Test_ingest_IgnoresClientIdentityClaims(t *testing.T) {
	store, post := serve(t, []string{"*"})

	body := `{"events":[{"eventId":"` + uuid.NewString() + `","level":"error","kind":"react",` +
		`"name":"TypeError","message":"boom","orgId":"` + uuid.NewString() + `",` +
		`"userId":"` + uuid.NewString() + `","role":"SUPER ADMIN"}]}`

	if w := post(body, nil, "203.0.113.10:1"); w.Code != http.StatusAccepted {
		t.Fatalf("status = %d", w.Code)
	}

	stored := store.stored()
	if len(stored) != 1 {
		t.Fatalf("stored %d events", len(stored))
	}
	if stored[0].OrgID != nil || stored[0].UserID != nil || stored[0].Role != "" {
		t.Errorf("client identity claims were trusted: %+v", stored[0])
	}
}

// Storage-bound fields are enforced on the way in, not left to the database.
func Test_ingest_ScrubsAndBounds(t *testing.T) {
	store, post := serve(t, []string{"*"})

	body := `{"events":[{"eventId":"` + uuid.NewString() + `","level":"error","kind":"api",` +
		`"name":"ApiError","message":"failed for joseph@bsa.ai with Bearer abc123def456",` +
		`"url":"/dashboard?token=supersecret123","stack":"at f (app.js:1:1)",` +
		`"api":{"method":"GET","path":"/v1/logs?token=leaky1234","status":401,"code":"unauthenticated"}}]}`

	if w := post(body, nil, "203.0.113.10:1"); w.Code != http.StatusAccepted {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}

	e := store.stored()[0]

	for _, secret := range []string{"joseph@bsa.ai", "abc123def456", "supersecret123", "leaky1234"} {
		if strings.Contains(e.Message+e.URL+e.API.Path, secret) {
			t.Errorf("secret %q survived scrubbing", secret)
		}
	}
	if e.URL != "/dashboard" {
		t.Errorf("url = %q, want the path only", e.URL)
	}
}

// An unknown level or kind is corrected rather than rejected: a report with a
// typo in a field is still a crash worth keeping.
func Test_ingest_NormalizesUnknownEnums(t *testing.T) {
	store, post := serve(t, []string{"*"})

	body := `{"events":[{"eventId":"` + uuid.NewString() + `","level":"catastrophe","kind":"telepathy","message":"boom"}]}`

	if w := post(body, nil, "203.0.113.10:1"); w.Code != http.StatusAccepted {
		t.Fatalf("status = %d", w.Code)
	}

	e := store.stored()[0]
	if e.Level != clienterrorbus.LevelError {
		t.Errorf("level = %q, want it corrected to error", e.Level)
	}
	if e.Kind != clienterrorbus.KindManual {
		t.Errorf("kind = %q, want it corrected to manual", e.Kind)
	}
	if e.Name != "Error" {
		t.Errorf("name = %q, want a default", e.Name)
	}
}

// A browser clock can be anything. A timestamp from the future would sort above
// every real event forever.
func Test_ingest_ClampsFutureTimestamps(t *testing.T) {
	store, post := serve(t, []string{"*"})

	future := time.Now().UTC().Add(72 * time.Hour).Format(time.RFC3339)
	body := `{"events":[{"eventId":"` + uuid.NewString() + `","timestamp":"` + future + `","level":"error","kind":"react","message":"boom"}]}`

	if w := post(body, nil, "203.0.113.10:1"); w.Code != http.StatusAccepted {
		t.Fatalf("status = %d", w.Code)
	}

	e := store.stored()[0]
	if e.OccurredAt.After(time.Now().UTC().Add(time.Minute)) {
		t.Errorf("occurredAt = %v, want it clamped to receive time", e.OccurredAt)
	}
}

func Test_clientKey(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
		remote  string
		want    string
	}{
		{"forwarded chain takes the client", map[string]string{"X-Forwarded-For": "203.0.113.5, 10.0.0.1, 10.0.0.2"}, "10.0.0.9:1", "203.0.113.5"},
		{"single forwarded value", map[string]string{"X-Forwarded-For": "203.0.113.6"}, "10.0.0.9:1", "203.0.113.6"},
		{"falls back to the socket", nil, "203.0.113.7:5555", "203.0.113.7"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/v1/client-errors", nil)
			r.RemoteAddr = c.remote
			for k, v := range c.headers {
				r.Header.Set(k, v)
			}

			if got := clientKey(r); got != c.want {
				t.Errorf("clientKey = %q, want %q", got, c.want)
			}
		})
	}
}

// The limiter must not grow without bound on a long-lived process.
func Test_ingestLimiter_SweepsIdleBuckets(t *testing.T) {
	l := newIngestLimiter()
	now := time.Now()

	for i := 0; i < 100; i++ {
		l.allow(uuid.NewString(), 1, now)
	}
	if len(l.buckets) != 100 {
		t.Fatalf("buckets = %d, want 100", len(l.buckets))
	}

	l.allow("203.0.113.1", 1, now.Add(bucketSweepInterval+time.Minute))
	if len(l.buckets) != 1 {
		t.Errorf("buckets = %d after the sweep, want only the active one", len(l.buckets))
	}
}

// Source map uploads come from CI, so they authenticate with a shared token
// rather than a session. An unconfigured token must refuse everything: a
// deployment that forgot to set it should fail to upload, not accept maps from
// anyone who asks.
func Test_uploadArtifacts_TokenAuth(t *testing.T) {
	cases := []struct {
		name       string
		configured string
		presented  string
		want       int
	}{
		{"no token configured", "", "Bearer anything", http.StatusForbidden},
		{"no header presented", "s3cret", "", http.StatusUnauthorized},
		{"wrong token", "s3cret", "Bearer wrong", http.StatusUnauthorized},
		{"not a bearer header", "s3cret", "s3cret", http.StatusUnauthorized},
		{"correct token", "s3cret", "Bearer s3cret", http.StatusBadRequest}, // past auth, no form
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := &app{uploadToken: c.configured}

			r := httptest.NewRequest(http.MethodPost, "/v1/client-errors/artifacts", strings.NewReader(""))
			if c.presented != "" {
				r.Header.Set("Authorization", c.presented)
			}

			resp := a.uploadArtifacts(context.Background(), r)

			e, ok := resp.(*errs.Error)
			if !ok {
				t.Fatalf("expected an error response, got %T", resp)
			}
			if got := e.HTTPStatus(); got != c.want {
				t.Errorf("status = %d, want %d (%v)", got, c.want, e.Code)
			}
		})
	}
}

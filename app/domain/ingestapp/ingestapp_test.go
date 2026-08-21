package ingestapp_test

import (
	"bytes"
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
	"github.com/jkarage/logingestor/app/domain/ingestapp"
	"github.com/jkarage/logingestor/app/domain/logapp"
	"github.com/jkarage/logingestor/business/domain/logbus"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/business/domain/rejectbus"
	"github.com/jkarage/logingestor/business/domain/sourcebus"
	"github.com/jkarage/logingestor/business/sdk/ingest"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jkarage/logingestor/foundation/web"
)

// fakeSourceBus resolves a single preset source by key hash.
type fakeSourceBus struct {
	sourcebus.ExtBusiness
	src sourcebus.Source
	err error
}

func (f fakeSourceBus) QueryByKeyHash(context.Context, string) (sourcebus.Source, error) {
	return f.src, f.err
}
func (f fakeSourceBus) TouchLastSeen(context.Context, uuid.UUID, time.Time) error { return nil }

// fakeLogBus captures the NewLog batch handed to BulkCreate.
type fakeLogBus struct {
	logbus.ExtBusiness
	mu      sync.Mutex
	created []logbus.NewLog
}

func (f *fakeLogBus) BulkCreate(_ context.Context, entries []logbus.NewLog) ([]logbus.Log, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, entries...)

	logs := make([]logbus.Log, len(entries))
	for i, e := range entries {
		logs[i] = logbus.Log{
			ID:         uuid.New(),
			ProjectID:  e.ProjectID,
			Level:      e.Level,
			Message:    e.Message,
			Source:     e.Source,
			Timestamp:  e.Timestamp,
			SourceType: e.SourceType,
			SourceID:   e.SourceID,
			Infra:      e.Infra,
			Attributes: e.Attributes,
		}
	}
	return logs, nil
}

// fakeProjectBus reports whether the source's org is enabled; only OrgEnabled
// is exercised by the ingest middleware.
type fakeProjectBus struct {
	projectbus.ExtBusiness
	orgSuspended bool
}

func (f fakeProjectBus) OrgEnabled(context.Context, uuid.UUID) (bool, error) {
	return !f.orgSuspended, nil
}

func newTestServer(t *testing.T, srcBus sourcebus.ExtBusiness, logBus logbus.ExtBusiness) *httptest.Server {
	return newTestServerWithProjects(t, srcBus, logBus, fakeProjectBus{})
}

func newTestServerWithProjects(t *testing.T, srcBus sourcebus.ExtBusiness, logBus logbus.ExtBusiness, projBus projectbus.ExtBusiness) *httptest.Server {
	t.Helper()
	lg := logger.New(io.Discard, logger.LevelError, "TEST", nil)
	app := web.NewApp(lg.Info, nil)
	ingestapp.Routes(app, ingestapp.Config{
		Log:        lg,
		LogBus:     logBus,
		SourceBus:  srcBus,
		ProjectBus: projBus,
		Hub:        logapp.NewHub(),
	})
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	return srv
}

func activeSource() (sourcebus.Source, string) {
	raw, hash, prefix, _ := sourcebus.GenerateKey()
	return sourcebus.Source{
		ID:              uuid.New(),
		OrgID:           uuid.New(),
		ProjectID:       uuid.New(),
		Kind:            "otel",
		Name:            "prod",
		KeyPrefix:       prefix,
		KeyHash:         hash,
		IsActive:        true,
		SampleDebugInfo: 1.0, // keep everything (default for real sources)
	}, raw
}

func Test_bulk_Success_StampsInfra(t *testing.T) {
	src, rawKey := activeSource()
	logBus := &fakeLogBus{}
	srv := newTestServer(t, fakeSourceBus{src: src}, logBus)

	body := `{"message":"disk full","level":"error","host":"node-1","attributes":{"disk":"sda"}}
{"message":"ok","host":"node-2"}`

	resp := doPost(t, srv, "/v1/ingest/bulk", "application/x-ndjson", rawKey, body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 202: %s", resp.StatusCode, b)
	}

	var out ingestapp.BulkResponse
	json.NewDecoder(resp.Body).Decode(&out)
	if out.Accepted != 2 {
		t.Fatalf("accepted = %d, want 2", out.Accepted)
	}

	logBus.mu.Lock()
	defer logBus.mu.Unlock()
	if len(logBus.created) != 2 {
		t.Fatalf("created %d logs, want 2", len(logBus.created))
	}
	for _, nl := range logBus.created {
		if nl.SourceType != logbus.SourceTypeInfra {
			t.Errorf("source_type = %q, want infra", nl.SourceType)
		}
		// Tenant binding: project always comes from the source.
		if nl.ProjectID != src.ProjectID {
			t.Errorf("project = %s, want source's %s", nl.ProjectID, src.ProjectID)
		}
		if nl.SourceID == nil || *nl.SourceID != src.ID {
			t.Errorf("source_id = %v, want %s", nl.SourceID, src.ID)
		}
	}
	if logBus.created[0].Infra.Host != "node-1" {
		t.Errorf("host not mapped: %q", logBus.created[0].Infra.Host)
	}
}

func Test_bulk_Redaction(t *testing.T) {
	src, rawKey := activeSource()
	logBus := &fakeLogBus{}
	srv := newTestServer(t, fakeSourceBus{src: src}, logBus)

	resp := doPost(t, srv, "/v1/ingest/bulk", "application/json",
		rawKey, `[{"message":"login from user@example.com at 10.0.0.5"}]`)
	defer resp.Body.Close()

	logBus.mu.Lock()
	defer logBus.mu.Unlock()
	if len(logBus.created) != 1 {
		t.Fatalf("created %d, want 1", len(logBus.created))
	}
	msg := logBus.created[0].Message
	if strings.Contains(msg, "user@example.com") || strings.Contains(msg, "10.0.0.5") {
		t.Errorf("secrets not redacted: %q", msg)
	}
}

func Test_bulk_InvalidKey_401(t *testing.T) {
	logBus := &fakeLogBus{}
	srv := newTestServer(t, fakeSourceBus{err: sourcebus.ErrNotFound}, logBus)

	resp := doPost(t, srv, "/v1/ingest/bulk", "application/json",
		"ls_src_live_"+strings.Repeat("a", 64), `[{"message":"hi"}]`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func Test_bulk_DisabledKey_403(t *testing.T) {
	src, rawKey := activeSource()
	src.IsActive = false
	logBus := &fakeLogBus{}
	srv := newTestServer(t, fakeSourceBus{src: src}, logBus)

	resp := doPost(t, srv, "/v1/ingest/bulk", "application/json", rawKey, `[{"message":"hi"}]`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func Test_bulk_PartialSuccess(t *testing.T) {
	src, rawKey := activeSource()
	logBus := &fakeLogBus{}
	srv := newTestServer(t, fakeSourceBus{src: src}, logBus)

	// Second record missing message -> rejected; others accepted.
	body := `{"message":"a"}
{"host":"no-message"}
{"message":"c"}`
	resp := doPost(t, srv, "/v1/ingest/bulk", "application/x-ndjson", rawKey, body)
	defer resp.Body.Close()

	var out ingestapp.BulkResponse
	json.NewDecoder(resp.Body).Decode(&out)
	if out.Accepted != 2 || out.Rejected != 1 {
		t.Fatalf("accepted=%d rejected=%d, want 2/1 (%+v)", out.Accepted, out.Rejected, out.Errors)
	}
}

func Test_bulk_RateLimit_429ThenRecover(t *testing.T) {
	src, rawKey := activeSource()
	src.RateLimitPerSec = 2
	src.RateLimitBurst = 2 // bucket holds 2 events
	logBus := &fakeLogBus{}
	srv := newTestServer(t, fakeSourceBus{src: src}, logBus)

	// A 3-record batch exceeds the burst of 2 -> 429 with Retry-After.
	body := `{"message":"a","level":"error"}
{"message":"b","level":"error"}
{"message":"c","level":"error"}`
	resp := doPost(t, srv, "/v1/ingest/bulk", "application/x-ndjson", rawKey, body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", resp.StatusCode)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Error("expected Retry-After header on 429")
	}

	// A single record still fits the (refilled) bucket and succeeds.
	resp2 := doPost(t, srv, "/v1/ingest/bulk", "application/x-ndjson", rawKey, `{"message":"d","level":"error"}`)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusAccepted {
		t.Fatalf("after backoff status = %d, want 202", resp2.StatusCode)
	}
}

func Test_bulk_Sampling_DropsInfo(t *testing.T) {
	src, rawKey := activeSource()
	src.SampleDebugInfo = 0 // drop all DEBUG/INFO; keep WARN/ERROR
	logBus := &fakeLogBus{}
	srv := newTestServer(t, fakeSourceBus{src: src}, logBus)

	body := `{"message":"keep me","level":"error"}
{"message":"drop me","level":"info"}`
	resp := doPost(t, srv, "/v1/ingest/bulk", "application/x-ndjson", rawKey, body)
	defer resp.Body.Close()

	var out ingestapp.BulkResponse
	json.NewDecoder(resp.Body).Decode(&out)
	if out.Accepted != 1 {
		t.Fatalf("accepted = %d, want 1 (info sampled out)", out.Accepted)
	}

	logBus.mu.Lock()
	defer logBus.mu.Unlock()
	if len(logBus.created) != 1 || logBus.created[0].Message != "keep me" {
		t.Fatalf("expected only the ERROR record persisted, got %+v", logBus.created)
	}
}

func doPost(t *testing.T, srv *httptest.Server, path, contentType, key, body string) *http.Response {
	t.Helper()
	return doPostBytes(t, srv, path, contentType, key, []byte(body))
}

func doPostBytes(t *testing.T, srv *httptest.Server, path, contentType, key string, body []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// A deactivated organization must stop accepting logs. Before this gate existed,
// PUT /orgs/{id} {enabled:false} changed a column and nothing else — ingestion
// (and therefore alerting, which is driven by ingestion) carried on.
func Test_bulk_SuspendedOrg_Rejected(t *testing.T) {
	src, rawKey := activeSource()
	logBus := &fakeLogBus{}
	srv := newTestServerWithProjects(t, fakeSourceBus{src: src}, logBus, fakeProjectBus{orgSuspended: true})

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/ingest/bulk",
		strings.NewReader(`{"message":"should not land","level":"error"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+rawKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "org_suspended") {
		t.Errorf("body %q should carry the stable org_suspended code", body)
	}

	if len(logBus.created) != 0 {
		t.Errorf("%d logs were written for a suspended org, want 0", len(logBus.created))
	}
}

// The same source on an enabled org still works, so the gate is not just
// rejecting everything.
func Test_bulk_EnabledOrg_Accepted(t *testing.T) {
	src, rawKey := activeSource()
	logBus := &fakeLogBus{}
	srv := newTestServerWithProjects(t, fakeSourceBus{src: src}, logBus, fakeProjectBus{orgSuspended: false})

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/ingest/bulk",
		strings.NewReader(`{"message":"should land","level":"error"}`))
	req.Header.Set("Authorization", "Bearer "+rawKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		t.Errorf("status = %d, want 2xx", resp.StatusCode)
	}
	if len(logBus.created) == 0 {
		t.Error("no logs written for an enabled org")
	}
}

// An expired ingest key must stop working on its own, with a code distinct from
// revocation so a shipper can tell "rotate me" from "you were turned off".
func Test_bulk_ExpiredKey_Rejected(t *testing.T) {
	src, rawKey := activeSource()
	expired := time.Now().Add(-time.Hour)
	src.ExpiresAt = &expired

	logBus := &fakeLogBus{}
	srv := newTestServer(t, fakeSourceBus{src: src}, logBus)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/ingest/bulk",
		strings.NewReader(`{"message":"stale key","level":"error"}`))
	req.Header.Set("Authorization", "Bearer "+rawKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "key_expired") {
		t.Errorf("body %q should carry the stable key_expired code", body)
	}
	if len(logBus.created) != 0 {
		t.Errorf("%d logs written for an expired key, want 0", len(logBus.created))
	}
}

// A key with a future expiry, and one with none, both still ingest.
func Test_bulk_UnexpiredKeys_Accepted(t *testing.T) {
	future := time.Now().Add(24 * time.Hour)

	for name, exp := range map[string]*time.Time{"no expiry": nil, "future expiry": &future} {
		t.Run(name, func(t *testing.T) {
			src, rawKey := activeSource()
			src.ExpiresAt = exp

			logBus := &fakeLogBus{}
			srv := newTestServer(t, fakeSourceBus{src: src}, logBus)

			req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/ingest/bulk",
				strings.NewReader(`{"message":"ok","level":"error"}`))
			req.Header.Set("Authorization", "Bearer "+rawKey)

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("post: %v", err)
			}
			defer resp.Body.Close()

			if len(logBus.created) == 0 {
				t.Errorf("status %d: no logs written", resp.StatusCode)
			}
		})
	}
}

// fakeRejectStorer records what the ingest path handed the dead-letter store.
type fakeRejectStorer struct {
	mu      sync.Mutex
	stored  []rejectbus.Reject
	counted int
}

func (f *fakeRejectStorer) Store(_ context.Context, rejects []rejectbus.Reject) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.stored = append(f.stored, rejects...)

	return len(rejects), nil
}

func (f *fakeRejectStorer) CountSince(context.Context, uuid.UUID, time.Time) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.counted, nil
}

func (f *fakeRejectStorer) Query(context.Context, rejectbus.Filter) ([]rejectbus.Reject, error) {
	return nil, nil
}

func (f *fakeRejectStorer) CountByKind(context.Context, uuid.UUID, time.Time) (map[string]int64, error) {
	return nil, nil
}

func (f *fakeRejectStorer) records() []rejectbus.Reject {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]rejectbus.Reject(nil), f.stored...)
}

// A refused record reaches the dead-letter store carrying the payload that was
// refused — the number alone was always in the response, and the record is the
// part that explains it.
func Test_bulk_RejectedRecordsAreKept(t *testing.T) {
	src, rawKey := activeSource()
	store := &fakeRejectStorer{}

	lg := logger.New(io.Discard, logger.LevelError, "TEST", nil)
	app := web.NewApp(lg.Info, nil)
	ingestapp.Routes(app, ingestapp.Config{
		Log:        lg,
		LogBus:     &fakeLogBus{},
		SourceBus:  fakeSourceBus{src: src},
		ProjectBus: fakeProjectBus{},
		RejectBus:  rejectbus.NewBusiness(lg, store, ingest.NewRedactor(), 100),
		Hub:        logapp.NewHub(),
	})
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)

	// A line that is not JSON, a line that parses but has no message, and one
	// good line — the two failure kinds and a success in one request.
	body := `{"message":"fine"}
{"message": "unterminated
{"host":"no-message-here","level":"INFO"}`

	resp := doPost(t, srv, "/v1/ingest/bulk", "application/x-ndjson", rawKey, body)
	defer resp.Body.Close()

	var out ingestapp.BulkResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Accepted != 1 || out.Rejected != 2 {
		t.Fatalf("accepted=%d rejected=%d, want 1/2 (%+v)", out.Accepted, out.Rejected, out.Errors)
	}

	// The response must not echo the payloads back: it is the caller's own data
	// and repeating a failing batch doubles traffic that is already failing.
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "no-message-here") {
		t.Errorf("the response echoed a rejected payload: %s", raw)
	}

	// Storing is asynchronous, so give the goroutine a moment.
	var kept []rejectbus.Reject
	for i := 0; i < 50; i++ {
		kept = store.records()
		if len(kept) == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if len(kept) != 2 {
		t.Fatalf("kept %d rejects, want 2", len(kept))
	}

	byKind := map[string]rejectbus.Reject{}
	for _, r := range kept {
		byKind[r.Kind] = r

		if r.SourceID != src.ID || r.OrgID != src.OrgID || r.ProjectID != src.ProjectID {
			t.Errorf("a reject was filed against the wrong tenant: %+v", r)
		}
	}

	parse, ok := byKind[rejectbus.KindParse]
	if !ok {
		t.Fatalf("no parse failure was kept: %+v", kept)
	}
	if !strings.Contains(parse.Payload, "unterminated") {
		t.Errorf("the parse failure lost its payload: %q", parse.Payload)
	}

	validate, ok := byKind[rejectbus.KindValidate]
	if !ok {
		t.Fatalf("no validation failure was kept: %+v", kept)
	}
	if !strings.Contains(validate.Payload, "no-message-here") {
		t.Errorf("the validation failure lost its payload: %q", validate.Payload)
	}
	if validate.Reason == "" {
		t.Errorf("the validation failure lost its reason")
	}
}

// With no store wired the ingest path behaves exactly as it did before the
// dead-letter store existed: counted, not kept.
func Test_bulk_RejectsWithoutAStore(t *testing.T) {
	src, rawKey := activeSource()
	srv := newTestServer(t, fakeSourceBus{src: src}, &fakeLogBus{})

	resp := doPost(t, srv, "/v1/ingest/bulk", "application/x-ndjson", rawKey, `{"host":"no-message"}`)
	defer resp.Body.Close()

	var out ingestapp.BulkResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Rejected != 1 {
		t.Errorf("rejected = %d, want 1", out.Rejected)
	}
}

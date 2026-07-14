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
	"github.com/jkarage/logingestor/business/domain/sourcebus"
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

func newTestServer(t *testing.T, srcBus sourcebus.ExtBusiness, logBus logbus.ExtBusiness) *httptest.Server {
	t.Helper()
	lg := logger.New(io.Discard, logger.LevelError, "TEST", nil)
	app := web.NewApp(lg.Info, nil)
	ingestapp.Routes(app, ingestapp.Config{
		Log:       lg,
		LogBus:    logBus,
		SourceBus: srcBus,
		Hub:       logapp.NewHub(),
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

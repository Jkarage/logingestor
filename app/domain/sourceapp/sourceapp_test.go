package sourceapp

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/business/domain/sourcebus"
	"github.com/jkarage/logingestor/business/domain/usagebus"
)

// fakeProjectBus embeds the interface so only QueryByID needs implementing;
// any other method would panic (and the tests never call them).
type fakeProjectBus struct {
	projectbus.ExtBusiness
	project projectbus.Project
	err     error
}

func (f fakeProjectBus) QueryByID(context.Context, uuid.UUID) (projectbus.Project, error) {
	return f.project, f.err
}

type fakeSourceBus struct {
	sourcebus.ExtBusiness
	created   sourcebus.Source
	rawKey    string
	createErr error
	byID      sourcebus.Source
	byIDErr   error
	byOrg     []sourcebus.Source
}

func (f fakeSourceBus) Create(context.Context, uuid.UUID, sourcebus.NewSource) (sourcebus.Source, string, error) {
	return f.created, f.rawKey, f.createErr
}
func (f fakeSourceBus) QueryByID(context.Context, uuid.UUID) (sourcebus.Source, error) {
	return f.byID, f.byIDErr
}
func (f fakeSourceBus) Disable(_ context.Context, _ uuid.UUID, s sourcebus.Source) (sourcebus.Source, error) {
	return s, nil
}
func (f fakeSourceBus) QueryByOrg(context.Context, uuid.UUID) ([]sourcebus.Source, error) {
	return f.byOrg, nil
}

type fakeUsageBus struct {
	usagebus.ExtBusiness
	counters map[uuid.UUID]usagebus.SourceCounters
	buckets  []usagebus.HourCounters
}

func (f fakeUsageBus) QuerySourceCounters(context.Context, []uuid.UUID, time.Time) (map[uuid.UUID]usagebus.SourceCounters, error) {
	return f.counters, nil
}
func (f fakeUsageBus) QuerySourceBuckets(context.Context, uuid.UUID, time.Time, time.Time) ([]usagebus.HourCounters, error) {
	return f.buckets, nil
}

func Test_create_Success(t *testing.T) {
	orgID := uuid.New()
	projectID := uuid.New()

	a := newApp(
		fakeSourceBus{
			created: sourcebus.Source{ID: uuid.New(), OrgID: orgID, ProjectID: projectID, Kind: "otel", Name: "prod", KeyPrefix: "ls_src_live_abc123", IsActive: true},
			rawKey:  "ls_src_live_" + strings.Repeat("a", 64),
		},
		fakeProjectBus{project: projectbus.Project{ID: projectID, OrgID: orgID}},
		nil,
	)

	r := httptest.NewRequest("POST", "/v1/orgs/"+orgID.String()+"/sources",
		strings.NewReader(`{"kind":"otel","name":"prod","projectId":"`+projectID.String()+`"}`))
	r.SetPathValue("org_id", orgID.String())

	resp := a.create(context.Background(), r)

	created, ok := resp.(SourceCreated)
	if !ok {
		t.Fatalf("expected SourceCreated, got %T: %v", resp, resp)
	}
	if created.HTTPStatus() != 201 {
		t.Errorf("status = %d, want 201", created.HTTPStatus())
	}
	if !sourcebus.HasKeyScheme(created.IngestKey) {
		t.Errorf("ingestKey %q missing scheme", created.IngestKey)
	}
}

func Test_create_ProjectInDifferentOrg_404(t *testing.T) {
	orgID := uuid.New()
	projectID := uuid.New()
	otherOrg := uuid.New()

	a := newApp(
		fakeSourceBus{},
		fakeProjectBus{project: projectbus.Project{ID: projectID, OrgID: otherOrg}}, // different org
		nil,
	)

	r := httptest.NewRequest("POST", "/v1/orgs/"+orgID.String()+"/sources",
		strings.NewReader(`{"kind":"otel","name":"prod","projectId":"`+projectID.String()+`"}`))
	r.SetPathValue("org_id", orgID.String())

	assertErrCode(t, a.create(context.Background(), r), errs.NotFound)
}

func Test_create_BadKind_400(t *testing.T) {
	orgID := uuid.New()
	projectID := uuid.New()

	a := newApp(fakeSourceBus{}, fakeProjectBus{project: projectbus.Project{ID: projectID, OrgID: orgID}}, nil)

	r := httptest.NewRequest("POST", "/v1/orgs/"+orgID.String()+"/sources",
		strings.NewReader(`{"kind":"kafka","name":"prod","projectId":"`+projectID.String()+`"}`))
	r.SetPathValue("org_id", orgID.String())

	assertErrCode(t, a.create(context.Background(), r), errs.InvalidArgument)
}

func Test_disconnect_CrossOrg_404(t *testing.T) {
	orgID := uuid.New()
	otherOrg := uuid.New()
	sourceID := uuid.New()

	a := newApp(
		fakeSourceBus{byID: sourcebus.Source{ID: sourceID, OrgID: otherOrg}}, // belongs to another org
		fakeProjectBus{},
		nil,
	)

	r := httptest.NewRequest("DELETE", "/v1/orgs/"+orgID.String()+"/sources/"+sourceID.String(), nil)
	r.SetPathValue("org_id", orgID.String())
	r.SetPathValue("source_id", sourceID.String())

	assertErrCode(t, a.disconnect(context.Background(), r), errs.NotFound)
}

func assertErrCode(t *testing.T, resp any, want errs.ErrCode) {
	t.Helper()
	e, ok := resp.(*errs.Error)
	if !ok {
		t.Fatalf("expected *errs.Error, got %T: %v", resp, resp)
	}
	if e.Code != want {
		t.Fatalf("error code = %v, want %v", e.Code, want)
	}
}

// The list carries health per row, derived from one counters lookup. A source
// with no ingest in the window is absent from the counters map and must read as
// silent rather than as missing data.
func Test_query_AttachesHealth(t *testing.T) {
	orgID := uuid.New()
	seen := time.Now().UTC().Add(-time.Minute)

	busy := sourcebus.Source{ID: uuid.New(), OrgID: orgID, IsActive: true, LastSeenAt: &seen}
	angry := sourcebus.Source{ID: uuid.New(), OrgID: orgID, IsActive: true, LastSeenAt: &seen}
	quiet := sourcebus.Source{ID: uuid.New(), OrgID: orgID, IsActive: true, LastSeenAt: &seen}

	a := newApp(
		fakeSourceBus{byOrg: []sourcebus.Source{busy, angry, quiet}},
		fakeProjectBus{},
		fakeUsageBus{counters: map[uuid.UUID]usagebus.SourceCounters{
			busy.ID:  {Events: 1000, Errors: 2, Dropped: 5},
			angry.ID: {Events: 100, Errors: 40},
		}},
	)

	r := httptest.NewRequest("GET", "/v1/orgs/"+orgID.String()+"/sources", nil)
	r.SetPathValue("org_id", orgID.String())

	resp, ok := a.query(context.Background(), r).(Sources)
	if !ok {
		t.Fatalf("expected Sources, got %T", resp)
	}
	if len(resp.Sources) != 3 {
		t.Fatalf("got %d sources, want 3", len(resp.Sources))
	}

	want := []struct {
		status string
		events int64
		errors int64
	}{
		{string(sourcebus.HealthHealthy), 1000, 2},
		{string(sourcebus.HealthDegraded), 100, 40},
		{string(sourcebus.HealthSilent), 0, 0},
	}

	for i, w := range want {
		got := resp.Sources[i]
		if got.HealthStatus != w.status {
			t.Errorf("sources[%d].healthStatus = %q, want %q", i, got.HealthStatus, w.status)
		}
		if got.Events24h != w.events || got.Errors24h != w.errors {
			t.Errorf("sources[%d] counters = %d/%d, want %d/%d", i, got.Events24h, got.Errors24h, w.events, w.errors)
		}
	}

	if rate := resp.Sources[1].ErrorRate24h; rate != 0.4 {
		t.Errorf("errorRate24h = %v, want 0.4", rate)
	}
	if resp.Sources[0].Dropped24h != 5 {
		t.Errorf("dropped24h = %d, want 5", resp.Sources[0].Dropped24h)
	}
}

// The health detail always returns the full hourly grid, so a client plots a
// fixed-width series and a gap in ingest reads as a gap.
func Test_health_ZeroFillsTheWindow(t *testing.T) {
	orgID := uuid.New()
	sourceID := uuid.New()
	seen := time.Now().UTC().Add(-time.Minute)
	hour := time.Now().UTC().Truncate(time.Hour)

	a := newApp(
		fakeSourceBus{byID: sourcebus.Source{ID: sourceID, OrgID: orgID, IsActive: true, LastSeenAt: &seen}},
		fakeProjectBus{},
		fakeUsageBus{
			counters: map[uuid.UUID]usagebus.SourceCounters{sourceID: {Events: 30, Errors: 1}},
			buckets:  []usagebus.HourCounters{{Hour: hour, Events: 30, Errors: 1}},
		},
	)

	r := httptest.NewRequest("GET", "/v1/orgs/"+orgID.String()+"/sources/"+sourceID.String()+"/health", nil)
	r.SetPathValue("org_id", orgID.String())
	r.SetPathValue("source_id", sourceID.String())

	resp, ok := a.health(context.Background(), r).(SourceHealth)
	if !ok {
		t.Fatalf("expected SourceHealth, got %T", resp)
	}

	if len(resp.Buckets) != 24 {
		t.Fatalf("got %d buckets, want 24", len(resp.Buckets))
	}
	if resp.Status != string(sourcebus.HealthHealthy) {
		t.Errorf("status = %q, want healthy", resp.Status)
	}
	if resp.Events24h != 30 || resp.Errors24h != 1 {
		t.Errorf("counters = %d/%d, want 30/1", resp.Events24h, resp.Errors24h)
	}

	last := resp.Buckets[len(resp.Buckets)-1]
	if last.Hour != hour.Format(time.RFC3339) {
		t.Errorf("last bucket hour = %q, want %q", last.Hour, hour.Format(time.RFC3339))
	}
	if last.Events != 30 {
		t.Errorf("last bucket events = %d, want 30", last.Events)
	}
	if resp.Buckets[0].Events != 0 {
		t.Errorf("first bucket events = %d, want 0", resp.Buckets[0].Events)
	}
}

// A source from another org must not be readable through the health route.
func Test_health_CrossOrg_404(t *testing.T) {
	orgID := uuid.New()
	sourceID := uuid.New()

	a := newApp(
		fakeSourceBus{byID: sourcebus.Source{ID: sourceID, OrgID: uuid.New()}},
		fakeProjectBus{},
		fakeUsageBus{},
	)

	r := httptest.NewRequest("GET", "/v1/orgs/"+orgID.String()+"/sources/"+sourceID.String()+"/health", nil)
	r.SetPathValue("org_id", orgID.String())
	r.SetPathValue("source_id", sourceID.String())

	assertErrCode(t, a.health(context.Background(), r), errs.NotFound)
}

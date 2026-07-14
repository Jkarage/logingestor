package sourceapp

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/business/domain/sourcebus"
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

func Test_create_Success(t *testing.T) {
	orgID := uuid.New()
	projectID := uuid.New()

	a := newApp(
		fakeSourceBus{
			created: sourcebus.Source{ID: uuid.New(), OrgID: orgID, ProjectID: projectID, Kind: "otel", Name: "prod", KeyPrefix: "ls_src_live_abc123", IsActive: true},
			rawKey:  "ls_src_live_" + strings.Repeat("a", 64),
		},
		fakeProjectBus{project: projectbus.Project{ID: projectID, OrgID: orgID}},
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
	)

	r := httptest.NewRequest("POST", "/v1/orgs/"+orgID.String()+"/sources",
		strings.NewReader(`{"kind":"otel","name":"prod","projectId":"`+projectID.String()+`"}`))
	r.SetPathValue("org_id", orgID.String())

	assertErrCode(t, a.create(context.Background(), r), errs.NotFound)
}

func Test_create_BadKind_400(t *testing.T) {
	orgID := uuid.New()
	projectID := uuid.New()

	a := newApp(fakeSourceBus{}, fakeProjectBus{project: projectbus.Project{ID: projectID, OrgID: orgID}})

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

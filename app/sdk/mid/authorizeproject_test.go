package mid

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/auth"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/business/types/role"
	"github.com/jkarage/logingestor/foundation/web"
)

var testOrgID = uuid.New()

// fakeProjectBus embeds the interface; only HasAccess and QueryByID are exercised.
type fakeProjectBus struct {
	projectbus.ExtBusiness
	access bool
}

func (f fakeProjectBus) HasAccess(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return f.access, nil
}

func (f fakeProjectBus) QueryByID(_ context.Context, projectID uuid.UUID) (projectbus.Project, error) {
	return projectbus.Project{ID: projectID, OrgID: testOrgID}, nil
}

// fakeOrgBus embeds the interface; only QueryByUserID is exercised.
type fakeOrgBus struct {
	orgbus.ExtBusiness
	member bool
}

func (f fakeOrgBus) QueryByUserID(context.Context, uuid.UUID) ([]orgbus.UserOrg, error) {
	if !f.member {
		return nil, nil
	}
	return []orgbus.UserOrg{{Org: orgbus.Org{ID: testOrgID}}}, nil
}

// allowed is a sentinel next-handler return so tests can tell pass from block.
type allowed struct{}

func (allowed) Encode() ([]byte, string, error) { return []byte("ok"), "text/plain", nil }

func runManage(t *testing.T, roles []string, access bool, orgMember bool) web.Encoder {
	t.Helper()

	mw := AuthorizeProjectManage(fakeProjectBus{access: access}, fakeOrgBus{member: orgMember})
	h := mw(func(ctx context.Context, r *http.Request) web.Encoder { return allowed{} })

	r := httptest.NewRequest("POST", "/x", nil)
	r.SetPathValue("project_id", uuid.NewString())

	ctx := setUserID(context.Background(), uuid.New())
	ctx = setClaims(ctx, auth.Claims{Roles: roles})

	return h(ctx, r)
}

func isAllowed(resp web.Encoder) bool {
	_, ok := resp.(allowed)
	return ok
}

func Test_AuthorizeProjectManage(t *testing.T) {
	cases := []struct {
		name      string
		roles     []string
		access    bool
		orgMember bool
		want      bool
	}{
		{"super admin", []string{role.Admin.String()}, false, false, true},
		{"org admin in project's org", []string{role.OrgAdmin.String()}, false, true, true},
		{"org admin of another org", []string{role.OrgAdmin.String()}, false, false, false},
		{"project manager with access", []string{role.PrjManager.String()}, true, false, true},
		{"project manager without access", []string{role.PrjManager.String()}, false, false, false},
		{"viewer with access", []string{role.User.String()}, true, false, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := runManage(t, c.roles, c.access, c.orgMember)
			if got := isAllowed(resp); got != c.want {
				t.Fatalf("allowed=%v, want %v (resp %T)", got, c.want, resp)
			}
			if !c.want {
				if e, ok := resp.(*errs.Error); !ok || e.Code != errs.PermissionDenied {
					t.Fatalf("expected PermissionDenied, got %T %v", resp, resp)
				}
			}
		})
	}
}

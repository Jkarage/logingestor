package mid

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/auth"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/business/types/role"
	"github.com/jkarage/logingestor/foundation/web"
)

// fakeProjectBus embeds the interface; only HasAccess is exercised.
type fakeProjectBus struct {
	projectbus.ExtBusiness
	access bool
}

func (f fakeProjectBus) HasAccess(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return f.access, nil
}

// allowed is a sentinel next-handler return so tests can tell pass from block.
type allowed struct{}

func (allowed) Encode() ([]byte, string, error) { return []byte("ok"), "text/plain", nil }

func runManage(t *testing.T, roles []string, access bool) web.Encoder {
	t.Helper()

	mw := AuthorizeProjectManage(fakeProjectBus{access: access})
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
		name   string
		roles  []string
		access bool
		want   bool
	}{
		{"super admin", []string{role.Admin.String()}, false, true},
		{"org admin", []string{role.OrgAdmin.String()}, false, true},
		{"project manager with access", []string{role.PrjManager.String()}, true, true},
		{"project manager without access", []string{role.PrjManager.String()}, false, false},
		{"viewer with access", []string{role.User.String()}, true, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := runManage(t, c.roles, c.access)
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

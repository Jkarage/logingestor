package mid

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/auth"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/business/types/role"
	"github.com/jkarage/logingestor/foundation/web"
)

func runOrgAdmin(t *testing.T, orgID uuid.UUID, globalRoles []string, org fakeOrgBus) web.Encoder {
	t.Helper()

	mw := AuthorizeOrgAdmin(org)
	h := mw(func(ctx context.Context, r *http.Request) web.Encoder { return allowed{} })

	r := httptest.NewRequest("POST", "/x", nil)
	r.SetPathValue("org_id", orgID.String())

	ctx := setUserID(context.Background(), uuid.New())
	ctx = setClaims(ctx, auth.Claims{Roles: globalRoles})

	return h(ctx, r)
}

func Test_AuthorizeOrgAdmin(t *testing.T) {
	otherOrgID := uuid.New()

	cases := []struct {
		name        string
		orgID       uuid.UUID
		globalRoles []string
		org         fakeOrgBus
		want        bool
	}{
		// The reported bug: a user invited as ORG ADMIN carries the default
		// VIEWER global role in their JWT. Authority must come from the
		// membership row for {org_id}, so this caller is an admin here.
		{
			"invited org admin with VIEWER jwt role",
			testOrgID,
			[]string{role.User.String()},
			fakeOrgBus{membershipRole: role.OrgAdmin},
			true,
		},
		// Membership role is what counts, so it works with no roles claim too.
		{
			"org admin membership, no jwt roles",
			testOrgID,
			nil,
			fakeOrgBus{membershipRole: role.OrgAdmin},
			true,
		},
		// A global ORG ADMIN claim must not grant admin in an org where the
		// caller's membership says VIEWER.
		{
			"viewer membership despite ORG ADMIN jwt role",
			testOrgID,
			[]string{role.OrgAdmin.String()},
			fakeOrgBus{membershipRole: role.User},
			false,
		},
		// Nor in an org the caller does not belong to at all.
		{
			"non-member with ORG ADMIN jwt role",
			otherOrgID,
			[]string{role.OrgAdmin.String()},
			fakeOrgBus{notMember: true},
			false,
		},
		{
			"admin of one org acting on another org",
			otherOrgID,
			[]string{role.User.String()},
			fakeOrgBus{membershipRole: role.OrgAdmin},
			false,
		},
		// Super admins keep system-wide access.
		{
			"super admin, not a member",
			otherOrgID,
			[]string{role.Admin.String()},
			fakeOrgBus{notMember: true},
			true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := runOrgAdmin(t, c.orgID, c.globalRoles, c.org)
			if got := isAllowed(resp); got != c.want {
				t.Fatalf("allowed=%v, want %v (resp %T)", got, c.want, resp)
			}

			// A role denial must be 403, never 401 — the frontend logs the user
			// out on 401.
			if !c.want {
				e, ok := resp.(*errs.Error)
				if !ok || e.Code != errs.PermissionDenied {
					t.Fatalf("expected PermissionDenied, got %T %v", resp, resp)
				}
				if e.HTTPStatus() != http.StatusForbidden {
					t.Fatalf("expected HTTP 403, got %d", e.HTTPStatus())
				}
			}
		})
	}
}

// Package orgapp maintains the app layer api for the organization domain.
package orgapp

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/auth"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/app/sdk/query"
	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/business/sdk/order"
	"github.com/jkarage/logingestor/business/sdk/page"
	"github.com/jkarage/logingestor/foundation/web"
)

type app struct {
	orgBus orgbus.ExtBusiness
	auth   *auth.Auth
}

func newApp(orgBus orgbus.ExtBusiness, auth *auth.Auth) *app {
	return &app{
		orgBus: orgBus,
		auth:   auth,
	}
}

func (a *app) create(ctx context.Context, r *http.Request) web.Encoder {
	var nu NewOrg
	if err := web.Decode(r, &nu); err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	busNew, err := toBusNewOrg(nu)
	if err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	org, err := a.orgBus.Create(ctx, mid.GetSubjectID(ctx), busNew)
	if err != nil {
		if errors.Is(err, orgbus.ErrUniqueSlug) {
			return errs.New(errs.Aborted, orgbus.ErrUniqueSlug)
		}

		return errs.Errorf(errs.Internal, "create: %s", err)
	}

	return toAppOrg(org)
}

func (a *app) update(ctx context.Context, r *http.Request) web.Encoder {
	var uu UpdateOrg
	if err := web.Decode(r, &uu); err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	busUpdate, err := toBusUpdateOrg(uu)
	if err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	org, err := a.orgBus.QueryByID(ctx, orgID)
	if err != nil {
		if errors.Is(err, orgbus.ErrNotFound) {
			return errs.New(errs.NotFound, err)
		}
		return errs.Errorf(errs.Internal, "querybyid: orgID[%s]: %s", orgID, err)
	}

	updated, err := a.orgBus.Update(ctx, mid.GetSubjectID(ctx), org, busUpdate)
	if err != nil {
		return errs.Errorf(errs.Internal, "update: orgID[%s]: %s", orgID, err)
	}

	return toAppOrg(updated)
}

func (a *app) updateRole(ctx context.Context, r *http.Request) web.Encoder {
	var ur UpdateOrgRole
	if err := web.Decode(r, &ur); err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	busRole, err := toBusUpdateOrgRole(ur)
	if err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	memberID, err := uuid.Parse(ur.MemberID)
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	member, err := a.orgBus.UpdateMemberRole(ctx, mid.GetSubjectID(ctx), memberID, busRole.Role)
	if err != nil {
		if errors.Is(err, orgbus.ErrMemberNotFound) {
			return errs.New(errs.NotFound, err)
		}
		return errs.Errorf(errs.Internal, "updatememberrole: memberID[%s]: %s", memberID, err)
	}

	_ = member
	return nil
}

func (a *app) removeMember(ctx context.Context, r *http.Request) web.Encoder {
	memberID, err := uuid.Parse(web.Param(r, "member_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	if err := a.orgBus.RemoveMember(ctx, mid.GetSubjectID(ctx), memberID); err != nil {
		if errors.Is(err, orgbus.ErrMemberNotFound) {
			return errs.New(errs.NotFound, err)
		}
		return errs.Errorf(errs.Internal, "removemember: memberID[%s]: %s", memberID, err)
	}

	return nil
}

func (a *app) delete(ctx context.Context, r *http.Request) web.Encoder {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	org, err := a.orgBus.QueryByID(ctx, orgID)
	if err != nil {
		if errors.Is(err, orgbus.ErrNotFound) {
			return errs.New(errs.NotFound, err)
		}
		return errs.Errorf(errs.Internal, "querybyid: orgID[%s]: %s", orgID, err)
	}

	if err := a.orgBus.Delete(ctx, mid.GetSubjectID(ctx), org); err != nil {
		return errs.Errorf(errs.Internal, "delete: orgID[%s]: %s", orgID, err)
	}

	return nil
}

func (a *app) query(ctx context.Context, r *http.Request) web.Encoder {
	qp, err := parseQueryParams(r)
	if err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	pg, err := page.Parse(qp.Page, qp.Rows)
	if err != nil {
		return errs.NewFieldErrors("page", err)
	}

	filter, err := parseFilter(qp)
	if err != nil {
		return err.(*errs.Error)
	}

	orderBy, err := order.Parse(orderByFields, qp.OrderBy, orgbus.DefaultOrderBy)
	if err != nil {
		return errs.NewFieldErrors("order", err)
	}

	orgs, err := a.orgBus.Query(ctx, filter, orderBy, pg)
	if err != nil {
		return errs.Errorf(errs.Internal, "query: %s", err)
	}

	total, err := a.orgBus.Count(ctx, filter)
	if err != nil {
		return errs.Errorf(errs.Internal, "count: %s", err)
	}

	return query.NewResult(toAppOrgs(orgs), total, pg)
}

func (a *app) queryOrgMembers(ctx context.Context, r *http.Request) web.Encoder {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	members, err := a.orgBus.QueryMembersWithUsers(ctx, orgID)
	if err != nil {
		if errors.Is(err, orgbus.ErrNotFound) {
			return errs.New(errs.NotFound, err)
		}
		return errs.Errorf(errs.Internal, "querymemberswithusers: orgID[%s]: %s", orgID, err)
	}

	return toAppOrgMembers(members)
}

// queryMine returns all orgs the authenticated user is a member of, including
// their role in each one. The frontend calls this after login to populate the
// workspace switcher.
func (a *app) queryMine(ctx context.Context, _ *http.Request) web.Encoder {
	userID := mid.GetSubjectID(ctx)

	orgs, err := a.orgBus.QueryByUserID(ctx, userID)
	if err != nil {
		return errs.Errorf(errs.Internal, "querybyuserid: userID[%s]: %s", userID, err)
	}

	return toAppUserOrgs(orgs)
}

func (a *app) queryByID(ctx context.Context, r *http.Request) web.Encoder {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	org, err := a.orgBus.QueryByID(ctx, orgID)
	if err != nil {
		if errors.Is(err, orgbus.ErrNotFound) {
			return errs.New(errs.NotFound, err)
		}
		return errs.Errorf(errs.Internal, "querybyid: orgID[%s]: %s", orgID, err)
	}

	return toAppOrg(org)
}

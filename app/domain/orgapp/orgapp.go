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
	"github.com/jkarage/logingestor/business/types/role"
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
		switch {
		case errors.Is(err, orgbus.ErrUniqueSlug):
			return errs.New(errs.SlugTaken, orgbus.ErrUniqueSlug)
		case errors.Is(err, orgbus.ErrOrgLimitReached):
			return errs.New(errs.OrgLimitReached, orgbus.ErrOrgLimitReached)
		default:
			return errs.Errorf(errs.Internal, "create: %s", err)
		}
	}

	// The creator is always seeded as ORG ADMIN by orgbus.Create.
	appOrg := toAppOrg(org)
	appOrg.Role = role.OrgAdmin.String()

	return appOrg
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

	current, errResp := a.loadOrgMember(ctx, r, memberID)
	if errResp != nil {
		return errResp
	}

	if errResp := a.guardMemberChange(ctx, current, busRole.Role == role.OrgAdmin); errResp != nil {
		return errResp
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

	member, errResp := a.loadOrgMember(ctx, r, memberID)
	if errResp != nil {
		return errResp
	}

	if errResp := a.guardMemberChange(ctx, member, false); errResp != nil {
		return errResp
	}

	if err := a.orgBus.RemoveMember(ctx, mid.GetSubjectID(ctx), memberID); err != nil {
		if errors.Is(err, orgbus.ErrMemberNotFound) {
			return errs.New(errs.NotFound, err)
		}
		return errs.Errorf(errs.Internal, "removemember: memberID[%s]: %s", memberID, err)
	}

	return nil
}

// loadOrgMember loads memberID and confirms it belongs to the org named by the
// {org_id} path param. The org-admin middleware only proves the caller
// administers THAT org, so without this an admin of one org could act on
// membership rows of another. Returns a non-nil web.Encoder on failure.
func (a *app) loadOrgMember(ctx context.Context, r *http.Request, memberID uuid.UUID) (orgbus.OrgMember, web.Encoder) {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return orgbus.OrgMember{}, errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	member, err := a.orgBus.QueryMemberByID(ctx, memberID)
	if err != nil {
		if errors.Is(err, orgbus.ErrMemberNotFound) {
			return orgbus.OrgMember{}, errs.New(errs.NotFound, err)
		}
		return orgbus.OrgMember{}, errs.Errorf(errs.Internal, "querymemberbyid: memberID[%s]: %s", memberID, err)
	}

	if member.OrgID != orgID {
		return orgbus.OrgMember{}, errs.New(errs.NotFound, orgbus.ErrMemberNotFound)
	}

	return member, nil
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

	// Deleting an org cascades to its projects, members, sources, rules and
	// logs, so require the caller to echo the slug back. ?force=true is an
	// additional acknowledgement needed only when other admins would lose access.
	q := r.URL.Query()

	if q.Get("confirm") != org.Slug {
		return errs.Errorf(errs.ConfirmationRequired,
			"pass ?confirm=%s to delete this organization and all of its data", org.Slug)
	}

	admins, errResp := a.orgAdminCount(ctx, orgID)
	if errResp != nil {
		return errResp
	}

	if admins > 1 && q.Get("force") != "true" {
		return errs.Errorf(errs.OtherAdminsExist,
			"%d other admins would lose access; retry with &force=true to confirm", admins-1)
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

	return toAppUserOrgs(orgs, userID)
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

// orgAdminCount returns how many of the org's members hold ORG ADMIN.
// Membership lists are small, so this counts in Go rather than adding a
// dedicated aggregate to the business layer.
func (a *app) orgAdminCount(ctx context.Context, orgID uuid.UUID) (int, web.Encoder) {
	members, err := a.orgBus.QueryMembers(ctx, orgID)
	if err != nil {
		return 0, errs.Errorf(errs.Internal, "querymembers: orgID[%s]: %s", orgID, err)
	}

	var n int
	for _, m := range members {
		if m.Role == role.OrgAdmin {
			n++
		}
	}

	return n, nil
}

// guardMemberChange blocks membership edits that would orphan an organization:
// removing or demoting its owner, or leaving it with no admin at all. This also
// covers "the sole admin tries to leave", which arrives as a self-removal.
// staysAdmin is true when the caller is assigning ORG ADMIN, which can never
// reduce the admin count.
func (a *app) guardMemberChange(ctx context.Context, member orgbus.OrgMember, staysAdmin bool) web.Encoder {
	org, err := a.orgBus.QueryByID(ctx, member.OrgID)
	if err != nil {
		if errors.Is(err, orgbus.ErrNotFound) {
			return errs.New(errs.NotFound, err)
		}
		return errs.Errorf(errs.Internal, "querybyid: orgID[%s]: %s", member.OrgID, err)
	}

	// The owner is the only principal who may delete the org, so an org whose
	// owner is not an admin member can never be wound down.
	if org.CreatedBy != nil && *org.CreatedBy == member.UserID {
		return errs.New(errs.CannotModifyOwner,
			errors.New("the organization owner cannot be removed or demoted; delete or transfer the organization instead"))
	}

	if staysAdmin || member.Role != role.OrgAdmin {
		return nil
	}

	admins, errResp := a.orgAdminCount(ctx, member.OrgID)
	if errResp != nil {
		return errResp
	}

	if admins <= 1 {
		return errs.New(errs.LastOrgAdmin,
			errors.New("this is the only admin; promote another member first, or deactivate or delete the organization"))
	}

	return nil
}

package mid

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/business/types/role"
	"github.com/jkarage/logingestor/foundation/web"
)

// hasRole reports whether the caller's claims include role r.
func hasRole(ctx context.Context, r role.Role) bool {
	for _, cr := range GetClaims(ctx).Roles {
		if cr == r.String() {
			return true
		}
	}
	return false
}

// AuthorizeProjectAccess gates read access to a {project_id} route: the caller
// must be a super admin, an org admin who is a member of the project's org, or
// have explicit access to the project (any role, including viewer). Used for
// the list/read endpoints.
func AuthorizeProjectAccess(projectBus projectbus.ExtBusiness, orgBus orgbus.ExtBusiness) web.MidFunc {
	m := func(next web.HandlerFunc) web.HandlerFunc {
		h := func(ctx context.Context, r *http.Request) web.Encoder {
			if _, resp := requireProjectAccess(ctx, r, projectBus, orgBus); resp != nil {
				return resp
			}
			return next(ctx, r)
		}
		return h
	}
	return m
}

// AuthorizeProjectManage gates write access to a {project_id} route: super
// admins may manage any project; org admins may manage projects in their own
// org; a project manager may manage a project they have access to. Viewers
// (and members without manage roles) are rejected — the team model is
// view-for-all, manage-for-managers.
func AuthorizeProjectManage(projectBus projectbus.ExtBusiness, orgBus orgbus.ExtBusiness) web.MidFunc {
	m := func(next web.HandlerFunc) web.HandlerFunc {
		h := func(ctx context.Context, r *http.Request) web.Encoder {
			projectID, err := uuid.Parse(web.Param(r, "project_id"))
			if err != nil {
				return errs.New(errs.InvalidArgument, ErrInvalidID)
			}

			if hasRole(ctx, role.Admin) {
				return next(ctx, r)
			}

			// Org admins manage only projects belonging to an org they are a
			// member of — the role alone must never grant cross-org control.
			if hasRole(ctx, role.OrgAdmin) {
				ok, resp := isProjectOrgMember(ctx, projectBus, orgBus, projectID)
				if resp != nil {
					return resp
				}
				if ok {
					return next(ctx, r)
				}
			}

			// Project managers need explicit access to this project.
			if hasRole(ctx, role.PrjManager) {
				if _, resp := requireProjectAccess(ctx, r, projectBus, orgBus); resp != nil {
					return resp
				}
				return next(ctx, r)
			}

			return errs.New(errs.PermissionDenied, errors.New("insufficient rights to manage this project"))
		}
		return h
	}
	return m
}

// requireProjectAccess parses {project_id} and confirms the caller can access
// it (super admin, org admin within the project's org, or explicit
// projectBus.HasAccess). Returns a non-nil web.Encoder error response on
// failure.
func requireProjectAccess(ctx context.Context, r *http.Request, projectBus projectbus.ExtBusiness, orgBus orgbus.ExtBusiness) (uuid.UUID, web.Encoder) {
	projectID, err := uuid.Parse(web.Param(r, "project_id"))
	if err != nil {
		return uuid.UUID{}, errs.New(errs.InvalidArgument, ErrInvalidID)
	}

	if hasRole(ctx, role.Admin) {
		return projectID, nil
	}

	if hasRole(ctx, role.OrgAdmin) {
		ok, resp := isProjectOrgMember(ctx, projectBus, orgBus, projectID)
		if resp != nil {
			return uuid.UUID{}, resp
		}
		if ok {
			return projectID, nil
		}
		// Not a member of the project's org: fall through to the explicit
		// per-project access check like any other user.
	}

	userID, err := GetUserID(ctx)
	if err != nil {
		return uuid.UUID{}, errs.New(errs.Unauthenticated, err)
	}

	ok, err := projectBus.HasAccess(ctx, userID, projectID)
	if err != nil {
		return uuid.UUID{}, errs.Errorf(errs.Internal, "hasaccess: projectID[%s]: %s", projectID, err)
	}
	if !ok {
		return uuid.UUID{}, errs.New(errs.PermissionDenied, errors.New("no access to this project"))
	}

	return projectID, nil
}

// isProjectOrgMember reports whether the caller belongs to the org that owns
// projectID. The second return value is a non-nil error response when the
// lookup itself fails.
func isProjectOrgMember(ctx context.Context, projectBus projectbus.ExtBusiness, orgBus orgbus.ExtBusiness, projectID uuid.UUID) (bool, web.Encoder) {
	project, err := projectBus.QueryByID(ctx, projectID)
	if err != nil {
		if errors.Is(err, projectbus.ErrNotFound) {
			return false, errs.New(errs.NotFound, err)
		}
		return false, errs.Errorf(errs.Internal, "querybyid: projectID[%s]: %s", projectID, err)
	}

	userID, err := GetUserID(ctx)
	if err != nil {
		return false, errs.New(errs.Unauthenticated, err)
	}

	userOrgs, err := orgBus.QueryByUserID(ctx, userID)
	if err != nil {
		return false, errs.Errorf(errs.Internal, "querybyuserid: userID[%s]: %s", userID, err)
	}

	for _, uo := range userOrgs {
		if uo.ID == project.OrgID {
			return true, nil
		}
	}

	return false, nil
}

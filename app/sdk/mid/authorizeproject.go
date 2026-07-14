package mid

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/errs"
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
// must be a super/org admin or have explicit access to the project (any role,
// including viewer). Used for the list/read endpoints.
func AuthorizeProjectAccess(projectBus projectbus.ExtBusiness) web.MidFunc {
	m := func(next web.HandlerFunc) web.HandlerFunc {
		h := func(ctx context.Context, r *http.Request) web.Encoder {
			projectID, resp := requireProjectAccess(ctx, r, projectBus)
			if resp != nil {
				return resp
			}
			_ = projectID
			return next(ctx, r)
		}
		return h
	}
	return m
}

// AuthorizeProjectManage gates write access to a {project_id} route: super
// admins and org admins may manage any project; a project manager may manage a
// project they have access to. Viewers (and members without manage roles) are
// rejected — the team model is view-for-all, manage-for-managers.
func AuthorizeProjectManage(projectBus projectbus.ExtBusiness) web.MidFunc {
	m := func(next web.HandlerFunc) web.HandlerFunc {
		h := func(ctx context.Context, r *http.Request) web.Encoder {
			if _, err := uuid.Parse(web.Param(r, "project_id")); err != nil {
				return errs.New(errs.InvalidArgument, ErrInvalidID)
			}

			// Org-wide managers.
			if hasRole(ctx, role.Admin) || hasRole(ctx, role.OrgAdmin) {
				return next(ctx, r)
			}

			// Project managers need explicit access to this project.
			if hasRole(ctx, role.PrjManager) {
				if _, resp := requireProjectAccess(ctx, r, projectBus); resp != nil {
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
// it (super/org admin, or projectBus.HasAccess). Returns a non-nil web.Encoder
// error response on failure.
func requireProjectAccess(ctx context.Context, r *http.Request, projectBus projectbus.ExtBusiness) (uuid.UUID, web.Encoder) {
	projectID, err := uuid.Parse(web.Param(r, "project_id"))
	if err != nil {
		return uuid.UUID{}, errs.New(errs.InvalidArgument, ErrInvalidID)
	}

	if hasRole(ctx, role.Admin) || hasRole(ctx, role.OrgAdmin) {
		return projectID, nil
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

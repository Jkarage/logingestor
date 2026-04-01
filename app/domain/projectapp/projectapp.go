// Package projectapp maintains the app layer api for the project domain.
package projectapp

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/business/types/role"
	"github.com/jkarage/logingestor/foundation/web"
)

type app struct {
	projectBus projectbus.ExtBusiness
}

func newApp(projectBus projectbus.ExtBusiness) *app {
	return &app{
		projectBus: projectBus,
	}
}

func (a *app) create(ctx context.Context, r *http.Request) web.Encoder {
	var np NewProject
	if err := web.Decode(r, &np); err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	busNew, err := toBusNewProject(np)
	if err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	busNew.OrgID = orgID

	project, err := a.projectBus.Create(ctx, mid.GetSubjectID(ctx), busNew)
	if err != nil {
		if errors.Is(err, projectbus.ErrDuplicateName) {
			return errs.New(errs.Aborted, projectbus.ErrDuplicateName)
		}
		return errs.Errorf(errs.Internal, "create: %s", err)
	}

	return toAppProject(project)
}

func (a *app) update(ctx context.Context, r *http.Request) web.Encoder {
	var up UpdateProject
	if err := web.Decode(r, &up); err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	busUpdate, err := toBusUpdateProject(up)
	if err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	projectID, err := uuid.Parse(web.Param(r, "project_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	project, err := a.projectBus.QueryByID(ctx, projectID)
	if err != nil {
		if errors.Is(err, projectbus.ErrNotFound) {
			return errs.New(errs.NotFound, err)
		}
		return errs.Errorf(errs.Internal, "querybyid: projectID[%s]: %s", projectID, err)
	}

	updated, err := a.projectBus.Update(ctx, mid.GetSubjectID(ctx), project, busUpdate)
	if err != nil {
		return errs.Errorf(errs.Internal, "update: projectID[%s]: %s", projectID, err)
	}

	return toAppProject(updated)
}

func (a *app) delete(ctx context.Context, r *http.Request) web.Encoder {
	projectID, err := uuid.Parse(web.Param(r, "project_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	project, err := a.projectBus.QueryByID(ctx, projectID)
	if err != nil {
		if errors.Is(err, projectbus.ErrNotFound) {
			return errs.New(errs.NotFound, err)
		}
		return errs.Errorf(errs.Internal, "querybyid: projectID[%s]: %s", projectID, err)
	}

	if err := a.projectBus.Delete(ctx, mid.GetSubjectID(ctx), project); err != nil {
		return errs.Errorf(errs.Internal, "delete: projectID[%s]: %s", projectID, err)
	}

	return nil
}

func (a *app) query(ctx context.Context, r *http.Request) web.Encoder {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	claims := mid.GetClaims(ctx)
	for _, claimRole := range claims.Roles {
		if claimRole == role.Admin.String() {
			projects, err := a.projectBus.QueryByOrg(ctx, orgID)
			if err != nil {
				return errs.Errorf(errs.Internal, "querybyorg: orgID[%s]: %s", orgID, err)
			}
			return toAppProjects(projects)
		}
	}

	userID := mid.GetSubjectID(ctx)

	projects, err := a.projectBus.QueryAccessible(ctx, orgID, userID)
	if err != nil {
		return errs.Errorf(errs.Internal, "queryaccessible: orgID[%s]: %s", orgID, err)
	}

	return toAppProjects(projects)
}

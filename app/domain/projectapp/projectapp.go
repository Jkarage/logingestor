// Package projectapp maintains the app layer api for the project domain.
package projectapp

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/business/types/role"
	"github.com/jkarage/logingestor/foundation/web"
)

type app struct {
	projectBus projectbus.ExtBusiness
	orgBus     orgbus.ExtBusiness
}

func newApp(projectBus projectbus.ExtBusiness, orgBus orgbus.ExtBusiness) *app {
	return &app{
		projectBus: projectBus,
		orgBus:     orgBus,
	}
}

// planRetentionLimit returns the longest retention, in days, the org's plan
// allows. A negative result means unlimited.
func (a *app) planRetentionLimit(ctx context.Context, orgID uuid.UUID) (int, error) {
	sub, err := a.orgBus.QuerySubscription(ctx, orgID)
	if err != nil {
		// No subscription row: treat as unconstrained rather than blocking edits.
		if errors.Is(err, orgbus.ErrNoBillingAccount) || errors.Is(err, orgbus.ErrNotFound) {
			return -1, nil
		}
		return 0, fmt.Errorf("querysubscription: %w", err)
	}

	plans, err := a.orgBus.QueryAllPlans(ctx)
	if err != nil {
		return 0, fmt.Errorf("queryallplans: %w", err)
	}

	for _, p := range plans {
		if p.PlanID == sub.PlanID {
			return p.Features.LogRetentionDays, nil
		}
	}

	return -1, nil
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

// requireProjectInOrg verifies that project belongs to the org named in the
// route's {org_id} param. Returns a non-nil error response on mismatch.
func requireProjectInOrg(r *http.Request, project projectbus.Project) web.Encoder {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}
	if project.OrgID != orgID {
		return errs.New(errs.NotFound, projectbus.ErrNotFound)
	}
	return nil
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

	// The route authorizes the caller against {org_id}; the project must
	// actually belong to that org or the check is meaningless.
	if resp := requireProjectInOrg(r, project); resp != nil {
		return resp
	}

	// Retention may not exceed what the org's plan allows. A nil override means
	// "inherit the plan", which is always within limits.
	if busUpdate.RetentionDays != nil && *busUpdate.RetentionDays != nil {
		limit, err := a.planRetentionLimit(ctx, project.OrgID)
		if err != nil {
			return errs.Errorf(errs.Internal, "planretentionlimit: orgID[%s]: %s", project.OrgID, err)
		}

		if limit >= 0 && **busUpdate.RetentionDays > limit {
			return errs.Errorf(errs.RetentionExceedsPlan,
				"retentionDays %d exceeds the plan limit of %d days", **busUpdate.RetentionDays, limit)
		}
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

	if resp := requireProjectInOrg(r, project); resp != nil {
		return resp
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

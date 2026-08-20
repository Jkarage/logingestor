package viewapp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/business/domain/viewbus"
	"github.com/jkarage/logingestor/business/types/role"
	"github.com/jkarage/logingestor/foundation/web"
)

type app struct {
	viewBus    *viewbus.Business
	orgBus     orgbus.ExtBusiness
	projectBus projectbus.ExtBusiness
}

func newApp(cfg Config) *app {
	return &app{viewBus: cfg.ViewBus, orgBus: cfg.OrgBus, projectBus: cfg.ProjectBus}
}

// viewer builds the access context for the caller: who they are, whether they
// administer this org, and which projects they can read.
func (a *app) viewer(ctx context.Context, orgID uuid.UUID) (viewbus.Viewer, web.Encoder) {
	userID, err := mid.GetUserID(ctx)
	if err != nil {
		return viewbus.Viewer{}, errs.New(errs.Unauthenticated, err)
	}

	who := viewbus.Viewer{UserID: userID}

	// A global SUPER ADMIN is treated as an org admin here, matching the bypass
	// the project authorization middleware applies.
	claims := mid.GetClaims(ctx)
	for _, r := range claims.Roles {
		if r == role.Admin.String() {
			who.OrgAdmin = true
		}
	}

	if !who.OrgAdmin {
		orgs, err := a.orgBus.QueryByUserID(ctx, userID)
		if err != nil {
			return viewbus.Viewer{}, errs.Errorf(errs.Internal, "querybyuserid: %s", err)
		}
		for _, o := range orgs {
			if o.ID == orgID && o.Role == role.OrgAdmin {
				who.OrgAdmin = true
				break
			}
		}
	}

	var projects []projectbus.Project
	if who.OrgAdmin {
		projects, err = a.projectBus.QueryByOrg(ctx, orgID)
	} else {
		projects, err = a.projectBus.QueryVisibleByOrg(ctx, orgID, userID)
	}
	if err != nil {
		return viewbus.Viewer{}, errs.Errorf(errs.Internal, "visible projects: %s", err)
	}

	who.VisibleProjects = make(map[uuid.UUID]struct{}, len(projects))
	for _, p := range projects {
		who.VisibleProjects[p.ID] = struct{}{}
	}

	return who, nil
}

// orgAndViewer resolves the path org plus the caller's access context.
func (a *app) orgAndViewer(ctx context.Context, r *http.Request) (uuid.UUID, viewbus.Viewer, web.Encoder) {
	orgID, err := uuid.Parse(web.Param(r, "org_id"))
	if err != nil {
		return uuid.UUID{}, viewbus.Viewer{}, errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	who, errResp := a.viewer(ctx, orgID)
	if errResp != nil {
		return uuid.UUID{}, viewbus.Viewer{}, errResp
	}

	return orgID, who, nil
}

// validationErr maps the business validation errors to 400 responses.
func validationErr(err error) web.Encoder {
	switch {
	case errors.Is(err, viewbus.ErrNameRequired),
		errors.Is(err, viewbus.ErrNameTooLong),
		errors.Is(err, viewbus.ErrDefTooLarge),
		errors.Is(err, viewbus.ErrBadVisibility),
		errors.Is(err, viewbus.ErrPanelsNotList):
		return errs.New(errs.InvalidArgument, err)
	}
	return nil
}

// =============================================================================
// Saved views

// queryViews lists the saved views the caller may read.
// GET /v1/orgs/{org_id}/saved-views
func (a *app) queryViews(ctx context.Context, r *http.Request) web.Encoder {
	orgID, who, errResp := a.orgAndViewer(ctx, r)
	if errResp != nil {
		return errResp
	}

	views, err := a.viewBus.QueryViewsVisible(ctx, orgID, who)
	if err != nil {
		return errs.Errorf(errs.Internal, "queryviews: orgID[%s]: %s", orgID, err)
	}

	out := make([]SavedView, len(views))
	for i, v := range views {
		out[i] = toAppSavedView(v, viewbus.CanModify(v.CreatedBy, v.Visibility, who))
	}

	return SavedViews{SavedViews: out}
}

// createView stores a new saved view.
// POST /v1/orgs/{org_id}/saved-views
func (a *app) createView(ctx context.Context, r *http.Request) web.Encoder {
	orgID, who, errResp := a.orgAndViewer(ctx, r)
	if errResp != nil {
		return errResp
	}

	var body NewSavedView
	if err := web.Decode(r, &body); err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	projectID, errResp := parsePin(body.ProjectID, who)
	if errResp != nil {
		return errResp
	}

	v, err := a.viewBus.CreateView(ctx, orgID, who.UserID, viewbus.NewSavedView{
		ProjectID:   projectID,
		Name:        body.Name,
		Description: body.Description,
		Query:       body.Query,
		Visibility:  body.Visibility,
	})
	if err != nil {
		if resp := validationErr(err); resp != nil {
			return resp
		}
		return errs.Errorf(errs.Internal, "createview: %s", err)
	}

	return toAppSavedView(v, true)
}

// queryViewByID returns one saved view.
// GET /v1/orgs/{org_id}/saved-views/{view_id}
func (a *app) queryViewByID(ctx context.Context, r *http.Request) web.Encoder {
	v, who, errResp := a.loadView(ctx, r)
	if errResp != nil {
		return errResp
	}

	return toAppSavedView(v, viewbus.CanModify(v.CreatedBy, v.Visibility, who))
}

// updateView applies changes to a saved view.
// PUT /v1/orgs/{org_id}/saved-views/{view_id}
func (a *app) updateView(ctx context.Context, r *http.Request) web.Encoder {
	v, who, errResp := a.loadView(ctx, r)
	if errResp != nil {
		return errResp
	}

	if !viewbus.CanModify(v.CreatedBy, v.Visibility, who) {
		return errs.New(errs.PermissionDenied, errors.New("only the creator or an org admin may change this saved view"))
	}

	var body UpdateSavedView
	if err := web.Decode(r, &body); err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	update := viewbus.UpdateSavedView{
		Name:        body.Name,
		Description: body.Description,
		Query:       body.Query,
		Visibility:  body.Visibility,
	}

	// projectId present as null clears the pin; present as a value re-pins;
	// absent leaves it alone.
	if len(body.ProjectID) > 0 {
		if string(body.ProjectID) == "null" {
			var cleared *uuid.UUID
			update.ProjectID = &cleared
		} else {
			var raw string
			if err := json.Unmarshal(body.ProjectID, &raw); err != nil {
				return errs.New(errs.InvalidArgument, errors.New("projectId must be a string or null"))
			}
			pinned, errResp := parsePin(raw, who)
			if errResp != nil {
				return errResp
			}
			update.ProjectID = &pinned
		}
	}

	updated, err := a.viewBus.UpdateView(ctx, v, update)
	if err != nil {
		if resp := validationErr(err); resp != nil {
			return resp
		}
		return errs.Errorf(errs.Internal, "updateview: %s", err)
	}

	return toAppSavedView(updated, true)
}

// deleteView removes a saved view.
// DELETE /v1/orgs/{org_id}/saved-views/{view_id}
func (a *app) deleteView(ctx context.Context, r *http.Request) web.Encoder {
	v, who, errResp := a.loadView(ctx, r)
	if errResp != nil {
		return errResp
	}

	if !viewbus.CanModify(v.CreatedBy, v.Visibility, who) {
		return errs.New(errs.PermissionDenied, errors.New("only the creator or an org admin may delete this saved view"))
	}

	if err := a.viewBus.DeleteView(ctx, v.ID); err != nil {
		return errs.Errorf(errs.Internal, "deleteview: %s", err)
	}

	return nil
}

// loadView fetches a saved view and confirms the caller may see it. A record in
// another org, or one they cannot see, is reported as not found so the endpoint
// never reveals that it exists.
func (a *app) loadView(ctx context.Context, r *http.Request) (viewbus.SavedView, viewbus.Viewer, web.Encoder) {
	orgID, who, errResp := a.orgAndViewer(ctx, r)
	if errResp != nil {
		return viewbus.SavedView{}, who, errResp
	}

	id, err := uuid.Parse(web.Param(r, "view_id"))
	if err != nil {
		return viewbus.SavedView{}, who, errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	notFound := errs.New(errs.NotFound, errors.New("saved view not found"))

	v, err := a.viewBus.QueryViewByID(ctx, id)
	if err != nil {
		if errors.Is(err, viewbus.ErrNotFound) {
			return viewbus.SavedView{}, who, notFound
		}
		return viewbus.SavedView{}, who, errs.Errorf(errs.Internal, "queryviewbyid: %s", err)
	}

	if v.OrgID != orgID || !viewbus.CanSeeView(v, who) {
		return viewbus.SavedView{}, who, notFound
	}

	return v, who, nil
}

// parsePin validates an optional project pin against what the caller can see.
func parsePin(raw string, who viewbus.Viewer) (*uuid.UUID, web.Encoder) {
	if raw == "" {
		return nil, nil
	}

	id, err := uuid.Parse(raw)
	if err != nil {
		return nil, errs.New(errs.InvalidArgument, errors.New("invalid projectId"))
	}

	// Pinning to a project the caller cannot read would create a view they
	// immediately lose sight of.
	if _, ok := who.VisibleProjects[id]; !ok {
		return nil, errs.Errorf(errs.NotFound, "project %s not found in this organization", id)
	}

	return &id, nil
}

// =============================================================================
// Dashboards

// queryDashboards lists the dashboards the caller may read.
// GET /v1/orgs/{org_id}/dashboards
func (a *app) queryDashboards(ctx context.Context, r *http.Request) web.Encoder {
	orgID, who, errResp := a.orgAndViewer(ctx, r)
	if errResp != nil {
		return errResp
	}

	boards, err := a.viewBus.QueryDashboardsVisible(ctx, orgID, who)
	if err != nil {
		return errs.Errorf(errs.Internal, "querydashboards: orgID[%s]: %s", orgID, err)
	}

	out := make([]Dashboard, len(boards))
	for i, d := range boards {
		out[i] = toAppDashboard(d, viewbus.CanModify(d.CreatedBy, d.Visibility, who))
	}

	return Dashboards{Dashboards: out}
}

// createDashboard stores a new dashboard.
// POST /v1/orgs/{org_id}/dashboards
func (a *app) createDashboard(ctx context.Context, r *http.Request) web.Encoder {
	orgID, who, errResp := a.orgAndViewer(ctx, r)
	if errResp != nil {
		return errResp
	}

	var body NewDashboard
	if err := web.Decode(r, &body); err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	d, err := a.viewBus.CreateDashboard(ctx, orgID, who.UserID, viewbus.NewDashboard{
		Name:        body.Name,
		Description: body.Description,
		Panels:      body.Panels,
		Visibility:  body.Visibility,
	})
	if err != nil {
		if resp := validationErr(err); resp != nil {
			return resp
		}
		return errs.Errorf(errs.Internal, "createdashboard: %s", err)
	}

	return toAppDashboard(d, true)
}

// queryDashboardByID returns one dashboard.
// GET /v1/orgs/{org_id}/dashboards/{dashboard_id}
func (a *app) queryDashboardByID(ctx context.Context, r *http.Request) web.Encoder {
	d, who, errResp := a.loadDashboard(ctx, r)
	if errResp != nil {
		return errResp
	}

	return toAppDashboard(d, viewbus.CanModify(d.CreatedBy, d.Visibility, who))
}

// updateDashboard applies changes to a dashboard.
// PUT /v1/orgs/{org_id}/dashboards/{dashboard_id}
func (a *app) updateDashboard(ctx context.Context, r *http.Request) web.Encoder {
	d, who, errResp := a.loadDashboard(ctx, r)
	if errResp != nil {
		return errResp
	}

	if !viewbus.CanModify(d.CreatedBy, d.Visibility, who) {
		return errs.New(errs.PermissionDenied, errors.New("only the creator or an org admin may change this dashboard"))
	}

	var body UpdateDashboard
	if err := web.Decode(r, &body); err != nil {
		return errs.New(errs.InvalidArgument, err)
	}

	updated, err := a.viewBus.UpdateDashboard(ctx, d, viewbus.UpdateDashboard{
		Name:        body.Name,
		Description: body.Description,
		Panels:      body.Panels,
		Visibility:  body.Visibility,
	})
	if err != nil {
		if resp := validationErr(err); resp != nil {
			return resp
		}
		return errs.Errorf(errs.Internal, "updatedashboard: %s", err)
	}

	return toAppDashboard(updated, true)
}

// deleteDashboard removes a dashboard.
// DELETE /v1/orgs/{org_id}/dashboards/{dashboard_id}
func (a *app) deleteDashboard(ctx context.Context, r *http.Request) web.Encoder {
	d, who, errResp := a.loadDashboard(ctx, r)
	if errResp != nil {
		return errResp
	}

	if !viewbus.CanModify(d.CreatedBy, d.Visibility, who) {
		return errs.New(errs.PermissionDenied, errors.New("only the creator or an org admin may delete this dashboard"))
	}

	if err := a.viewBus.DeleteDashboard(ctx, d.ID); err != nil {
		return errs.Errorf(errs.Internal, "deletedashboard: %s", err)
	}

	return nil
}

func (a *app) loadDashboard(ctx context.Context, r *http.Request) (viewbus.Dashboard, viewbus.Viewer, web.Encoder) {
	orgID, who, errResp := a.orgAndViewer(ctx, r)
	if errResp != nil {
		return viewbus.Dashboard{}, who, errResp
	}

	id, err := uuid.Parse(web.Param(r, "dashboard_id"))
	if err != nil {
		return viewbus.Dashboard{}, who, errs.New(errs.InvalidArgument, mid.ErrInvalidID)
	}

	notFound := errs.New(errs.NotFound, errors.New("dashboard not found"))

	d, err := a.viewBus.QueryDashboardByID(ctx, id)
	if err != nil {
		if errors.Is(err, viewbus.ErrNotFound) {
			return viewbus.Dashboard{}, who, notFound
		}
		return viewbus.Dashboard{}, who, errs.Errorf(errs.Internal, "querydashboardbyid: %s", err)
	}

	if d.OrgID != orgID || !viewbus.CanSeeDashboard(d, who) {
		return viewbus.Dashboard{}, who, notFound
	}

	return d, who, nil
}

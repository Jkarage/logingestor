package viewapp

import (
	"net/http"

	"github.com/jkarage/logingestor/app/sdk/authclient"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/business/domain/viewbus"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jkarage/logingestor/foundation/web"
)

// Config contains all the mandatory systems required by handlers.
type Config struct {
	Log        *logger.Logger
	AuthClient authclient.Authenticator
	ViewBus    *viewbus.Business
	OrgBus     orgbus.ExtBusiness
	ProjectBus projectbus.ExtBusiness
}

// Routes adds specific routes for this group.
//
// Membership gates every route. Who may read or change a particular record is
// decided per record: private records are creator-only, and project-pinned
// views are hidden from callers who cannot read that project.
func Routes(app *web.App, cfg Config) {
	const version = "v1"

	authen := mid.Authenticate(cfg.AuthClient)
	orgMember := mid.AuthorizeOrgMember(cfg.OrgBus)

	api := newApp(cfg)

	views := "/orgs/{org_id}/saved-views"
	app.HandlerFunc(http.MethodGet, version, views, api.queryViews, authen, orgMember)
	app.HandlerFunc(http.MethodPost, version, views, api.createView, authen, orgMember)
	app.HandlerFunc(http.MethodGet, version, views+"/{view_id}", api.queryViewByID, authen, orgMember)
	app.HandlerFunc(http.MethodPut, version, views+"/{view_id}", api.updateView, authen, orgMember)
	app.HandlerFunc(http.MethodDelete, version, views+"/{view_id}", api.deleteView, authen, orgMember)

	// A permalink is looked up by its slug, which is what travels in a URL.
	links := "/orgs/{org_id}/permalinks"
	app.HandlerFunc(http.MethodGet, version, links, api.queryPermalinks, authen, orgMember)
	app.HandlerFunc(http.MethodPost, version, links, api.createPermalink, authen, orgMember)
	app.HandlerFunc(http.MethodGet, version, links+"/{slug}", api.queryPermalink, authen, orgMember)
	app.HandlerFunc(http.MethodDelete, version, links+"/{slug}", api.deletePermalink, authen, orgMember)

	boards := "/orgs/{org_id}/dashboards"
	app.HandlerFunc(http.MethodGet, version, boards, api.queryDashboards, authen, orgMember)
	app.HandlerFunc(http.MethodPost, version, boards, api.createDashboard, authen, orgMember)
	app.HandlerFunc(http.MethodGet, version, boards+"/{dashboard_id}", api.queryDashboardByID, authen, orgMember)
	app.HandlerFunc(http.MethodPut, version, boards+"/{dashboard_id}", api.updateDashboard, authen, orgMember)
	app.HandlerFunc(http.MethodDelete, version, boards+"/{dashboard_id}", api.deleteDashboard, authen, orgMember)
}

package integrationapp

import (
	"net/http"

	"github.com/jkarage/logingestor/app/sdk/auth"
	"github.com/jkarage/logingestor/app/sdk/authclient"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/auditbus"
	"github.com/jkarage/logingestor/business/domain/integrationbus"
	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/business/domain/userbus"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jkarage/logingestor/foundation/web"
)

// Config contains all the mandatory systems required by handlers.
type Config struct {
	Log            *logger.Logger
	Auth           *auth.Auth
	AuthClient     authclient.Authenticator
	UserBus        userbus.ExtBusiness
	OrgBus         orgbus.ExtBusiness
	ProjectBus     projectbus.ExtBusiness
	IntegrationBus *integrationbus.Business
	AuditBus       auditbus.ExtBusiness
}

// Routes adds specific routes for this group.
func Routes(app *web.App, cfg Config) {
	const version = "v1"

	authen := mid.Authenticate(cfg.AuthClient)
	orgAdmin := mid.AuthorizeOrgAdmin(cfg.OrgBus)

	// Read: any member with access to the project. Manage: project managers of
	// that project, org admins of the project's org, and super admins (viewers
	// are read-only).
	projRead := mid.AuthorizeProjectAccess(cfg.ProjectBus, cfg.OrgBus)
	projManage := mid.AuthorizeProjectManage(cfg.ProjectBus, cfg.OrgBus)

	a := newApp(cfg.IntegrationBus, cfg.ProjectBus, cfg.UserBus, cfg.AuditBus)

	// Provider catalog — global, authenticated only.
	app.HandlerFunc(http.MethodGet, version, "/integration-providers", a.listProviders, authen)

	// Read-only org aggregates across projects (admin view, org-admin only).
	// Writes stay on the project-scoped routes below.
	app.HandlerFunc(http.MethodGet, version, "/orgs/{org_id}/integrations", a.orgAggregate, authen, orgAdmin)
	app.HandlerFunc(http.MethodGet, version, "/orgs/{org_id}/rules", a.listRulesByOrg, authen, orgAdmin)

	// Alert history and lifecycle. Org-admin, matching the org-wide rule list.
	app.HandlerFunc(http.MethodGet, version, "/orgs/{org_id}/alerts", a.queryAlerts, authen, orgAdmin)
	app.HandlerFunc(http.MethodPost, version, "/orgs/{org_id}/alerts/{alert_id}/acknowledge", a.acknowledgeAlert, authen, orgAdmin)
	app.HandlerFunc(http.MethodPost, version, "/orgs/{org_id}/alerts/{alert_id}/resolve", a.resolveAlert, authen, orgAdmin)

	// Maintenance windows suppress delivery without disabling rules.
	mw := "/orgs/{org_id}/maintenance-windows"
	app.HandlerFunc(http.MethodGet, version, mw, a.queryMaintenance, authen, orgAdmin)
	app.HandlerFunc(http.MethodPost, version, mw, a.createMaintenance, authen, orgAdmin)
	app.HandlerFunc(http.MethodDelete, version, mw+"/{window_id}", a.deleteMaintenance, authen, orgAdmin)

	// Project-scoped connection CRUD.
	base := "/orgs/{org_id}/projects/{project_id}/integrations"
	app.HandlerFunc(http.MethodGet, version, base, a.list, authen, projRead)
	app.HandlerFunc(http.MethodPost, version, base, a.create, authen, projManage)
	app.HandlerFunc(http.MethodPut, version, base+"/{integration_id}", a.update, authen, projManage)
	app.HandlerFunc(http.MethodDelete, version, base+"/{integration_id}", a.delete, authen, projManage)
	app.HandlerFunc(http.MethodPost, version, base+"/{integration_id}/test", a.test, authen, projManage)

	// Project-scoped alert rules.
	rules := "/orgs/{org_id}/projects/{project_id}/rules"
	app.HandlerFunc(http.MethodGet, version, rules, a.listRules, authen, projRead)
	app.HandlerFunc(http.MethodPost, version, rules, a.createRule, authen, projManage)
	app.HandlerFunc(http.MethodPut, version, rules+"/{rule_id}", a.updateRule, authen, projManage)
	app.HandlerFunc(http.MethodPatch, version, rules+"/{rule_id}/toggle", a.toggleRule, authen, projManage)
	app.HandlerFunc(http.MethodDelete, version, rules+"/{rule_id}", a.deleteRule, authen, projManage)
}

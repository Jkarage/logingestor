package integrationapp

import (
	"net/http"

	"github.com/jkarage/logingestor/app/sdk/auth"
	"github.com/jkarage/logingestor/app/sdk/authclient"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/auditbus"
	"github.com/jkarage/logingestor/business/domain/integrationbus"
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
	ProjectBus     projectbus.ExtBusiness
	IntegrationBus *integrationbus.Business
	AuditBus       auditbus.ExtBusiness
}

// Routes adds specific routes for this group.
func Routes(app *web.App, cfg Config) {
	const version = "v1"

	authen := mid.Authenticate(cfg.AuthClient)
	ruleOrgAdmin := mid.AuthorizeUser(cfg.AuthClient, cfg.UserBus, auth.RuleOrgAdminOnly)

	// Read: any member with access to the project. Manage: project managers of
	// that project, org admins, and super admins (viewers are read-only).
	projRead := mid.AuthorizeProjectAccess(cfg.ProjectBus)
	projManage := mid.AuthorizeProjectManage(cfg.ProjectBus)

	a := newApp(cfg.IntegrationBus, cfg.ProjectBus, cfg.UserBus, cfg.AuditBus)

	// Provider catalog — global, authenticated only.
	app.HandlerFunc(http.MethodGet, version, "/integration-providers", a.listProviders, authen)

	// Read-only org aggregates across projects (admin view, org-admin only).
	// Writes stay on the project-scoped routes below.
	app.HandlerFunc(http.MethodGet, version, "/orgs/{org_id}/integrations", a.orgAggregate, authen, ruleOrgAdmin)
	app.HandlerFunc(http.MethodGet, version, "/orgs/{org_id}/rules", a.listRulesByOrg, authen, ruleOrgAdmin)

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

package projectapp

import (
	"net/http"

	"github.com/jkarage/logingestor/app/sdk/auth"
	"github.com/jkarage/logingestor/app/sdk/authclient"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/business/domain/userbus"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jkarage/logingestor/foundation/web"
)

// Config contains all the mandatory systems required by handlers.
type Config struct {
	Log        *logger.Logger
	Auth       *auth.Auth
	AuthClient authclient.Authenticator
	UserBus    userbus.ExtBusiness
	OrgBus     orgbus.ExtBusiness
	ProjectBus projectbus.ExtBusiness
}

// Routes adds specific routes for this group.
func Routes(app *web.App, cfg Config) {
	const version = "v1"

	authen := mid.Authenticate(cfg.AuthClient)
	ruleOrgAdmin := mid.AuthorizeUser(cfg.AuthClient, cfg.UserBus, auth.RuleOrgAdminOnly)
	orgMember := mid.AuthorizeOrgMember(cfg.OrgBus)

	api := newApp(cfg.ProjectBus)

	app.HandlerFunc(http.MethodGet, version, "/orgs/{org_id}/projects", api.query, authen, orgMember)
	app.HandlerFunc(http.MethodPost, version, "/orgs/{org_id}/projects", api.create, authen, ruleOrgAdmin, orgMember)
	app.HandlerFunc(http.MethodPut, version, "/orgs/{org_id}/projects/{project_id}", api.update, authen, ruleOrgAdmin, orgMember)
	app.HandlerFunc(http.MethodDelete, version, "/orgs/{org_id}/projects/{project_id}", api.delete, authen, ruleOrgAdmin, orgMember)
}

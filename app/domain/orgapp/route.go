package orgapp

import (
	"net/http"

	"github.com/jkarage/logingestor/app/sdk/auth"
	"github.com/jkarage/logingestor/app/sdk/authclient"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/orgbus"
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
}

// Routes adds specific routes for this group.
func Routes(app *web.App, cfg Config) {
	const version = "v1"

	authen := mid.Authenticate(cfg.AuthClient)
	ruleSuperAdmin := mid.Authorize(cfg.AuthClient, auth.RuleAdminOnly)
	ruleAuthorizeUser := mid.AuthorizeUser(cfg.AuthClient, cfg.UserBus, auth.RuleAdminOrSubject)
	ruleOrgAdmin := mid.AuthorizeUser(cfg.AuthClient, cfg.UserBus, auth.RuleOrgAdminOnly)

	api := newApp(cfg.OrgBus, cfg.Auth)

	app.HandlerFunc(http.MethodGet, version, "/orgs/mine", api.queryMine, authen)
	app.HandlerFunc(http.MethodGet, version, "/orgs/{org_id}/members", api.queryOrgMembers, authen, ruleOrgAdmin)
	app.HandlerFunc(http.MethodGet, version, "/orgs/{org_id}", api.queryByID, authen, ruleAuthorizeUser)
	app.HandlerFunc(http.MethodGet, version, "/orgs", api.query, authen, ruleSuperAdmin)
	app.HandlerFunc(http.MethodPost, version, "/orgs", api.create, authen)
	app.HandlerFunc(http.MethodPut, version, "/orgs/role/{org_id}", api.updateRole, authen, ruleOrgAdmin)
	app.HandlerFunc(http.MethodPut, version, "/orgs/{org_id}", api.update, authen, ruleAuthorizeUser)
	app.HandlerFunc(http.MethodDelete, version, "/orgs/{org_id}", api.delete, authen, ruleAuthorizeUser)
}

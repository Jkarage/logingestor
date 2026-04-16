package integrationapp

import (
	"net/http"

	"github.com/jkarage/logingestor/app/sdk/auth"
	"github.com/jkarage/logingestor/app/sdk/authclient"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/integrationbus"
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
	IntegrationBus *integrationbus.Business
}

// Routes adds specific routes for this group.
func Routes(app *web.App, cfg Config) {
	const version = "v1"

	authen := mid.Authenticate(cfg.AuthClient)
	ruleOrgAdmin := mid.AuthorizeUser(cfg.AuthClient, cfg.UserBus, auth.RuleOrgAdminOnly)

	a := newApp(cfg.IntegrationBus)

	// Provider catalog — authenticated but no org-admin requirement.
	app.HandlerFunc(http.MethodGet, version, "/integration-providers", a.listProviders, authen)

	// Per-org integration CRUD.
	app.HandlerFunc(http.MethodGet, version, "/orgs/{org_id}/integrations", a.list, authen)
	app.HandlerFunc(http.MethodPost, version, "/orgs/{org_id}/integrations", a.create, authen, ruleOrgAdmin)
	app.HandlerFunc(http.MethodPut, version, "/orgs/{org_id}/integrations/{integration_id}", a.update, authen, ruleOrgAdmin)
	app.HandlerFunc(http.MethodDelete, version, "/orgs/{org_id}/integrations/{integration_id}", a.delete, authen, ruleOrgAdmin)
	app.HandlerFunc(http.MethodPost, version, "/orgs/{org_id}/integrations/{integration_id}/test", a.test, authen, ruleOrgAdmin)
}

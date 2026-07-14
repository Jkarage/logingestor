package sourceapp

import (
	"net/http"

	"github.com/jkarage/logingestor/app/sdk/auth"
	"github.com/jkarage/logingestor/app/sdk/authclient"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/business/domain/sourcebus"
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
	ProjectBus projectbus.ExtBusiness
	SourceBus  sourcebus.ExtBusiness
}

// Routes adds specific routes for this group.
func Routes(app *web.App, cfg Config) {
	const version = "v1"

	authen := mid.Authenticate(cfg.AuthClient)
	ruleOrgAdmin := mid.AuthorizeUser(cfg.AuthClient, cfg.UserBus, auth.RuleOrgAdminOnly)

	api := newApp(cfg.SourceBus, cfg.ProjectBus)

	app.HandlerFunc(http.MethodGet, version, "/orgs/{org_id}/sources", api.query, authen, ruleOrgAdmin)
	app.HandlerFunc(http.MethodPost, version, "/orgs/{org_id}/sources", api.create, authen, ruleOrgAdmin)
	app.HandlerFunc(http.MethodDelete, version, "/orgs/{org_id}/sources/{source_id}", api.disconnect, authen, ruleOrgAdmin)
	app.HandlerFunc(http.MethodPost, version, "/orgs/{org_id}/sources/{source_id}/rotate-key", api.rotateKey, authen, ruleOrgAdmin)
}

package usageapp

import (
	"net/http"

	"github.com/jkarage/logingestor/app/sdk/authclient"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/business/domain/usagebus"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jkarage/logingestor/foundation/web"
)

// Config contains all the mandatory systems required by handlers.
type Config struct {
	Log        *logger.Logger
	AuthClient authclient.Authenticator
	UsageBus   usagebus.ExtBusiness
	OrgBus     orgbus.ExtBusiness
}

// Routes adds specific routes for this group.
func Routes(app *web.App, cfg Config) {
	const version = "v1"

	authen := mid.Authenticate(cfg.AuthClient)
	orgAdmin := mid.AuthorizeOrgAdmin(cfg.OrgBus)

	api := newApp(cfg.UsageBus, cfg.OrgBus)

	// Usage feeds billing dashboards, so it is org-admin only — the same gate as
	// /orgs/{org_id}/billing.
	app.HandlerFunc(http.MethodGet, version, "/orgs/{org_id}/usage", api.query, authen, orgAdmin)
}

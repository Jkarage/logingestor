package apikeyapp

import (
	"net/http"

	"github.com/jkarage/logingestor/app/sdk/authclient"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/apikeybus"
	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jkarage/logingestor/foundation/web"
)

// Config contains all the mandatory systems required by handlers.
type Config struct {
	Log        *logger.Logger
	AuthClient authclient.Authenticator
	APIKeyBus  *apikeybus.Business
	OrgBus     orgbus.ExtBusiness
	ProjectBus projectbus.ExtBusiness

	// DefaultRatePerMin and DefaultRateBurst are the query API budget a key with
	// no limits of its own receives.
	DefaultRatePerMin int
	DefaultRateBurst  int
}

// Routes adds specific routes for this group.
//
// Minting a credential that reads an org's logs is an administrative act, so
// every route here is org-admin only — unlike the query API the keys unlock,
// which has no session at all.
func Routes(app *web.App, cfg Config) {
	const version = "v1"

	authen := mid.Authenticate(cfg.AuthClient)
	orgAdmin := mid.AuthorizeOrgAdmin(cfg.OrgBus)

	api := newApp(cfg)

	base := "/orgs/{org_id}/api-keys"
	app.HandlerFunc(http.MethodGet, version, base, api.query, authen, orgAdmin)
	app.HandlerFunc(http.MethodPost, version, base, api.create, authen, orgAdmin)
	app.HandlerFunc(http.MethodDelete, version, base+"/{key_id}", api.revoke, authen, orgAdmin)
}

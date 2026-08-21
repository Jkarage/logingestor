package sourceapp

import (
	"net/http"

	"github.com/jkarage/logingestor/app/sdk/auth"
	"github.com/jkarage/logingestor/app/sdk/authclient"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/business/domain/rejectbus"
	"github.com/jkarage/logingestor/business/domain/sourcebus"
	"github.com/jkarage/logingestor/business/domain/usagebus"
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
	SourceBus  sourcebus.ExtBusiness
	UsageBus   usagebus.ExtBusiness

	// RejectBus reads the dead-letter store. Nil leaves the endpoint answering
	// that refused records are not being kept.
	RejectBus *rejectbus.Business
}

// Routes adds specific routes for this group.
func Routes(app *web.App, cfg Config) {
	const version = "v1"

	authen := mid.Authenticate(cfg.AuthClient)
	orgAdmin := mid.AuthorizeOrgAdmin(cfg.OrgBus)

	api := newApp(cfg.SourceBus, cfg.ProjectBus, cfg.UsageBus, cfg.RejectBus)

	app.HandlerFunc(http.MethodGet, version, "/orgs/{org_id}/sources", api.query, authen, orgAdmin)
	app.HandlerFunc(http.MethodPost, version, "/orgs/{org_id}/sources", api.create, authen, orgAdmin)
	app.HandlerFunc(http.MethodDelete, version, "/orgs/{org_id}/sources/{source_id}", api.disconnect, authen, orgAdmin)
	app.HandlerFunc(http.MethodPost, version, "/orgs/{org_id}/sources/{source_id}/rotate-key", api.rotateKey, authen, orgAdmin)
	app.HandlerFunc(http.MethodGet, version, "/orgs/{org_id}/sources/{source_id}/health", api.health, authen, orgAdmin)

	// The dead-letter store: which records were refused and why. Org admin,
	// because a rejected record is the customer's own payload.
	app.HandlerFunc(http.MethodGet, version, "/orgs/{org_id}/ingest-rejects", api.queryRejects, authen, orgAdmin)
}

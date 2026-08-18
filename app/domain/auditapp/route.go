package auditapp

import (
	"net/http"

	"github.com/jkarage/logingestor/app/sdk/auth"
	"github.com/jkarage/logingestor/app/sdk/authclient"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/auditbus"
	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/business/domain/userbus"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jkarage/logingestor/foundation/web"
)

// Config contains all the mandatory systems required by handlers.
type Config struct {
	Log        *logger.Logger
	AuditBus   auditbus.ExtBusiness
	OrgBus     orgbus.ExtBusiness
	UserBus    userbus.ExtBusiness
	AuthClient authclient.Authenticator
}

// Routes adds specific routes for this group.
func Routes(app *web.App, cfg Config) {
	const version = "v1"

	authen := mid.Authenticate(cfg.AuthClient)
	ruleSuperAdmin := mid.Authorize(cfg.AuthClient, auth.RuleAdminOnly)
	orgAdmin := mid.AuthorizeOrgAdmin(cfg.OrgBus)

	api := newApp(cfg.AuditBus)

	// Platform-wide audit log (super_admin only).
	app.HandlerFunc(http.MethodGet, version, "/audit", api.query, authen, ruleSuperAdmin)

	// Org-scoped audit log. "org admin" is the caller's membership role for
	// {org_id}, not their global JWT role, so an invited ORG ADMIN qualifies.
	app.HandlerFunc(http.MethodGet, version, "/orgs/{org_id}/audit", api.queryByOrg, authen, orgAdmin)
}

package invitationapp

import (
	"net/http"

	"github.com/jkarage/logingestor/app/sdk/auth"
	"github.com/jkarage/logingestor/app/sdk/authclient"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/invitationbus"
	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/business/domain/userbus"
	emailer "github.com/jkarage/logingestor/foundation/email"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jkarage/logingestor/foundation/web"
)

// Config contains all the mandatory systems required by handlers.
type Config struct {
	Log           *logger.Logger
	Auth          *auth.Auth
	AuthClient    authclient.Authenticator
	UserBus       userbus.ExtBusiness
	OrgBus        orgbus.ExtBusiness
	ProjectBus    projectbus.ExtBusiness
	InvitationBus invitationbus.ExtBusiness
	Mailer        *emailer.Config
	EmailBaseURL  string
	SigningKey     string
}

// Routes adds specific routes for this group.
func Routes(app *web.App, cfg Config) {
	const version = "v1"

	authen := mid.Authenticate(cfg.AuthClient)
	ruleOrgAdmin := mid.AuthorizeUser(cfg.AuthClient, cfg.UserBus, auth.RuleOrgAdminOnly)

	api := newApp(
		cfg.InvitationBus,
		cfg.UserBus,
		cfg.OrgBus,
		cfg.ProjectBus,
		cfg.Auth,
		cfg.SigningKey,
		cfg.Mailer,
		cfg.EmailBaseURL,
	)

	// Org-scoped invitation management (requires ORG ADMIN).
	app.HandlerFunc(http.MethodGet, version, "/orgs/{org_id}/invitations", api.query, authen, ruleOrgAdmin)
	app.HandlerFunc(http.MethodPost, version, "/orgs/{org_id}/invitations", api.create, authen, ruleOrgAdmin)
	app.HandlerFunc(http.MethodDelete, version, "/orgs/{org_id}/invitations/{invitation_id}", api.revoke, authen, ruleOrgAdmin)

	// Accept is public — the token IS the credential.
	app.HandlerFunc(http.MethodPost, version, "/invitations/accept", api.accept)
}

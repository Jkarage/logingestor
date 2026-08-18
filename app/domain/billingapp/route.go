package billingapp

import (
	"net/http"

	"github.com/jkarage/logingestor/app/sdk/authclient"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/business/domain/userbus"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jkarage/logingestor/foundation/web"
)

// Config contains all the mandatory systems required by handlers.
type Config struct {
	Log                 *logger.Logger
	AuthClient          authclient.Authenticator
	UserBus             userbus.ExtBusiness
	OrgBus              orgbus.ExtBusiness
	StripeSecretKey     string
	StripeWebhookSecret string
	AppBaseURL          string
}

// Routes adds specific routes for this group.
func Routes(app *web.App, cfg Config) {
	const version = "v1"

	authen := mid.Authenticate(cfg.AuthClient)
	orgAdmin := mid.AuthorizeOrgAdmin(cfg.OrgBus)

	a := newApp(cfg)

	// Public — no auth required (pricing page)
	app.HandlerFunc(http.MethodGet, version, "/billing/plans", a.listPlans)

	// Stripe webhook — no JWT auth, verified by Stripe-Signature header
	app.HandlerFunc(http.MethodPost, version, "/billing/webhook", a.webhook)

	// Org-scoped billing endpoints — org admin of THIS org, resolved from the
	// caller's org_members role rather than the JWT's global role claim. Swap
	// orgAdmin for mid.AuthorizeOrgOwner(cfg.OrgBus) to make billing
	// owner-only.
	app.HandlerFunc(http.MethodGet, version, "/orgs/{org_id}/billing", a.getBilling, authen, orgAdmin)
	app.HandlerFunc(http.MethodPost, version, "/orgs/{org_id}/billing/checkout", a.checkout, authen, orgAdmin)
	app.HandlerFunc(http.MethodPost, version, "/orgs/{org_id}/billing/portal", a.portal, authen, orgAdmin)
	app.HandlerFunc(http.MethodPost, version, "/orgs/{org_id}/billing/cancel", a.cancel, authen, orgAdmin)
}

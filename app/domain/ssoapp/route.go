package ssoapp

import (
	"net/http"

	"github.com/jkarage/logingestor/app/sdk/auth"
	"github.com/jkarage/logingestor/app/sdk/authclient"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/business/domain/scimbus"
	"github.com/jkarage/logingestor/business/domain/ssobus"
	"github.com/jkarage/logingestor/business/domain/userbus"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jkarage/logingestor/foundation/web"
)

// Config contains all the mandatory systems required by handlers.
type Config struct {
	Log        *logger.Logger
	Auth       *auth.Auth
	AuthClient authclient.Authenticator
	SSOBus     *ssobus.Business
	SCIMBus    *scimbus.Business
	OrgBus     orgbus.ExtBusiness
	UserBus    userbus.ExtBusiness

	// SigningKey is the active kid used to mint session tokens.
	SigningKey string

	// CallbackURL is this API's callback, and must be registered as a redirect
	// URI with every configured identity provider.
	CallbackURL string

	// CompleteURL is the frontend route the browser lands on after the callback.
	CompleteURL string

	// RequireVerifiedEmail rejects identities whose email_verified is not true.
	RequireVerifiedEmail bool
}

// Routes adds specific routes for this group.
func Routes(app *web.App, cfg Config) {
	const version = "v1"

	authen := mid.Authenticate(cfg.AuthClient)
	orgAdmin := mid.AuthorizeOrgAdmin(cfg.OrgBus)

	api := newApp(cfg)

	// Provider configuration is org-admin only. The client secret is write-only:
	// it can be set here but is never returned.
	app.HandlerFunc(http.MethodGet, version, "/orgs/{org_id}/sso", api.query, authen, orgAdmin)
	// POST rather than PUT: the pre-existing PUT /v1/orgs/role/{org_id} puts the
	// id in the trailing segment, which Go's mux considers ambiguous with any
	// PUT /v1/orgs/{org_id}/* pattern and refuses to register. Reshaping that
	// legacy route would change a contract the frontend already calls, so the
	// upsert is a POST until it moves to /orgs/{org_id}/role.
	app.HandlerFunc(http.MethodPost, version, "/orgs/{org_id}/sso", api.upsert, authen, orgAdmin)
	app.HandlerFunc(http.MethodDelete, version, "/orgs/{org_id}/sso", api.remove, authen, orgAdmin)

	// The SCIM provisioning token is managed here but consumed by the SCIM
	// endpoints, which authenticate with it instead of a user JWT.
	app.HandlerFunc(http.MethodGet, version, "/orgs/{org_id}/scim-token", api.querySCIMToken, authen, orgAdmin)
	app.HandlerFunc(http.MethodPost, version, "/orgs/{org_id}/scim-token", api.issueSCIMToken, authen, orgAdmin)
	app.HandlerFunc(http.MethodDelete, version, "/orgs/{org_id}/scim-token", api.revokeSCIMToken, authen, orgAdmin)

	// The login flow is necessarily public — the caller has no session yet. Each
	// step is protected by the single-use state, nonce and PKCE verifier held
	// server-side rather than by authentication.
	app.HandlerFunc(http.MethodGet, version, "/auth/sso/{org_slug}/start", api.start)
	app.HandlerFunc(http.MethodGet, version, "/auth/sso/callback", api.callback)
	app.HandlerFunc(http.MethodPost, version, "/auth/sso/exchange", api.exchange)
}

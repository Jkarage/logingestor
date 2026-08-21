package clienterrorapp

import (
	"net/http"

	"github.com/jkarage/logingestor/app/sdk/authclient"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/clienterrorbus"
	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jkarage/logingestor/foundation/web"
)

// Config contains all the mandatory systems required by handlers.
type Config struct {
	Log            *logger.Logger
	AuthClient     authclient.Authenticator
	ClientErrorBus *clienterrorbus.Business
	OrgBus         orgbus.ExtBusiness
	ProjectBus     projectbus.ExtBusiness

	// AllowedOrigins is the set of hosts allowed to post error reports. Empty
	// means any, which is only appropriate in development.
	AllowedOrigins []string

	// UploadToken authenticates CI's source map uploads. Empty disables them.
	UploadToken string
}

// Routes adds specific routes for this group.
func Routes(app *web.App, cfg Config) {
	const version = "v1"

	authen := mid.Authenticate(cfg.AuthClient)
	orgAdmin := mid.AuthorizeOrgAdmin(cfg.OrgBus)

	api := newApp(cfg)

	// Ingest carries no auth middleware by design. An error on the login page is
	// exactly the error worth having, and it has no session to present. The
	// handler reads a token when one is there and files the report anonymously
	// when it is not; abuse is handled by the size cap, the batch cap, the
	// per-address rate limit and the origin check rather than by a credential.
	app.HandlerFunc(http.MethodPost, version, "/client-errors", api.ingest)

	// Triage is org admin, matching alerts and audit.
	base := "/orgs/{org_id}/client-errors"
	app.HandlerFunc(http.MethodGet, version, base+"/issues", api.queryIssues, authen, orgAdmin)
	app.HandlerFunc(http.MethodGet, version, base+"/issues/{issue_id}", api.queryIssue, authen, orgAdmin)
	app.HandlerFunc(http.MethodPatch, version, base+"/issues/{issue_id}", api.updateIssue, authen, orgAdmin)
	app.HandlerFunc(http.MethodGet, version, base+"/stats", api.queryStats, authen, orgAdmin)
	app.HandlerFunc(http.MethodDelete, version, base, api.purge, authen, orgAdmin)

	// Source maps, uploaded by CI at deploy time and never served to a browser.
	// Authenticated by a shared token rather than a session: there is no user and
	// no org involved, and one build serves every tenant.
	app.HandlerFunc(http.MethodPost, version, "/client-errors/artifacts", api.uploadArtifacts)
	app.HandlerFunc(http.MethodGet, version, "/client-errors/artifacts", api.queryArtifacts)

	// The cross-org view. Anonymous reports — the crashes on the landing and
	// login pages — belong to no org, so without this nobody could see them.
	// Super admin only, checked in the handler because the rule is about the
	// caller's global role rather than a membership.
	app.HandlerFunc(http.MethodGet, version, "/client-errors/issues", api.queryIssuesGlobal, authen)
	app.HandlerFunc(http.MethodGet, version, "/client-errors/issues/{issue_id}", api.queryIssueGlobal, authen)
	app.HandlerFunc(http.MethodGet, version, "/client-errors/stats", api.queryStatsGlobal, authen)
}

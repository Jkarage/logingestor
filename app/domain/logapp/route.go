package logapp

import (
	"net/http"

	"github.com/jkarage/logingestor/app/sdk/authclient"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/analyzebus"
	"github.com/jkarage/logingestor/business/domain/apikeybus"
	"github.com/jkarage/logingestor/business/domain/logbus"
	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/business/domain/usagebus"
	"github.com/jkarage/logingestor/business/sdk/ratelimit"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jkarage/logingestor/foundation/web"
)

// Config contains all the mandatory systems required by handlers.
type Config struct {
	Log            *logger.Logger
	AuthClient     authclient.Authenticator
	LogBus         logbus.ExtBusiness
	OrgBus         orgbus.ExtBusiness
	ProjectBus     projectbus.ExtBusiness
	AnalyzeBus     *analyzebus.Business
	UsageBus       usagebus.ExtBusiness
	Hub            *Hub
	AllowedOrigins []string

	// APIKeyBus authenticates the public query API. Nil leaves those routes
	// unregistered, so a deployment can withhold the programmatic surface.
	APIKeyBus *apikeybus.Business

	// QueryRatePerMin and QueryRateBurst are the default budget for a key that
	// carries none of its own.
	QueryRatePerMin int
	QueryRateBurst  int
}

// Routes adds specific routes for this group.
func Routes(app *web.App, cfg Config) {
	const version = "v1"

	authen := mid.Authenticate(cfg.AuthClient)
	projRead := mid.AuthorizeProjectAccess(cfg.ProjectBus, cfg.OrgBus)
	orgMember := mid.AuthorizeOrgMember(cfg.OrgBus)

	a := newApp(cfg.Log, cfg.LogBus, cfg.ProjectBus, cfg.AnalyzeBus, cfg.Hub, cfg.AuthClient, cfg.AllowedOrigins, cfg.UsageBus)

	app.HandlerFunc(http.MethodPost, version, "/ingest", a.ingest, authen)
	app.HandlerFunc(http.MethodGet, version, "/projects/{project_id}/logs", a.query, authen, projRead)

	// Org-wide read across projects. Membership gates the request; which
	// projects are actually readable is resolved inside the handler, so a viewer
	// sees only what they were granted.
	app.HandlerFunc(http.MethodGet, version, "/orgs/{org_id}/logs", a.queryOrg, authen, orgMember)

	// Exports stream, and stream from the same filters as the list above, so
	// "download what I am looking at" needs no second contract.
	app.HandlerFunc(http.MethodGet, version, "/projects/{project_id}/logs/export", a.exportProject, authen, projRead)
	app.HandlerFunc(http.MethodGet, version, "/orgs/{org_id}/logs/export", a.exportOrg, authen, orgMember)
	app.HandlerFunc(http.MethodGet, version, "/projects/{project_id}/logs/stats", a.stats, authen, projRead)
	app.HandlerFunc(http.MethodGet, version, "/projects/{project_id}/logs/timeseries", a.timeseries, authen, projRead)
	app.HandlerFunc(http.MethodGet, version, "/projects/{project_id}/logs/aggregate", a.aggregate, authen, projRead)
	// Reading one log by id. Deep links and permalinks resolve through here.
	app.HandlerFunc(http.MethodGet, version, "/projects/{project_id}/logs/{log_id}", a.queryByID, authen, projRead)
	app.HandlerFunc(http.MethodPost, version, "/projects/{project_id}/logs/{log_id}/analyze", a.analyze, authen, projRead)

	// Mints the single-use ticket the WebSocket endpoint below accepts in place
	// of a JWT in the query string.
	app.HandlerFunc(http.MethodPost, version, "/projects/{project_id}/logs/stream-ticket", a.streamTicket, authen, projRead)

	// The stream endpoint upgrades to WebSocket. It MUST bypass the app-level
	// middleware stack (logging, error handling) because those middleware
	// functions capture the http.ResponseWriter before the upgrade and may write
	// to it after the connection has been hijacked, which corrupts the WS frames
	// and causes "WebSocket is closed before the connection is established".
	// Panic recovery is still applied by RawHandlerFuncNoMid itself.
	// Authentication is handled manually inside a.stream via the ?token= param.
	app.RawHandlerFuncNoMid(http.MethodGet, version, "/projects/{project_id}/logs/stream", a.stream)

	// The public query API. Authenticated by a read-only API key rather than a
	// session, for scripts and CI. It is a separate path prefix so the key auth
	// can never be reached by a route that expects a user, and vice versa.
	if cfg.APIKeyBus != nil {
		apiKey := mid.AuthenticateAPIKey(cfg.APIKeyBus, cfg.OrgBus)

		// Authenticate, then throttle. The budget belongs to the key, so the
		// limiter has to know which key it is — and a request that fails to
		// authenticate should not spend somebody's budget.
		//
		// One limiter instance across all five routes, so a caller cannot get five
		// separate budgets by spreading its traffic over them.
		throttle := mid.RateLimitAPIKey(ratelimit.New(), cfg.QueryRatePerMin, cfg.QueryRateBurst)

		app.HandlerFunc(http.MethodGet, version, "/query/projects", a.queryAPIProjects, apiKey, throttle)
		app.HandlerFunc(http.MethodGet, version, "/query/logs", a.queryAPILogs, apiKey, throttle)
		app.HandlerFunc(http.MethodGet, version, "/query/logs/export", a.exportAPILogs, apiKey, throttle)
		app.HandlerFunc(http.MethodGet, version, "/query/logs/{log_id}", a.queryAPILogByID, apiKey, throttle)
		app.HandlerFunc(http.MethodGet, version, "/query/stats", a.queryAPIStats, apiKey, throttle)
	}
}

package logapp

import (
	"net/http"

	"github.com/jkarage/logingestor/app/sdk/authclient"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/analyzebus"
	"github.com/jkarage/logingestor/business/domain/logbus"
	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/business/domain/usagebus"
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
}

// Routes adds specific routes for this group.
func Routes(app *web.App, cfg Config) {
	const version = "v1"

	authen := mid.Authenticate(cfg.AuthClient)
	projRead := mid.AuthorizeProjectAccess(cfg.ProjectBus, cfg.OrgBus)

	a := newApp(cfg.Log, cfg.LogBus, cfg.ProjectBus, cfg.AnalyzeBus, cfg.Hub, cfg.AuthClient, cfg.AllowedOrigins, cfg.UsageBus)

	app.HandlerFunc(http.MethodPost, version, "/ingest", a.ingest, authen)
	app.HandlerFunc(http.MethodGet, version, "/projects/{project_id}/logs", a.query, authen, projRead)
	app.HandlerFunc(http.MethodGet, version, "/projects/{project_id}/logs/stats", a.stats, authen, projRead)
	app.HandlerFunc(http.MethodGet, version, "/projects/{project_id}/logs/timeseries", a.timeseries, authen, projRead)
	app.HandlerFunc(http.MethodGet, version, "/projects/{project_id}/logs/aggregate", a.aggregate, authen, projRead)
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
}

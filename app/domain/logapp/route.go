package logapp

import (
	"net/http"

	"github.com/jkarage/logingestor/app/sdk/authclient"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/logbus"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jkarage/logingestor/foundation/web"
)

// Config contains all the mandatory systems required by handlers.
type Config struct {
	Log            *logger.Logger
	AuthClient     authclient.Authenticator
	LogBus         logbus.ExtBusiness
	ProjectBus     projectbus.ExtBusiness
	Hub            *Hub
	AllowedOrigins []string
}

// Routes adds specific routes for this group.
func Routes(app *web.App, cfg Config) {
	const version = "v1"

	authen := mid.Authenticate(cfg.AuthClient)

	a := newApp(cfg.LogBus, cfg.ProjectBus, cfg.Hub, cfg.AuthClient, cfg.AllowedOrigins)

	app.HandlerFunc(http.MethodPost, version, "/ingest", a.ingest, authen)
	app.HandlerFunc(http.MethodGet, version, "/projects/{project_id}/logs", a.query, authen)
	app.HandlerFunc(http.MethodGet, version, "/projects/{project_id}/logs/stats", a.stats, authen)

	// The stream endpoint upgrades to WebSocket. It MUST bypass the app-level
	// middleware stack (logging, error handling, panics) because those middleware
	// functions capture the http.ResponseWriter before the upgrade and may write
	// to it after the connection has been hijacked, which corrupts the WS frames
	// and causes "WebSocket is closed before the connection is established".
	// Authentication is handled manually inside a.stream via the ?token= param.
	app.RawHandlerFuncNoMid(http.MethodGet, version, "/projects/{project_id}/logs/stream", a.stream)
}

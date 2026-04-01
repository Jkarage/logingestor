package logapp

import (
	"net/http"

	"github.com/jkarage/logingestor/app/sdk/authclient"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/logbus"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jkarage/logingestor/foundation/web"
)

// Config contains all the mandatory systems required by handlers.
type Config struct {
	Log        *logger.Logger
	AuthClient authclient.Authenticator
	LogBus     logbus.ExtBusiness
	Hub        *Hub
}

// Routes adds specific routes for this group.
func Routes(app *web.App, cfg Config) {
	const version = "v1"

	authen := mid.Authenticate(cfg.AuthClient)

	a := newApp(cfg.LogBus, cfg.Hub)

	app.HandlerFunc(http.MethodPost, version, "/ingest", a.ingest, authen)
	app.HandlerFunc(http.MethodGet, version, "/projects/{project_id}/logs", a.query, authen)
	app.HandlerFunc(http.MethodGet, version, "/projects/{project_id}/logs/stats", a.stats, authen)
	app.RawHandlerFunc(http.MethodGet, version, "/projects/{project_id}/logs/stream", a.stream)
}

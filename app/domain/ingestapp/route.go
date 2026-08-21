package ingestapp

import (
	"net/http"

	"github.com/jkarage/logingestor/app/domain/logapp"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/logbus"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/business/domain/rejectbus"
	"github.com/jkarage/logingestor/business/domain/sourcebus"
	"github.com/jkarage/logingestor/business/domain/usagebus"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jkarage/logingestor/foundation/web"
)

// Config contains all the mandatory systems required by handlers.
type Config struct {
	Log        *logger.Logger
	LogBus     logbus.ExtBusiness
	SourceBus  sourcebus.ExtBusiness
	ProjectBus projectbus.ExtBusiness
	UsageBus   usagebus.ExtBusiness
	Hub        *logapp.Hub

	// RejectBus keeps a sample of refused records. Nil counts them and keeps
	// none, which is how it behaved before the dead-letter store existed.
	RejectBus *rejectbus.Business
}

// Routes adds the infrastructure-log ingestion routes. These are authenticated
// by a source-scoped ingest key (ls_src_live_…), not a user JWT.
func Routes(app *web.App, cfg Config) {
	const version = "v1"

	sourceAuth := mid.AuthenticateSource(cfg.SourceBus, cfg.ProjectBus)

	a := newApp(cfg)

	app.HandlerFunc(http.MethodPost, version, "/ingest/bulk", a.bulk, sourceAuth)
	app.HandlerFunc(http.MethodPost, version, "/ingest/otlp", a.otlp, sourceAuth)
}

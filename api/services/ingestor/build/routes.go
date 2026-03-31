package build

import (
	"github.com/jkarage/logingestor/app/domain/auditapp"
	"github.com/jkarage/logingestor/app/domain/checkapp"
	"github.com/jkarage/logingestor/app/domain/orgapp"
	"github.com/jkarage/logingestor/app/domain/projectapp"
	"github.com/jkarage/logingestor/app/domain/userapp"
	"github.com/jkarage/logingestor/app/sdk/mux"
	"github.com/jkarage/logingestor/foundation/web"
)

// Routes binds all the routes for the sales service.
func Routes() all {
	return all{}
}

type all struct{}

// Add implements the RouterAdder interface.
func (all) Add(app *web.App, cfg mux.Config) {
	checkapp.Routes(app, checkapp.Config{
		Build: cfg.Build,
		Log:   cfg.Log,
		DB:    cfg.DB,
	})

	userapp.Routes(app, userapp.Config{
		Log:          cfg.Log,
		UserBus:      cfg.BusConfig.UserBus,
		AuthClient:   cfg.IngestorConfig.AuthClient,
		Auth:         cfg.AuthConfig.Auth,
		SigningKey:   cfg.SigningKey,
		EmailBaseURL: cfg.EmailBaseURL,
		Mailer:       cfg.EmailConfig,
	})

	auditapp.Routes(app, auditapp.Config{
		Log:        cfg.Log,
		AuditBus:   cfg.BusConfig.AuditBus,
		AuthClient: cfg.IngestorConfig.AuthClient,
	})

	orgapp.Routes(app, orgapp.Config{
		Log:        cfg.Log,
		Auth:       cfg.AuthConfig.Auth,
		AuthClient: cfg.IngestorConfig.AuthClient,
		UserBus:    cfg.BusConfig.UserBus,
		OrgBus:     cfg.BusConfig.OrgBus,
	})

	projectapp.Routes(app, projectapp.Config{
		Log:        cfg.Log,
		Auth:       cfg.AuthConfig.Auth,
		AuthClient: cfg.IngestorConfig.AuthClient,
		UserBus:    cfg.BusConfig.UserBus,
		ProjectBus: cfg.BusConfig.ProjectBus,
	})
}

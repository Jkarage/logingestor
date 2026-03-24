// Package build manages different build options.
package build

import (
	"github.com/jkarage/logingestor/app/domain/authapp"
	"github.com/jkarage/logingestor/app/domain/checkapp"
	"github.com/jkarage/logingestor/app/sdk/mux"
	"github.com/jkarage/logingestor/foundation/web"
)

// Routes binds all the routes for the auth service.
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

	authapp.Routes(app, authapp.Config{
		UserBus: cfg.BusConfig.UserBus,
		Auth:    cfg.AuthConfig.Auth,
	})
}

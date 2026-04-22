package userapp

import (
	"net/http"

	"github.com/jkarage/logingestor/app/sdk/auth"
	"github.com/jkarage/logingestor/app/sdk/authclient"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/userbus"
	emailer "github.com/jkarage/logingestor/foundation/email"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jkarage/logingestor/foundation/web"
)

// Config contains all the mandatory systems required by handlers.
type Config struct {
	EmailBaseURL string
	SigningKey   string
	Mailer       *emailer.Config
	Log          *logger.Logger
	Auth         *auth.Auth
	AuthClient   authclient.Authenticator
	UserBus      userbus.ExtBusiness
}

// Routes adds specific routes for this group.
func Routes(app *web.App, cfg Config) {
	const version = "v1"

	authen := mid.Authenticate(cfg.AuthClient)
	_ = mid.Authorize(cfg.AuthClient, auth.RuleAdminOnly)
	ruleAuthorizeUser := mid.AuthorizeUser(cfg.AuthClient, cfg.UserBus, auth.RuleOrgAdminOnly)
	ruleAuthorizeAdmin := mid.AuthorizeUser(cfg.AuthClient, cfg.UserBus, auth.RuleAdminOnly)

	api := newApp(cfg.EmailBaseURL, cfg.SigningKey, cfg.Mailer, cfg.UserBus, cfg.Auth)

	app.HandlerFunc(http.MethodGet, version, "/users", api.query, authen, ruleAuthorizeAdmin)
	app.HandlerFunc(http.MethodGet, version, "/users/me", api.queryMe, authen)
	app.HandlerFunc(http.MethodGet, version, "/users/{user_id}", api.queryByID, authen, ruleAuthorizeUser)
	app.HandlerFunc(http.MethodPost, version, "/users", api.create)
	app.HandlerFunc(http.MethodPut, version, "/users/role/{user_id}", api.updateRole, authen)
	app.HandlerFunc(http.MethodPut, version, "/users/{user_id}", api.update, authen, ruleAuthorizeUser)
	app.HandlerFunc(http.MethodDelete, version, "/users/{user_id}", api.delete, authen, ruleAuthorizeUser)
	app.HandlerFunc(http.MethodPost, version, "/users/verify", api.verify)
}

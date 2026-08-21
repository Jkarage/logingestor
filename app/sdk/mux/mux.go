// Package mux provides support to bind domain level routes
// to the application mux.
package mux

import (
	"embed"
	"net/http"

	"github.com/jkarage/logingestor/app/domain/logapp"
	"github.com/jkarage/logingestor/app/sdk/auth"
	"github.com/jkarage/logingestor/app/sdk/authclient"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/analyzebus"
	"github.com/jkarage/logingestor/business/domain/annotationbus"
	"github.com/jkarage/logingestor/business/domain/apikeybus"
	"github.com/jkarage/logingestor/business/domain/auditbus"
	"github.com/jkarage/logingestor/business/domain/clienterrorbus"
	"github.com/jkarage/logingestor/business/domain/integrationbus"
	"github.com/jkarage/logingestor/business/domain/invitationbus"
	"github.com/jkarage/logingestor/business/domain/logbus"
	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/business/domain/scimbus"
	"github.com/jkarage/logingestor/business/domain/sourcebus"
	"github.com/jkarage/logingestor/business/domain/ssobus"
	"github.com/jkarage/logingestor/business/domain/usagebus"
	"github.com/jkarage/logingestor/business/domain/userbus"
	"github.com/jkarage/logingestor/business/domain/viewbus"
	emailer "github.com/jkarage/logingestor/foundation/email"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jkarage/logingestor/foundation/web"
	"github.com/jmoiron/sqlx"
	"go.opentelemetry.io/otel/trace"
)

// StaticSite represents a static site to run.
type StaticSite struct {
	react      bool
	static     embed.FS
	staticDir  string
	staticPath string
}

// Options represent optional parameters.
type Options struct {
	corsOrigin []string
	sites      []StaticSite
}

// WithCORS provides configuration options for CORS.
func WithCORS(origins []string) func(opts *Options) {
	return func(opts *Options) {
		opts.corsOrigin = origins
	}
}

// WithFileServer provides configuration options for file server.
func WithFileServer(react bool, static embed.FS, dir string, path string) func(opts *Options) {
	return func(opts *Options) {
		opts.sites = append(opts.sites, StaticSite{
			react:      react,
			static:     static,
			staticDir:  dir,
			staticPath: path,
		})
	}
}

// IngestorConfig contains sales service specific config.
type IngestorConfig struct {
	AuthClient authclient.Authenticator
}

// AuthConfig contains auth service specific config.
type AuthConfig struct {
	Auth *auth.Auth
}

type BusConfig struct {
	UserBus        userbus.ExtBusiness
	OrgBus         orgbus.ExtBusiness
	AuditBus       auditbus.ExtBusiness
	ProjectBus     projectbus.ExtBusiness
	InvitationBus  invitationbus.ExtBusiness
	LogBus         logbus.ExtBusiness
	IntegrationBus *integrationbus.Business
	AnalyzeBus     *analyzebus.Business
	SourceBus      sourcebus.ExtBusiness
	UsageBus       usagebus.ExtBusiness
	SSOBus         *ssobus.Business
	SCIMBus        *scimbus.Business
	ViewBus        *viewbus.Business
	AnnotationBus  *annotationbus.Business
	ClientErrorBus *clienterrorbus.Business
	APIKeyBus      *apikeybus.Business
}

// SSOConfig contains single sign-on endpoint configuration.
type SSOConfig struct {
	CallbackURL          string
	CompleteURL          string
	RequireVerifiedEmail bool
}

// BillingConfig contains Stripe billing configuration.
type BillingConfig struct {
	StripeSecretKey     string
	StripeWebhookSecret string
	AppBaseURL          string
}

// Config contains all the mandatory systems required by handlers.
type Config struct {
	// ClientErrorUploadToken authenticates CI's source map uploads.
	ClientErrorUploadToken string

	// ClientErrorSpikes is the spike threshold set, shared by the background
	// detector and the endpoint that reports what is spiking now.
	ClientErrorSpikes clienterrorbus.SpikeConfig

	Build          string
	Log            *logger.Logger
	DB             *sqlx.DB
	Tracer         trace.Tracer
	BusConfig      BusConfig
	IngestorConfig IngestorConfig
	AuthConfig     AuthConfig
	BillingConfig  BillingConfig
	SSOConfig      SSOConfig
	EmailConfig    *emailer.Config
	EmailBaseURL   string
	SupportEmail   string
	SigningKey     string
	SCIMBaseURL    string
	LogHub         *logapp.Hub
	AllowedOrigins []string
}

// RouteAdder defines behavior that sets the routes to bind for an instance
// of the service.
type RouteAdder interface {
	Add(app *web.App, cfg Config)
}

// WebAPI constructs a http.Handler with all application routes bound.
func WebAPI(cfg Config, routeAdder RouteAdder, options ...func(opts *Options)) http.Handler {
	app := web.NewApp(
		cfg.Log.Info,
		nil,
		mid.Logger(cfg.Log),
		mid.Errors(cfg.Log),
		mid.Metrics(),
		mid.Panics(),
	)

	var opts Options
	for _, option := range options {
		option(&opts)
	}

	if len(opts.corsOrigin) > 0 {
		app.EnableCORS(opts.corsOrigin)
	}

	routeAdder.Add(app, cfg)

	for _, site := range opts.sites {
		switch site.react {
		case true:
			app.FileServerReact(site.static, site.staticDir, site.staticPath)

		default:
			app.FileServer(site.static, site.staticDir, site.staticPath)
		}
	}

	return app
}

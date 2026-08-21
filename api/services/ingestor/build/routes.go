package build

import (
	"github.com/jkarage/logingestor/app/domain/annotationapp"
	"github.com/jkarage/logingestor/app/domain/apikeyapp"
	"github.com/jkarage/logingestor/app/domain/auditapp"
	"github.com/jkarage/logingestor/app/domain/billingapp"
	"github.com/jkarage/logingestor/app/domain/checkapp"
	"github.com/jkarage/logingestor/app/domain/clienterrorapp"
	"github.com/jkarage/logingestor/app/domain/contactapp"
	"github.com/jkarage/logingestor/app/domain/ingestapp"
	"github.com/jkarage/logingestor/app/domain/integrationapp"
	"github.com/jkarage/logingestor/app/domain/invitationapp"
	"github.com/jkarage/logingestor/app/domain/logapp"
	"github.com/jkarage/logingestor/app/domain/orgapp"
	"github.com/jkarage/logingestor/app/domain/projectapp"
	"github.com/jkarage/logingestor/app/domain/scimapp"
	"github.com/jkarage/logingestor/app/domain/sourceapp"
	"github.com/jkarage/logingestor/app/domain/ssoapp"
	"github.com/jkarage/logingestor/app/domain/usageapp"
	"github.com/jkarage/logingestor/app/domain/userapp"
	"github.com/jkarage/logingestor/app/domain/viewapp"
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
		OrgBus:       cfg.BusConfig.OrgBus,
		AuthClient:   cfg.IngestorConfig.AuthClient,
		Auth:         cfg.AuthConfig.Auth,
		SigningKey:   cfg.SigningKey,
		EmailBaseURL: cfg.EmailBaseURL,
		Mailer:       cfg.EmailConfig,
	})

	auditapp.Routes(app, auditapp.Config{
		Log:        cfg.Log,
		AuditBus:   cfg.BusConfig.AuditBus,
		OrgBus:     cfg.BusConfig.OrgBus,
		UserBus:    cfg.BusConfig.UserBus,
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
		OrgBus:     cfg.BusConfig.OrgBus,
		ProjectBus: cfg.BusConfig.ProjectBus,
	})

	invitationapp.Routes(app, invitationapp.Config{
		Log:           cfg.Log,
		Auth:          cfg.AuthConfig.Auth,
		AuthClient:    cfg.IngestorConfig.AuthClient,
		UserBus:       cfg.BusConfig.UserBus,
		OrgBus:        cfg.BusConfig.OrgBus,
		ProjectBus:    cfg.BusConfig.ProjectBus,
		InvitationBus: cfg.BusConfig.InvitationBus,
		Mailer:        cfg.EmailConfig,
		EmailBaseURL:  cfg.EmailBaseURL,
		SigningKey:    cfg.SigningKey,
	})

	logapp.Routes(app, logapp.Config{
		Log:            cfg.Log,
		AuthClient:     cfg.IngestorConfig.AuthClient,
		LogBus:         cfg.BusConfig.LogBus,
		OrgBus:         cfg.BusConfig.OrgBus,
		ProjectBus:     cfg.BusConfig.ProjectBus,
		AnalyzeBus:     cfg.BusConfig.AnalyzeBus,
		Hub:            cfg.LogHub,
		AllowedOrigins: cfg.AllowedOrigins,
		UsageBus:       cfg.BusConfig.UsageBus,
		APIKeyBus:      cfg.BusConfig.APIKeyBus,
	})

	sourceapp.Routes(app, sourceapp.Config{
		Log:        cfg.Log,
		Auth:       cfg.AuthConfig.Auth,
		AuthClient: cfg.IngestorConfig.AuthClient,
		UserBus:    cfg.BusConfig.UserBus,
		OrgBus:     cfg.BusConfig.OrgBus,
		ProjectBus: cfg.BusConfig.ProjectBus,
		SourceBus:  cfg.BusConfig.SourceBus,
		UsageBus:   cfg.BusConfig.UsageBus,
	})

	ingestapp.Routes(app, ingestapp.Config{
		Log:        cfg.Log,
		LogBus:     cfg.BusConfig.LogBus,
		SourceBus:  cfg.BusConfig.SourceBus,
		ProjectBus: cfg.BusConfig.ProjectBus,
		UsageBus:   cfg.BusConfig.UsageBus,
		Hub:        cfg.LogHub,
	})

	usageapp.Routes(app, usageapp.Config{
		Log:        cfg.Log,
		AuthClient: cfg.IngestorConfig.AuthClient,
		UsageBus:   cfg.BusConfig.UsageBus,
		OrgBus:     cfg.BusConfig.OrgBus,
	})

	annotationapp.Routes(app, annotationapp.Config{
		Log:           cfg.Log,
		AuthClient:    cfg.IngestorConfig.AuthClient,
		AnnotationBus: cfg.BusConfig.AnnotationBus,
		LogBus:        cfg.BusConfig.LogBus,
		OrgBus:        cfg.BusConfig.OrgBus,
		ProjectBus:    cfg.BusConfig.ProjectBus,
	})

	clienterrorapp.Routes(app, clienterrorapp.Config{
		Log:            cfg.Log,
		AuthClient:     cfg.IngestorConfig.AuthClient,
		ClientErrorBus: cfg.BusConfig.ClientErrorBus,
		OrgBus:         cfg.BusConfig.OrgBus,
		ProjectBus:     cfg.BusConfig.ProjectBus,
		AllowedOrigins: cfg.AllowedOrigins,
		UploadToken:    cfg.ClientErrorUploadToken,
	})

	apikeyapp.Routes(app, apikeyapp.Config{
		Log:        cfg.Log,
		AuthClient: cfg.IngestorConfig.AuthClient,
		APIKeyBus:  cfg.BusConfig.APIKeyBus,
		OrgBus:     cfg.BusConfig.OrgBus,
		ProjectBus: cfg.BusConfig.ProjectBus,
	})

	viewapp.Routes(app, viewapp.Config{
		Log:        cfg.Log,
		AuthClient: cfg.IngestorConfig.AuthClient,
		ViewBus:    cfg.BusConfig.ViewBus,
		OrgBus:     cfg.BusConfig.OrgBus,
		ProjectBus: cfg.BusConfig.ProjectBus,
	})

	scimapp.Routes(app, scimapp.Config{
		Log:          cfg.Log,
		SCIMBus:      cfg.BusConfig.SCIMBus,
		OrgBus:       cfg.BusConfig.OrgBus,
		UserBus:      cfg.BusConfig.UserBus,
		DefaultRoles: scimapp.SSODefaultRole{SSOBus: cfg.BusConfig.SSOBus},
		BaseURL:      cfg.SCIMBaseURL,
	})

	ssoapp.Routes(app, ssoapp.Config{
		Log:                  cfg.Log,
		Auth:                 cfg.AuthConfig.Auth,
		AuthClient:           cfg.IngestorConfig.AuthClient,
		SSOBus:               cfg.BusConfig.SSOBus,
		SCIMBus:              cfg.BusConfig.SCIMBus,
		OrgBus:               cfg.BusConfig.OrgBus,
		UserBus:              cfg.BusConfig.UserBus,
		SigningKey:           cfg.SigningKey,
		CallbackURL:          cfg.SSOConfig.CallbackURL,
		CompleteURL:          cfg.SSOConfig.CompleteURL,
		RequireVerifiedEmail: cfg.SSOConfig.RequireVerifiedEmail,
	})

	contactapp.Routes(app, contactapp.Config{
		Mailer:       cfg.EmailConfig,
		SupportEmail: cfg.SupportEmail,
	})

	integrationapp.Routes(app, integrationapp.Config{
		Log:            cfg.Log,
		Auth:           cfg.AuthConfig.Auth,
		AuthClient:     cfg.IngestorConfig.AuthClient,
		UserBus:        cfg.BusConfig.UserBus,
		OrgBus:         cfg.BusConfig.OrgBus,
		ProjectBus:     cfg.BusConfig.ProjectBus,
		IntegrationBus: cfg.BusConfig.IntegrationBus,
		AuditBus:       cfg.BusConfig.AuditBus,
	})

	billingapp.Routes(app, billingapp.Config{
		Log:                 cfg.Log,
		AuthClient:          cfg.IngestorConfig.AuthClient,
		UserBus:             cfg.BusConfig.UserBus,
		OrgBus:              cfg.BusConfig.OrgBus,
		StripeSecretKey:     cfg.BillingConfig.StripeSecretKey,
		StripeWebhookSecret: cfg.BillingConfig.StripeWebhookSecret,
		AppBaseURL:          cfg.BillingConfig.AppBaseURL,
	})

}

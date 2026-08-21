package main

import (
	"context"
	"errors"
	"expvar"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"encoding/hex"

	"github.com/ardanlabs/conf/v3"
	"github.com/jmoiron/sqlx"

	"github.com/jkarage/logingestor/api/services/ingestor/build"
	"github.com/jkarage/logingestor/app/domain/logapp"
	"github.com/jkarage/logingestor/app/sdk/auth"
	http2 "github.com/jkarage/logingestor/app/sdk/authclient/http"
	"github.com/jkarage/logingestor/app/sdk/debug"
	"github.com/jkarage/logingestor/app/sdk/mux"
	"github.com/jkarage/logingestor/business/domain/analyzebus"
	"github.com/jkarage/logingestor/business/domain/annotationbus"
	"github.com/jkarage/logingestor/business/domain/annotationbus/stores/annotationdb"
	"github.com/jkarage/logingestor/business/domain/apikeybus"
	"github.com/jkarage/logingestor/business/domain/apikeybus/stores/apikeydb"
	"github.com/jkarage/logingestor/business/domain/auditbus"
	"github.com/jkarage/logingestor/business/domain/auditbus/extensions/auditotel"
	"github.com/jkarage/logingestor/business/domain/auditbus/stores/auditdb"
	"github.com/jkarage/logingestor/business/domain/clienterrorbus"
	"github.com/jkarage/logingestor/business/domain/clienterrorbus/stores/clienterrordb"
	"github.com/jkarage/logingestor/business/domain/integrationbus"
	"github.com/jkarage/logingestor/business/domain/integrationbus/providers"
	"github.com/jkarage/logingestor/business/domain/integrationbus/stores/integrationdb"
	"github.com/jkarage/logingestor/business/domain/invitationbus"
	"github.com/jkarage/logingestor/business/domain/invitationbus/extensions/invitationaudit"
	"github.com/jkarage/logingestor/business/domain/invitationbus/extensions/invitationotel"
	"github.com/jkarage/logingestor/business/domain/invitationbus/stores/invitationdb"
	"github.com/jkarage/logingestor/business/domain/logbus"
	"github.com/jkarage/logingestor/business/domain/logbus/extensions/logalert"
	"github.com/jkarage/logingestor/business/domain/logbus/extensions/logotel"
	"github.com/jkarage/logingestor/business/domain/logbus/stores/logdb"
	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/business/domain/orgbus/extensions/orgaudit"
	"github.com/jkarage/logingestor/business/domain/orgbus/extensions/orgotel"
	"github.com/jkarage/logingestor/business/domain/orgbus/stores/orgdb"
	"github.com/jkarage/logingestor/business/domain/projectbus"
	"github.com/jkarage/logingestor/business/domain/projectbus/extensions/projectaudit"
	"github.com/jkarage/logingestor/business/domain/projectbus/extensions/projectotel"
	"github.com/jkarage/logingestor/business/domain/projectbus/stores/projectdb"
	"github.com/jkarage/logingestor/business/domain/scimbus"
	"github.com/jkarage/logingestor/business/domain/scimbus/stores/scimdb"
	"github.com/jkarage/logingestor/business/domain/sourcebus"
	"github.com/jkarage/logingestor/business/domain/sourcebus/extensions/sourceaudit"
	"github.com/jkarage/logingestor/business/domain/sourcebus/extensions/sourceotel"
	"github.com/jkarage/logingestor/business/domain/sourcebus/stores/sourcedb"
	"github.com/jkarage/logingestor/business/domain/ssobus"
	"github.com/jkarage/logingestor/business/domain/ssobus/stores/ssodb"
	"github.com/jkarage/logingestor/business/domain/usagebus"
	"github.com/jkarage/logingestor/business/domain/usagebus/stores/usagedb"
	"github.com/jkarage/logingestor/business/domain/userbus"
	"github.com/jkarage/logingestor/business/domain/userbus/extensions/useraudit"
	"github.com/jkarage/logingestor/business/domain/userbus/extensions/userotel"
	"github.com/jkarage/logingestor/business/domain/userbus/stores/usercache"
	"github.com/jkarage/logingestor/business/domain/userbus/stores/userdb"
	"github.com/jkarage/logingestor/business/domain/viewbus"
	"github.com/jkarage/logingestor/business/domain/viewbus/stores/viewdb"
	"github.com/jkarage/logingestor/business/sdk/alerting"
	"github.com/jkarage/logingestor/business/sdk/auditexport"
	"github.com/jkarage/logingestor/business/sdk/retention"
	"github.com/jkarage/logingestor/business/sdk/sqldb"
	"github.com/jkarage/logingestor/business/sdk/sqldb/delegate"
	emailer "github.com/jkarage/logingestor/foundation/email"
	"github.com/jkarage/logingestor/foundation/keystore"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jkarage/logingestor/foundation/otel"
)

var tag = "develop"

func main() {
	var log *logger.Logger

	events := logger.Events{
		Error: func(ctx context.Context, r logger.Record) {
			log.Info(ctx, "******* SEND ALERT *******")
		},
	}

	log = logger.NewWithEvents(os.Stdout, logger.LevelInfo, "INGESTOR", otel.GetTraceID, events)

	// -------------------------------------------------------------------------

	ctx := context.Background()

	if err := run(ctx, log); err != nil {
		log.Error(ctx, "startup", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log *logger.Logger) error {

	// -------------------------------------------------------------------------
	// GOMAXPROCS

	log.Info(ctx, "startup", "GOMAXPROCS", runtime.GOMAXPROCS(0))

	// -------------------------------------------------------------------------
	// Configuration

	cfg := struct {
		conf.Version
		Web struct {
			ReadTimeout        time.Duration `conf:"default:10s"`
			WriteTimeout       time.Duration `conf:"default:20s"`
			IdleTimeout        time.Duration `conf:"default:120s"`
			ShutdownTimeout    time.Duration `conf:"default:30s"`
			APIHost            string        `conf:"default:0.0.0.0:3002"`
			DebugHost          string        `conf:"default:0.0.0.0:3012"`
			CORSAllowedOrigins []string      `conf:"default:*"`
		}
		DB struct {
			User            string        `conf:"default:postgres,env:DB_USERNAME"`
			Password        string        `conf:"default:postgres,env:DB_PASSWORD,mask"`
			Host            string        `conf:"default:12.13.14.15:5432,env:DB_HOST"`
			Name            string        `conf:"default:bsa,env:DB_NAME"`
			MaxIdleConns    int           `conf:"default:25"`
			MaxOpenConns    int           `conf:"default:25"`
			DisableTLS      bool          `conf:"default:true"`
			ConnMaxLifetime time.Duration `conf:"default:2m"`
			ConnMaxIdleTime time.Duration `conf:"default:1m"`
		}
		Retention struct {
			// The worker exists because retention was previously a manual admin
			// command that nobody ran, letting one project reach 50M rows.
			Enabled    bool          `conf:"default:true"`
			Interval   time.Duration `conf:"default:1h"`
			StartDelay time.Duration `conf:"default:5m"`
			BatchSize  int           `conf:"default:10000"`
			MaxRows    int           `conf:"default:2000000"`
			MaxRuntime time.Duration `conf:"default:10m"`

			// AuditDays defaults to -1 (keep forever): audit is a compliance
			// surface, so ageing it out is an explicit choice.
			AuditDays int `conf:"default:-1"`

			// SourceStatsDays bounds the per-source hourly counters behind source
			// health. Health reads 24 hours; -1 keeps them forever.
			SourceStatsDays int `conf:"default:14"`
		}
		Auth struct {
			Host       string `conf:"default:http://localhost:6000"`
			KeysFolder string `conf:"default:zarf/keys/"`
			ActiveKID  string `conf:"default:231c6f21-0207-4d5c-bc83-a4fdbd5cb06f"`
			Issuer     string `conf:"default:confirm mail"`
		}
		Resend struct {
			APIKey       string `conf:"default:xxxxxxxxxxxxxxxxxxxxx,env:RESEND_API_KEY,mask"`
			From         string `conf:"default:info@streamlogia.com"`
			FromName     string `conf:"default:Info"`
			EmailBaseURL string `conf:"default:https://streamlogia.com"`
			SupportEmail string `conf:"default:support@streamlogia.com"`
		}
		Tempo struct {
			Host        string  `conf:"default:tempo:4317"`
			ServiceName string  `conf:"default:sales"`
			Probability float64 `conf:"default:0.05"`
			// Shouldn't use a high Probability value in non-developer systems.
			// 0.05 should be enough for most systems. Some might want to have
			// this even lower.
		}
		Integration struct {
			// EncryptionKey must be a 64-character hex string (32 bytes → AES-256).
			// Generate with: openssl rand -hex 32
			EncryptionKey string `conf:"default:0000000000000000000000000000000000000000000000000000000000000000,env:INTEGRATION_ENCRYPTION_KEY,mask"`
		}
		ClientErrors struct {
			// The grouping worker. Ingest stores events synchronously and returns
			// 202; this is what turns them into issues.
			Enabled   bool          `conf:"default:true"`
			Interval  time.Duration `conf:"default:5s"`
			BatchSize int           `conf:"default:200"`

			// EventDays and IssueDays bound retention. Raw events are the bulk and
			// go first; issues are small and are what a human triages, so they
			// live longer.
			EventDays int `conf:"default:30"`
			IssueDays int `conf:"default:180"`
		}
		Alerting struct {
			// Threshold rules are decided on a timer because a threshold is a
			// statement about a window, not about any single log.
			Enabled  bool          `conf:"default:true"`
			Interval time.Duration `conf:"default:1m"`
		}
		AuditExport struct {
			// URL is the SIEM ingest endpoint. Empty disables export.
			// No default tag: conf rejects an empty default value, and a string
			// zero-values to "" anyway, which is what disables export.
			URL       string        `conf:"env:AUDIT_EXPORT_URL"`
			Token     string        `conf:"env:AUDIT_EXPORT_TOKEN,mask"`
			Interval  time.Duration `conf:"default:1m"`
			BatchSize int           `conf:"default:500"`
			Timeout   time.Duration `conf:"default:30s"`
		}
		SSO struct {
			// CallbackURL must be registered as a redirect URI with every
			// configured identity provider.
			//
			// This and CompleteURL default to the deployed hosts rather than to
			// localhost. A localhost default fails silently and only in
			// production: the service starts, the config looks set, and the
			// browser is sent to a machine that is not the user's — which is
			// exactly what happened here until it was measured.
			CallbackURL string `conf:"default:https://api.streamlogia.com/v1/auth/sso/callback,env:SSO_CALLBACK_URL"`

			// CompleteURL is the frontend route the browser lands on afterwards.
			// It must be the page that performs the code exchange and renders
			// ?error=, which in this app is /login.
			CompleteURL string `conf:"default:https://streamlogia.com/login,env:SSO_COMPLETE_URL"`

			// RequireVerifiedEmail rejects identities whose email_verified claim is
			// not true. Turning this off allows an IdP that lets users self-assert
			// an address to take over an existing account.
			RequireVerifiedEmail bool `conf:"default:true,env:SSO_REQUIRE_VERIFIED_EMAIL"`

			// SCIMBaseURL is this SCIM service's public root, used to build
			// resource Location values that IdPs follow back. The /v1 segment is
			// part of the path every route in this service is registered under.
			SCIMBaseURL string `conf:"default:https://api.streamlogia.com/v1/scim/v2,env:SCIM_BASE_URL"`
		}
		AI struct {
			CerebriumAPIKey  string `conf:"default:xxxxxxxxxxxxx,env:AI_CEREBRIUM_API_KEY,mask"`
			CerebriumBaseURL string `conf:"default:xxxxxxxxxxxxx,env:AI_CEREBRIUM_BASE_URL"`
		}
		Stripe struct {
			SecretKey     string `conf:"default:sk_test_placeholder,env:STRIPE_SECRET_KEY,mask"`
			WebhookSecret string `conf:"default:whsec_placeholder,env:STRIPE_WEBHOOK_SECRET,mask"`
			ProPriceID    string `conf:"default:price_placeholder,env:STRIPE_PRO_PRICE_ID"`
			// AppBaseURL is where Stripe returns the browser after checkout. It is
			// the app's own host, which is the apex domain — app.streamlogia.com
			// has no DNS record.
			AppBaseURL string `conf:"default:https://streamlogia.com,env:APP_BASE_URL"`
		}
	}{
		Version: conf.Version{
			Build: tag,
			Desc:  "Ingestor",
		},
	}

	const prefix = "INGESTOR"
	help, err := conf.Parse(prefix, &cfg)
	if err != nil {
		if errors.Is(err, conf.ErrHelpWanted) {
			fmt.Println(help)
			return nil
		}
		return fmt.Errorf("parsing config: %w", err)
	}

	// -------------------------------------------------------------------------
	// App Starting

	log.Info(ctx, "starting service", "version", cfg.Build)
	defer log.Info(ctx, "shutdown complete")

	out, err := conf.String(&cfg)
	if err != nil {
		return fmt.Errorf("generating config for output: %w", err)
	}
	log.Info(ctx, "startup", "config", out)

	// Logged on their own because these three are the values that break SSO
	// without any error to point at: the browser simply lands somewhere else.
	// Having them in the startup log makes a misconfigured deployment checkable
	// without reproducing a login.
	log.Info(ctx, "startup", "status", "sso endpoints",
		"callback_url", cfg.SSO.CallbackURL,
		"complete_url", cfg.SSO.CompleteURL,
		"scim_base_url", cfg.SSO.SCIMBaseURL)

	log.BuildInfo(ctx)

	expvar.NewString("build").Set(cfg.Build)

	// -------------------------------------------------------------------------
	// Database Support

	log.Info(ctx, "startup", "status", "initializing database support", "hostport", cfg.DB.Host)

	db, err := sqldb.Open(sqldb.Config{
		User:            cfg.DB.User,
		Password:        cfg.DB.Password,
		Host:            cfg.DB.Host,
		Name:            cfg.DB.Name,
		MaxIdleConns:    cfg.DB.MaxIdleConns,
		MaxOpenConns:    cfg.DB.MaxOpenConns,
		DisableTLS:      cfg.DB.DisableTLS,
		ConnMaxLifetime: cfg.DB.ConnMaxLifetime,
		ConnMaxIdleTime: cfg.DB.ConnMaxIdleTime,
	})
	if err != nil {
		return fmt.Errorf("connecting to db: %w", err)
	}

	defer db.Close()

	// -------------------------------------------------------------------------
	// Create Business Packages

	delegate := delegate.New(log)

	auditOtelExt := auditotel.NewExtension()
	auditStorage := auditdb.NewStore(log, db)
	auditBus := auditbus.NewBusiness(log, auditStorage, auditOtelExt)

	userOtelExt := userotel.NewExtension()
	userAuditExt := useraudit.NewExtension(auditBus)
	userStorage := usercache.NewStore(log, userdb.NewStore(log, db), time.Minute)
	userBus := userbus.NewBusiness(log, delegate, userStorage, userOtelExt, userAuditExt)

	orgOtelExt := orgotel.NewExtension()
	orgAuditExt := orgaudit.NewExtension(auditBus)
	orgStorage := orgdb.NewStore(log, db)
	orgBus := orgbus.NewBusiness(log, delegate, orgStorage, orgOtelExt, orgAuditExt)

	projectOtelExt := projectotel.NewExtension()
	projectAuditExt := projectaudit.NewExtension(auditBus)
	projectStorage := projectdb.NewStore(log, db)
	projectBus := projectbus.NewBusiness(log, delegate, projectStorage, projectOtelExt, projectAuditExt)

	invitationOtelExt := invitationotel.NewExtension()
	invitationAuditExt := invitationaudit.NewExtension(auditBus)
	invitationStorage := invitationdb.NewStore(log, db)
	invitationBus := invitationbus.NewBusiness(log, delegate, invitationStorage, invitationOtelExt, invitationAuditExt)

	logStorage := logdb.NewStore(log, db)

	hub := logapp.NewHub()

	// -------------------------------------------------------------------------
	// Initialize authentication support
	log.Info(ctx, "startup", "status", "initializing authentication support")

	// Check the environment first to see if a key is being provided. Then
	// load any private keys files from disk. We can assume some system like
	// Vault has created these files already. How that happens is not our
	// concern.

	ks := keystore.New()

	n, err := ks.LoadByFileSystem(os.DirFS(cfg.Auth.KeysFolder))
	if err != nil {
		return fmt.Errorf("loading keys by fs: %w", err)
	}

	if n == 0 {
		return errors.New("no keys exist")
	}

	authCfg := auth.Config{
		Log:       log,
		KeyLookup: ks,
		UserBus:   userBus,
		Issuer:    cfg.Auth.Issuer,
	}

	ath := auth.New(authCfg)

	authClient, err := http2.New(log, cfg.Auth.Host)
	if err != nil {
		log.Error(ctx, "failed to initialize authentication client", "error", err)
		return fmt.Errorf("failed to initialize authentication client: %w", err)
	}

	defer authClient.Close()

	// -------------------------------------------------------------------------
	// Email Setup
	em := emailer.New(cfg.Resend.APIKey, cfg.Resend.From, cfg.Resend.FromName)

	// -------------------------------------------------------------------------
	// Integration Bus

	encKey, err := hex.DecodeString(cfg.Integration.EncryptionKey)
	if err != nil || len(encKey) != 32 {
		return fmt.Errorf("integration encryption key must be a 64-character hex string (32 bytes): %w", err)
	}

	integrationCallers := providers.All(em)

	integrationStorage := integrationdb.NewStore(log, db, encKey)
	integrationBus := integrationbus.NewBusiness(log, integrationStorage, integrationCallers)

	// SSO provider configuration reuses the integration encryption key to seal
	// each org's OIDC client secret at rest.
	ssoStorage, err := ssodb.NewStore(log, db, encKey)
	if err != nil {
		return fmt.Errorf("sso store: %w", err)
	}
	ssoBus := ssobus.NewBusiness(log, ssoStorage)

	// SCIM provisioning tokens are hashed, not encrypted: they are only ever
	// compared, never recovered.
	scimBus := scimbus.NewBusiness(log, scimdb.NewStore(log, db))

	viewBus := viewbus.NewBusiness(log, viewdb.NewStore(log, db))
	annotationBus := annotationbus.NewBusiness(log, annotationdb.NewStore(log, db))

	// API keys are hashed like the SCIM tokens: compared, never recovered.
	apiKeyBus := apikeybus.NewBusiness(log, apikeydb.NewStore(log, db))

	// Client error monitoring. The notifier is nil for now: alert rules and
	// integration connections are project-scoped and a browser crash has no
	// project, so routing these through the existing channels needs a modelling
	// decision rather than a wire-up. Grouping and the dashboard work without it.
	clientErrorBus := clienterrorbus.NewBusiness(log, clienterrordb.NewStore(log, db), nil)

	logOtelExt := logotel.NewExtension()
	logAlertExt := logalert.NewExtension(log, projectBus, integrationBus)
	logBus := logbus.NewBusiness(log, logStorage, logOtelExt, logAlertExt)

	sourceOtelExt := sourceotel.NewExtension()
	sourceAuditExt := sourceaudit.NewExtension(auditBus)
	sourceStorage := sourcedb.NewStore(log, db)
	sourceBus := sourcebus.NewBusiness(log, sourceStorage, sourceOtelExt, sourceAuditExt)

	usageStorage := usagedb.NewStore(log, db)
	usageBus := usagebus.NewBusiness(log, usageStorage)

	analyzeBus := analyzebus.NewBusiness(log, cfg.AI.CerebriumBaseURL, cfg.AI.CerebriumAPIKey)

	// -------------------------------------------------------------------------
	// Start Debug Service

	go func() {
		log.Info(ctx, "startup", "status", "debug v1 router started", "host", cfg.Web.DebugHost)

		if err := http.ListenAndServe(cfg.Web.DebugHost, debug.Mux()); err != nil {
			log.Error(ctx, "shutdown", "status", "debug v1 router closed", "host", cfg.Web.DebugHost, "msg", err)
		}
	}()

	// -------------------------------------------------------------------------
	// Start Retention Worker

	retentionCtx, stopRetention := context.WithCancel(ctx)
	defer stopRetention()

	if cfg.Retention.Enabled {
		retentionCfg := retention.Config{
			BatchSize:       cfg.Retention.BatchSize,
			MaxRows:         cfg.Retention.MaxRows,
			MaxRuntime:      cfg.Retention.MaxRuntime,
			AuditDays:       cfg.Retention.AuditDays,
			SourceStatsDays: cfg.Retention.SourceStatsDays,

			ClientErrorEventDays: cfg.ClientErrors.EventDays,
			ClientErrorIssueDays: cfg.ClientErrors.IssueDays,
		}

		log.Info(ctx, "startup", "status", "retention worker started",
			"interval", cfg.Retention.Interval.String(),
			"start_delay", cfg.Retention.StartDelay.String(),
			"max_rows_per_run", cfg.Retention.MaxRows)

		go runRetention(retentionCtx, log, db, retentionCfg, cfg.Retention.Interval, cfg.Retention.StartDelay)
	} else {
		log.Info(ctx, "startup", "status", "retention worker disabled")
	}

	// -------------------------------------------------------------------------
	// Start Audit Export Worker

	auditExportCfg := auditexport.Config{
		URL:       cfg.AuditExport.URL,
		Token:     cfg.AuditExport.Token,
		BatchSize: cfg.AuditExport.BatchSize,
		Timeout:   cfg.AuditExport.Timeout,
	}

	if auditExportCfg.Enabled() {
		log.Info(ctx, "startup", "status", "audit export worker started",
			"interval", cfg.AuditExport.Interval.String(), "batch_size", cfg.AuditExport.BatchSize)

		go runAuditExport(retentionCtx, log, db, auditExportCfg, cfg.AuditExport.Interval)
	} else {
		log.Info(ctx, "startup", "status", "audit export disabled (no AUDIT_EXPORT_URL)")
	}

	// -------------------------------------------------------------------------
	// Start Threshold Alert Evaluator

	if cfg.Alerting.Enabled {
		log.Info(ctx, "startup", "status", "threshold alert evaluator started",
			"interval", cfg.Alerting.Interval.String())

		go runThresholdAlerts(retentionCtx, log, integrationBus, alerting.NewCounter(logStorage), cfg.Alerting.Interval)
	} else {
		log.Info(ctx, "startup", "status", "threshold alert evaluator disabled")
	}

	if cfg.ClientErrors.Enabled {
		log.Info(ctx, "startup", "status", "client error grouping worker started",
			"interval", cfg.ClientErrors.Interval.String(), "batch_size", cfg.ClientErrors.BatchSize)

		go runClientErrorGrouping(retentionCtx, log, clientErrorBus, cfg.ClientErrors.Interval, cfg.ClientErrors.BatchSize)
	} else {
		log.Info(ctx, "startup", "status", "client error grouping worker disabled")
	}

	// -------------------------------------------------------------------------
	// Start API Service
	log.Info(ctx, "startup", "status", "initializing V1 API support")

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	cfgMux := mux.Config{
		Build: tag,
		Log:   log,
		DB:    db,
		BusConfig: mux.BusConfig{
			AuditBus:       auditBus,
			UserBus:        userBus,
			OrgBus:         orgBus,
			ProjectBus:     projectBus,
			InvitationBus:  invitationBus,
			LogBus:         logBus,
			IntegrationBus: integrationBus,
			AnalyzeBus:     analyzeBus,
			SourceBus:      sourceBus,
			UsageBus:       usageBus,
			SSOBus:         ssoBus,
			SCIMBus:        scimBus,
			ViewBus:        viewBus,
			AnnotationBus:  annotationBus,
			ClientErrorBus: clientErrorBus,
			APIKeyBus:      apiKeyBus,
		},
		IngestorConfig: mux.IngestorConfig{
			AuthClient: authClient,
		},
		AuthConfig: mux.AuthConfig{
			Auth: ath,
		},
		BillingConfig: mux.BillingConfig{
			StripeSecretKey:     cfg.Stripe.SecretKey,
			StripeWebhookSecret: cfg.Stripe.WebhookSecret,
			AppBaseURL:          cfg.Stripe.AppBaseURL,
		},
		EmailConfig:  em,
		EmailBaseURL: cfg.Resend.EmailBaseURL,
		SupportEmail: cfg.Resend.SupportEmail,
		SigningKey:   cfg.Auth.ActiveKID,
		SSOConfig: mux.SSOConfig{
			CallbackURL:          cfg.SSO.CallbackURL,
			CompleteURL:          cfg.SSO.CompleteURL,
			RequireVerifiedEmail: cfg.SSO.RequireVerifiedEmail,
		},
		LogHub:         hub,
		AllowedOrigins: cfg.Web.CORSAllowedOrigins,
	}

	webAPI := mux.WebAPI(cfgMux,
		build.Routes(),
		mux.WithCORS(cfg.Web.CORSAllowedOrigins),
	)

	api := http.Server{
		Addr:         cfg.Web.APIHost,
		Handler:      webAPI,
		ReadTimeout:  cfg.Web.ReadTimeout,
		WriteTimeout: cfg.Web.WriteTimeout,
		IdleTimeout:  cfg.Web.IdleTimeout,
		ErrorLog:     logger.NewStdLogger(log, logger.LevelError),
	}

	serverErrors := make(chan error, 1)

	go func() {
		log.Info(ctx, "startup", "status", "api router started", "host", api.Addr)

		serverErrors <- api.ListenAndServe()
	}()

	// -------------------------------------------------------------------------
	// Shutdown

	select {
	case err := <-serverErrors:
		return fmt.Errorf("server error: %w", err)

	case sig := <-shutdown:
		log.Info(ctx, "shutdown", "status", "shutdown started", "signal", sig)
		defer log.Info(ctx, "shutdown", "status", "shutdown complete", "signal", sig)

		ctx, cancel := context.WithTimeout(ctx, cfg.Web.ShutdownTimeout)
		defer cancel()

		if err := api.Shutdown(ctx); err != nil {
			api.Close()
			return fmt.Errorf("could not stop server gracefully: %w", err)
		}
	}

	return nil
}

// runRetention drives the retention pass on an interval until ctx is cancelled.
//
// This deployment runs a single API instance, so an in-process ticker is
// sufficient and needs no leader election. If the service is ever scaled out,
// every replica would run its own pass concurrently — at that point move this to
// a single CronJob invoking `admin retention`, or add an advisory lock.
func runRetention(ctx context.Context, log *logger.Logger, db *sqlx.DB, cfg retention.Config, interval, startDelay time.Duration) {
	// Let the API finish coming up before competing for the database.
	select {
	case <-ctx.Done():
		return
	case <-time.After(startDelay):
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		res, err := retention.Run(ctx, log, db, cfg)
		switch {
		case err != nil && ctx.Err() != nil:
			// Shutting down; the partial pass resumes on the next boot.
			return
		case err != nil:
			log.Error(ctx, "retention run failed", "msg", err)
		case res.Incomplete:
			// Budget spent with rows still expired: come back promptly rather
			// than waiting a full interval, so a large backlog drains steadily.
			log.Info(ctx, "retention incomplete, rescheduling early", "deleted", res.Total())
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Minute):
			}
			continue
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// runAuditExport ships audit records to the configured SIEM on an interval until
// ctx is cancelled. A failed run is logged and retried on the next tick; the
// cursor does not advance past an undelivered batch, so nothing is skipped.
func runAuditExport(ctx context.Context, log *logger.Logger, db *sqlx.DB, cfg auditexport.Config, interval time.Duration) {
	exporter := auditexport.New(log, db, cfg)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		if _, err := exporter.Run(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Error(ctx, "audit export failed", "msg", err)
		}
	}
}

// runThresholdAlerts evaluates threshold alert rules on an interval until ctx is
// cancelled.
//
// Threshold rules cannot be decided from an ingest batch: "twenty errors in five
// minutes" is a property of the window, not of the log that happens to be
// twentieth. A failed pass is logged and retried on the next tick — re-notifying
// is governed by each rule's dedup window, so a missed pass cannot cause a storm.
func runThresholdAlerts(ctx context.Context, log *logger.Logger, bus *integrationbus.Business, counter integrationbus.ThresholdCounter, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		fired, err := bus.EvaluateThresholds(ctx, counter)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Error(ctx, "threshold alerts failed", "msg", err)
			continue
		}

		if fired > 0 {
			log.Info(ctx, "threshold alerts fired", "count", fired)
		}
	}
}

// runClientErrorGrouping turns stored error reports into issues on an interval
// until ctx is cancelled.
//
// The events table is the queue. Grouping is deliberately not done in the
// request: the browser is waiting, and one report the fingerprinter cannot
// handle must not be able to fail the batch that carried it. The worker drains
// in a tight loop while there is a backlog, so a burst from a crash loop is
// caught up in seconds rather than at the tick rate.
func runClientErrorGrouping(ctx context.Context, log *logger.Logger, bus *clienterrorbus.Business, interval time.Duration, batchSize int) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		// Keep draining while full batches come back, then wait for the next
		// tick. The inner bound stops a flood from monopolising the worker.
		for i := 0; i < 20; i++ {
			n, err := bus.ProcessBatch(ctx, batchSize)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Error(ctx, "client error grouping failed", "msg", err)
				break
			}

			if n < batchSize {
				break
			}
		}
	}
}

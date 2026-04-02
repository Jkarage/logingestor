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

	"github.com/ardanlabs/conf/v3"
	"github.com/jkarage/logingestor/api/services/ingestor/build"
	"github.com/jkarage/logingestor/app/domain/logapp"
	"github.com/jkarage/logingestor/app/sdk/auth"
	http2 "github.com/jkarage/logingestor/app/sdk/authclient/http"
	"github.com/jkarage/logingestor/app/sdk/debug"
	"github.com/jkarage/logingestor/app/sdk/mux"
	"github.com/jkarage/logingestor/business/domain/auditbus"
	"github.com/jkarage/logingestor/business/domain/auditbus/extensions/auditotel"
	"github.com/jkarage/logingestor/business/domain/auditbus/stores/auditdb"
	"github.com/jkarage/logingestor/business/domain/invitationbus"
	"github.com/jkarage/logingestor/business/domain/invitationbus/extensions/invitationaudit"
	"github.com/jkarage/logingestor/business/domain/invitationbus/extensions/invitationotel"
	"github.com/jkarage/logingestor/business/domain/invitationbus/stores/invitationdb"
	"github.com/jkarage/logingestor/business/domain/logbus"
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
	"github.com/jkarage/logingestor/business/domain/userbus"
	"github.com/jkarage/logingestor/business/domain/userbus/extensions/useraudit"
	"github.com/jkarage/logingestor/business/domain/userbus/extensions/userotel"
	"github.com/jkarage/logingestor/business/domain/userbus/stores/usercache"
	"github.com/jkarage/logingestor/business/domain/userbus/stores/userdb"
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
			MaxIdleConns    int           `conf:"default:0"`
			MaxOpenConns    int           `conf:"default:0"`
			DisableTLS      bool          `conf:"default:true"`
			ConnMaxLifetime time.Duration `conf:"default:2m"`
			ConnMaxIdleTime time.Duration `conf:"default:1m"`
		}
		Auth struct {
			Host       string `conf:"default:https://api.auth.streamlogia.com"`
			KeysFolder string `conf:"default:zarf/keys/"`
			ActiveKID  string `conf:"default:231c6f21-0207-4d5c-bc83-a4fdbd5cb06f"`
			Issuer     string `conf:"default:confirm mail"`
		}
		Resend struct {
			APIKey       string `conf:"default:re_FM93RW4o_fps4M3Uk8eWvkJEMytbTGG6j"`
			From         string `conf:"default:info@streamlogia.com"`
			FromName     string `conf:"default:Info"`
			EmailBaseURL string `conf:"default:https://streamlogia.com"`
		}
		Tempo struct {
			Host        string  `conf:"default:tempo:4317"`
			ServiceName string  `conf:"default:sales"`
			Probability float64 `conf:"default:0.05"`
			// Shouldn't use a high Probability value in non-developer systems.
			// 0.05 should be enough for most systems. Some might want to have
			// this even lower.
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

	logOtelExt := logotel.NewExtension()
	logStorage := logdb.NewStore(log, db)
	logBus := logbus.NewBusiness(log, logStorage, logOtelExt)

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
	// Start Debug Service

	go func() {
		log.Info(ctx, "startup", "status", "debug v1 router started", "host", cfg.Web.DebugHost)

		if err := http.ListenAndServe(cfg.Web.DebugHost, debug.Mux()); err != nil {
			log.Error(ctx, "shutdown", "status", "debug v1 router closed", "host", cfg.Web.DebugHost, "msg", err)
		}
	}()

	// -------------------------------------------------------------------------
	// Start API Service
	log.Info(ctx, "startup", "status", "initializing V1 API support")

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	cfgMux := mux.Config{
		Build: tag,
		Log:   log,
		BusConfig: mux.BusConfig{
			AuditBus:      auditBus,
			UserBus:       userBus,
			OrgBus:        orgBus,
			ProjectBus:    projectBus,
			InvitationBus: invitationBus,
			LogBus:        logBus,
		},
		IngestorConfig: mux.IngestorConfig{
			AuthClient: authClient,
		},
		AuthConfig: mux.AuthConfig{
			Auth: ath,
		},
		EmailConfig:  em,
		EmailBaseURL: cfg.Resend.EmailBaseURL,
		SigningKey:   cfg.Auth.ActiveKID,
		LogHub:       hub,
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

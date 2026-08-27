package app

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/gsoultan/pontus/api/proto/domain"
	"github.com/gsoultan/pontus/api/proto/service/serviceconnect"
	"github.com/gsoultan/pontus/pkg/auth"
	"github.com/gsoultan/pontus/pkg/config"
	"github.com/gsoultan/pontus/pkg/observability"
	obsStore "github.com/gsoultan/pontus/pkg/observability/store"
	"github.com/gsoultan/pontus/pkg/system"
	"github.com/gsoultan/pontus/server/management"
	"github.com/gsoultan/pontus/server/management/handler"
	"github.com/gsoultan/pontus/server/management/infrastructure"
	mgmtMiddleware "github.com/gsoultan/pontus/server/management/middleware"
	"github.com/gsoultan/pontus/server/management/service"
	"github.com/gsoultan/pontus/server/management/store"
	"github.com/gsoultan/pontus/server/proxy"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/cors"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// App represents the Pontus application.
type App struct {
	cfg          *config.Options
	projectStore store.Project
	userStore    store.User
	settingStore service.SettingProvider
	server       *http.Server
	backendTLS   *tls.Config
}

// NewApp creates a new instance of the Pontus application.
func NewApp(cfg *config.Options) *App {
	return new(App{cfg: cfg})
}

// Run starts the Pontus application and blocks until the context is canceled.
func (a *App) Run(ctx context.Context) error {
	var err error
	a.backendTLS, _ = proxy.CreateTLSConfig(a.cfg.BackendTLS)

	// Initialize Management DB (SQLite)
	mgmtPath, _ := system.GetDatabasePath("management.db", a.cfg.DataDir)
	mgmtDB, err := store.NewManagementDB(mgmtPath)
	if err != nil {
		return fmt.Errorf("failed to initialize management database: %w", err)
	}
	defer mgmtDB.Close()

	a.projectStore = store.NewSQLiteProject(mgmtDB)
	a.userStore = store.NewSQLiteUser(mgmtDB)
	a.settingStore = store.NewSQLiteSetting(mgmtDB)

	// Migrate from JSON if necessary
	a.migrateFromJSON()

	// Bootstrap an administrator if the store is empty.
	//
	// This used to create admin/admin123 — a published default credential on a
	// service that fronts production databases. A random password is generated
	// instead and printed once; it is never recoverable afterwards, which is
	// the point.
	if len(a.userStore.List()) == 0 {
		password, err := generatePassword()
		if err != nil {
			return fmt.Errorf("generate bootstrap admin password: %w", err)
		}
		if err := a.userStore.Upsert("admin", password, "admin"); err != nil {
			return fmt.Errorf("create bootstrap admin user: %w", err)
		}
		log.Printf("\n"+
			"========================================================\n"+
			"  No users found. Created administrator account:\n"+
			"      username: admin\n"+
			"      password: %s\n"+
			"  This password is shown once and is not recoverable.\n"+
			"  Store it now, then change it.\n"+
			"========================================================", password)
	}

	// Migrate existing raw passwords if any
	a.migratePasswords()

	// Perform migration for outdated projects.json
	MigrateProjects(a.projectStore)

	// Migrate from config.yaml if store is empty
	a.bootstrapFromConfig()

	// Initialize Log Store
	logPath, _ := system.GetDatabasePath("logs.db", a.cfg.DataDir)
	logStore, err := obsStore.NewSQLiteLogStore(logPath)
	if err != nil {
		log.Printf("Warning: Failed to initialize log store: %v", err)
	} else {
		observability.GlobalLogBroadcaster.SetStore(logStore)
		// Prune logs every hour, keep for 7 days
		observability.GlobalLogBroadcaster.StartPruner(ctx, time.Hour, 7*24*time.Hour)
		defer logStore.Close()
	}

	// Initialize Metric Store
	metricPath, _ := system.GetDatabasePath("metrics.db", a.cfg.DataDir)
	metricStore, err := obsStore.NewSQLiteMetricStore(metricPath)
	if err != nil {
		log.Printf("Warning: Failed to initialize metric store: %v", err)
	} else {
		// SetStore also hydrates the in-memory trend ring and the lifetime
		// counters from disk, so a restart does not blank the dashboard.
		observability.DefaultTracker.SetStore(metricStore)
		// Prune metrics every hour, keep for 7 days
		observability.DefaultTracker.StartPruner(ctx, time.Hour, 7*24*time.Hour)

		// Persist the final interval on the way out; without this every
		// restart discards up to a full snapshot period.
		defer func() {
			flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			observability.DefaultTracker.Flush(flushCtx)
			cancel()
			metricStore.Close()
		}()
	}

	// Start system metrics reporting
	observability.StartSystemMetricsReporting(ctx)
	observability.DefaultTracker.StartHistoryCollector(ctx)
	// Refresh the live RPS/error-rate window well inside the dashboard's poll
	// interval so the tiles show current throughput, not a lifetime average.
	observability.DefaultTracker.StartRateSampler(ctx, 5*time.Second)

	// Start Observability Sync
	observability.StartBackgroundSync(ctx, 30*time.Second)

	// Token issuer. Fail closed: without a configured key there is no safe
	// default, and inventing one would publish it in the source tree.
	issuer, err := auth.NewIssuer(a.cfg.JWTSecret)
	if err != nil {
		return fmt.Errorf("auth key is not configured: set auth_key (or jwt_secret) in config, "+
			"or PONTUS_AUTH_KEY in the environment: %w", err)
	}

	// Initialize Management Service (Multi-project aware)
	svc := infrastructure.NewService(ctx, a.projectStore, a.userStore, a.settingStore, a.cfg.DialTimeout, a.backendTLS, issuer, a.cfg)
	endpoints := management.MakeEndpoints(svc)

	// Start Management gRPC Server
	mgmtLn, err := net.Listen("tcp", a.cfg.MgmtAddr)
	if err != nil {
		return err
	}
	defer mgmtLn.Close()

	// Start Management Server (ConnectRPC + gRPC compatible)
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	mgmtPath, mgmtHandler := serviceconnect.NewManagementServiceHandler(
		handler.NewManagementHandler(endpoints),
		connect.WithInterceptors(mgmtMiddleware.NewAuth(a.cfg.AdminToken, issuer)),
	)
	mux.Handle(mgmtPath, mgmtHandler)

	// Serve Embedded UI
	uiH, err := handler.NewUIHandler()
	if err != nil {
		log.Printf("Warning: Failed to initialize UI handler: %v", err)
	} else {
		mux.Handle("/", uiH)
		log.Printf("Web Dashboard available at http://%s", a.cfg.MgmtAddr)
	}

	// CORS for the web UI.
	//
	// The dashboard is served from this same origin, so no cross-origin access
	// is needed by default and none is granted. A wildcard here would have
	// exposed the ConnectRPC handler and /metrics to any site the operator
	// happened to visit. Operators running the dashboard elsewhere list their
	// origins explicitly; credentials are only allowed once an origin is named.
	corsOptions := cors.Options{
		AllowedOrigins:   a.cfg.AllowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders:   []string{"Content-Type", "Connect-Protocol-Version", "Authorization"},
		AllowCredentials: len(a.cfg.AllowedOrigins) > 0,
	}
	for _, origin := range a.cfg.AllowedOrigins {
		if origin == "*" {
			return errors.New("allowed_origins must not contain \"*\": it would expose the " +
				"management API and /metrics to any origin; list the dashboard origins explicitly")
		}
	}
	corsHandler := cors.New(corsOptions).Handler(mux)

	a.server = new(http.Server{
		Handler: h2c.NewHandler(corsHandler, new(http2.Server{})),
	})

	log.Printf("Management API (ConnectRPC) listening on %s", a.cfg.MgmtAddr)
	go func() {
		if err := a.server.Serve(mgmtLn); err != nil && err != http.ErrServerClosed {
			log.Printf("Management server error: %v", err)
		}
	}()

	<-ctx.Done()
	return a.Shutdown()
}

// Shutdown performs a graceful shutdown of the application.
func (a *App) Shutdown() error {
	log.Println("Shutting down Pontus...")

	if a.server == nil {
		return nil
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := a.server.Shutdown(shutdownCtx); err != nil {
		return err
	}

	log.Println("Pontus shutdown complete")
	return nil
}

// migrateFromJSON moves data from legacy JSON files to SQLite.
func (a *App) migrateFromJSON() {
	// If management.db already has data, skip migration
	if len(a.projectStore.List()) > 0 || len(a.userStore.List()) > 0 {
		return
	}

	// Try to load from projects.json
	if jsonPStore, err := store.NewJSONProjectStore("projects.json"); err == nil {
		projects := jsonPStore.List()
		if len(projects) > 0 {
			log.Printf("Migrating %d projects from projects.json to SQLite", len(projects))
			for _, p := range projects {
				_ = a.projectStore.Upsert(p)
			}
			os.Rename("projects.json", "projects.json.bak")
		}
	}

	// Try to load from users.json
	if jsonUStore, err := store.NewJSONUserStore("users.json"); err == nil {
		users := jsonUStore.List()
		if len(users) > 0 {
			log.Printf("Migrating %d users from users.json to SQLite", len(users))
			for _, u := range users {
				_ = a.userStore.Upsert(u.Username, u.Token, u.Role)
			}
			os.Rename("users.json", "users.json.bak")
		}
	}
}

// migratePasswords ensures all passwords in the store are hashed.
func (a *App) migratePasswords() {
	users := a.userStore.List()
	for _, u := range users {
		// Just calling Upsert will ensure the password is hashed if it wasn't already
		if len(u.Token) > 0 && !strings.HasPrefix(u.Token, "$2a$") && !strings.HasPrefix(u.Token, "$2b$") && !strings.HasPrefix(u.Token, "$2y$") {
			log.Printf("Hashing raw password for user %s", u.Username)
			if err := a.userStore.Upsert(u.Username, u.Token, u.Role); err != nil {
				log.Printf("Warning: Failed to update hashed password for user %s: %v", u.Username, err)
			}
		}
	}
}

// bootstrapFromConfig migrates settings from config.yaml if the project store is empty.
func (a *App) bootstrapFromConfig() {
	if len(a.projectStore.List()) > 0 || a.cfg == nil {
		return
	}

	pcfg := new(domain.Project{
		Id:       "default",
		Name:     "Default",
		Protocol: a.cfg.Protocol,
		Proxies: []*domain.ProxyConfig{
			new(domain.ProxyConfig{
				Id:       "default-proxy",
				Name:     "Default Proxy",
				Address:  a.cfg.ProxyAddr,
				Balancer: a.cfg.Balancer,
				MaxConns: a.cfg.MaxConns,
			}),
		},
		CreatedAt: timestamppb.Now(),
	})

	for _, b := range a.cfg.Backends {
		// Carry every field. This dropped agent_addr, agent_token, zone and
		// admin_dsn, so a backend configured in YAML lost its agent binding,
		// its locality and Pontus's own credentials the moment the config was
		// migrated into the store — silently, because the proxy still started.
		pcfg.Proxies[0].Backends = append(pcfg.Proxies[0].Backends, new(domain.BackendConfig{
			Address:      b.Addr,
			Role:         b.Role,
			Weight:       int32(b.Weight),
			Zone:         b.Zone,
			AgentAddress: b.AgentAddr,
			AgentToken:   b.AgentToken,
			AdminDsn:     b.AdminDSN,
		}))
	}

	a.projectStore.Upsert(pcfg)
	log.Println("Migrated default project from configuration")
}

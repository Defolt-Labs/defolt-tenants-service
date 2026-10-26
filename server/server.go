package server

import (
	"context"
	"fmt"
	"time"

	"defolt-tenants-service/cache"
	"defolt-tenants-service/config"
	"defolt-tenants-service/database"
	"defolt-tenants-service/handler"
	"defolt-tenants-service/logger"
	"defolt-tenants-service/middleware"
	"defolt-tenants-service/repository"
	"defolt-tenants-service/routes"
	"defolt-tenants-service/service"

	hk "github.com/Defolt-Labs/defolt-httpkit"
	"github.com/Defolt-Labs/defolt-svckit/localmode"
	"github.com/gin-gonic/gin"
	"github.com/nats-io/nats.go"
	"gorm.io/gorm"
)

const (
	sweepFirstDelay = 1 * time.Minute
	sweepInterval   = 15 * time.Minute

	// The activation sweep must run before the first reap: both read the same rows and reaping deletes.
	exploreSweepFirstDelay = 20 * time.Second
	exploreSweepInterval   = 1 * time.Hour
)

type App struct {
	Config *config.Config
	Engine *gin.Engine
	DB     *gorm.DB
	NC     *nats.Conn
	Cache  *cache.Cache

	stopSweeper context.CancelFunc
}

func Initialize() (*App, error) {
	hk.RegisterJSONFieldNames()
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	logger.LogInfo("internal-keys", "inbound internal keys: "+cfg.InternalKeys.Source())
	db, err := database.Connect(cfg)
	if err != nil {
		return nil, err
	}
	plan := localmode.Plan(cfg.LocalMode)
	if cfg.LocalMode {
		logger.LogWarn("", "local-mode", plan.Banner())
	}
	if plan.Migrate {
		database.EnsureSchema(db, cfg)
	}

	var nc *nats.Conn
	if plan.NATS {
		nc, err = nats.Connect(cfg.NatsURL, nats.Name("defolt-tenants-service"))
		if err != nil {
			logger.LogWarn("", "nats", "could not connect to NATS: "+natsConnectErrText(err))
			nc = nil
		}
	}

	// Shared Redis: slug-lookup cache (30s TTL) + Idempotency-Key
	// storage. Non-fatal — nil means cache-less operation.
	rc := cache.Connect(cfg.RedisURL)

	repo := repository.New(db)
	ts := service.NewTurnstile(cfg.TurnstileSecret, cfg.DeclaredEnvironment())
	idClient := service.NewIdentityClient(cfg.IdentityURL, cfg.OutboundServiceKey, cfg.DefaultPlatform)
	billing := service.NewBillingClient(cfg.BillingURL, cfg.OutboundServiceKey)
	svc := service.New(repo, nc, rc, idClient, billing, cfg.ReservedSlugs, cfg.PublicRootDomain)
	h := handler.New(svc, ts, rc, cfg.ServiceVersion)

	if cfg.Environment == "production" {
		gin.SetMode(gin.ReleaseMode)
	}
	engine := gin.New()
	engine.Use(gin.Recovery(), middleware.CORS(), middleware.RequestID())

	hk.ReadinessFailedLog = func(err error) { logger.LogWarn("", "ready", "database ping failed: "+err.Error()) }
	ready := hk.Readiness(dbPing(db), natsState(nc))
	routes.Mount(routes.Config{Engine: engine, Config: cfg, Handlers: h, Ready: ready})

	app := &App{Config: cfg, Engine: engine, DB: db, NC: nc, Cache: rc}
	// LOCAL_MODE leaves the sweeps to the UAT pods: they delete and activate tenant rows.
	if plan.Tickers {
		app.startSweeper(svc)
		app.startExploreActivator(svc)
	}
	return app, nil
}

// dbPing pings the pool behind the GORM handle.
func dbPing(db *gorm.DB) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		sqlDB, err := db.DB()
		if err != nil {
			return err
		}
		return sqlDB.PingContext(ctx)
	}
}

// natsState names the bus state for /ready: connected, disconnected, or absent when this run has no connection.
func natsState(nc *nats.Conn) func() string {
	return func() string {
		switch {
		case nc == nil:
			return "absent"
		case nc.IsConnected():
			return "connected"
		default:
			return "disconnected"
		}
	}
}

// startSweeper reaps abandoned pending_payment signups one minute after boot, then every 15 minutes.
func (a *App) startSweeper(svc *service.TenantsService) {
	ctx, cancel := context.WithCancel(context.Background())
	a.stopSweeper = cancel

	run := func() {
		n, err := svc.SweepAbandoned(ctx)
		if err != nil {
			logger.LogWarn("", "sweep-abandoned", "sweep failed: "+err.Error())
			return
		}
		if n > 0 {
			logger.LogInfo("sweep-abandoned", fmt.Sprintf("deleted %d abandoned signup(s) stuck in pending_payment", n))
		}
	}

	go func() {
		first := time.NewTimer(sweepFirstDelay)
		defer first.Stop()
		select {
		case <-ctx.Done():
			return
		case <-first.C:
		}
		run()

		ticker := time.NewTicker(sweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	}()
}

// startExploreActivator activates pending exploring tenants 20 seconds after boot, then hourly.
func (a *App) startExploreActivator(svc *service.TenantsService) {
	ctx, cancel := context.WithCancel(context.Background())
	prev := a.stopSweeper
	a.stopSweeper = func() {
		if prev != nil {
			prev()
		}
		cancel()
	}

	run := func() {
		n, err := svc.SweepExploringPending(ctx)
		if err != nil {
			logger.LogWarn("", "sweep-exploring", "sweep failed: "+err.Error())
			return
		}
		if n > 0 {
			logger.LogInfo("sweep-exploring", fmt.Sprintf("activated %d exploring tenant(s); their products provision on tenant.activated", n))
		}
	}

	go func() {
		first := time.NewTimer(exploreSweepFirstDelay)
		defer first.Stop()
		select {
		case <-ctx.Done():
			return
		case <-first.C:
		}
		run()

		ticker := time.NewTicker(exploreSweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	}()
}

func (a *App) Cleanup() {
	if a.stopSweeper != nil {
		a.stopSweeper()
	}
	if a.NC != nil {
		_ = a.NC.Drain()
	}
	a.Cache.Close()
	if a.DB != nil {
		if sqlDB, err := a.DB.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
}

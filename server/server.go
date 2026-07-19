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
	"defolt-tenants-service/service"
	"defolt-tenants-service/static"

	"github.com/gin-gonic/gin"
	"github.com/nats-io/nats.go"
	"gorm.io/gorm"
)

const (
	sweepFirstDelay = 1 * time.Minute
	sweepInterval   = 15 * time.Minute
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
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	db, err := database.Connect(cfg)
	if err != nil {
		return nil, err
	}
	database.EnsureSchema(db, cfg)

	nc, err := nats.Connect(cfg.NatsURL, nats.Name("defolt-tenants-service"))
	if err != nil {
		logger.LogWarn("", "nats", "could not connect to NATS: "+err.Error())
		nc = nil
	}

	// Shared Redis: slug-lookup cache (30s TTL) + Idempotency-Key
	// storage. Non-fatal — nil means cache-less operation.
	rc := cache.Connect(cfg.RedisURL)

	repo := repository.New(db)
	ts := service.NewTurnstile(cfg.TurnstileSecret)
	idClient := service.NewIdentityClient(cfg.IdentityURL, cfg.InternalServiceKey, cfg.DefaultPlatform)
	billing := service.NewBillingClient(cfg.BillingURL, cfg.InternalServiceKey)
	svc := service.New(repo, nc, rc, idClient, billing, cfg.ReservedSlugs)
	h := handler.New(svc, ts, rc, cfg.ServiceVersion, cfg.TurnstileSiteKey, cfg.TenantBaseDomain)

	if cfg.Environment == "production" {
		gin.SetMode(gin.ReleaseMode)
	}
	engine := gin.New()
	engine.Use(gin.Recovery(), middleware.CORS(), middleware.RequestID())

	engine.GET("/health", h.Health)
	engine.GET("/ready", h.Health)

	// Public read: rate-limit at Traefik so the fleet edge absorbs
	// enumeration attempts. Redis fronts the by-slug lookup with a
	// 30s TTL inside the service layer.
	pub := engine.Group("/api/v1")
	{
		pub.GET("/tenants/by-slug/:slug", h.GetBySlug)
		pub.POST("/public/signup", h.PublicSignup)
	}

	// Lifecycle landing pages (plan §5.17): Traefik points unresolved,
	// suspended and pending-payment hosts here.
	for page, file := range static.Pages {
		engine.GET("/public/"+file, h.LandingPageNamed(page))
	}
	engine.GET("/public/landing/:page", h.LandingPage)

	// Internal service-to-service admin routes.
	internal := engine.Group("/api/v1", middleware.InternalServiceKey(cfg.InternalServiceKey))
	{
		internal.POST("/tenants", h.Create)
		internal.GET("/tenants/:id", h.Get)
		internal.PATCH("/tenants/:id", h.Patch)
		internal.POST("/tenants/:id/suspend", h.Suspend)
		internal.POST("/tenants/:id/restore", h.Restore)
		internal.POST("/tenants/:id/reissue-owner-otp", h.ReissueOwnerOTP)
		internal.POST("/tenants/:id/resend-payment-link", h.ResendPaymentLink)
		internal.GET("/tenants/:id/health", h.TenantHealth)
		internal.POST("/internal/tenants/:id/activate", h.Activate)
	}

	// Traefik forward-auth entry point. Public because Traefik is the
	// only caller and the ingress network policy prevents anyone else
	// from reaching it. Response body is empty; headers do the work.
	engine.POST("/internal/resolve-host", h.ResolveHost)
	engine.GET("/internal/resolve-host", h.ResolveHost)

	app := &App{Config: cfg, Engine: engine, DB: db, NC: nc, Cache: rc}
	app.startSweeper(svc)
	return app, nil
}

// startSweeper runs the abandoned-signup reaper (plan §5.11): first
// pass one minute after boot, then every 15 minutes, until Cleanup
// cancels the context.
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

package server

import (
	"defolt-tenants-service/config"
	"defolt-tenants-service/database"
	"defolt-tenants-service/handler"
	"defolt-tenants-service/logger"
	"defolt-tenants-service/middleware"
	"defolt-tenants-service/repository"
	"defolt-tenants-service/service"

	"github.com/gin-gonic/gin"
	"github.com/nats-io/nats.go"
	"gorm.io/gorm"
)

type App struct {
	Config *config.Config
	Engine *gin.Engine
	DB     *gorm.DB
	NC     *nats.Conn
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

	repo := repository.New(db)
	svc := service.New(repo, nc, cfg.ReservedSlugs)
	ts := service.NewTurnstile(cfg.TurnstileSecret)
	idClient := service.NewIdentityClient(cfg.IdentityURL, cfg.InternalServiceKey, cfg.DefaultPlatform)
	h := handler.New(svc, ts, idClient, cfg.ServiceVersion)

	if cfg.Environment == "production" {
		gin.SetMode(gin.ReleaseMode)
	}
	engine := gin.New()
	engine.Use(gin.Recovery(), middleware.CORS(), middleware.RequestID())

	engine.GET("/health", h.Health)
	engine.GET("/ready", h.Health)

	// Public read: rate-limit at Traefik so the fleet edge absorbs
	// enumeration attempts. Cache the result in Redis with a 30s TTL
	// (implemented at the ingress layer).
	pub := engine.Group("/api/v1")
	{
		pub.GET("/tenants/by-slug/:slug", h.GetBySlug)
		pub.POST("/public/signup", h.PublicSignup)
	}

	// Internal service-to-service admin routes.
	internal := engine.Group("/api/v1", middleware.InternalServiceKey(cfg.InternalServiceKey))
	{
		internal.POST("/tenants", h.Create)
		internal.GET("/tenants/:id", h.Get)
		internal.POST("/tenants/:id/suspend", h.Suspend)
		internal.POST("/tenants/:id/restore", h.Restore)
		internal.GET("/tenants/:id/health", h.TenantHealth)
		internal.POST("/internal/tenants/:id/activate", h.Activate)
	}

	// Traefik forward-auth entry point. Public because Traefik is the
	// only caller and the ingress network policy prevents anyone else
	// from reaching it. Response body is empty; headers do the work.
	engine.POST("/internal/resolve-host", h.ResolveHost)
	engine.GET("/internal/resolve-host", h.ResolveHost)

	return &App{Config: cfg, Engine: engine, DB: db, NC: nc}, nil
}

func (a *App) Cleanup() {
	if a.NC != nil {
		_ = a.NC.Drain()
	}
	if a.DB != nil {
		if sqlDB, err := a.DB.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
}

package routes

import (
	"defolt-tenants-service/config"
	"defolt-tenants-service/handler"
	v1 "defolt-tenants-service/routes/v1"

	"github.com/gin-gonic/gin"
)

// Config carries dependencies needed to mount all routes.
type Config struct {
	Engine   *gin.Engine
	Config   *config.Config
	Handlers *handler.Handlers
	// Ready answers /ready; it pings the database.
	Ready gin.HandlerFunc
}

// Mount mounts probe routes and API versions.
func Mount(cfg Config) {
	MountProbes(cfg.Engine, cfg.Handlers, cfg.Ready)

	v1.Register(v1.Config{
		Engine:   cfg.Engine,
		Config:   cfg.Config,
		Handlers: cfg.Handlers,
	})
}

// MountProbes mounts health and readiness endpoints.
func MountProbes(engine *gin.Engine, h *handler.Handlers, ready gin.HandlerFunc) {
	if engine == nil {
		return
	}
	engine.GET("/health", h.Health)
	engine.GET("/ready", ready)
}

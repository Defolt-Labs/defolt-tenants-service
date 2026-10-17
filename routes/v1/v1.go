package v1

import (
	"defolt-tenants-service/config"
	"defolt-tenants-service/handler"

	"github.com/gin-gonic/gin"
)

// Config carries dependencies needed to wire v1 route groups.
type Config struct {
	Engine   *gin.Engine
	Config   *config.Config
	Handlers *handler.Handlers
}

// Register mounts every v1 route group.
func Register(c Config) {
	RegisterPublic(c)
	RegisterTenants(c)
	RegisterInternal(c)
}

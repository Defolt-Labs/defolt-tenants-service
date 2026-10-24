package v1

import "defolt-tenants-service/middleware"

// RegisterInternal wires internal lifecycle routes and the Traefik forward-auth entry.
func RegisterInternal(c Config) {
	h := c.Handlers

	internal := c.Engine.Group("", middleware.InternalServiceKey(c.Config.InternalKeys))
	{
		internal.POST("/tenants/v1/internal/tenants/:id/activate", h.Activate)
		internal.PUT("/tenants/v1/internal/tenants/:id/subscription-state", h.SyncSubscriptionState)
		internal.POST("/tenants/v1/internal/sweep-exploring", h.SweepExploring)
	}

	// Public on purpose: only Traefik calls it and the network policy blocks anyone else.
	c.Engine.POST("/tenants/v1/internal/resolve-host", h.ResolveHost)
	c.Engine.GET("/tenants/v1/internal/resolve-host", h.ResolveHost)
}

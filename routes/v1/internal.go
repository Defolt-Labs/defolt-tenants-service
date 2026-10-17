package v1

import "defolt-tenants-service/middleware"

// RegisterInternal wires internal lifecycle routes and the Traefik forward-auth entry.
func RegisterInternal(c Config) {
	h := c.Handlers

	internal := c.Engine.Group("/api/v1", middleware.InternalServiceKey(c.Config.InternalServiceKey))
	{
		internal.POST("/internal/tenants/:id/activate", h.Activate)
		internal.PUT("/internal/tenants/:id/subscription-state", h.SyncSubscriptionState)
		internal.POST("/internal/sweep-exploring", h.SweepExploring)
	}

	// Public on purpose: only Traefik calls it and the network policy blocks anyone else.
	c.Engine.POST("/internal/resolve-host", h.ResolveHost)
	c.Engine.GET("/internal/resolve-host", h.ResolveHost)
}

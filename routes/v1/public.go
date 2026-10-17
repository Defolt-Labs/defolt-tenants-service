package v1

// RegisterPublic wires the unauthenticated routes.
func RegisterPublic(c Config) {
	h := c.Handlers

	// Rate-limited at Traefik; the by-slug read is cached in the service layer.
	pub := c.Engine.Group("/api/v1")
	{
		pub.GET("/tenants/by-slug/:slug", h.GetBySlug)
		pub.POST("/public/signup", h.PublicSignup)
	}
}

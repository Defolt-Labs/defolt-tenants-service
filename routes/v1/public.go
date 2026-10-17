package v1

// RegisterPublic wires the unauthenticated routes.
func RegisterPublic(c Config) {
	h := c.Handlers

	// Rate-limited at Traefik; the by-slug read is cached in the service layer.
	pub := c.Engine.Group("")
	{
		pub.GET("/tenants/v1/by-slug/:slug", h.GetBySlug)
		pub.POST("/tenants/v1/public/signup", h.PublicSignup)
	}
}

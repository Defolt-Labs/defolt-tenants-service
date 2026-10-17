package v1

import "defolt-tenants-service/middleware"

// RegisterTenants wires the service-to-service tenant admin routes.
func RegisterTenants(c Config) {
	h := c.Handlers

	admin := c.Engine.Group("/api/v1", middleware.InternalServiceKey(c.Config.InternalServiceKey))
	{
		admin.POST("/tenants", h.Create)
		admin.GET("/tenants/:id", h.Get)
		admin.PATCH("/tenants/:id", h.Patch)
		admin.POST("/tenants/:id/suspend", h.Suspend)
		admin.POST("/tenants/:id/restore", h.Restore)
		admin.POST("/tenants/:id/reissue-owner-otp", h.ReissueOwnerOTP)
		admin.POST("/tenants/:id/resend-payment-link", h.ResendPaymentLink)
		admin.GET("/tenants/pending-checkout", h.PendingCheckoutByEmail)
		admin.GET("/tenants/:id/health", h.TenantHealth)
	}
}

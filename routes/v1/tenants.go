package v1

import "defolt-tenants-service/middleware"

// RegisterTenants wires the service-to-service tenant admin routes.
func RegisterTenants(c Config) {
	h := c.Handlers

	admin := c.Engine.Group("", middleware.InternalServiceKey(c.Config.InternalServiceKey))
	{
		admin.POST("/tenants/v1", h.Create)
		admin.GET("/tenants/v1/:id", h.Get)
		admin.PATCH("/tenants/v1/:id", h.Patch)
		admin.POST("/tenants/v1/:id/suspend", h.Suspend)
		admin.POST("/tenants/v1/:id/restore", h.Restore)
		admin.POST("/tenants/v1/:id/reissue-owner-otp", h.ReissueOwnerOTP)
		admin.POST("/tenants/v1/:id/resend-payment-link", h.ResendPaymentLink)
		admin.GET("/tenants/v1/pending-checkout", h.PendingCheckoutByEmail)
		admin.GET("/tenants/v1/:id/health", h.TenantHealth)
	}
}

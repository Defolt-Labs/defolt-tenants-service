package response

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func OK(c *gin.Context, code string, meta Meta, data any) {
	write(c, http.StatusOK, code, meta, data, nil)
}

func Created(c *gin.Context, code string, meta Meta, data any) {
	write(c, http.StatusCreated, code, meta, data, nil)
}

var (
	OKTenant = struct {
		Code string
		Meta Meta
	}{"DL_TENANT", Meta{EN: "Tenant fetched.", SW: "Mtu ameamepatikana."}}
	OKTenantCreated = struct {
		Code string
		Meta Meta
	}{"DL_TENANT_CREATED", Meta{EN: "Tenant created.", SW: "Mtu ameundwa."}}
	OKTenantUpdated = struct {
		Code string
		Meta Meta
	}{"DL_TENANT_UPDATED", Meta{EN: "Tenant updated.", SW: "Mtu amesasishwa."}}
	OKTenantSuspended = struct {
		Code string
		Meta Meta
	}{"DL_TENANT_SUSPENDED", Meta{EN: "Tenant suspended.", SW: "Mtu amesimamishwa."}}
	OKTenantRestored = struct {
		Code string
		Meta Meta
	}{"DL_TENANT_RESTORED", Meta{EN: "Tenant restored.", SW: "Mtu amerudishwa."}}
	OKTenantHealthy = struct {
		Code string
		Meta Meta
	}{"DL_TENANT_HEALTHY", Meta{EN: "Tenant healthy.", SW: "Mtu yuko sawa."}}
	OKTenantOTPReissued = struct {
		Code string
		Meta Meta
	}{"DL_TENANT_OTP_REISSUED", Meta{EN: "One-time password reissued.", SW: "Nenosiri la mara moja limetolewa upya."}}
	OKTenantPaymentLink = struct {
		Code string
		Meta Meta
	}{"DL_TENANT_PAYMENT_LINK", Meta{EN: "Payment link issued.", SW: "Kiungo cha malipo kimetolewa."}}
	OKHealth = struct {
		Code string
		Meta Meta
	}{"DL_HEALTH_OK", Meta{EN: "OK.", SW: "Sawa."}}
)

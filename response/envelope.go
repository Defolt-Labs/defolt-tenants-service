package response

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Fleet-shared envelope. Every downstream product speaks this shape.
type Meta struct {
	EN string `json:"en"`
	SW string `json:"sw"`
}

type Envelope struct {
	Code    string      `json:"code"`
	Meta    Meta        `json:"meta"`
	Data    interface{} `json:"data,omitempty"`
	Details interface{} `json:"details,omitempty"`
}

func write(c *gin.Context, status int, code string, meta Meta, data, details any) {
	c.JSON(status, Envelope{Code: code, Meta: meta, Data: data, Details: details})
}

func OK(c *gin.Context, code string, meta Meta, data any)            { write(c, http.StatusOK, code, meta, data, nil) }
func Created(c *gin.Context, code string, meta Meta, data any)       { write(c, http.StatusCreated, code, meta, data, nil) }
func BadRequest(c *gin.Context, code string, meta Meta, details any) { write(c, http.StatusBadRequest, code, meta, nil, details) }
func Unauthorized(c *gin.Context, code string, meta Meta)            { write(c, http.StatusUnauthorized, code, meta, nil, nil) }
func Forbidden(c *gin.Context, code string, meta Meta)               { write(c, http.StatusForbidden, code, meta, nil, nil) }
func NotFound(c *gin.Context, code string, meta Meta)                { write(c, http.StatusNotFound, code, meta, nil, nil) }
func Conflict(c *gin.Context, code string, meta Meta, details any)   { write(c, http.StatusConflict, code, meta, nil, details) }
func InternalError(c *gin.Context, code string, meta Meta)           { write(c, http.StatusInternalServerError, code, meta, nil, nil) }

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

	ErrValidation = struct {
		Code string
		Meta Meta
	}{"DL_VALIDATION_FAILED", Meta{EN: "Request could not be validated.", SW: "Ombi halikupita ukaguzi."}}
	ErrForbidden = struct {
		Code string
		Meta Meta
	}{"DL_FORBIDDEN", Meta{EN: "Forbidden.", SW: "Huna ruhusa."}}
	ErrTenantUnknown = struct {
		Code string
		Meta Meta
	}{"DL_TENANT_UNKNOWN", Meta{EN: "This subdomain is not registered.", SW: "Kikoa hiki hakijasajiliwa."}}
	ErrTenantSlugTaken = struct {
		Code string
		Meta Meta
	}{"DL_TENANT_SLUG_TAKEN", Meta{EN: "That slug is already in use.", SW: "Kikoa hicho tayari kinatumika."}}
	ErrTenantSlugReserved = struct {
		Code string
		Meta Meta
	}{"DL_TENANT_SLUG_RESERVED", Meta{EN: "That slug is reserved.", SW: "Kikoa hicho kimehifadhiwa."}}
	ErrTenantSuspended = struct {
		Code string
		Meta Meta
	}{"DL_TENANT_SUSPENDED", Meta{EN: "This tenant is suspended.", SW: "Mtu huyu amesimamishwa."}}
	ErrTenantNoOwner = struct {
		Code string
		Meta Meta
	}{"DL_TENANT_NO_OWNER", Meta{EN: "No owner account could be resolved for this tenant.", SW: "Akaunti ya mmiliki haikupatikana kwa mteja huyu."}}
	ErrBillingUnavailable = struct {
		Code string
		Meta Meta
	}{"DL_BILLING_UNAVAILABLE", Meta{EN: "Billing is unavailable right now. Try again shortly.", SW: "Huduma ya malipo haipatikani kwa sasa. Jaribu tena baadaye."}}
	ErrInternal = struct {
		Code string
		Meta Meta
	}{"DL_INTERNAL_ERROR", Meta{EN: "Something went wrong.", SW: "Hitilafu imetokea."}}
)

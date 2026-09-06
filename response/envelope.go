package response

import (
	"net/http"
	"strconv"
	"strings"

	"defolt-tenants-service/logger"

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
	if status >= http.StatusInternalServerError {
		logServerError(c, status, code)
	}
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

// ServiceUnavailable answers 503 through the same funnel. It exists because
// two handlers built the envelope by hand with c.JSON, which put a 5xx
// outside the writer and therefore outside the log.
func ServiceUnavailable(c *gin.Context, code string, meta Meta) {
	write(c, http.StatusServiceUnavailable, code, meta, nil, nil)
}

// Status answers with a bare status and no envelope, for the storefront
// resolve probe the SPA reads by header alone. It routes through the same 5xx
// log so a body-less refusal is not a silent one either.
func Status(c *gin.Context, status int, code string) {
	if status >= http.StatusInternalServerError {
		logServerError(c, status, code)
	}
	c.Status(status)
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

// logServerError makes a 5xx impossible to serve silently.
//
// It lives in the writer rather than in a middleware because write is the one
// funnel every refusal in this service passes through, so there is no route,
// no handler and no future 5xx that can be added outside it. A middleware
// would have to be remembered on each router group; this cannot be forgotten.
//
// The cause travels on gin's own error list. A handler that has a Go error in
// hand calls c.Error(err) before answering, and it is printed here. Without
// that the line still carries the method, the path, the status, the envelope
// code and the request id, which is the difference between "POST /path
// answered 500 at 05:30" and nothing at all.
//
// WHY. A DRS login returned 500 for every account on UAT for 20 hours 31
// minutes and nothing noticed, because drs-setup-service's writeAuthError had
// a default branch that answered 500 and discarded the error it was handed,
// and no service in the fleet wrote a line for a 5xx. A survey on 2026-09-05
// measured 294 places that can answer 500 across the fleet and 32 that write
// anything. drs-setup-service is the model this follows.
//
// This service provisions every tenant in the fleet, so a silent 500 here is a signup that sticks with nobody told. Thirteen of its sites could answer 500 and none of them wrote a line; five of those were the exact default-branch shape that hid the DRS outage.
//
// response must not import middleware, which imports response, so the request
// id is read with the literal "request_id" key that the RequestID middleware
// stamps.
func logServerError(c *gin.Context, status int, code string) {
	msg := c.Request.Method + " " + c.FullPath() + " -> " + strconv.Itoa(status) + " " + code
	if causes := c.Errors.Errors(); len(causes) > 0 {
		msg += ": " + strings.Join(causes, "; ")
	}
	rid := ""
	if v, ok := c.Get("request_id"); ok {
		if s, ok := v.(string); ok {
			rid = s
		}
	}
	logger.LogError(rid, "http-5xx", msg)
}

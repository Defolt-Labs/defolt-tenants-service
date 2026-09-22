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
	// RequestID repeats the X-Request-ID the answer already carries as a
	// header. WP-SIGNUP1: a browser on another origin cannot read a response
	// header the edge does not expose, so a screen that wants to print a
	// reference for support reads it here. Additive and omitempty, so a
	// consumer decoding the old four fields is unaffected.
	RequestID string `json:"request_id,omitempty"`
}

func write(c *gin.Context, status int, code string, meta Meta, data, details any) {
	if status >= http.StatusInternalServerError {
		logServerError(c, status, code)
	}
	c.JSON(status, Envelope{Code: code, Meta: meta, Data: data, Details: details, RequestID: requestID(c)})
}

// requestID reads the id the RequestID middleware stamps, by its literal key
// (response must not import middleware, which imports response).
func requestID(c *gin.Context) string {
	if v, ok := c.Get("request_id"); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// RequestIDOf is requestID for the handlers that build an envelope by hand
// (the idempotent 201 and its replay).
func RequestIDOf(c *gin.Context) string { return requestID(c) }

func OK(c *gin.Context, code string, meta Meta, data any)            { write(c, http.StatusOK, code, meta, data, nil) }
func Created(c *gin.Context, code string, meta Meta, data any)       { write(c, http.StatusCreated, code, meta, data, nil) }
func BadRequest(c *gin.Context, code string, meta Meta, details any) { write(c, http.StatusBadRequest, code, meta, nil, details) }
func Unauthorized(c *gin.Context, code string, meta Meta)            { write(c, http.StatusUnauthorized, code, meta, nil, nil) }
func Forbidden(c *gin.Context, code string, meta Meta)               { write(c, http.StatusForbidden, code, meta, nil, nil) }
func ForbiddenDetails(c *gin.Context, code string, meta Meta, details any) {
	write(c, http.StatusForbidden, code, meta, nil, details)
}
func NotFound(c *gin.Context, code string, meta Meta)                { write(c, http.StatusNotFound, code, meta, nil, nil) }
func Conflict(c *gin.Context, code string, meta Meta, details any)   { write(c, http.StatusConflict, code, meta, nil, details) }
func InternalError(c *gin.Context, code string, meta Meta)           { write(c, http.StatusInternalServerError, code, meta, nil, nil) }

// ServiceUnavailable answers 503 through the same funnel. It exists because
// two handlers built the envelope by hand with c.JSON, which put a 5xx
// outside the writer and therefore outside the log.
func ServiceUnavailable(c *gin.Context, code string, meta Meta) {
	write(c, http.StatusServiceUnavailable, code, meta, nil, nil)
}

// ServiceUnavailableDetails is ServiceUnavailable with a details object.
func ServiceUnavailableDetails(c *gin.Context, code string, meta Meta, details any) {
	write(c, http.StatusServiceUnavailable, code, meta, nil, details)
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
	}{"DL_TENANT_SLUG_TAKEN", Meta{
		EN: "That web address is already taken by another business. Choose a different one.",
		SW: "Anwani hiyo ya tovuti tayari imechukuliwa na biashara nyingine. Chagua nyingine.",
	}}
	ErrTenantSlugReserved = struct {
		Code string
		Meta Meta
	}{"DL_TENANT_SLUG_RESERVED", Meta{
		EN: "That web address is kept for Defolt's own use. Choose a different one.",
		SW: "Anwani hiyo ya tovuti imehifadhiwa kwa matumizi ya Defolt. Chagua nyingine.",
	}}

	// The signup refusals, WP-SIGNUP1. Each one names what is wrong and what
	// to do next, because the only person who can fix a signup is the one
	// being told. The owner was shown the bare "Forbidden." for a refused
	// human check on 2026-09-20 while the log named the reason.
	ErrTenantSlugInvalid = struct {
		Code string
		Meta Meta
	}{"DL_TENANT_SLUG_INVALID", Meta{
		EN: "The web address must be 3 to 32 characters of small letters, digits and dashes, start with a letter and not end with a dash.",
		SW: "Anwani ya tovuti iwe na herufi 3 hadi 32 za herufi ndogo, tarakimu na vistari, ianze na herufi na isimalizike kwa kistari.",
	}}
	ErrSignupHumanCheckFailed = struct {
		Code string
		Meta Meta
	}{"DL_SIGNUP_HUMAN_CHECK_FAILED", Meta{
		EN: "The check that you are a person did not pass, so nothing was created. Complete the check again and send the form once more.",
		SW: "Ukaguzi wa kuthibitisha kuwa wewe ni mtu haukufaulu, kwa hiyo hakuna kilichoundwa. Kamilisha ukaguzi tena kisha utume fomu upya.",
	}}
	ErrSignupHumanCheckUnavailable = struct {
		Code string
		Meta Meta
	}{"DL_SIGNUP_HUMAN_CHECK_UNAVAILABLE", Meta{
		EN: "The check that you are a person could not be made on our side, so nothing was created. This is not something you did. Try again in a few minutes.",
		SW: "Ukaguzi wa kuthibitisha kuwa wewe ni mtu haukuweza kufanyika upande wetu, kwa hiyo hakuna kilichoundwa. Hili si kosa lako. Jaribu tena baada ya dakika chache.",
	}}
	ErrSignupEmailInvalid = struct {
		Code string
		Meta Meta
	}{"DL_SIGNUP_EMAIL_INVALID", Meta{
		EN: "That email address is not written correctly. Check it and send the form again.",
		SW: "Barua pepe hiyo haijaandikwa sawa. Iangalie kisha utume fomu tena.",
	}}
	ErrSignupPhoneInvalid = struct {
		Code string
		Meta Meta
	}{"DL_SIGNUP_PHONE_INVALID", Meta{
		EN: "That phone number cannot be used. Write it with a + and the country code, for example +255 712 345 678.",
		SW: "Namba hiyo ya simu haiwezi kutumika. Iandike na + na msimbo wa nchi, kwa mfano +255 712 345 678.",
	}}
	ErrSignupPhoneCountryUnclear = struct {
		Code string
		Meta Meta
	}{"DL_SIGNUP_PHONE_COUNTRY_UNCLEAR", Meta{
		EN: "The country of that phone number is unclear. Write it with a + and the country code, for example +255 712 345 678.",
		SW: "Nchi ya namba hiyo ya simu haieleweki. Iandike na + na msimbo wa nchi, kwa mfano +255 712 345 678.",
	}}
	ErrSignupBodyUnreadable = struct {
		Code string
		Meta Meta
	}{"DL_SIGNUP_BODY_UNREADABLE", Meta{
		EN: "The signup form could not be read, so nothing was created. Reload the page and send it again.",
		SW: "Fomu ya kujisajili haikuweza kusomeka, kwa hiyo hakuna kilichoundwa. Pakia ukurasa upya kisha uitume tena.",
	}}
	ErrTenantSuspended = struct {
		Code string
		Meta Meta
	}{"DL_TENANT_SUSPENDED", Meta{EN: "This tenant is suspended.", SW: "Mtu huyu amesimamishwa."}}
	ErrTenantNoOwner = struct {
		Code string
		Meta Meta
	}{"DL_TENANT_NO_OWNER", Meta{EN: "No owner account could be resolved for this tenant.", SW: "Akaunti ya mmiliki haikupatikana kwa mteja huyu."}}
	// ErrTenantOwnerRequired is the internal POST /tenants refusal for a
	// health tenant created with no owner.
	//
	// It names BOTH halves deliberately: WHICH product demands an owner,
	// and that this request carried none. A bare "validation failed" leaves
	// the caller to guess which of the two is wrong. The refusal exists
	// because the quiet version of it cost a live clinic: an ownerless
	// health tenant is created, activated, and dhs-setup then logs
	// "admin mirror NOT created" — the facility exists and nobody can log
	// into it. Distinct from ErrTenantNoOwner above, which is a LOOKUP
	// failing on an existing tenant, not a create being refused.
	ErrTenantOwnerRequired = struct {
		Code string
		Meta Meta
	}{"DL_TENANT_OWNER_REQUIRED", Meta{
		EN: "The health product requires an owner on every tenant, and this request named none. Send owner_user_id.",
		SW: "Bidhaa ya afya inahitaji mmiliki kwa kila mteja, na ombi hili halikumtaja yeyote. Tuma owner_user_id.",
	}}
	ErrBillingUnavailable = struct {
		Code string
		Meta Meta
	}{"DL_BILLING_UNAVAILABLE", Meta{EN: "Billing is unavailable right now. Try again shortly.", SW: "Huduma ya malipo haipatikani kwa sasa. Jaribu tena baadaye."}}
	// ErrSignupFieldRequired is built per field by SignupFieldRequired,
	// because the sentence names the field.
	ErrSignupFieldRequired = "DL_SIGNUP_FIELD_REQUIRED"
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
	logger.LogError(requestID(c), "http-5xx", msg)
}

// signupFieldLabels are the names a person reads for each signup field.
var signupFieldLabels = map[string][2]string{
	"slug":          {"web address", "anwani ya tovuti"},
	"name":          {"business name", "jina la biashara"},
	"contact_email": {"email address", "barua pepe"},
	"phone":         {"phone number", "namba ya simu"},
	"first_name":    {"first name", "jina la kwanza"},
	"last_name":     {"last name", "jina la mwisho"},
}

// SignupFieldRequired is the sentence for a signup field left empty, naming
// the field. ok is false for a field this table does not know, so the caller
// falls back to the general refusal rather than print a raw json key.
func SignupFieldRequired(field string) (Meta, bool) {
	l, ok := signupFieldLabels[field]
	if !ok {
		return Meta{}, false
	}
	return Meta{
		EN: "Fill in the " + l[0] + ", then send the form again.",
		SW: "Jaza " + l[1] + ", kisha utume fomu tena.",
	}, true
}

package response

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func BadRequest(c *gin.Context, code string, meta Meta, details any) {
	write(c, http.StatusBadRequest, code, meta, nil, details)
}

func Unauthorized(c *gin.Context, code string, meta Meta) {
	write(c, http.StatusUnauthorized, code, meta, nil, nil)
}

func Forbidden(c *gin.Context, code string, meta Meta) {
	write(c, http.StatusForbidden, code, meta, nil, nil)
}

func ForbiddenDetails(c *gin.Context, code string, meta Meta, details any) {
	write(c, http.StatusForbidden, code, meta, nil, details)
}

func NotFound(c *gin.Context, code string, meta Meta) {
	write(c, http.StatusNotFound, code, meta, nil, nil)
}

func Conflict(c *gin.Context, code string, meta Meta, details any) {
	write(c, http.StatusConflict, code, meta, nil, details)
}

func InternalError(c *gin.Context, code string, meta Meta) {
	write(c, http.StatusInternalServerError, code, meta, nil, nil)
}

// ServiceUnavailable answers 503 through the standard writer funnel.
func ServiceUnavailable(c *gin.Context, code string, meta Meta) {
	write(c, http.StatusServiceUnavailable, code, meta, nil, nil)
}

// ServiceUnavailableDetails is ServiceUnavailable with details.
func ServiceUnavailableDetails(c *gin.Context, code string, meta Meta, details any) {
	write(c, http.StatusServiceUnavailable, code, meta, nil, details)
}

var (
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

	// ErrTenantOwnerLinkElsewhere points a reissue at the tenant's own service.
	ErrTenantOwnerLinkElsewhere = struct {
		Code string
		Meta Meta
	}{"DL_TENANT_OWNER_LINK_ELSEWHERE", Meta{EN: "This owner's password link is sent by the clinic's own system. Ask for a new link there; this route does not issue one.", SW: "Kiungo cha nenosiri la mmiliki huyu hutumwa na mfumo wa kliniki yenyewe. Omba kiungo kipya huko; njia hii haitoi kimoja."}}

	// ErrTenantOwnerRequired rejects health tenant creation without an owner.
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

	// ErrSignupFieldRequired code is completed with field specifics by SignupFieldRequired.
	ErrSignupFieldRequired = "DL_SIGNUP_FIELD_REQUIRED"

	ErrInternal = struct {
		Code string
		Meta Meta
	}{"DL_INTERNAL_ERROR", Meta{EN: "Something went wrong.", SW: "Hitilafu imetokea."}}
)

var signupFieldLabels = map[string][2]string{
	"slug":          {"web address", "anwani ya tovuti"},
	"name":          {"business name", "jina la biashara"},
	"contact_email": {"email address", "barua pepe"},
	"phone":         {"phone number", "namba ya simu"},
	"first_name":    {"first name", "jina la kwanza"},
	"last_name":     {"last name", "jina la mwisho"},
}

// SignupFieldRequired constructs the bilingual requirement message for a field.
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

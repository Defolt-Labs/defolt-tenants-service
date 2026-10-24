package handler

import (
	"encoding/json"
	"errors"
	"io"
	"strings"

	"defolt-tenants-service/response"
	"defolt-tenants-service/service"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
)

type signupFieldDetails struct {
	Field  string `json:"field"`
	Reason string `json:"reason,omitempty"`
}

// writeSignupBindError translates request binding failures to localized responses; fe.Field() is the json name because server wiring registers the json tag names at boot.
func writeSignupBindError(c *gin.Context, err error) {
	var ve validator.ValidationErrors
	if errors.As(err, &ve) && len(ve) > 0 {
		fe := ve[0]
		field := fe.Field()
		switch fe.Tag() {
		case "required":
			if meta, ok := response.SignupFieldRequired(field); ok {
				response.BadRequest(c, response.ErrSignupFieldRequired, meta, signupFieldDetails{Field: field})
				return
			}
		case "email":
			response.BadRequest(c, response.ErrSignupEmailInvalid.Code, response.ErrSignupEmailInvalid.Meta, signupFieldDetails{Field: "contact_email"})
			return
		}
		response.BadRequest(c, response.ErrValidation.Code, response.ErrValidation.Meta, signupFieldDetails{Field: field, Reason: fe.Tag()})
		return
	}
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	if errors.As(err, &syn) || errors.As(err, &typ) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		response.BadRequest(c, response.ErrSignupBodyUnreadable.Code, response.ErrSignupBodyUnreadable.Meta, nil)
		return
	}
	response.BadRequest(c, response.ErrValidation.Code, response.ErrValidation.Meta, err.Error())
}

// blankSignupField finds the first required field containing only whitespace.
func blankSignupField(b signupBody) string {
	for _, f := range []struct{ key, val string }{
		{"slug", b.Slug}, {"name", b.Name}, {"contact_email", b.ContactEmail},
		{"phone", b.Phone}, {"first_name", b.FirstName}, {"last_name", b.LastName},
	} {
		if strings.TrimSpace(f.val) == "" {
			return f.key
		}
	}
	return ""
}

// writeSignupError maps service signup failures to HTTP responses.
func writeSignupError(c *gin.Context, err error) {
	var te *service.TurnstileError
	reason := ""
	if errors.As(err, &te) {
		reason = strings.Join(te.Codes, ",")
	}
	switch {
	case errors.Is(err, service.ErrTurnstileUnavailable):
		c.Error(err)
		response.ServiceUnavailableDetails(c, response.ErrSignupHumanCheckUnavailable.Code, response.ErrSignupHumanCheckUnavailable.Meta, signupFieldDetails{Field: "turnstile_token", Reason: reason})
	case errors.Is(err, service.ErrTurnstile):
		response.ForbiddenDetails(c, response.ErrSignupHumanCheckFailed.Code, response.ErrSignupHumanCheckFailed.Meta, signupFieldDetails{Field: "turnstile_token", Reason: reason})
	case errors.Is(err, service.ErrSlugInvalid):
		response.BadRequest(c, response.ErrTenantSlugInvalid.Code, response.ErrTenantSlugInvalid.Meta, signupFieldDetails{Field: "slug"})
	case errors.Is(err, service.ErrSlugReserved):
		response.Conflict(c, response.ErrTenantSlugReserved.Code, response.ErrTenantSlugReserved.Meta, signupFieldDetails{Field: "slug"})
	case errors.Is(err, service.ErrSlugTaken):
		response.Conflict(c, response.ErrTenantSlugTaken.Code, response.ErrTenantSlugTaken.Meta, signupFieldDetails{Field: "slug"})
	case errors.Is(err, service.ErrPhoneRequired):
		meta, _ := response.SignupFieldRequired("phone")
		response.BadRequest(c, response.ErrSignupFieldRequired, meta, signupFieldDetails{Field: "phone"})
	case errors.Is(err, service.ErrPhoneAmbiguous):
		response.BadRequest(c, response.ErrSignupPhoneCountryUnclear.Code, response.ErrSignupPhoneCountryUnclear.Meta, signupFieldDetails{Field: "phone"})
	case errors.Is(err, service.ErrPhoneInvalid):
		response.BadRequest(c, response.ErrSignupPhoneInvalid.Code, response.ErrSignupPhoneInvalid.Meta, signupFieldDetails{Field: "phone"})
	case errors.Is(err, service.ErrValidation):
		response.BadRequest(c, response.ErrValidation.Code, response.ErrValidation.Meta, err.Error())
	default:
		c.Error(err)
		response.InternalError(c, response.ErrInternal.Code, response.ErrInternal.Meta)
	}
}

package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"defolt-tenants-service/cache"
	"defolt-tenants-service/middleware"
	"defolt-tenants-service/model"
	"defolt-tenants-service/repository"
	"defolt-tenants-service/response"
	"defolt-tenants-service/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Handlers struct {
	svc     *service.TenantsService
	ts      *service.Turnstile
	cache   *cache.Cache
	version string
}

func New(svc *service.TenantsService, ts *service.Turnstile, c *cache.Cache, version string) *Handlers {
	return &Handlers{
		svc:     svc,
		ts:      ts,
		cache:   c,
		version: version,
	}
}

// Health answers liveness and readiness probe requests.
func (h *Handlers) Health(c *gin.Context) {
	response.OK(c, response.OKHealth.Code, response.OKHealth.Meta, gin.H{"version": h.version})
}

func idemKey(c *gin.Context) string {
	k := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if k == "" {
		return ""
	}
	return "idem:tenants:" + k
}

// replayIdempotent writes cached response when idempotency key is matched.
func (h *Handlers) replayIdempotent(c *gin.Context) bool {
	key := idemKey(c)
	if key == "" {
		return false
	}
	if body, ok := h.cache.GetIdempotent(c.Request.Context(), key); ok {
		var env response.Envelope
		if err := json.Unmarshal(body, &env); err == nil {
			env.RequestID = response.RequestIDOf(c)
			if b, err := json.Marshal(env); err == nil {
				body = b
			}
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", body)
		return true
	}
	return false
}

// respondCreatedIdempotent writes 201 response and stores body under idempotency key.
func (h *Handlers) respondCreatedIdempotent(c *gin.Context, code string, meta response.Meta, data any) {
	env := response.Envelope{Code: code, Meta: meta, Data: data}
	stored, err := json.Marshal(env)
	if err != nil {
		response.Created(c, code, meta, data)
		return
	}
	if key := idemKey(c); key != "" {
		h.cache.StoreIdempotent(c.Request.Context(), key, stored)
	}
	env.RequestID = response.RequestIDOf(c)
	body, err := json.Marshal(env)
	if err != nil {
		body = stored
	}
	c.Data(http.StatusCreated, "application/json; charset=utf-8", body)
}

type createBody struct {
	Slug            string `json:"slug" binding:"required"`
	Name            string `json:"name" binding:"required"`
	ContactEmail    string `json:"contact_email" binding:"required,email"`
	Phone           string `json:"phone" binding:"required"`
	Currency        string `json:"currency"`
	Timezone        string `json:"timezone"`
	CountryCode     string `json:"country_code"`
	Product         string `json:"product"`
	Plan            string `json:"plan"`
	OwnerUserID     string `json:"owner_user_id"`
	OwnerEmail      string `json:"owner_email"`
	OwnerFirstName  string `json:"owner_first_name"`
	OwnerMiddleName string `json:"owner_middle_name"`
	OwnerLastName   string `json:"owner_last_name"`
}

// createInputFrom maps create request body to service input.
func createInputFrom(body createBody) (service.CreateInput, error) {
	in := service.CreateInput{
		Slug:            body.Slug,
		Name:            body.Name,
		ContactEmail:    body.ContactEmail,
		Phone:           body.Phone,
		Currency:        body.Currency,
		Timezone:        body.Timezone,
		CountryCode:     body.CountryCode,
		Product:         body.Product,
		Plan:            body.Plan,
		OwnerEmail:      body.OwnerEmail,
		OwnerFirstName:  body.OwnerFirstName,
		OwnerMiddleName: body.OwnerMiddleName,
		OwnerLastName:   body.OwnerLastName,
	}
	if raw := strings.TrimSpace(body.OwnerUserID); raw != "" {
		ownerID, perr := uuid.Parse(raw)
		if perr != nil {
			return in, errors.New("owner_user_id must be a UUID")
		}
		in.OwnerUserID = &ownerID
	}
	return in, nil
}

// Create handles tenant creation for internal callers.
func (h *Handlers) Create(c *gin.Context) {
	if h.replayIdempotent(c) {
		return
	}
	var body createBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, response.ErrValidation.Code, response.ErrValidation.Meta, err.Error())
		return
	}
	in, err := createInputFrom(body)
	if err != nil {
		response.BadRequest(c, response.ErrValidation.Code, response.ErrValidation.Meta, err.Error())
		return
	}
	if err := service.RequireOwnerForProduct(in.Product, in.OwnerUserID); err != nil {
		response.BadRequest(c, response.ErrTenantOwnerRequired.Code, response.ErrTenantOwnerRequired.Meta, err.Error())
		return
	}
	t, err := h.svc.Create(c, in)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrSlugInvalid), errors.Is(err, service.ErrValidation):
			response.BadRequest(c, response.ErrValidation.Code, response.ErrValidation.Meta, err.Error())
		case errors.Is(err, service.ErrSlugReserved):
			response.Conflict(c, response.ErrTenantSlugReserved.Code, response.ErrTenantSlugReserved.Meta, nil)
		case errors.Is(err, service.ErrSlugTaken):
			response.Conflict(c, response.ErrTenantSlugTaken.Code, response.ErrTenantSlugTaken.Meta, nil)
		default:
			c.Error(err)
			response.InternalError(c, response.ErrInternal.Code, response.ErrInternal.Meta)
		}
		return
	}
	h.respondCreatedIdempotent(c, response.OKTenantCreated.Code, response.OKTenantCreated.Meta, t)
}

// GetBySlug resolves slim tenant projection for public lookup.
func (h *Handlers) GetBySlug(c *gin.Context) {
	slug := c.Param("slug")
	product := c.Query("product")
	if product == "" {
		product = c.GetHeader(middleware.ProductHeader)
	}
	product = service.NormalizeProduct(product)
	t, err := h.svc.ResolveBySlug(c, product, slug)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			response.NotFound(c, response.ErrTenantUnknown.Code, response.ErrTenantUnknown.Meta)
			return
		}
		c.Error(err)
		response.InternalError(c, response.ErrInternal.Code, response.ErrInternal.Meta)
		return
	}
	response.OK(c, response.OKTenant.Code, response.OKTenant.Meta, t)
}

// Get returns the full tenant record by ID for internal callers.
func (h *Handlers) Get(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, response.ErrValidation.Code, response.ErrValidation.Meta, "invalid id")
		return
	}
	t, err := h.svc.Get(c, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			response.NotFound(c, response.ErrTenantUnknown.Code, response.ErrTenantUnknown.Meta)
			return
		}
		c.Error(err)
		response.InternalError(c, response.ErrInternal.Code, response.ErrInternal.Meta)
		return
	}
	response.OK(c, response.OKTenant.Code, response.OKTenant.Meta, t)
}

type patchBody struct {
	Name         *string `json:"name"`
	ContactEmail *string `json:"contact_email" binding:"omitempty,email"`
	Phone        *string `json:"phone"`
	Plan         *string `json:"plan"`
}

// Patch updates mutable fields on a tenant.
func (h *Handlers) Patch(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, response.ErrValidation.Code, response.ErrValidation.Meta, "invalid id")
		return
	}
	var body patchBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, response.ErrValidation.Code, response.ErrValidation.Meta, err.Error())
		return
	}
	t, err := h.svc.Update(c, id, service.UpdateInput{
		Name:         body.Name,
		ContactEmail: body.ContactEmail,
		Phone:        body.Phone,
		Plan:         body.Plan,
	})
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			response.NotFound(c, response.ErrTenantUnknown.Code, response.ErrTenantUnknown.Meta)
			return
		}
		if errors.Is(err, service.ErrValidation) {
			response.BadRequest(c, response.ErrValidation.Code, response.ErrValidation.Meta, err.Error())
			return
		}
		c.Error(err)
		response.InternalError(c, response.ErrInternal.Code, response.ErrInternal.Meta)
		return
	}
	response.OK(c, response.OKTenantUpdated.Code, response.OKTenantUpdated.Meta, t)
}

// Suspend transitions a tenant to suspended status.
func (h *Handlers) Suspend(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, response.ErrValidation.Code, response.ErrValidation.Meta, "invalid id")
		return
	}
	t, err := h.svc.Suspend(c, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			response.NotFound(c, response.ErrTenantUnknown.Code, response.ErrTenantUnknown.Meta)
			return
		}
		c.Error(err)
		response.InternalError(c, response.ErrInternal.Code, response.ErrInternal.Meta)
		return
	}
	response.OK(c, response.OKTenantSuspended.Code, response.OKTenantSuspended.Meta, t)
}

// Restore transitions a suspended tenant back to active status.
func (h *Handlers) Restore(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, response.ErrValidation.Code, response.ErrValidation.Meta, "invalid id")
		return
	}
	t, err := h.svc.Restore(c, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			response.NotFound(c, response.ErrTenantUnknown.Code, response.ErrTenantUnknown.Meta)
			return
		}
		c.Error(err)
		response.InternalError(c, response.ErrInternal.Code, response.ErrInternal.Meta)
		return
	}
	response.OK(c, response.OKTenantRestored.Code, response.OKTenantRestored.Meta, t)
}

// ReissueOwnerOTP generates and pushes a fresh OTP to identity for the tenant owner.
func (h *Handlers) ReissueOwnerOTP(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, response.ErrValidation.Code, response.ErrValidation.Meta, "invalid id")
		return
	}
	otp, err := h.svc.ReissueOwnerOTP(c.Request.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrNotFound):
			response.NotFound(c, response.ErrTenantUnknown.Code, response.ErrTenantUnknown.Meta)
		case errors.Is(err, service.ErrNoOwner):
			response.Conflict(c, response.ErrTenantNoOwner.Code, response.ErrTenantNoOwner.Meta, nil)
		default:
			c.Error(err)
			response.InternalError(c, response.ErrInternal.Code, response.ErrInternal.Meta)
		}
		return
	}
	response.OK(c, response.OKTenantOTPReissued.Code, response.OKTenantOTPReissued.Meta, gin.H{
		"one_time_password": otp,
	})
}

// ResendPaymentLink generates a fresh registration checkout URL.
func (h *Handlers) ResendPaymentLink(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, response.ErrValidation.Code, response.ErrValidation.Meta, "invalid id")
		return
	}
	checkout, err := h.svc.ResendPaymentLink(c.Request.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrNotFound):
			response.NotFound(c, response.ErrTenantUnknown.Code, response.ErrTenantUnknown.Meta)
		case errors.Is(err, service.ErrBillingDown):
			c.Error(err)
			response.ServiceUnavailable(c, response.ErrBillingUnavailable.Code, response.ErrBillingUnavailable.Meta)
		default:
			c.Error(err)
			response.InternalError(c, response.ErrInternal.Code, response.ErrInternal.Meta)
		}
		return
	}
	response.OK(c, response.OKTenantPaymentLink.Code, response.OKTenantPaymentLink.Meta, gin.H{
		"payment_url": checkout.PaymentURL,
		"amount_tzs":  checkout.AmountTZS,
	})
}

// PendingCheckoutByEmail returns unpaid registration details for an owner email.
func (h *Handlers) PendingCheckoutByEmail(c *gin.Context) {
	email := c.Query("email")
	if email == "" {
		response.BadRequest(c, response.ErrValidation.Code, response.ErrValidation.Meta, "email is required")
		return
	}
	t, checkout, err := h.svc.PendingCheckoutByEmail(c.Request.Context(), email, c.Query("product"))
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrNotFound):
			response.NotFound(c, response.ErrTenantUnknown.Code, response.ErrTenantUnknown.Meta)
		case errors.Is(err, service.ErrBillingDown):
			c.Error(err)
			response.ServiceUnavailable(c, response.ErrBillingUnavailable.Code, response.ErrBillingUnavailable.Meta)
		default:
			c.Error(err)
			response.InternalError(c, response.ErrInternal.Code, response.ErrInternal.Meta)
		}
		return
	}
	response.OK(c, response.OKTenantPaymentLink.Code, response.OKTenantPaymentLink.Meta, gin.H{
		"slug":        t.Slug,
		"payment_url": checkout.PaymentURL,
		"amount_tzs":  checkout.AmountTZS,
	})
}

// TenantHealth returns operational health and active status summary for a tenant.
func (h *Handlers) TenantHealth(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, response.ErrValidation.Code, response.ErrValidation.Meta, "invalid id")
		return
	}
	t, err := h.svc.Get(c, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			response.NotFound(c, response.ErrTenantUnknown.Code, response.ErrTenantUnknown.Meta)
			return
		}
		c.Error(err)
		response.InternalError(c, response.ErrInternal.Code, response.ErrInternal.Meta)
		return
	}
	healthy := t.Status == model.StatusActive || t.Status == model.StatusGrace
	response.OK(c, response.OKTenantHealthy.Code, response.OKTenantHealthy.Meta, gin.H{
		"id":      t.ID,
		"slug":    t.Slug,
		"status":  t.Status,
		"healthy": healthy,
	})
}

type signupBody struct {
	Slug         string `json:"slug" binding:"required"`
	Name         string `json:"name" binding:"required"`
	ContactEmail string `json:"contact_email" binding:"required,email"`
	Phone        string `json:"phone" binding:"required"`
	FirstName    string `json:"first_name" binding:"required"`
	MiddleName   string `json:"middle_name"`
	LastName     string `json:"last_name" binding:"required"`
	CountryCode  string `json:"country_code"`
	TurnstileTok string `json:"turnstile_token"`
	RedirectURL  string `json:"redirect_url"`
	Product      string `json:"product"`
}

// PublicSignup processes self-service tenant registration.
func (h *Handlers) PublicSignup(c *gin.Context) {
	if h.replayIdempotent(c) {
		return
	}
	var body signupBody
	if err := c.ShouldBindJSON(&body); err != nil {
		writeSignupBindError(c, err)
		return
	}
	if field := blankSignupField(body); field != "" {
		meta, _ := response.SignupFieldRequired(field)
		response.BadRequest(c, response.ErrSignupFieldRequired, meta, signupFieldDetails{Field: field})
		return
	}
	res, err := h.svc.PublicSignup(c.Request.Context(), service.SignupInput{
		Slug:         body.Slug,
		Name:         body.Name,
		ContactEmail: body.ContactEmail,
		Phone:        body.Phone,
		FirstName:    body.FirstName,
		MiddleName:   body.MiddleName,
		LastName:     body.LastName,
		CountryCode:  body.CountryCode,
		TurnstileTok: body.TurnstileTok,
		RedirectURL:  body.RedirectURL,
		Product:      body.Product,
		ClientIP:     c.ClientIP(),
	}, h.ts)
	if err != nil {
		writeSignupError(c, err)
		return
	}
	h.respondCreatedIdempotent(c, response.OKTenantCreated.Code, response.OKTenantCreated.Meta, gin.H{
		"id":                res.Tenant.ID,
		"slug":              res.Tenant.Slug,
		"status":            res.Tenant.Status,
		"one_time_password": res.OneTimePassword,
		"owner_existing":    res.OwnerExisting,
		"owner_email":       res.OwnerEmail,
		"payment_url":       res.PaymentURL,
		"amount_tzs":        res.AmountTZS,
	})
}

// Activate handles billing confirmation callback to activate tenant.
func (h *Handlers) Activate(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, response.ErrValidation.Code, response.ErrValidation.Meta, "invalid id")
		return
	}
	t, err := h.svc.ActivateAfterRegistration(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			response.NotFound(c, response.ErrTenantUnknown.Code, response.ErrTenantUnknown.Meta)
			return
		}
		c.Error(err)
		response.InternalError(c, response.ErrInternal.Code, response.ErrInternal.Meta)
		return
	}
	response.OK(c, response.OKTenantRestored.Code, response.OKTenantRestored.Meta, t)
}

// SyncSubscriptionState updates tenant status from billing webhook.
func (h *Handlers) SyncSubscriptionState(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, response.ErrValidation.Code, response.ErrValidation.Meta, "invalid id")
		return
	}
	var req struct {
		State string `json:"state" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, response.ErrValidation.Code, response.ErrValidation.Meta, "invalid payload")
		return
	}
	t, err := h.svc.SyncSubscriptionState(c.Request.Context(), id, req.State)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			response.NotFound(c, response.ErrTenantUnknown.Code, response.ErrTenantUnknown.Meta)
			return
		}
		c.Error(err)
		response.InternalError(c, response.ErrInternal.Code, response.ErrInternal.Meta)
		return
	}
	response.OK(c, response.OKTenantUpdated.Code, response.OKTenantUpdated.Meta, t)
}

// SweepExploring triggers the exploring activation sweep on demand.
func (h *Handlers) SweepExploring(c *gin.Context) {
	n, err := h.svc.SweepExploringPending(c.Request.Context())
	if err != nil {
		c.Error(err)
		response.InternalError(c, response.ErrInternal.Code, response.ErrInternal.Meta)
		return
	}
	response.OK(c, response.OKTenantUpdated.Code, response.OKTenantUpdated.Meta, gin.H{"activated": n})
}

// ResolveHost handles Traefik forward auth host header resolution.
func (h *Handlers) ResolveHost(c *gin.Context) {
	host := c.GetHeader("X-Forwarded-Host")
	if host == "" {
		host = c.Request.Host
	}
	slug := ""
	if p := indexByte(host, ':'); p >= 0 {
		host = host[:p]
	}
	if dot := indexByte(host, '.'); dot > 0 {
		slug = host[:dot]
	}
	if slug == "" {
		c.Status(http.StatusForbidden)
		return
	}
	product := productFromHost(host, c.GetHeader(middleware.ProductHeader))
	t, err := h.svc.ResolveBySlug(c, product, slug)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.Header(middleware.TenantStatusHeader, "unknown")
			c.Status(http.StatusOK)
			return
		}
		c.Error(err)
		response.Status(c, http.StatusServiceUnavailable, "DL_TENANT_RESOLVE_UNAVAILABLE")
		return
	}
	switch t.Status {
	case model.StatusArchived:
		c.Status(http.StatusForbidden)
	default:
		c.Header(middleware.TenantHeader, t.ID.String())
		c.Header(middleware.TenantSlugHeader, t.Slug)
		c.Header(middleware.TenantProductHeader, t.Product)
		c.Header(middleware.TenantStatusHeader, string(t.Status))
		c.Status(http.StatusOK)
	}
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

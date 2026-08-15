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

// Health is the /health + /ready probe reply.
func (h *Handlers) Health(c *gin.Context) {
	response.OK(c, response.OKHealth.Code, response.OKHealth.Meta, gin.H{"version": h.version})
}

// ---------- Idempotency-Key support (plan §5.19) ----------
// Best effort via Redis: replay of a stored key returns the original
// success body with HTTP 200; if Redis is down the request processes
// normally.

func idemKey(c *gin.Context) string {
	k := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if k == "" {
		return ""
	}
	return "idem:tenants:" + k
}

// replayIdempotent returns true when a stored response was replayed.
func (h *Handlers) replayIdempotent(c *gin.Context) bool {
	key := idemKey(c)
	if key == "" {
		return false
	}
	if body, ok := h.cache.GetIdempotent(c.Request.Context(), key); ok {
		c.Data(http.StatusOK, "application/json; charset=utf-8", body)
		return true
	}
	return false
}

// respondCreatedIdempotent writes the 201 envelope and, when an
// Idempotency-Key is present, stores the serialized body for replay.
func (h *Handlers) respondCreatedIdempotent(c *gin.Context, code string, meta response.Meta, data any) {
	env := response.Envelope{Code: code, Meta: meta, Data: data}
	body, err := json.Marshal(env)
	if err != nil {
		response.Created(c, code, meta, data)
		return
	}
	if key := idemKey(c); key != "" {
		h.cache.StoreIdempotent(c.Request.Context(), key, body)
	}
	c.Data(http.StatusCreated, "application/json; charset=utf-8", body)
}

// ---------- POST /api/v1/tenants (X-Internal-Service-Key) ----------

type createBody struct {
	Slug         string `json:"slug" binding:"required"`
	Name         string `json:"name" binding:"required"`
	ContactEmail string `json:"contact_email" binding:"required,email"`
	Phone        string `json:"phone"`
	Currency     string `json:"currency"`
	Timezone     string `json:"timezone"`
	CountryCode  string `json:"country_code"`
	Product      string `json:"product"`
	Plan         string `json:"plan"`
}

func (h *Handlers) Create(c *gin.Context) {
	if h.replayIdempotent(c) {
		return
	}
	var body createBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, response.ErrValidation.Code, response.ErrValidation.Meta, err.Error())
		return
	}
	t, err := h.svc.Create(c, service.CreateInput{
		Slug:         body.Slug,
		Name:         body.Name,
		ContactEmail: body.ContactEmail,
		Phone:        body.Phone,
		Currency:     body.Currency,
		Timezone:     body.Timezone,
		CountryCode:  body.CountryCode,
		Product:      body.Product,
		Plan:         body.Plan,
	})
	if err != nil {
		switch {
		case errors.Is(err, service.ErrSlugInvalid), errors.Is(err, service.ErrValidation):
			response.BadRequest(c, response.ErrValidation.Code, response.ErrValidation.Meta, err.Error())
		case errors.Is(err, service.ErrSlugReserved):
			response.Conflict(c, response.ErrTenantSlugReserved.Code, response.ErrTenantSlugReserved.Meta, nil)
		case errors.Is(err, service.ErrSlugTaken):
			response.Conflict(c, response.ErrTenantSlugTaken.Code, response.ErrTenantSlugTaken.Meta, nil)
		default:
			response.InternalError(c, response.ErrInternal.Code, response.ErrInternal.Meta)
		}
		return
	}
	h.respondCreatedIdempotent(c, response.OKTenantCreated.Code, response.OKTenantCreated.Meta, t)
}

// ---------- GET /api/v1/tenants/by-slug/:slug (public, rate-limited) ----------

func (h *Handlers) GetBySlug(c *gin.Context) {
	slug := c.Param("slug")
	// Public endpoint returns the slim projection — never expose the
	// full tenant record (contact email, timestamps) to anonymous
	// callers. Redis fronts this lookup with a 30s TTL.
	t, err := h.svc.ResolveBySlug(c, slug)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			response.NotFound(c, response.ErrTenantUnknown.Code, response.ErrTenantUnknown.Meta)
			return
		}
		response.InternalError(c, response.ErrInternal.Code, response.ErrInternal.Meta)
		return
	}
	response.OK(c, response.OKTenant.Code, response.OKTenant.Meta, t)
}

// ---------- GET /api/v1/tenants/:id (X-Internal-Service-Key) ----------

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
		response.InternalError(c, response.ErrInternal.Code, response.ErrInternal.Meta)
		return
	}
	response.OK(c, response.OKTenant.Code, response.OKTenant.Meta, t)
}

// ---------- PATCH /api/v1/tenants/:id ----------

type patchBody struct {
	Name         *string `json:"name"`
	ContactEmail *string `json:"contact_email" binding:"omitempty,email"`
	Phone        *string `json:"phone"`
	Plan         *string `json:"plan"`
}

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
		response.InternalError(c, response.ErrInternal.Code, response.ErrInternal.Meta)
		return
	}
	response.OK(c, response.OKTenantUpdated.Code, response.OKTenantUpdated.Meta, t)
}

// ---------- POST /api/v1/tenants/:id/suspend ----------

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
		response.InternalError(c, response.ErrInternal.Code, response.ErrInternal.Meta)
		return
	}
	response.OK(c, response.OKTenantSuspended.Code, response.OKTenantSuspended.Meta, t)
}

// ---------- POST /api/v1/tenants/:id/restore ----------

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
		response.InternalError(c, response.ErrInternal.Code, response.ErrInternal.Meta)
		return
	}
	response.OK(c, response.OKTenantRestored.Code, response.OKTenantRestored.Meta, t)
}

// ---------- POST /api/v1/tenants/:id/reissue-owner-otp ----------
// Generates a fresh one-time password for the Store Admin and pushes
// it through identity's internal reset-password endpoint.

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
			response.InternalError(c, response.ErrInternal.Code, response.ErrInternal.Meta)
		}
		return
	}
	response.OK(c, response.OKTenantOTPReissued.Code, response.OKTenantOTPReissued.Meta, gin.H{
		"one_time_password": otp,
	})
}

// ---------- POST /api/v1/tenants/:id/resend-payment-link ----------

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
			c.JSON(http.StatusServiceUnavailable, response.Envelope{
				Code: response.ErrBillingUnavailable.Code,
				Meta: response.ErrBillingUnavailable.Meta,
			})
		default:
			response.InternalError(c, response.ErrInternal.Code, response.ErrInternal.Meta)
		}
		return
	}
	response.OK(c, response.OKTenantPaymentLink.Code, response.OKTenantPaymentLink.Meta, gin.H{
		"payment_url": checkout.PaymentURL,
		"amount_tzs":  checkout.AmountTZS,
	})
}

// ---------- GET /internal/tenants/pending-checkout?email= ----------
// Backs drs-setup-service's sign-in path. Deliberately scoped to
// pending_payment only (see FindPendingByContactEmail) — this must
// never be usable to discover whether an email owns an ACTIVE store,
// only whether there is an unpaid registration to resume.
func (h *Handlers) PendingCheckoutByEmail(c *gin.Context) {
	email := c.Query("email")
	if email == "" {
		response.BadRequest(c, response.ErrValidation.Code, response.ErrValidation.Meta, "email is required")
		return
	}
	t, checkout, err := h.svc.PendingCheckoutByEmail(c.Request.Context(), email)
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrNotFound):
			response.NotFound(c, response.ErrTenantUnknown.Code, response.ErrTenantUnknown.Meta)
		case errors.Is(err, service.ErrBillingDown):
			c.JSON(http.StatusServiceUnavailable, response.Envelope{
				Code: response.ErrBillingUnavailable.Code,
				Meta: response.ErrBillingUnavailable.Meta,
			})
		default:
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

// ---------- GET /api/v1/tenants/:id/health ----------
// Rollup of "is this tenant still live and paid". Consumers include
// the SPA topbar's tenant status pill.
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

// ---------- POST /api/v1/public/signup (Cloudflare Turnstile) ----------
type signupBody struct {
	Slug         string `json:"slug" binding:"required"`
	Name         string `json:"name" binding:"required"`
	ContactEmail string `json:"contact_email" binding:"required,email"`
	// Not `binding:"required"` even though the form requires it. A hard
	// server-side requirement would break every signup in the window
	// between deploying this service and deploying the drs-vue form that
	// sends the field — a self-inflicted signup outage, to gain nothing
	// billing's fallback does not already cover (§10.9 item 5).
	Phone        string `json:"phone"`
	FirstName    string `json:"first_name" binding:"required"`
	LastName     string `json:"last_name" binding:"required"`
	CountryCode  string `json:"country_code"`
	TurnstileTok string `json:"turnstile_token"`
	RedirectURL  string `json:"redirect_url"`
}

func (h *Handlers) PublicSignup(c *gin.Context) {
	if h.replayIdempotent(c) {
		return
	}
	var body signupBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, response.ErrValidation.Code, response.ErrValidation.Meta, err.Error())
		return
	}
	res, err := h.svc.PublicSignup(c.Request.Context(), service.SignupInput{
		Slug:         body.Slug,
		Name:         body.Name,
		ContactEmail: body.ContactEmail,
		Phone:        body.Phone,
		FirstName:    body.FirstName,
		LastName:     body.LastName,
		CountryCode:  body.CountryCode,
		TurnstileTok: body.TurnstileTok,
		RedirectURL:  body.RedirectURL,
		ClientIP:     c.ClientIP(),
	}, h.ts)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrTurnstile):
			response.Forbidden(c, response.ErrForbidden.Code, response.ErrForbidden.Meta)
		case errors.Is(err, service.ErrSlugInvalid), errors.Is(err, service.ErrValidation):
			response.BadRequest(c, response.ErrValidation.Code, response.ErrValidation.Meta, err.Error())
		case errors.Is(err, service.ErrSlugReserved):
			response.Conflict(c, response.ErrTenantSlugReserved.Code, response.ErrTenantSlugReserved.Meta, nil)
		case errors.Is(err, service.ErrSlugTaken):
			response.Conflict(c, response.ErrTenantSlugTaken.Code, response.ErrTenantSlugTaken.Meta, nil)
		default:
			response.InternalError(c, response.ErrInternal.Code, response.ErrInternal.Meta)
		}
		return
	}
	h.respondCreatedIdempotent(c, response.OKTenantCreated.Code, response.OKTenantCreated.Meta, gin.H{
		"id":                res.Tenant.ID,
		"slug":              res.Tenant.Slug,
		"status":            res.Tenant.Status,
		// Empty when owner_existing is true: identity kept the password
		// the owner already has rather than taking the generated one, so
		// there is nothing to show them.
		"one_time_password": res.OneTimePassword,
		"owner_existing":    res.OwnerExisting,
		"owner_email":       res.OwnerEmail,
		"payment_url":       res.PaymentURL,
		"amount_tzs":        res.AmountTZS,
	})
}

// ---------- POST /api/v1/internal/tenants/:id/activate (billing→tenants) ----------
// Called by defolt-billing when the registration invoice is paid.
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
		response.InternalError(c, response.ErrInternal.Code, response.ErrInternal.Meta)
		return
	}
	response.OK(c, response.OKTenantRestored.Code, response.OKTenantRestored.Meta, t)
}

// ---------- PUT /api/v1/internal/tenants/:id/subscription-state (billing→tenants) ----------
// Called by defolt-billing when the subscription state changes.
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
		response.InternalError(c, response.ErrInternal.Code, response.ErrInternal.Meta)
		return
	}
	response.OK(c, response.OKTenantUpdated.Code, response.OKTenantUpdated.Meta, t)
}

// ---------- POST /internal/resolve-host (Traefik forward auth) ----------
// Resolves the leftmost host label to a tenant and stamps the
// X-Defolt-Tenant-* headers Traefik forwards to the upstream.
//
// The edge is no longer the gate. Every state the SPA can render now
// comes back 200 with a body-less reply, and drs-vue decides what the
// visitor sees from X-Defolt-Tenant-Status plus its own lookup through
// the public GET /api/v1/tenants/by-slug/:slug (whose 404 drives the
// "unknown store" screen). Earlier revisions answered 404 and 403 here
// with an embedded HTML page as the body, because Traefik relays the
// status, headers and body of a non-2xx forward-auth reply verbatim to
// the browser and there is no second hop. Those pages now live in
// drs-vue as real views, so the reply must be 200 or the SPA never
// loads to render them.
//
// This is not a security regression. Entitlement is enforced by the API
// services themselves: drs-setup, drs-inventory and drs-sales each run
// a RequirePaid middleware that answers 402 DL_PAYMENT_REQUIRED for a
// lapsed tenant, keyed off SubscriptionState in defolt-billing-service.
// Serving the SPA shell to a suspended tenant therefore grants no data
// access; the shell only renders the screen that explains the state.
//
// Note the two enums stay deliberately unreconciled. TenantStatus here
// (pending_payment|active|grace|suspended|archived) now only drives the
// SPA gate copy, while enforcement keys off billing's SubscriptionState
// (awaiting_registration|trial|active|grace|suspended|cancelled). They
// can disagree, and nothing in this service should try to map them.
func (h *Handlers) ResolveHost(c *gin.Context) {
	host := c.GetHeader("X-Forwarded-Host")
	if host == "" {
		host = c.Request.Host
	}
	// Strip port + take the leftmost label as the slug.
	slug := ""
	if p := indexByte(host, ':'); p >= 0 {
		host = host[:p]
	}
	if dot := indexByte(host, '.'); dot > 0 {
		slug = host[:dot]
	}
	if slug == "" {
		// No subdomain at all: this host carries no tenant and must
		// never have reached forward auth. Refuse rather than guess.
		c.Status(http.StatusForbidden)
		return
	}
	t, err := h.svc.ResolveBySlug(c, slug)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			// Unknown slug: 200 with status "unknown" and no tenant
			// identity headers, so the SPA loads and renders its
			// "store not found" screen. Deliberately no tenant id,
			// slug or product header: there is no tenant to name.
			c.Header(middleware.TenantStatusHeader, "unknown")
			c.Status(http.StatusOK)
			return
		}
		c.Status(http.StatusServiceUnavailable)
		return
	}
	switch t.Status {
	case model.StatusArchived:
		// Archived stays a hard 403 with an empty body. Suspended and
		// archived used to share this branch and are now deliberately
		// split: a suspended tenant is a customer who may pay and come
		// back, so it gets a screen; an archived tenant is gone, with
		// no SPA screen worth serving and no route back.
		c.Status(http.StatusForbidden)
	default:
		// active, grace, suspended and pending_payment all pass with
		// the full header set. The status header is what the SPA gate
		// reads to pick its screen.
		c.Header(middleware.TenantHeader, t.ID.String())
		c.Header(middleware.TenantSlugHeader, t.Slug)
		c.Header(middleware.TenantProductHeader, t.Product)
		c.Header(middleware.TenantStatusHeader, string(t.Status))
		c.Status(http.StatusOK)
	}
}

// indexByte is a tiny helper so we don't pull in strings.IndexByte
// (the whole strings package chain adds imports we don't otherwise
// need in this handler file).
func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

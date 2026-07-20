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
	"defolt-tenants-service/static"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Handlers struct {
	svc     *service.TenantsService
	ts      *service.Turnstile
	cache   *cache.Cache
	version string

	// Values substituted into the embedded landing pages at serve time.
	turnstileSiteKey string
	tenantBaseDomain string
}

func New(svc *service.TenantsService, ts *service.Turnstile, c *cache.Cache, version, turnstileSiteKey, tenantBaseDomain string) *Handlers {
	return &Handlers{
		svc:              svc,
		ts:               ts,
		cache:            c,
		version:          version,
		turnstileSiteKey: turnstileSiteKey,
		tenantBaseDomain: tenantBaseDomain,
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
	FirstName    string `json:"first_name" binding:"required"`
	LastName     string `json:"last_name" binding:"required"`
	CountryCode  string `json:"country_code"`
	TurnstileTok string `json:"turnstile_token"`
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
		FirstName:    body.FirstName,
		LastName:     body.LastName,
		CountryCode:  body.CountryCode,
		TurnstileTok: body.TurnstileTok,
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
		"one_time_password": res.OneTimePassword,
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

// ---------- GET /public/<page>.html + /public/landing/:page (plan §5.17) ----------
// Lifecycle landing pages Traefik points unresolved / suspended /
// pending hosts at. Served from the embedded FS, allowlisted.
func (h *Handlers) LandingPage(c *gin.Context) {
	h.serveLandingPage(c, c.Param("page"))
}

// LandingPageNamed pins a handler to one specific page for the
// explicit /public/<page>.html routes.
func (h *Handlers) LandingPageNamed(page string) gin.HandlerFunc {
	return func(c *gin.Context) { h.serveLandingPage(c, page) }
}

func (h *Handlers) serveLandingPage(c *gin.Context, page string) {
	page = strings.TrimSuffix(page, ".html")
	file, ok := static.Pages[page]
	if !ok {
		c.Status(http.StatusNotFound)
		return
	}
	body, err := static.FS.ReadFile(file)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	body = h.injectPageConfig(body)
	c.Header("Cache-Control", "public, max-age=300")
	c.Data(http.StatusOK, "text/html; charset=utf-8", body)
}

// injectPageConfig substitutes the runtime config placeholders in an
// embedded page. Pages that carry no placeholder come back unchanged,
// and a page opened straight off disk still works because it falls
// back to the placeholder-as-literal default in its own script.
func (h *Handlers) injectPageConfig(body []byte) []byte {
	out := string(body)
	out = strings.ReplaceAll(out, "__TURNSTILE_SITE_KEY__", h.turnstileSiteKey)
	out = strings.ReplaceAll(out, "__TENANT_BASE_DOMAIN__", h.tenantBaseDomain)
	return []byte(out)
}

// lifecyclePage writes an embedded lifecycle page as the body of a
// non-2xx forward-auth reply. Traefik relays the status, headers and
// body of a forward-auth response verbatim to the browser whenever the
// auth service answers non-2xx, so the page the visitor should see has
// to travel in that reply: there is no second hop to serve it from.
// If the embedded read ever fails we degrade to the bare status rather
// than turning a routing answer into a 500.
func (h *Handlers) lifecyclePage(c *gin.Context, status int, page string) {
	file, ok := static.Pages[page]
	if !ok {
		c.Status(status)
		return
	}
	body, err := static.FS.ReadFile(file)
	if err != nil {
		c.Status(status)
		return
	}
	// No caching: a host resolves differently the moment the tenant
	// signs up or is restored, and a cached error page would outlive
	// that change in the browser.
	c.Header("Cache-Control", "no-store")
	c.Data(status, "text/html; charset=utf-8", h.injectPageConfig(body))
}

// ---------- POST /internal/resolve-host (Traefik forward auth) ----------
// Takes the Host header, extracts the leftmost label as the slug, and
// returns 200 + X-Defolt-Tenant-* headers, or a non-2xx carrying the
// matching lifecycle page as its body. Used by Traefik as a
// forward-auth middleware: on 200 the headers do the work and the body
// is discarded, on non-2xx the body is what the visitor sees.
func (h *Handlers) ResolveHost(c *gin.Context) {
	host := c.GetHeader("X-Forwarded-Host")
	if host == "" {
		host = c.Request.Host
	}
	// Strip port + take the leftmost label as the slug.
	slug := ""
	if i := len(host); i > 0 {
		if p := indexByte(host, ':'); p >= 0 {
			host = host[:p]
		}
	}
	if dot := indexByte(host, '.'); dot > 0 {
		slug = host[:dot]
	}
	if slug == "" {
		c.Status(http.StatusForbidden)
		return
	}
	t, err := h.svc.ResolveBySlug(c, slug)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			h.lifecyclePage(c, http.StatusNotFound, "not-registered")
			return
		}
		c.Status(http.StatusServiceUnavailable)
		return
	}
	switch t.Status {
	case model.StatusActive, model.StatusGrace:
		c.Header(middleware.TenantHeader, t.ID.String())
		c.Header(middleware.TenantSlugHeader, t.Slug)
		c.Header(middleware.TenantProductHeader, t.Product)
		c.Header(middleware.TenantStatusHeader, string(t.Status))
		c.Status(http.StatusOK)
	case model.StatusSuspended, model.StatusArchived:
		h.lifecyclePage(c, http.StatusForbidden, "suspended")
	default:
		c.Status(http.StatusAccepted) // pending_payment → landing page
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

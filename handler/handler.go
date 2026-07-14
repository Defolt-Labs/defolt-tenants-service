package handler

import (
	"errors"
	"net/http"

	"defolt-tenants-service/middleware"
	"defolt-tenants-service/model"
	"defolt-tenants-service/repository"
	"defolt-tenants-service/response"
	"defolt-tenants-service/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Handlers struct {
	svc      *service.TenantsService
	ts       *service.Turnstile
	identity *service.IdentityClient
	version  string
}

func New(svc *service.TenantsService, ts *service.Turnstile, id *service.IdentityClient, version string) *Handlers {
	return &Handlers{svc: svc, ts: ts, identity: id, version: version}
}

// Health is the /health + /ready probe reply.
func (h *Handlers) Health(c *gin.Context) {
	response.OK(c, response.OKHealth.Code, response.OKHealth.Meta, gin.H{"version": h.version})
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
	response.Created(c, response.OKTenantCreated.Code, response.OKTenantCreated.Meta, t)
}

// ---------- GET /api/v1/tenants/by-slug/:slug (public, rate-limited) ----------

func (h *Handlers) GetBySlug(c *gin.Context) {
	slug := c.Param("slug")
	t, err := h.svc.ResolveBySlug(c, slug)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			response.NotFound(c, response.ErrTenantUnknown.Code, response.ErrTenantUnknown.Meta)
			return
		}
		response.InternalError(c, response.ErrInternal.Code, response.ErrInternal.Meta)
		return
	}
	// Public endpoint returns a slim projection — never expose the
	// full tenant record (contact email, timestamps) to anonymous
	// callers. Traefik forward auth uses this and only needs id +
	// status + product.
	response.OK(c, response.OKTenant.Code, response.OKTenant.Meta, gin.H{
		"id":      t.ID,
		"slug":    t.Slug,
		"status":  t.Status,
		"product": t.Product,
	})
}

// ---------- GET /api/v1/tenants/:id (JWT — internal for now) ----------

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
	var body signupBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, response.ErrValidation.Code, response.ErrValidation.Meta, err.Error())
		return
	}
	t, err := h.svc.PublicSignup(c.Request.Context(), service.SignupInput{
		Slug:         body.Slug,
		Name:         body.Name,
		ContactEmail: body.ContactEmail,
		FirstName:    body.FirstName,
		LastName:     body.LastName,
		CountryCode:  body.CountryCode,
		TurnstileTok: body.TurnstileTok,
		ClientIP:     c.ClientIP(),
	}, h.ts, h.identity)
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
	response.Created(c, response.OKTenantCreated.Code, response.OKTenantCreated.Meta, gin.H{
		"id":   t.ID,
		"slug": t.Slug,
		"status": t.Status,
	})
}

// ---------- POST /api/v1/internal/tenants/:id/activate (billing→tenants) ----------
// Called by drs-billing when the registration invoice is paid.
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

// ---------- POST /internal/resolve-host (Traefik forward auth) ----------
// Takes the Host header, extracts the leftmost label as the slug,
// and returns 200 + X-Defolt-Tenant-* headers or a 403 / 404. Used by
// Traefik as a forward-auth middleware; the response body is
// discarded — headers do the work.
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
			c.Status(http.StatusNotFound)
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
		c.Status(http.StatusForbidden)
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

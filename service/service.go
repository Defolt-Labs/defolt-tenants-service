package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"defolt-tenants-service/cache"
	"defolt-tenants-service/logger"
	"defolt-tenants-service/model"
	"defolt-tenants-service/repository"
	"defolt-tenants-service/reqid"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
)

var (
	ErrSlugInvalid  = errors.New("slug is invalid")
	ErrSlugReserved = errors.New("slug is reserved")
	ErrSlugTaken    = errors.New("slug is taken")
	ErrValidation   = errors.New("validation failed")
	ErrNoOwner      = errors.New("tenant has no resolvable owner user")
	ErrBillingDown  = errors.New("billing service unavailable")
)

// slugRegex mirrors plan §4.9: 3-32 chars, starts with a letter, ends
// with alnum, dashes allowed internally.
var slugRegex = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}[a-z0-9]$`)

type TenantsService struct {
	repo     *repository.Repo
	nc       *nats.Conn
	cache    *cache.Cache
	identity *IdentityClient
	billing  *BillingClient
	reserved map[string]struct{}
}

func New(repo *repository.Repo, nc *nats.Conn, c *cache.Cache, identity *IdentityClient, billing *BillingClient, reserved []string) *TenantsService {
	set := make(map[string]struct{}, len(reserved))
	for _, r := range reserved {
		set[strings.ToLower(strings.TrimSpace(r))] = struct{}{}
	}
	return &TenantsService{repo: repo, nc: nc, cache: c, identity: identity, billing: billing, reserved: set}
}

// CreateInput mirrors the POST /api/v1/tenants body. Product defaults
// to "drs" for now — Phase 3+ opens the door to other verticals.
type CreateInput struct {
	Slug         string
	Name         string
	ContactEmail string
	Currency     string
	Timezone     string
	CountryCode  string
	Product      string
	Plan         string
}

func (s *TenantsService) Create(ctx context.Context, in CreateInput) (*model.Tenant, error) {
	slug := strings.ToLower(strings.TrimSpace(in.Slug))
	if !slugRegex.MatchString(slug) {
		return nil, ErrSlugInvalid
	}
	if _, ok := s.reserved[slug]; ok {
		return nil, ErrSlugReserved
	}
	if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.ContactEmail) == "" {
		return nil, ErrValidation
	}
	t := &model.Tenant{
		Slug:         slug,
		Name:         strings.TrimSpace(in.Name),
		ContactEmail: strings.TrimSpace(in.ContactEmail),
		Currency:     defaultStr(in.Currency, "TZS"),
		Timezone:     defaultStr(in.Timezone, "Africa/Dar_es_Salaam"),
		CountryCode:  defaultStr(in.CountryCode, "TZ"),
		Product:      defaultStr(in.Product, "drs"),
		Plan:         defaultStr(in.Plan, "standard"),
		Status:       model.StatusPendingPayment,
	}
	if err := s.repo.Insert(ctx, t); err != nil {
		if errors.Is(err, repository.ErrSlugTaken) {
			return nil, ErrSlugTaken
		}
		return nil, err
	}
	s.emit("tenant.created", map[string]any{
		"tenant_id":   t.ID,
		"slug":        t.Slug,
		"owner_email": t.ContactEmail,
		"product":     t.Product,
		"state":       string(t.Status),
	})
	return t, nil
}

// ResolveBySlug is the hot-path lookup called by Traefik forward auth
// on every request. Tries the shared Redis cache first (30s TTL, plan
// §5.3/§5.13); on miss reads Postgres and repopulates the cache.
// Returns the slim projection — the only shape the hot path needs.
func (s *TenantsService) ResolveBySlug(ctx context.Context, slug string) (*model.TenantSlim, error) {
	slug = strings.ToLower(strings.TrimSpace(slug))
	if b, ok := s.cache.GetSlug(ctx, slug); ok {
		var slim model.TenantSlim
		if err := json.Unmarshal(b, &slim); err == nil {
			return &slim, nil
		}
	}
	t, err := s.repo.FindBySlug(ctx, slug)
	if err != nil {
		return nil, err
	}
	slim := t.Slim()
	if b, err := json.Marshal(slim); err == nil {
		s.cache.SetSlug(ctx, slug, b)
	}
	return &slim, nil
}

// Get by UUID.
func (s *TenantsService) Get(ctx context.Context, id uuid.UUID) (*model.Tenant, error) {
	return s.repo.FindByID(ctx, id)
}

// UpdateInput carries the PATCH /api/v1/tenants/:id body. Nil fields
// stay untouched.
type UpdateInput struct {
	Name         *string
	ContactEmail *string
	Plan         *string
}

// Update mutates the editable tenant fields, invalidates the slug
// cache and emits `tenant.updated`.
func (s *TenantsService) Update(ctx context.Context, id uuid.UUID, in UpdateInput) (*model.Tenant, error) {
	t, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	changed := false
	if in.Name != nil && strings.TrimSpace(*in.Name) != "" {
		t.Name = strings.TrimSpace(*in.Name)
		changed = true
	}
	if in.ContactEmail != nil && strings.TrimSpace(*in.ContactEmail) != "" {
		t.ContactEmail = strings.TrimSpace(*in.ContactEmail)
		changed = true
	}
	if in.Plan != nil && strings.TrimSpace(*in.Plan) != "" {
		t.Plan = strings.TrimSpace(*in.Plan)
		changed = true
	}
	if !changed {
		return t, nil
	}
	if err := s.repo.Save(ctx, t); err != nil {
		return nil, err
	}
	s.cache.InvalidateSlug(ctx, t.Slug)
	s.emit("tenant.updated", map[string]any{
		"tenant_id": t.ID,
		"slug":      t.Slug,
		"plan":      t.Plan,
	})
	return t, nil
}

// Suspend moves a tenant into `suspended`. Emits `tenant.status_changed`
// plus the distinct `tenant.suspended` subject, and drops the shared
// Redis cache entry so every replica sees the new state within a tick.
func (s *TenantsService) Suspend(ctx context.Context, id uuid.UUID) (*model.Tenant, error) {
	if err := s.repo.SetStatus(ctx, id, model.StatusSuspended); err != nil {
		return nil, err
	}
	t, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	s.cache.InvalidateSlug(ctx, t.Slug)
	payload := map[string]any{
		"tenant_id": t.ID,
		"slug":      t.Slug,
		"status":    string(t.Status),
	}
	s.emit("tenant.status_changed", payload)
	s.emit("tenant.suspended", payload)
	return t, nil
}

// Restore flips a suspended/archived tenant back to `active`.
func (s *TenantsService) Restore(ctx context.Context, id uuid.UUID) (*model.Tenant, error) {
	if err := s.repo.SetStatus(ctx, id, model.StatusActive); err != nil {
		return nil, err
	}
	t, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	s.cache.InvalidateSlug(ctx, t.Slug)
	payload := map[string]any{
		"tenant_id": t.ID,
		"slug":      t.Slug,
		"status":    string(t.Status),
	}
	s.emit("tenant.status_changed", payload)
	s.emit("tenant.restored", payload)
	return t, nil
}

// ActivateAfterRegistration is called by the billing consumer when
// the 2,000 TZS registration invoice is paid. Flips status to `active`
// and starts the 7-day trial clock (plan §5.11).
func (s *TenantsService) ActivateAfterRegistration(ctx context.Context, id uuid.UUID) (*model.Tenant, error) {
	t, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if t.Status != model.StatusPendingPayment {
		return t, nil
	}
	now := time.Now()
	trialEnd := now.Add(7 * 24 * time.Hour)
	t.Status = model.StatusActive
	t.TrialStartsAt = &now
	t.TrialEndsAt = &trialEnd
	if err := s.repo.Save(ctx, t); err != nil {
		return nil, err
	}
	s.cache.InvalidateSlug(ctx, t.Slug)
	// Carry the owner identity so a DRS consumer can provision the owner's
	// user_mirror on activation and let them log in. Signup persists
	// OwnerUserID/OwnerEmail on the tenant; without them here nothing
	// downstream could tie the activated tenant to its Store Admin — the
	// onboarding "sealed room" this closes.
	s.emit("tenant.activated", map[string]any{
		"tenant_id":       t.ID,
		"slug":            t.Slug,
		"name":            t.Name,
		"owner_user_id":   t.OwnerUserID,
		"owner_email":     t.OwnerEmail,
		"trial_starts_at": now,
		"trial_ends_at":   trialEnd,
	})
	return t, nil
}

// ReissueOwnerOTP generates a fresh one-time password for the tenant's
// Store Admin and pushes it to identity's internal reset-password
// endpoint. When owner_user_id was never stored (old rows, identity
// hiccup at signup) it is backfilled via identity's by-email lookup.
func (s *TenantsService) ReissueOwnerOTP(ctx context.Context, id uuid.UUID) (string, error) {
	t, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return "", err
	}
	if t.OwnerUserID == nil {
		email := t.OwnerEmail
		if email == "" {
			email = t.ContactEmail
		}
		if email == "" || s.identity == nil {
			return "", ErrNoOwner
		}
		userID, err := s.identity.FindUserIDByEmail(ctx, email)
		if err != nil {
			logger.LogWarn("", "reissue-otp", fmt.Sprintf("tenant=%s by-email lookup failed: %v", t.ID, err))
			return "", ErrNoOwner
		}
		t.OwnerUserID = userID
		if t.OwnerEmail == "" {
			t.OwnerEmail = email
		}
		if err := s.repo.Save(ctx, t); err != nil {
			return "", err
		}
	}
	password := generatePassword()
	if err := s.identity.ResetPassword(ctx, *t.OwnerUserID, password); err != nil {
		return "", err
	}
	return password, nil
}

// ResendPaymentLink asks billing for a fresh Selcom checkout URL for
// a tenant still stuck in pending_payment.
func (s *TenantsService) ResendPaymentLink(ctx context.Context, id uuid.UUID) (*CheckoutResult, error) {
	t, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	email := t.OwnerEmail
	if email == "" {
		email = t.ContactEmail
	}
	res, err := s.billing.CreateCheckout(ctx, t.ID, email)
	if err != nil {
		logger.LogWarn("", "resend-payment-link", fmt.Sprintf("tenant=%s: %v", t.ID, err))
		return nil, ErrBillingDown
	}
	return res, nil
}

// SweepAbandoned runs on the 15-minute ticker and hard-deletes tenants
// stuck in `pending_payment` past the 24-hour threshold. Publishes
// `tenant.abandoned` per §5.11 so downstream cleanups fire, and
// best-effort deletes the orphaned Store Admin in identity when
// owner_user_id was stored (old rows without it are skipped silently).
func (s *TenantsService) SweepAbandoned(ctx context.Context) (int, error) {
	// The sweeper is a ticker, not a request: there is no inbound
	// X-Request-ID to inherit, and defolt-identity rejects any call
	// without one. So this is the one place that MINTS an ID rather
	// than forwarding — it is the origin of its own trace, exactly like
	// the RequestID middleware minting for a request that arrived
	// without a header. Forwarding still applies everywhere else.
	ctx = reqid.With(ctx, "sweep-"+uuid.NewString()[:8])

	rows, err := s.repo.ListPendingCleanup(ctx, 24)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, t := range rows {
		// Identity user first, tenant row second. The tenant row is the
		// ONLY record of which identity user belongs to this signup, so
		// dropping it before the owner is deleted strands that account
		// permanently — and with it the email address, which then blocks
		// the person from ever signing up again. Failing here leaves the
		// whole tenant for the next tick to retry; the delete is
		// idempotent, so retrying is free.
		if t.OwnerUserID != nil && s.identity != nil {
			if err := s.identity.DeleteUser(ctx, *t.OwnerUserID); err != nil {
				logger.LogWarn("", "sweep-abandoned", fmt.Sprintf("tenant=%s identity delete failed, retrying next tick: %v", t.ID, err))
				continue
			}
		}
		if err := s.repo.HardDelete(ctx, t.ID); err != nil {
			continue
		}
		s.cache.InvalidateSlug(ctx, t.Slug)
		s.emit("tenant.abandoned", map[string]any{
			"tenant_id": t.ID,
			"slug":      t.Slug,
		})
		n++
	}
	return n, nil
}

func (s *TenantsService) emit(subject string, payload map[string]any) {
	if s.nc == nil {
		return
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_ = s.nc.Publish(subject, b)
}

func defaultStr(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

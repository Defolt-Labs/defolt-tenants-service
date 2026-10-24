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
	ErrSlugInvalid   = errors.New("slug is invalid")
	ErrSlugReserved  = errors.New("slug is reserved")
	ErrSlugTaken     = errors.New("slug is taken")
	ErrValidation    = errors.New("validation failed")
	ErrNoOwner       = errors.New("tenant has no resolvable owner user")
	ErrBillingDown   = errors.New("billing service unavailable")
	ErrOwnerRequired = errors.New("the health product requires an owner on every tenant, and owner_user_id was not given")
)

// RequireOwnerForProduct refuses a health tenant created with no owner.
func RequireOwnerForProduct(product string, owner *uuid.UUID) error {
	if NormalizeProduct(product) != "health" {
		return nil
	}
	if owner == nil || *owner == uuid.Nil {
		return ErrOwnerRequired
	}
	return nil
}

// slugRegex validates tenant slugs.
var slugRegex = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}[a-z0-9]$`)

// pendingSweepLimit bounds one pass of the activation sweep.
const pendingSweepLimit = 500

type TenantsService struct {
	repo       *repository.Repo
	rows       signupRows
	nc         *nats.Conn
	publish    func(subject string, data []byte) error
	cache      *cache.Cache
	identity   *IdentityClient
	billing    *BillingClient
	reserved   map[string]struct{}
	rootDomain string
}

func New(repo *repository.Repo, nc *nats.Conn, c *cache.Cache, identity *IdentityClient, billing *BillingClient, reserved []string, rootDomain string) *TenantsService {
	set := make(map[string]struct{}, len(reserved))
	for _, r := range reserved {
		set[strings.ToLower(strings.TrimSpace(r))] = struct{}{}
	}
	s := &TenantsService{repo: repo, rows: repo, nc: nc, cache: c, identity: identity, billing: billing, reserved: set, rootDomain: rootDomain}
	if nc != nil {
		s.publish = nc.Publish
	}
	return s
}

type signupRows interface {
	Insert(ctx context.Context, t *model.Tenant) error
	FindByID(ctx context.Context, id uuid.UUID) (*model.Tenant, error)
	FindBySlug(ctx context.Context, namespace, slug string) (*model.Tenant, error)
	SaveName(ctx context.Context, t *model.Tenant) error
	SavePhone(ctx context.Context, t *model.Tenant) error
	SaveOwner(ctx context.Context, t *model.Tenant) error
}

// CreateInput mirrors the tenant creation payload.
type CreateInput struct {
	Slug            string
	Name            string
	ContactEmail    string
	Phone           string
	Currency        string
	Timezone        string
	CountryCode     string
	Product         string
	Plan            string
	OwnerUserID     *uuid.UUID
	OwnerEmail      string
	OwnerFirstName  string
	OwnerMiddleName string
	OwnerLastName   string
}

// Create inserts the tenant record and publishes tenant.created.
func (s *TenantsService) Create(ctx context.Context, in CreateInput) (*model.Tenant, error) {
	t, err := s.createRow(ctx, in)
	if err != nil {
		return nil, err
	}
	s.emit("tenant.created", tenantCreatedPayload(ctx, t))
	return t, nil
}

// createRow validates and inserts tenant row without publishing events.
func (s *TenantsService) createRow(ctx context.Context, in CreateInput) (*model.Tenant, error) {
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
	phone, err := RequirePhone(in.Phone)
	if err != nil {
		return nil, err
	}
	t := newTenantFromCreate(in, slug, phone)
	if err := s.rows.Insert(ctx, t); err != nil {
		if errors.Is(err, repository.ErrSlugTaken) {
			return nil, ErrSlugTaken
		}
		return nil, err
	}
	return t, nil
}

// newTenantFromCreate maps CreateInput to a Tenant model instance.
func newTenantFromCreate(in CreateInput, slug, phone string) *model.Tenant {
	return &model.Tenant{
		Slug:            slug,
		Name:            strings.TrimSpace(in.Name),
		ContactEmail:    strings.TrimSpace(in.ContactEmail),
		Phone:           phone,
		Currency:        defaultStr(in.Currency, "TZS"),
		Timezone:        defaultStr(in.Timezone, "Africa/Dar_es_Salaam"),
		CountryCode:     defaultStr(in.CountryCode, "TZ"),
		Product:         defaultStr(in.Product, "drs"),
		Plan:            defaultStr(in.Plan, "standard"),
		Status:          model.StatusPendingPayment,
		OwnerUserID:     in.OwnerUserID,
		OwnerEmail:      strings.TrimSpace(in.OwnerEmail),
		OwnerFirstName:  strings.TrimSpace(in.OwnerFirstName),
		OwnerMiddleName: strings.TrimSpace(in.OwnerMiddleName),
		OwnerLastName:   strings.TrimSpace(in.OwnerLastName),
		OwnerExisted:    ownerExistedOnCreate(in.OwnerUserID),
	}
}

// ownerExistedOnCreate returns true when an existing user is attached.
func ownerExistedOnCreate(owner *uuid.UUID) *bool {
	if owner == nil || *owner == uuid.Nil {
		return nil
	}
	v := true
	return &v
}

// tenantCreatedPayload constructs the wire payload for tenant.created.
func tenantCreatedPayload(ctx context.Context, t *model.Tenant) map[string]any {
	return map[string]any{
		"tenant_id":         t.ID,
		"slug":              t.Slug,
		"name":              t.Name,
		"owner_email":       t.ContactEmail,
		"product":           t.Product,
		"state":             string(t.Status),
		"owner_first_name":  t.OwnerFirstName,
		"owner_middle_name": t.OwnerMiddleName,
		"owner_last_name":   t.OwnerLastName,
		"request_id":        reqid.From(ctx),
	}
}

// ResolveBySlug returns slim tenant details, using cache when available.
func (s *TenantsService) ResolveBySlug(ctx context.Context, product, slug string) (*model.TenantSlim, error) {
	product = NormalizeProduct(product)
	slug = strings.ToLower(strings.TrimSpace(slug))
	if b, ok := s.cache.GetSlug(ctx, product, slug); ok {
		var slim model.TenantSlim
		if err := json.Unmarshal(b, &slim); err == nil {
			return &slim, nil
		}
	}
	t, err := s.repo.FindBySlug(ctx, product, slug)
	if err != nil {
		return nil, err
	}
	slim := t.Slim()
	if b, err := json.Marshal(slim); err == nil {
		s.cache.SetSlug(ctx, product, slug, b)
	}
	return &slim, nil
}

// Get returns the tenant matching the UUID.
func (s *TenantsService) Get(ctx context.Context, id uuid.UUID) (*model.Tenant, error) {
	return s.repo.FindByID(ctx, id)
}

type UpdateInput struct {
	Name         *string
	ContactEmail *string
	Phone        *string
	Plan         *string
}

// Update mutates editable tenant fields and invalidates slug cache.
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
	if in.Phone != nil {
		p, err := RequirePhone(*in.Phone)
		if err != nil {
			return nil, err
		}
		if p != t.Phone {
			t.Phone = p
			changed = true
		}
	}
	if in.Plan != nil && strings.TrimSpace(*in.Plan) != "" {
		t.Plan = strings.TrimSpace(*in.Plan)
		changed = true
	}
	if !changed {
		return t, nil
	}
	if err := s.repo.SaveDetails(ctx, t); err != nil {
		return nil, err
	}
	s.cache.InvalidateSlug(ctx, t.Product, t.Slug)
	s.emit("tenant.updated", map[string]any{
		"tenant_id": t.ID,
		"slug":      t.Slug,
		"plan":      t.Plan,
	})
	return t, nil
}

// Suspend transitions tenant status to suspended.
func (s *TenantsService) Suspend(ctx context.Context, id uuid.UUID) (*model.Tenant, error) {
	if err := s.repo.SetStatus(ctx, id, model.StatusSuspended); err != nil {
		return nil, err
	}
	t, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	s.cache.InvalidateSlug(ctx, t.Product, t.Slug)
	payload := map[string]any{
		"tenant_id": t.ID,
		"slug":      t.Slug,
		"status":    string(t.Status),
	}
	s.emit("tenant.status_changed", payload)
	s.emit("tenant.suspended", payload)
	return t, nil
}

// Restore transitions tenant status back to active.
func (s *TenantsService) Restore(ctx context.Context, id uuid.UUID) (*model.Tenant, error) {
	if err := s.repo.SetStatus(ctx, id, model.StatusActive); err != nil {
		return nil, err
	}
	t, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	s.cache.InvalidateSlug(ctx, t.Product, t.Slug)
	payload := map[string]any{
		"tenant_id": t.ID,
		"slug":      t.Slug,
		"status":    string(t.Status),
	}
	s.emit("tenant.status_changed", payload)
	s.emit("tenant.restored", payload)
	return t, nil
}

// ActivateAfterRegistration activates a tenant after initial registration payment.
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
	if t.ActivatedAt == nil {
		t.ActivatedAt = &now
	}
	if err := s.repo.SaveRegistrationActivation(ctx, t); err != nil {
		return nil, err
	}
	s.cache.InvalidateSlug(ctx, t.Product, t.Slug)
	s.emit("tenant.activated", activatedPayload(ctx, t))
	return t, nil
}

// activatedPayload constructs the wire payload for tenant.activated.
func activatedPayload(ctx context.Context, t *model.Tenant) map[string]any {
	ownerEmail := strings.TrimSpace(t.OwnerEmail)
	if ownerEmail == "" {
		ownerEmail = strings.TrimSpace(t.ContactEmail)
	}
	p := map[string]any{
		"tenant_id":         t.ID,
		"slug":              t.Slug,
		"name":              t.Name,
		"owner_user_id":     t.OwnerUserID,
		"owner_email":       ownerEmail,
		"owner_first_name":  t.OwnerFirstName,
		"owner_middle_name": t.OwnerMiddleName,
		"owner_last_name":   t.OwnerLastName,
		"owner_existed":     t.OwnerExisted != nil && *t.OwnerExisted,
		"product":           t.Product,
		"request_id":        reqid.From(ctx),
	}
	if t.ActivatedAt != nil {
		p["activated_at"] = *t.ActivatedAt
	}
	if t.TrialStartsAt != nil {
		p["trial_starts_at"] = *t.TrialStartsAt
	}
	if t.TrialEndsAt != nil {
		p["trial_ends_at"] = *t.TrialEndsAt
	}
	return p
}

// ActivateExploring activates a tenant with an exploring subscription state.
func (s *TenantsService) ActivateExploring(ctx context.Context, id uuid.UUID) (*model.Tenant, bool, error) {
	t, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, false, err
	}
	if t.Status != model.StatusPendingPayment {
		return t, false, nil
	}
	now := time.Now()
	t.Status = model.StatusActive
	if t.ActivatedAt == nil {
		t.ActivatedAt = &now
	}
	if err := s.repo.SaveActivation(ctx, t); err != nil {
		return nil, false, err
	}
	s.cache.InvalidateSlug(ctx, t.Product, t.Slug)
	s.emit("tenant.activated", activatedPayload(ctx, t))
	logger.LogInfo("activate-exploring", fmt.Sprintf("tenant=%s slug=%s product=%s is exploring and is now active; tenant.activated published", t.ID, t.Slug, t.Product))
	return t, true, nil
}

// TenantStatusForSubscriptionState maps billing subscription states to tenant statuses.
func TenantStatusForSubscriptionState(subState string) model.TenantStatus {
	switch subState {
	case "awaiting_registration":
		return model.StatusPendingPayment
	case "exploring":
		return model.StatusActive
	case "trial", "active":
		return model.StatusActive
	case "grace":
		return model.StatusGrace
	case "suspended", "cancelled":
		return model.StatusSuspended
	default:
		return model.StatusSuspended
	}
}

// SyncSubscriptionState updates tenant status from billing state updates.
func (s *TenantsService) SyncSubscriptionState(ctx context.Context, id uuid.UUID, subState string) (*model.Tenant, error) {
	t, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	newStatus := TenantStatusForSubscriptionState(subState)
	oldStatus := t.Status
	if oldStatus == newStatus {
		return t, nil
	}
	t.Status = newStatus
	if newStatus == model.StatusActive && t.ActivatedAt == nil {
		now := time.Now()
		t.ActivatedAt = &now
	}
	if err := s.repo.SaveActivation(ctx, t); err != nil {
		return nil, err
	}
	s.cache.InvalidateSlug(ctx, t.Product, t.Slug)
	s.emit("tenant.status_changed", map[string]any{
		"tenant_id":        t.ID,
		"slug":             t.Slug,
		"status":           string(t.Status),
		"billing_substate": subState,
	})

	if oldStatus == model.StatusPendingPayment && newStatus == model.StatusActive {
		s.emit("tenant.activated", activatedPayload(ctx, t))
	}

	return t, nil
}

// ReissueOwnerOTP resets the owner password and updates credentials in identity.
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
		if err := s.repo.SaveOwnerLink(ctx, t); err != nil {
			return "", err
		}
	}
	password := generatePassword()
	if err := s.identity.ResetPassword(ctx, *t.OwnerUserID, password); err != nil {
		return "", err
	}
	return password, nil
}

// ResendPaymentLink requests a fresh checkout URL for pending registration.
func (s *TenantsService) ResendPaymentLink(ctx context.Context, id uuid.UUID) (*CheckoutResult, error) {
	t, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	email := t.OwnerEmail
	if email == "" {
		email = t.ContactEmail
	}
	returnURL := ProductReturnURL(t.Product, t.Slug, s.rootDomain)
	res, err := s.billing.CreateCheckout(ctx, t.ID, email, returnURL)
	if err != nil {
		logger.LogWarn("", "resend-payment-link", fmt.Sprintf("tenant=%s: %v", t.ID, err))
		return nil, ErrBillingDown
	}
	return res, nil
}

// PendingCheckoutByEmail returns pending tenant and fresh checkout for an owner email.
func (s *TenantsService) PendingCheckoutByEmail(ctx context.Context, email, product string) (*model.Tenant, *CheckoutResult, error) {
	t, err := s.repo.FindPendingByContactEmail(ctx, email, product)
	if err != nil {
		return nil, nil, err
	}
	checkout, err := s.ResendPaymentLink(ctx, t.ID)
	if err != nil {
		return t, nil, err
	}
	return t, checkout, nil
}

// SweepAbandoned permanently deletes unactivated signups after the retention period.
func (s *TenantsService) SweepAbandoned(ctx context.Context) (int, error) {
	ctx = reqid.With(ctx, "sweep-"+uuid.NewString()[:8])

	rows, err := s.repo.ListPendingCleanup(ctx, 24)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, t := range rows {
		if keep, why := s.abandonBlocked(ctx, t.ID); keep {
			logger.LogInfo("sweep-abandoned", fmt.Sprintf("tenant=%s left alone: %s", t.ID, why))
			continue
		}
		if t.OwnerUserID != nil && s.identity != nil {
			others, err := s.repo.CountOtherTenantsOwnedBy(ctx, *t.OwnerUserID, t.ID)
			if err != nil {
				logger.LogWarn("", "sweep-abandoned", fmt.Sprintf("tenant=%s owner check failed, retrying next tick: %v", t.ID, err))
				continue
			}
			if others > 0 {
				logger.LogInfo("sweep-abandoned", fmt.Sprintf("tenant=%s owner=%s owns %d other tenant(s), keeping the identity account", t.ID, *t.OwnerUserID, others))
			} else if err := s.identity.DeleteUser(ctx, *t.OwnerUserID); err != nil {
				logger.LogWarn("", "sweep-abandoned", fmt.Sprintf("tenant=%s identity delete failed, retrying next tick: %v", t.ID, err))
				continue
			}
		}
		if err := s.repo.HardDelete(ctx, t.ID); err != nil {
			continue
		}
		s.cache.InvalidateSlug(ctx, t.Product, t.Slug)
		s.emit("tenant.abandoned", map[string]any{
			"tenant_id": t.ID,
			"slug":      t.Slug,
		})
		n++
	}
	return n, nil
}

// abandonBlocked checks whether a pending tenant has active billing entitlements.
func (s *TenantsService) abandonBlocked(ctx context.Context, id uuid.UUID) (bool, string) {
	if s.billing == nil {
		return false, ""
	}
	state, err := s.billing.SubscriptionState(ctx, id)
	switch {
	case errors.Is(err, ErrNoSubscription):
		return false, ""
	case err != nil:
		return true, fmt.Sprintf("billing could not be asked for its subscription state (%v); a tenant is never deleted on an unanswered question", err)
	case state == "awaiting_registration":
		return false, ""
	default:
		return true, fmt.Sprintf("billing holds its subscription as %q, which is a live tenant", state)
	}
}

// SweepExploringPending activates tenants with confirmed exploring status.
func (s *TenantsService) SweepExploringPending(ctx context.Context) (int, error) {
	ctx = reqid.With(ctx, "explore-"+uuid.NewString()[:8])

	if s.billing == nil {
		return 0, nil
	}
	rows, err := s.repo.ListPending(ctx, pendingSweepLimit)
	if err != nil {
		return 0, err
	}
	n := 0
	for i := range rows {
		t := rows[i]
		state, err := s.billing.SubscriptionState(ctx, t.ID)
		if err != nil {
			if !errors.Is(err, ErrNoSubscription) {
				logger.LogWarn("", "sweep-exploring", fmt.Sprintf("tenant=%s: %v", t.ID, err))
			}
			continue
		}
		if state != "exploring" {
			continue
		}
		if _, activated, err := s.ActivateExploring(ctx, t.ID); err != nil {
			logger.LogWarn("", "sweep-exploring", fmt.Sprintf("tenant=%s activation failed, retrying next tick: %v", t.ID, err))
		} else if activated {
			n++
		}
	}
	return n, nil
}

func (s *TenantsService) emit(subject string, payload map[string]any) {
	if s.publish == nil {
		return
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_ = s.publish(subject, b)
}

func defaultStr(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

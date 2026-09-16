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
	// ErrOwnerRequired refuses a HEALTH tenant that names no owner.
	// See RequireOwnerForProduct for why it is not enforced inside Create.
	ErrOwnerRequired = errors.New("the health product requires an owner on every tenant, and owner_user_id was not given")
)

// RequireOwnerForProduct refuses a health tenant created with no owner.
//
// WHY it exists: `tenant.activated` is the only thing that makes a product
// provision a tenant, and dhs-setup refuses to create the owner's staff
// mirror without `owner_user_id` — it logs "activated with no owner_user_id;
// admin mirror NOT created" and carries on. So an ownerless health tenant is
// a clinic that exists, looks provisioned, and that nobody can sign into.
//
// WHY it is NOT inside Create: PublicSignup calls Create BEFORE it registers
// the owner in defolt-identity — it runs identity and billing concurrently and
// assigns OwnerUserID afterwards — so a guard inside Create would refuse every
// health signup the marketing form has ever made. The internal POST /tenants
// route is the one door whose caller already holds the identity id and has
// nowhere else to put it, so that is the door this guards.
//
// A non-health product is untouched: a `drs` tenant has always been creatable
// without an owner and this round does not tighten that.
func RequireOwnerForProduct(product string, owner *uuid.UUID) error {
	if NormalizeProduct(product) != "health" {
		return nil
	}
	if owner == nil || *owner == uuid.Nil {
		return ErrOwnerRequired
	}
	return nil
}

// slugRegex mirrors plan §4.9: 3-32 chars, starts with a letter, ends
// with alnum, dashes allowed internally.
var slugRegex = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}[a-z0-9]$`)

// pendingSweepLimit bounds one pass of the WP-B16 activation sweep. Each row
// costs one HTTP call to billing, and the sweep runs again in an hour, so a
// bounded pass that finishes beats an unbounded one that stalls the ticker.
const pendingSweepLimit = 500

type TenantsService struct {
	repo     *repository.Repo
	nc       *nats.Conn
	cache    *cache.Cache
	identity *IdentityClient
	billing  *BillingClient
	reserved map[string]struct{}
	// rootDomain is the DNS root customer-facing hosts hang off. Needed
	// here because this service is what asks billing for a checkout, and
	// therefore what decides where Selcom returns the payer.
	rootDomain string
}

func New(repo *repository.Repo, nc *nats.Conn, c *cache.Cache, identity *IdentityClient, billing *BillingClient, reserved []string, rootDomain string) *TenantsService {
	set := make(map[string]struct{}, len(reserved))
	for _, r := range reserved {
		set[strings.ToLower(strings.TrimSpace(r))] = struct{}{}
	}
	return &TenantsService{repo: repo, nc: nc, cache: c, identity: identity, billing: billing, reserved: set, rootDomain: rootDomain}
}

// CreateInput mirrors the POST /api/v1/tenants body. Product defaults
// to "drs" for now — Phase 3+ opens the door to other verticals.
type CreateInput struct {
	Slug         string
	Name         string
	ContactEmail string
	// Phone is REQUIRED, and that is the point of the whole row.
	//
	// §10.9 originally kept DEFOLT_CLIENT_PHONE as a permanent fallback
	// for tenants without one. The owner amended that on 2026-08-15: a
	// global constant standing in for a per-merchant field is exactly
	// what put "UAT Demo Client" on real merchants' Selcom records. The
	// globals are deleted, so a new tenant that lacks a phone would have
	// nothing behind it — which means the only safe place to insist is
	// here, at the point of creation.
	//
	// Refused input is a 400 with a readable reason (see RequirePhone),
	// never a silently empty column.
	Phone       string
	Currency    string
	Timezone    string
	CountryCode string
	Product     string
	Plan        string
	// The owner, as the caller has already registered them in
	// defolt-identity. Optional: the marketing signup form fills these in
	// after Create returns (it registers the owner concurrently with the
	// checkout call), and the internal route fills them in here because it
	// has nowhere else to. WP-B16.
	OwnerUserID *uuid.UUID
	OwnerEmail  string
	// The owner's legal name, in three parts. Optional at this layer
	// because Create also serves the admin path, where a tenant is
	// provisioned without a person attached; PublicSignup always supplies
	// them and refuses without a first and last name.
	//
	// They are set BEFORE Insert, and therefore before the tenant.created
	// emit below, which is the whole reason they are on CreateInput rather
	// than assigned by the caller afterwards. See the emit.
	OwnerFirstName  string
	OwnerMiddleName string
	OwnerLastName   string
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
	phone, err := RequirePhone(in.Phone)
	if err != nil {
		return nil, err
	}
	t := newTenantFromCreate(in, slug, phone)
	if err := s.repo.Insert(ctx, t); err != nil {
		if errors.Is(err, repository.ErrSlugTaken) {
			return nil, ErrSlugTaken
		}
		return nil, err
	}
	// tenant.created carries the NAMES, and it did not until now.
	//
	// WP-B10's addendum asserts that "the owner's three names have been on
	// that event since defolt-tenants-service:51". That is true of
	// tenant.activated, below, and it was never true of this one. This
	// event carried five fields, none of which is a name: an id, a slug, an
	// email, a product and a state.
	//
	// The cost of that was paid by a customer. defolt-billing consumes this
	// event to open the registration invoice and to send the welcome email,
	// had no name to put in it, and so passed the literal string "Your
	// workspace" instead. The owner registered Rani Dental Clinic and was
	// welcomed as "Your". A consumer cannot use a fact it was never sent,
	// so the defect is as much here as it is in the template that printed
	// it.
	//
	// `name` is the ORGANISATION's name and the owner_* fields are the
	// PERSON's. Keeping them distinct on the wire is the same lesson
	// tenant.activated learned the hard way, when dhs-setup fell back to
	// the tenant's own name and made the facility admin of "Rani Dental
	// Clinic" a staff member called after the clinic.
	//
	// Every field is additive, so an existing consumer that decodes the old
	// five is unaffected.
	s.emit("tenant.created", tenantCreatedPayload(t))
	return t, nil
}

// newTenantFromCreate is the ONE place a CreateInput becomes a tenant row.
//
// Split out of Create so the mapping can be asserted without a database, which
// is the whole lesson of the defect it closes: `owner_user_id` was on the
// request struct and on the model, and the assignment between them was simply
// missing. A struct literal inside a method that needs Postgres to run is a
// mapping no test ever reads, and a missed field in one is invisible — the
// tenant is created, activated, and its product logs "admin mirror NOT
// created" while every status code on the way says 201.
//
// slug and phone are passed in already normalised: Create validates them and
// they are the two fields whose accepted form differs from what was sent.
func newTenantFromCreate(in CreateInput, slug, phone string) *model.Tenant {
	return &model.Tenant{
		Slug:         slug,
		Name:         strings.TrimSpace(in.Name),
		ContactEmail: strings.TrimSpace(in.ContactEmail),
		Phone:        phone,
		Currency:     defaultStr(in.Currency, "TZS"),
		Timezone:     defaultStr(in.Timezone, "Africa/Dar_es_Salaam"),
		CountryCode:  defaultStr(in.CountryCode, "TZ"),
		Product:      defaultStr(in.Product, "drs"),
		Plan:         defaultStr(in.Plan, "standard"),
		Status:       model.StatusPendingPayment,

		OwnerUserID:     in.OwnerUserID,
		OwnerEmail:      strings.TrimSpace(in.OwnerEmail),
		OwnerFirstName:  strings.TrimSpace(in.OwnerFirstName),
		OwnerMiddleName: strings.TrimSpace(in.OwnerMiddleName),
		OwnerLastName:   strings.TrimSpace(in.OwnerLastName),
	}
}

// tenantCreatedPayload is the wire shape of tenant.created, split out from
// the emit so it can be asserted in a test. A payload built inline inside a
// method that needs a live NATS connection is a payload nothing checks, and
// the whole reason this event is being changed is that nobody noticed for
// months that it carried no name.
func tenantCreatedPayload(t *model.Tenant) map[string]any {
	return map[string]any{
		"tenant_id":   t.ID,
		"slug":        t.Slug,
		"name":        t.Name,
		"owner_email": t.ContactEmail,
		"product":     t.Product,
		"state":       string(t.Status),

		"owner_first_name":  t.OwnerFirstName,
		"owner_middle_name": t.OwnerMiddleName,
		"owner_last_name":   t.OwnerLastName,
	}
}

// ResolveBySlug is the hot-path lookup called by Traefik forward auth
// on every request. Tries the shared Redis cache first (30s TTL, plan
// §5.3/§5.13); on miss reads Postgres and repopulates the cache.
// Returns the slim projection — the only shape the hot path needs.
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

// Get by UUID.
func (s *TenantsService) Get(ctx context.Context, id uuid.UUID) (*model.Tenant, error) {
	return s.repo.FindByID(ctx, id)
}

// UpdateInput carries the PATCH /api/v1/tenants/:id body. Nil fields
// stay untouched.
type UpdateInput struct {
	Name         *string
	ContactEmail *string
	Phone        *string
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
	// A PATCH that omits phone leaves it alone; a PATCH that sends one
	// must send a usable one. Deliberately not RequirePhone: clearing a
	// phone by sending "" is refused here rather than accepted, because
	// an existing tenant silently losing its number would put the
	// checkout back where §10.9 started — but a caller who simply is not
	// editing the phone sends nil and is unaffected.
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
	if err := s.repo.Save(ctx, t); err != nil {
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

// Restore flips a suspended/archived tenant back to `active`.
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
	if t.ActivatedAt == nil {
		t.ActivatedAt = &now
	}
	if err := s.repo.Save(ctx, t); err != nil {
		return nil, err
	}
	s.cache.InvalidateSlug(ctx, t.Product, t.Slug)
	// Carry the owner identity so a DRS consumer can provision the owner's
	// user_mirror on activation and let them log in. Signup persists
	// OwnerUserID/OwnerEmail on the tenant; without them here nothing
	// downstream could tie the activated tenant to its Store Admin — the
	// onboarding "sealed room" this closes.
	s.emit("tenant.activated", activatedPayload(ctx, t))
	return t, nil
}

// activatedPayload is the wire shape of tenant.activated, split out of the
// two places that used to build it inline. They had DRIFTED: the payment
// path carried the trial window and the billing-sync path did not, and WP-B16
// adds a third publisher (the exploring activation), which is two chances too
// many to emit a subtly different event for the same fact.
//
// Every consumer of this event — dhs-setup and drs-setup — provisions the
// product's first-run state from it, and both refuse to create the owner's
// staff mirror without `owner_user_id` and address it by `owner_email`. So
// the two fields that decide whether an owner can log in at all are built
// here, once, with the fallback that matters:
//
// `owner_email` falls back to the tenant's contact email. OwnerEmail is set
// by the public signup form and by nothing else — a tenant created through
// the internal POST /tenants route (which is how support, the DHS onboarding
// tooling and every proof script create one) has only ContactEmail, so the
// event went out with an empty owner email and dhs-setup wrote a staff row
// with an empty address that no login can ever match.
func activatedPayload(ctx context.Context, t *model.Tenant) map[string]any {
	ownerEmail := strings.TrimSpace(t.OwnerEmail)
	if ownerEmail == "" {
		ownerEmail = strings.TrimSpace(t.ContactEmail)
	}
	p := map[string]any{
		"tenant_id":     t.ID,
		"slug":          t.Slug,
		"name":          t.Name,
		"owner_user_id": t.OwnerUserID,
		"owner_email":   ownerEmail,
		// The PERSON's name, so the consuming product can create its first staff
		// record under it. `name` above is the FACILITY's name; dhs-setup used to
		// fall back to that and made the facility admin a staff member called
		// after the clinic. PHI-free: a tenant owner is a business contact.
		"owner_first_name":  t.OwnerFirstName,
		"owner_middle_name": t.OwnerMiddleName,
		"owner_last_name":   t.OwnerLastName,
		// Which product must provision this tenant. Additive; both consumers
		// are bound to their own subject today and ignore it.
		"product": t.Product,
		// The request id that caused this activation, lifted off the inbound
		// HTTP context. NOT generated here and NEVER defaulted: a request id
		// is born at the caller and rides every HTTP call, NATS message and
		// goroutine in the fleet, and a fabricated one is worse than an empty
		// one because it looks like a trace and joins nothing.
		//
		// dhs-setup's consumer calls defolt-identity to resolve the owner, and
		// identity 400s DL_REQUEST_ID_REQUIRED on any request without an
		// X-Request-ID. Measured on the first production boot of dhs-setup
		// v1.0.0: request_id was "" on every tenant, every identity lookup was
		// refused, and the consumer fell back to the event's own name parts —
		// a warning, a successful-looking provision, and a wrong name.
		"request_id": reqid.From(ctx),
	}
	if t.ActivatedAt != nil {
		p["activated_at"] = *t.ActivatedAt
	}
	// Present only when there IS a trial. An exploring tenant has no trial
	// window — WP-B15 starts that clock at first real use — and sending a
	// zero time would read as one that had already expired.
	if t.TrialStartsAt != nil {
		p["trial_starts_at"] = *t.TrialStartsAt
	}
	if t.TrialEndsAt != nil {
		p["trial_ends_at"] = *t.TrialEndsAt
	}
	return p
}

// ActivateExploring is WP-B16's entry point: a tenant whose billing
// subscription is `exploring` is a LIVE tenant and must exist inside its
// product.
//
// WP-B15 stopped raising the registration fee at signup, which was the
// right call and had a consequence nobody traced: payment was the ONLY
// thing that ever moved a tenant out of `pending_payment`, and
// `tenant.activated` is the ONLY thing that makes dhs-setup or drs-setup
// provision one. So from WP-B15 onward every self-signed-up facility was
// created and then never came to exist in the product it had signed up
// for — no facility row, no staff row, and therefore no login, because
// login resolves memberships from staff rows. The owner met this himself
// on 2026-09-13 with `defolt-labs`.
//
// Idempotent by the same structural guard ActivateAfterRegistration uses:
// only a `pending_payment` tenant is moved, so a redelivered push, the
// hourly sweep and a hand-run sweep all settle to one activation and one
// event. That matters because the event's consumers are the thing that
// creates a facility, and a second facility for one tenant is not a
// duplicate row the database would refuse — it is a second clinic.
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
	// No trial window is set here, deliberately. An exploring tenant owes
	// nothing and is not on a clock; WP-B15's first-use signal is what
	// raises the fee and starts the trial, and writing a trial here would
	// hand the merchant a second, earlier deadline that nothing enforces.
	if err := s.repo.Save(ctx, t); err != nil {
		return nil, false, err
	}
	s.cache.InvalidateSlug(ctx, t.Product, t.Slug)
	s.emit("tenant.activated", activatedPayload(ctx, t))
	logger.LogInfo("activate-exploring", fmt.Sprintf("tenant=%s slug=%s product=%s is exploring and is now active; tenant.activated published", t.ID, t.Slug, t.Product))
	return t, true, nil
}

// TenantStatusForSubscriptionState maps billing's lifecycle enum onto this
// service's SPA-gate status. Pure, and split out of SyncSubscriptionState so
// the mapping can be asserted without a database: the whole of WP-B16 turns
// on one row of it, and the defect it fixes was invisible precisely because
// nothing ever read the table out loud.
//
// The two enums stay deliberately unreconciled elsewhere (see ResolveHost).
// This is the ONE place either is translated into the other.
func TenantStatusForSubscriptionState(subState string) model.TenantStatus {
	switch subState {
	case "awaiting_registration":
		return model.StatusPendingPayment
	case "exploring":
		// WP-B16. `exploring` is WP-B15's entry state: no invoice, no due
		// date, full access. It is therefore an ACTIVE tenant, and it has to
		// be, because the caller emits tenant.activated on the
		// pending_payment → active edge and that event is the only thing
		// that makes dhs-setup or drs-setup provision the tenant at all.
		//
		// It fell through to `default` before this line, which maps an
		// unknown billing state to `suspended`. That default is the right
		// instinct for a state this service has genuinely never heard of,
		// and it was exactly wrong for this one: billing's own seed would
		// have suspended every new tenant in the fleet the moment it pushed
		// the state it had just written.
		return model.StatusActive
	case "trial", "active":
		return model.StatusActive
	case "grace":
		return model.StatusGrace
	case "suspended", "cancelled":
		return model.StatusSuspended
	default:
		// Unknown states from billing should safely lock the gate.
		return model.StatusSuspended
	}
}

// SyncSubscriptionState is called by defolt-billing-service whenever a
// subscription changes state. Maps billing's lifecycle enum onto the
// tenant's SPA gate status so the edge always reflects the true billing state.
func (s *TenantsService) SyncSubscriptionState(ctx context.Context, id uuid.UUID, subState string) (*model.Tenant, error) {
	t, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	newStatus := TenantStatusForSubscriptionState(subState)
	oldStatus := t.Status
	if oldStatus == newStatus {
		return t, nil // no change
	}
	t.Status = newStatus
	if newStatus == model.StatusActive && t.ActivatedAt == nil {
		now := time.Now()
		t.ActivatedAt = &now
	}
	if err := s.repo.Save(ctx, t); err != nil {
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
	// Return the payer to the product they are buying.
	//
	// This argument was the empty string. billing passes redirect_url
	// through to Selcom unchanged (defolt-billing-service/service/
	// billing_service.go:464), and Selcom given no return URL leaves the
	// payer sitting on its own confirmation page. So an owner who resumed
	// an abandoned registration paid the 2,000 TZS and was never brought
	// back to the product at all.
	//
	// The signup FORM has always sent one, which is why this stayed
	// invisible: only the resume path was broken, and the resume path is
	// where somebody lands precisely because the first attempt failed.
	returnURL := ProductReturnURL(t.Product, t.Slug, s.rootDomain)
	res, err := s.billing.CreateCheckout(ctx, t.ID, email, returnURL)
	if err != nil {
		logger.LogWarn("", "resend-payment-link", fmt.Sprintf("tenant=%s: %v", t.ID, err))
		return nil, ErrBillingDown
	}
	return res, nil
}

// PendingCheckoutByEmail resumes payment for the pending_payment tenant
// this email owns, if any. Backs drs-setup-service's sign-in path: a
// returning owner who hasn't paid yet has no UserMirror row (only
// tenant.activated creates one) and would otherwise always hit the
// generic "no store account" message. This lets sign-in hand back a
// fresh payment link directly instead of sending them through the
// signup form again.
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
		// WP-B16: never reap a tenant that is merely EXPLORING.
		//
		// This sweeper hard-deletes the tenant row AND the owner's identity
		// account. It was safe while `pending_payment` meant "started a
		// registration payment and walked away", because 24 hours of silence
		// after a checkout really is an abandoned signup. WP-B15 changed what
		// the state means without changing this: nobody pays at signup any
		// more, so from that release every live, happily-exploring facility
		// became eligible for deletion on its second day.
		//
		// The activation sweep below moves an exploring tenant to `active`
		// within the hour, so in practice these two never race. "In practice"
		// is not a guarantee — the activation sweep depends on billing being
		// reachable, and so a billing outage would have turned into deleted
		// customers. Ask billing here too, and treat every answer that is not
		// a definite "no subscription" as a reason to leave the row alone.
		if keep, why := s.abandonBlocked(ctx, t.ID); keep {
			logger.LogInfo("sweep-abandoned", fmt.Sprintf("tenant=%s left alone: %s", t.ID, why))
			continue
		}
		// Identity user first, tenant row second. The tenant row is the
		// ONLY record of which identity user belongs to this signup, so
		// dropping it before the owner is deleted strands that account
		// permanently — and with it the email address, which then blocks
		// the person from ever signing up again. Failing here leaves the
		// whole tenant for the next tick to retry; the delete is
		// idempotent, so retrying is free.
		if t.OwnerUserID != nil && s.identity != nil {
			// One person can hold several tenants: a first store plus a
			// second signup, or two attempts at the same one. The owner
			// belongs to the PERSON, not to this row, so an abandoned
			// signup is not evidence that the account is unwanted. Deleting
			// it anyway destroys the login for every other tenant they own,
			// silently — the stores keep serving, the person simply can
			// never sign in again, and identity scrubs the password hash on
			// the way out so there is nothing left to restore them with.
			// That is not hypothetical: it happened on UAT on 2026-08-01.
			others, err := s.repo.CountOtherTenantsOwnedBy(ctx, *t.OwnerUserID, t.ID)
			if err != nil {
				logger.LogWarn("", "sweep-abandoned", fmt.Sprintf("tenant=%s owner check failed, retrying next tick: %v", t.ID, err))
				continue
			}
			if others > 0 {
				// Drop the abandoned tenant, keep the person. Any sibling
				// that is itself abandoned is swept on a later tick, by
				// which point this row no longer counts against it.
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

// abandonBlocked answers whether the abandonment sweeper must leave this
// tenant alone, and why. The bias is deliberate and one-directional: a
// deletion is irreversible and takes the owner's identity account with it,
// so anything other than a definite "billing has no subscription for this
// tenant" blocks it.
//
// `awaiting_registration` is the one live state that does NOT block: it is
// the state of a product that still charges at signup, which is the case
// this sweeper was written for and the only one it is still right about.
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

// SweepExploringPending activates every tenant that is still
// `pending_payment` while billing holds its subscription as `exploring`.
//
// This is the self-healing half of WP-B16 and the reason the round needs no
// data migration: the tenants stranded by WP-B15 are found and provisioned by
// the deploy itself, one minute after boot, and any later push that is lost
// is picked up within the hour.
//
// Returns how many tenants it actually activated — activations, not rows
// examined — so a log line that says "1" means one facility came into
// existence and a caller can assert on it.
func (s *TenantsService) SweepExploringPending(ctx context.Context) (int, error) {
	// Mints its own trace id for the same reason SweepAbandoned does: a
	// ticker is the origin of its own trace, and the internal calls this
	// makes are refused without one.
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

package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// TenantStatus tracks the lifecycle of a tenant. Traefik forward auth
// treats each state distinctly (see MULTI_TENANT_PLAN.md §5.3):
//
//   - pending_payment: registration invoice unpaid; block SPA except
//     billing endpoints.
//   - active: normal operation.
//   - suspended: manually or auto-suspended; block writes except
//     billing (self-reactivate).
//   - archived: terminal; block everything.
type TenantStatus string

const (
	StatusPendingPayment TenantStatus = "pending_payment"
	StatusActive         TenantStatus = "active"
	StatusGrace          TenantStatus = "grace"
	StatusSuspended      TenantStatus = "suspended"
	StatusArchived       TenantStatus = "archived"
)

// Tenant is the fleet-shared tenant registry row. One row per
// merchant. Slug is the routing key baked into the subdomain
// `<slug>.drs.defoltlabs.com`; it is enforced 3-32 chars, lowercase
// alnum + hyphens at the API layer.
//
// Plan (Phase 4 dropped `region`): everything runs in a single k3s
// cluster, so no per-region column.
type Tenant struct {
	ID           uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	Slug         string    `gorm:"size:32;uniqueIndex;not null" json:"slug"`
	Name         string    `gorm:"size:120;not null" json:"name"`
	ContactEmail string    `gorm:"size:180;not null" json:"contact_email"`
	Currency     string    `gorm:"size:8;default:'TZS'" json:"currency"`
	Timezone     string    `gorm:"size:64;default:'Africa/Dar_es_Salaam'" json:"timezone"`
	CountryCode  string    `gorm:"size:2;default:'TZ'" json:"country_code"`
	// Product identifies which vertical the tenant belongs to (drs,
	// nyaraka, vinono). Multi-product signup is a Phase 3+ decision
	// (§4.3) — for Phase 1 a tenant is single-product.
	Product string `gorm:"size:32;default:'drs';index" json:"product"`
	// Plan is the single flat plan slug per §8.2 (`standard`). Kept
	// as a string column so a future tiered offering can extend it
	// without a migration.
	Plan   string       `gorm:"size:32;default:'standard'" json:"plan"`
	Status TenantStatus `gorm:"size:32;default:'pending_payment';index" json:"status"`

	// OwnerUserID / OwnerEmail identify the initial Store Admin created
	// in defolt-identity during public signup (§5.11). OwnerUserID is
	// nullable: rows created before this column existed, and signups
	// where the identity call failed, leave it nil. The reissue-OTP
	// endpoint backfills it lazily via identity's by-email lookup.
	OwnerUserID *uuid.UUID `gorm:"type:uuid" json:"owner_user_id,omitempty"`
	OwnerEmail  string     `gorm:"size:180" json:"owner_email,omitempty"`

	// TrialStartsAt / TrialEndsAt bracket the 7-day free window that
	// unlocks on registration-payment confirmation. Filled in by the
	// billing consumer's `tenant.activated` handler.
	TrialStartsAt *time.Time `json:"trial_starts_at,omitempty"`
	TrialEndsAt   *time.Time `json:"trial_ends_at,omitempty"`

	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"deleted_at,omitempty"`
}

// TenantSlim is the public projection of a tenant: exactly the fields
// the anonymous by-slug lookup and Traefik forward auth need, nothing
// else (no contact email, no timestamps). This is also the shape
// cached in Redis under `tenant:slug:<slug>`.
type TenantSlim struct {
	ID      uuid.UUID    `json:"id"`
	Slug    string       `json:"slug"`
	Status  TenantStatus `json:"status"`
	Product string       `json:"product"`
}

func (t *Tenant) Slim() TenantSlim {
	return TenantSlim{ID: t.ID, Slug: t.Slug, Status: t.Status, Product: t.Product}
}

func (t *Tenant) BeforeCreate(_ *gorm.DB) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	return nil
}

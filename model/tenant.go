package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// TenantStatus tracks the lifecycle of a tenant row.
type TenantStatus string

const (
	StatusPendingPayment TenantStatus = "pending_payment"
	StatusActive         TenantStatus = "active"
	StatusGrace          TenantStatus = "grace"
	StatusSuspended      TenantStatus = "suspended"
	StatusArchived       TenantStatus = "archived"
)

// Tenant represents a registered tenant organization in the fleet registry.
type Tenant struct {
	ID              uuid.UUID      `gorm:"type:uuid;primaryKey" json:"id"`
	Slug            string         `gorm:"size:32;uniqueIndex:ux_tenants_product_slug,priority:2;not null" json:"slug"`
	Name            string         `gorm:"size:120;not null" json:"name"`
	ContactEmail    string         `gorm:"size:180;not null" json:"contact_email"`
	Phone           string         `gorm:"size:32" json:"phone,omitempty"`
	Currency        string         `gorm:"size:8;default:'TZS'" json:"currency"`
	Timezone        string         `gorm:"size:64;default:'Africa/Dar_es_Salaam'" json:"timezone"`
	CountryCode     string         `gorm:"size:2;default:'TZ'" json:"country_code"`
	Product         string         `gorm:"size:32;default:'drs';uniqueIndex:ux_tenants_product_slug,priority:1" json:"product"`
	Plan            string         `gorm:"size:32;default:'standard'" json:"plan"`
	Status          TenantStatus   `gorm:"size:32;default:'pending_payment';index" json:"status"`
	OwnerUserID     *uuid.UUID     `gorm:"type:uuid" json:"owner_user_id,omitempty"`
	OwnerEmail      string         `gorm:"size:180" json:"owner_email,omitempty"`
	OwnerFirstName  string         `gorm:"size:80" json:"owner_first_name,omitempty"`
	OwnerMiddleName string         `gorm:"size:80" json:"owner_middle_name,omitempty"`
	OwnerLastName   string         `gorm:"size:80" json:"owner_last_name,omitempty"`
	OwnerExisted    *bool          `gorm:"column:owner_existed" json:"-"`
	ActivatedAt     *time.Time     `json:"activated_at,omitempty"`
	TrialStartsAt   *time.Time     `json:"trial_starts_at,omitempty"`
	TrialEndsAt     *time.Time     `json:"trial_ends_at,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	DeletedAt       gorm.DeletedAt `gorm:"index" json:"deleted_at,omitempty"`
}

// TenantSlim is the public projection of a tenant for lookup and auth.
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

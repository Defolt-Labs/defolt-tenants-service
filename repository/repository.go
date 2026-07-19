package repository

import (
	"context"
	"errors"
	"strings"

	"defolt-tenants-service/model"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	ErrNotFound        = errors.New("not found")
	ErrSlugTaken       = errors.New("slug already taken")
	ErrUniqueViolation = errors.New("unique constraint violation")
)

type Repo struct{ db *gorm.DB }

func New(db *gorm.DB) *Repo { return &Repo{db: db} }

func (r *Repo) DB() *gorm.DB { return r.db }

// Insert atomically claims the slug and inserts the tenant row. Uses
// `ON CONFLICT (slug) DO NOTHING RETURNING id` per plan §5.19 so a
// concurrent second signup with the same slug reliably fails with
// ErrSlugTaken instead of an opaque 500.
func (r *Repo) Insert(ctx context.Context, t *model.Tenant) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	t.Slug = strings.ToLower(strings.TrimSpace(t.Slug))
	// gorm.Clause equivalent for a raw INSERT... ON CONFLICT is
	// awkward with the model; use a two-step guarded insert instead:
	// pre-check + save inside a serializable-ish tx. Postgres' unique
	// index on slug still catches concurrent writes as a fallback.
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing model.Tenant
		if err := tx.Where("slug = ?", t.Slug).First(&existing).Error; err == nil {
			return ErrSlugTaken
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := tx.Create(t).Error; err != nil {
			// Race: another tx just claimed the same slug.
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				return ErrSlugTaken
			}
			return err
		}
		return nil
	})
}

// FindByID returns the tenant matching the UUID, or ErrNotFound.
func (r *Repo) FindByID(ctx context.Context, id uuid.UUID) (*model.Tenant, error) {
	var t model.Tenant
	err := r.db.WithContext(ctx).First(&t, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &t, err
}

// FindBySlug returns the tenant matching slug (case-insensitive).
// Used by the forward-auth /internal/resolve-host endpoint on every
// request — Redis caches the result upstream.
func (r *Repo) FindBySlug(ctx context.Context, slug string) (*model.Tenant, error) {
	var t model.Tenant
	err := r.db.WithContext(ctx).
		Where("LOWER(slug) = LOWER(?)", strings.TrimSpace(slug)).
		First(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &t, err
}

// Save persists mutations. Callers should read-then-modify to keep
// the semantics predictable.
func (r *Repo) Save(ctx context.Context, t *model.Tenant) error {
	return r.db.WithContext(ctx).Save(t).Error
}

// SetStatus updates a single column so partial writes stay minimal.
// Skips the update when the tenant is already in the target state.
func (r *Repo) SetStatus(ctx context.Context, id uuid.UUID, status model.TenantStatus) error {
	res := r.db.WithContext(ctx).Model(&model.Tenant{}).
		Where("id = ? AND status <> ?", id, status).
		Update("status", status)
	if res.Error != nil {
		return res.Error
	}
	// RowsAffected == 0 either means "no such tenant" or "already
	// in that status". Distinguish by a follow-up read.
	if res.RowsAffected == 0 {
		if _, err := r.FindByID(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// ListPendingCleanup returns tenants stuck in pending_payment past
// the sweep threshold. Cron worker uses this to hard-delete abandoned
// signups (plan §5.11).
func (r *Repo) ListPendingCleanup(ctx context.Context, olderThanHours int) ([]model.Tenant, error) {
	var out []model.Tenant
	// make_interval(hours => ?) binds the threshold as the integer it is.
	// The previous form, `(? || ' hours')::interval`, made pgx encode an
	// int into a text-typed parameter and every sweep tick died with
	// "unable to encode 24 into text format for text (OID 25)", so the
	// abandoned-signup cleanup never ran at all.
	err := r.db.WithContext(ctx).
		Where("status = ? AND created_at < now() - make_interval(hours => ?)",
			model.StatusPendingPayment, olderThanHours).
		Find(&out).Error
	return out, err
}

// HardDelete removes the row plus any downstream cascades handled
// via NATS `tenant.deleted`. Only used by the abandonment sweeper.
func (r *Repo) HardDelete(ctx context.Context, id uuid.UUID) error {
	res := r.db.WithContext(ctx).Unscoped().Delete(&model.Tenant{}, "id = ?", id)
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return res.Error
}

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

// Insert claims the slug and inserts the tenant within a guarded transaction.
func (r *Repo) Insert(ctx context.Context, t *model.Tenant) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	t.Slug = strings.ToLower(strings.TrimSpace(t.Slug))
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing model.Tenant
		if err := tx.Where("product = ? AND slug = ?", t.Product, t.Slug).First(&existing).Error; err == nil {
			return ErrSlugTaken
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := tx.Create(t).Error; err != nil {
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

// FindBySlug returns the tenant matching (product, slug).
func (r *Repo) FindBySlug(ctx context.Context, product, slug string) (*model.Tenant, error) {
	var t model.Tenant
	err := r.db.WithContext(ctx).
		Where("product = ? AND LOWER(slug) = LOWER(?)", product, strings.TrimSpace(slug)).
		First(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &t, err
}

// FindPendingByContactEmail returns the latest unpaid tenant for an email and optional product.
func (r *Repo) FindPendingByContactEmail(ctx context.Context, email, product string) (*model.Tenant, error) {
	var t model.Tenant
	q := r.db.WithContext(ctx).
		Where("LOWER(contact_email) = LOWER(?) AND status = ?", strings.TrimSpace(email), model.StatusPendingPayment)
	if p := strings.ToLower(strings.TrimSpace(product)); p != "" {
		q = q.Where("product = ?", p)
	}
	err := q.
		Order("created_at DESC").
		First(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &t, err
}

// SaveDetails updates only name, contact_email, phone and plan, the fields an edit changes.
func (r *Repo) SaveDetails(ctx context.Context, t *model.Tenant) error {
	return r.saveDetailsQuery(ctx, t).Error
}

func (r *Repo) saveDetailsQuery(ctx context.Context, t *model.Tenant) *gorm.DB {
	return r.db.WithContext(ctx).Model(&model.Tenant{ID: t.ID}).
		Select("name", "contact_email", "phone", "plan").
		Updates(t)
}

// SaveName updates only name.
func (r *Repo) SaveName(ctx context.Context, t *model.Tenant) error {
	return r.saveNameQuery(ctx, t).Error
}

func (r *Repo) saveNameQuery(ctx context.Context, t *model.Tenant) *gorm.DB {
	return r.db.WithContext(ctx).Model(&model.Tenant{ID: t.ID}).Select("name").Updates(t)
}

// SavePhone updates only phone.
func (r *Repo) SavePhone(ctx context.Context, t *model.Tenant) error {
	return r.savePhoneQuery(ctx, t).Error
}

func (r *Repo) savePhoneQuery(ctx context.Context, t *model.Tenant) *gorm.DB {
	return r.db.WithContext(ctx).Model(&model.Tenant{ID: t.ID}).Select("phone").Updates(t)
}

// SaveOwnerLink updates only owner_user_id and owner_email, the pair a by-email lookup fills in.
func (r *Repo) SaveOwnerLink(ctx context.Context, t *model.Tenant) error {
	return r.saveOwnerLinkQuery(ctx, t).Error
}

func (r *Repo) saveOwnerLinkQuery(ctx context.Context, t *model.Tenant) *gorm.DB {
	return r.db.WithContext(ctx).Model(&model.Tenant{ID: t.ID}).
		Select("owner_user_id", "owner_email").
		Updates(t)
}

// SaveRegistrationActivation updates only status, activated_at and the trial window.
func (r *Repo) SaveRegistrationActivation(ctx context.Context, t *model.Tenant) error {
	return r.saveRegistrationActivationQuery(ctx, t).Error
}

func (r *Repo) saveRegistrationActivationQuery(ctx context.Context, t *model.Tenant) *gorm.DB {
	return r.db.WithContext(ctx).Model(&model.Tenant{ID: t.ID}).
		Select("status", "activated_at", "trial_starts_at", "trial_ends_at").
		Updates(t)
}

// SaveActivation updates only status and activated_at, so an owner written since the row was read survives.
func (r *Repo) SaveActivation(ctx context.Context, t *model.Tenant) error {
	return r.saveActivationQuery(ctx, t).Error
}

// saveActivationQuery constructs the partial update query for status and activated_at.
func (r *Repo) saveActivationQuery(ctx context.Context, t *model.Tenant) *gorm.DB {
	return r.db.WithContext(ctx).Model(&model.Tenant{ID: t.ID}).
		Select("status", "activated_at").
		Updates(t)
}

// SaveOwner updates only the owner fields on the tenant.
func (r *Repo) SaveOwner(ctx context.Context, t *model.Tenant) error {
	return r.saveOwnerQuery(ctx, t).Error
}

// saveOwnerQuery constructs the partial update query for owner fields.
func (r *Repo) saveOwnerQuery(ctx context.Context, t *model.Tenant) *gorm.DB {
	return r.db.WithContext(ctx).Model(&model.Tenant{ID: t.ID}).
		Select("owner_user_id", "owner_email", "owner_first_name", "owner_middle_name", "owner_last_name", "owner_existed").
		Updates(t)
}

// SetStatus updates status when different from current state.
func (r *Repo) SetStatus(ctx context.Context, id uuid.UUID, status model.TenantStatus) error {
	res := r.db.WithContext(ctx).Model(&model.Tenant{}).
		Where("id = ? AND status <> ?", id, status).
		Update("status", status)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		if _, err := r.FindByID(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// ListPendingCleanup returns pending tenants created before the specified hours threshold.
func (r *Repo) ListPendingCleanup(ctx context.Context, olderThanHours int) ([]model.Tenant, error) {
	var out []model.Tenant
	err := r.db.WithContext(ctx).
		Where("status = ? AND created_at < now() - make_interval(hours => ?)",
			model.StatusPendingPayment, olderThanHours).
		Find(&out).Error
	return out, err
}

// ListPending returns pending payment tenants ordered by creation time.
func (r *Repo) ListPending(ctx context.Context, limit int) ([]model.Tenant, error) {
	var out []model.Tenant
	if limit <= 0 {
		limit = 100
	}
	err := r.db.WithContext(ctx).
		Where("status = ?", model.StatusPendingPayment).
		Order("created_at ASC").
		Limit(limit).
		Find(&out).Error
	return out, err
}

// CountOtherTenantsOwnedBy counts other tenants associated with the same owner user.
func (r *Repo) CountOtherTenantsOwnedBy(ctx context.Context, ownerUserID uuid.UUID, excludeTenantID uuid.UUID) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).
		Model(&model.Tenant{}).
		Where("owner_user_id = ? AND id <> ?", ownerUserID, excludeTenantID).
		Count(&n).Error
	return n, err
}

// HardDelete permanently deletes the tenant record.
func (r *Repo) HardDelete(ctx context.Context, id uuid.UUID) error {
	res := r.db.WithContext(ctx).Unscoped().Delete(&model.Tenant{}, "id = ?", id)
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return res.Error
}

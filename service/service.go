package service

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"defolt-tenants-service/model"
	"defolt-tenants-service/repository"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
)

var (
	ErrSlugInvalid  = errors.New("slug is invalid")
	ErrSlugReserved = errors.New("slug is reserved")
	ErrSlugTaken    = errors.New("slug is taken")
	ErrValidation   = errors.New("validation failed")
)

// slugRegex mirrors plan §4.9: 3-32 chars, starts with a letter, ends
// with alnum, dashes allowed internally.
var slugRegex = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}[a-z0-9]$`)

type TenantsService struct {
	repo     *repository.Repo
	nc       *nats.Conn
	reserved map[string]struct{}
}

func New(repo *repository.Repo, nc *nats.Conn, reserved []string) *TenantsService {
	set := make(map[string]struct{}, len(reserved))
	for _, r := range reserved {
		set[strings.ToLower(strings.TrimSpace(r))] = struct{}{}
	}
	return &TenantsService{repo: repo, nc: nc, reserved: set}
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
		"tenant_id":     t.ID,
		"slug":          t.Slug,
		"owner_email":   t.ContactEmail,
		"product":       t.Product,
		"state":         string(t.Status),
	})
	return t, nil
}

// ResolveBySlug is the hot-path lookup called by Traefik forward auth
// on every request. Returns the tenant or ErrNotFound. Redis caching
// wraps this at the handler layer.
func (s *TenantsService) ResolveBySlug(ctx context.Context, slug string) (*model.Tenant, error) {
	return s.repo.FindBySlug(ctx, slug)
}

// Get by UUID.
func (s *TenantsService) Get(ctx context.Context, id uuid.UUID) (*model.Tenant, error) {
	return s.repo.FindByID(ctx, id)
}

// Suspend moves a tenant into `suspended`. Emits `tenant.status_changed`
// so every cluster-local Redis cache invalidates within the same tick.
func (s *TenantsService) Suspend(ctx context.Context, id uuid.UUID) (*model.Tenant, error) {
	if err := s.repo.SetStatus(ctx, id, model.StatusSuspended); err != nil {
		return nil, err
	}
	t, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	s.emit("tenant.status_changed", map[string]any{
		"tenant_id": t.ID,
		"slug":      t.Slug,
		"status":    string(t.Status),
	})
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
	s.emit("tenant.status_changed", map[string]any{
		"tenant_id": t.ID,
		"slug":      t.Slug,
		"status":    string(t.Status),
	})
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
	s.emit("tenant.activated", map[string]any{
		"tenant_id":       t.ID,
		"slug":            t.Slug,
		"trial_starts_at": now,
		"trial_ends_at":   trialEnd,
	})
	return t, nil
}

// SweepAbandoned runs on a cron and hard-deletes tenants stuck in
// `pending_payment` past the 24-hour threshold. Publishes
// `tenant.abandoned` per §5.11 so downstream cleanups fire.
func (s *TenantsService) SweepAbandoned(ctx context.Context) (int, error) {
	rows, err := s.repo.ListPendingCleanup(ctx, 24)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, t := range rows {
		if err := s.repo.HardDelete(ctx, t.ID); err != nil {
			continue
		}
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

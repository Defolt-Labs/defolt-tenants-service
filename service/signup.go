package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"defolt-tenants-service/logger"
	"defolt-tenants-service/model"
	"defolt-tenants-service/reqid"

	"github.com/google/uuid"
)

// ErrTurnstile indicates verification failed on the provided token.
var ErrTurnstile = errors.New("turnstile verification failed")

// SignupInput holds fields submitted by public marketing signup callers.
type SignupInput struct {
	Slug         string
	Name         string
	ContactEmail string
	Phone        string
	FirstName    string
	MiddleName   string
	LastName     string
	CountryCode  string
	TurnstileTok string
	RedirectURL  string
	ClientIP     string
	Product      string
}

// SignupResult returns created tenant details and initial credentials.
type SignupResult struct {
	Tenant          *model.Tenant
	OneTimePassword string
	PaymentURL      string
	AmountTZS       int64
	OwnerExisting   bool
	OwnerEmail      string
}

// PublicSignup validates captcha, creates tenant, provisions owner, and requests checkout.
func (s *TenantsService) PublicSignup(ctx context.Context, in SignupInput, ts *Turnstile) (*SignupResult, error) {
	rid := reqid.From(ctx)
	if err := ts.Verify(ctx, in.TurnstileTok, in.ClientIP); err != nil {
		logger.LogWarn(rid, "signup-turnstile", err.Error())
		return nil, err
	}
	if strings.TrimSpace(in.FirstName) == "" || strings.TrimSpace(in.LastName) == "" {
		return nil, ErrValidation
	}

	product := defaultStr(in.Product, "drs")

	t, err := s.createRow(ctx, CreateInput{
		Slug:            in.Slug,
		Name:            in.Name,
		ContactEmail:    in.ContactEmail,
		Phone:           in.Phone,
		CountryCode:     in.CountryCode,
		Product:         product,
		Plan:            "standard",
		OwnerFirstName:  in.FirstName,
		OwnerMiddleName: in.MiddleName,
		OwnerLastName:   in.LastName,
	})
	if err != nil {
		if errors.Is(err, ErrSlugTaken) {
			if resumed, rerr := s.resumeStalledSignup(ctx, in); rerr == nil {
				t = resumed
			} else {
				return nil, err
			}
		} else {
			return nil, err
		}
	}

	out := &SignupResult{Tenant: t}
	t.OwnerEmail = strings.TrimSpace(in.ContactEmail)
	out.OwnerEmail = t.OwnerEmail
	t.OwnerFirstName = strings.TrimSpace(in.FirstName)
	t.OwnerMiddleName = strings.TrimSpace(in.MiddleName)
	t.OwnerLastName = strings.TrimSpace(in.LastName)

	var identityPassword string
	if s.identity != nil {
		identityPassword = generatePassword()
		identityUserID, identityExisted, identityErr := s.identity.CreateUser(ctx, RegisterInput{
			Email:      in.ContactEmail,
			FirstName:  in.FirstName,
			MiddleName: in.MiddleName,
			LastName:   in.LastName,
			Password:   identityPassword,
		})
		if identityErr != nil {
			logger.LogWarn(rid, "signup-identity", fmt.Sprintf("tenant=%s email=%s: %v", t.ID, in.ContactEmail, identityErr))
		} else {
			applyIdentityOwner(t, identityUserID, identityExisted)
			out.OwnerExisting = identityExisted
			if !identityExisted {
				out.OneTimePassword = identityPassword
			} else {
				logger.LogInfo("signup-identity", fmt.Sprintf("tenant=%s: owner already has a Defolt account, keeping their credential", t.ID))
			}
		}
		if err := s.rows.SaveOwner(ctx, t); err != nil {
			logger.LogWarn(rid, "signup-owner", fmt.Sprintf("tenant=%s: persisting owner fields failed: %v", t.ID, err))
		}
	}

	s.emit("tenant.created", tenantCreatedPayload(ctx, t))

	var checkout *CheckoutResult
	var billErr error
	if s.billing != nil {
		checkout, billErr = s.billing.CreateCheckout(ctx, t.ID, t.OwnerEmail, in.RedirectURL)
		switch {
		case billErr != nil:
			logger.LogWarn(rid, "signup-billing", fmt.Sprintf("tenant=%s: checkout unavailable: %v", t.ID, billErr))
		case checkout.NothingOwed():
			if checkout.State == "exploring" {
				if _, _, aerr := s.ActivateExploring(ctx, t.ID); aerr != nil {
					logger.LogWarn(rid, "signup-activate", fmt.Sprintf("tenant=%s: %v; the activation sweep will retry", t.ID, aerr))
				}
			}
			logger.LogInfo("signup-billing", fmt.Sprintf("tenant=%s: nothing owed (%s), no payment step", t.ID, checkout.State))
		default:
			out.PaymentURL = checkout.PaymentURL
			out.AmountTZS = checkout.AmountTZS
		}
	}
	if fresh, ferr := s.rows.FindByID(ctx, t.ID); ferr == nil {
		out.Tenant = fresh
	}
	return out, nil
}

// applyIdentityOwner records owner user ID and existence status on tenant model.
func applyIdentityOwner(t *model.Tenant, userID *uuid.UUID, existed bool) {
	if t.OwnerUserID == nil {
		t.OwnerExisted = &existed
	}
	t.OwnerUserID = userID
}

// resumeStalledSignup checks if a conflicting slug matches an unactivated retry by the same email.
func (s *TenantsService) resumeStalledSignup(ctx context.Context, in SignupInput) (*model.Tenant, error) {
	existing, err := s.rows.FindBySlug(ctx, defaultStr(in.Product, "drs"), in.Slug)
	if err != nil {
		return nil, err
	}
	if existing.Status != model.StatusPendingPayment {
		return nil, ErrSlugTaken
	}
	if !strings.EqualFold(strings.TrimSpace(existing.ContactEmail), strings.TrimSpace(in.ContactEmail)) {
		return nil, ErrSlugTaken
	}
	if strings.TrimSpace(in.Name) != "" && in.Name != existing.Name {
		existing.Name = in.Name
		if err := s.rows.Save(ctx, existing); err != nil {
			logger.LogWarn(reqid.From(ctx), "signup-resume-name", fmt.Sprintf("tenant=%s: %v", existing.ID, err))
		}
	}
	if strings.TrimSpace(in.Phone) != "" {
		if norm, perr := RequirePhone(in.Phone); perr == nil && norm != existing.Phone {
			existing.Phone = norm
			if err := s.rows.Save(ctx, existing); err != nil {
				logger.LogWarn(reqid.From(ctx), "signup-resume-phone", fmt.Sprintf("tenant=%s: %v", existing.ID, err))
			}
		}
	}
	return existing, nil
}

// generatePassword returns a random 16-character alphanumeric string.
func generatePassword() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "DefoltInitPass1!"
	}
	return base64.RawURLEncoding.EncodeToString(b) + "1aA"
}

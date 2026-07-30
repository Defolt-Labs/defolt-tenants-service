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
)

var ErrTurnstile = errors.New("turnstile verification failed")

// SignupInput is the public marketing signup body.
type SignupInput struct {
	Slug         string
	Name         string // workspace / merchant name
	ContactEmail string // becomes the initial Store Admin
	FirstName    string
	LastName     string
	CountryCode  string
	TurnstileTok string
	RedirectURL  string
	ClientIP     string
}

// SignupResult is what the marketing form gets back: the tenant plus
// the one-time password for the freshly created Store Admin and the
// Selcom checkout link covering the registration invoice (§5.11).
type SignupResult struct {
	Tenant          *model.Tenant
	OneTimePassword string
	PaymentURL      string
	AmountTZS       float64
	// OwnerExisting says the contact email already had a Defolt account,
	// so no one-time password was issued and the owner signs in with the
	// credential they already have. See the CreateUser call below.
	OwnerExisting bool
	OwnerEmail    string
}

// PublicSignup is the marketing form entrypoint. Verifies Turnstile,
// creates the tenant record, registers the Store Admin user in
// defolt-identity (persisting owner_user_id + owner_email), and asks
// billing for the Selcom registration checkout URL. Identity and
// billing failures are non-fatal: the tenant record stays as
// `pending_payment` and the sweep ticker cleans up abandoned rows.
func (s *TenantsService) PublicSignup(ctx context.Context, in SignupInput, ts *Turnstile) (*SignupResult, error) {
	if err := ts.Verify(ctx, in.TurnstileTok, in.ClientIP); err != nil {
		logger.LogWarn("", "signup-turnstile", err.Error())
		return nil, ErrTurnstile
	}
	if strings.TrimSpace(in.FirstName) == "" || strings.TrimSpace(in.LastName) == "" {
		return nil, ErrValidation
	}

	t, err := s.Create(ctx, CreateInput{
		Slug:         in.Slug,
		Name:         in.Name,
		ContactEmail: in.ContactEmail,
		CountryCode:  in.CountryCode,
		Product:      "drs",
		Plan:         "standard",
	})
	if err != nil {
		return nil, err
	}

	out := &SignupResult{Tenant: t}

	// Store Admin provisioning. The generated temp password IS the
	// one-time password the owner logs in with, so it goes back in the
	// signup response — which only means anything because CreateUser
	// inserts a real, immediately loggable-in users row.
	t.OwnerEmail = strings.TrimSpace(in.ContactEmail)
	out.OwnerEmail = t.OwnerEmail
	if s.identity != nil {
		password := generatePassword()
		userID, existed, regErr := s.identity.CreateUser(ctx, RegisterInput{
			Email:     in.ContactEmail,
			FirstName: in.FirstName,
			LastName:  in.LastName,
			Password:  password,
		})
		if regErr != nil {
			logger.LogWarn("", "signup-identity", fmt.Sprintf("tenant=%s email=%s: %v", t.ID, in.ContactEmail, regErr))
			// Tenant record stays for support to unblock manually or the
			// sweep ticker to reap.
		} else {
			t.OwnerUserID = userID
			// existed means identity kept the credential this person
			// already has and ignored the one generated above. Returning
			// that password would be actively harmful: it looks like the
			// way in, and it is not. Say so instead, and let the frontend
			// tell them their existing Defolt password (or Google) opens
			// the new store.
			out.OwnerExisting = existed
			if !existed {
				out.OneTimePassword = password
			} else {
				logger.LogInfo("signup-identity", fmt.Sprintf("tenant=%s: owner already has a Defolt account, keeping their credential", t.ID))
			}
		}
	}
	if err := s.repo.Save(ctx, t); err != nil {
		logger.LogWarn("", "signup-owner", fmt.Sprintf("tenant=%s: persisting owner fields failed: %v", t.ID, err))
	}

	// Registration checkout. Non-fatal: an empty payment_url tells the
	// frontend to surface the resend-payment-link path.
	if s.billing != nil {
		checkout, billErr := s.billing.CreateCheckout(ctx, t.ID, t.OwnerEmail, in.RedirectURL)
		if billErr != nil {
			logger.LogWarn("", "signup-billing", fmt.Sprintf("tenant=%s: checkout unavailable: %v", t.ID, billErr))
		} else {
			out.PaymentURL = checkout.PaymentURL
			out.AmountTZS = checkout.AmountTZS
		}
	}
	return out, nil
}

// generatePassword makes a one-time password: "Defolt-" prefix plus
// 11 URL-safe random chars plus a guaranteed trailing digit. That
// satisfies the defolt-identity complexity policy (min 8 chars, upper
// + lower case, digit) by construction. Shared by signup and
// reissue-OTP.
func generatePassword() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "Defolt-" + base64.URLEncoding.EncodeToString([]byte("fallback"))[:10] + "3"
	}
	digit := byte('0' + b[11]%10)
	return "Defolt-" + base64.URLEncoding.EncodeToString(b[:11])[:11] + string(digit)
}

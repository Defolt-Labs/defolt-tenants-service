package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"

	"defolt-tenants-service/logger"
	"defolt-tenants-service/model"

	"github.com/google/uuid"
)

var ErrTurnstile = errors.New("turnstile verification failed")

// SignupInput is the public marketing signup body.
type SignupInput struct {
	Slug         string
	Name         string // workspace / merchant name
	ContactEmail string // becomes the initial Store Admin
	// Phone is the merchant's own number. The signup form makes it
	// required (§10.9 item 2); the server normalises it and carries on if
	// it is absent, so an older client cannot be locked out of signup.
	Phone        string
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
		Phone:        in.Phone,
		CountryCode:  in.CountryCode,
		Product:      "drs",
		Plan:         "standard",
	})
	if err != nil {
		if errors.Is(err, ErrSlugTaken) {
			// The same person, cancelling out of payment and immediately
			// retrying with the same slug, hit a hard uniqueness conflict
			// here otherwise: this row's own pending_payment tenant blocked
			// its own owner from ever finishing signup, for up to 24h until
			// SweepAbandoned reaped it. Resume instead of rejecting when the
			// existing row is unmistakably the same abandoned attempt (same
			// slug, same email, still pending_payment) — everything below
			// this point (identity CreateUser, billing CreateCheckout) is
			// already idempotent for an existing tenant, so falling through
			// into the normal flow with the resumed row is enough; no
			// separate code path needed.
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

	// Store Admin provisioning (defolt-identity) and registration
	// checkout (defolt-billing, which itself reaches defolt-payment and
	// from there Selcom's real gateway) don't depend on each other's
	// result — billing only needs t.ID/t.OwnerEmail/in.RedirectURL,
	// all already set above, not anything CreateUser returns. Run them
	// concurrently rather than back to back: this was the single
	// biggest chunk of the signup form's end-to-end wait, all of it
	// spent blocked on one external call while a completely unrelated
	// one sat idle. Each goroutine writes only its own local result;
	// out/t are touched solely after both complete via wg.Wait(), so
	// there is nothing to guard with a mutex.
	var wg sync.WaitGroup
	var identityUserID *uuid.UUID
	var identityExisted bool
	var identityPassword string
	var identityErr error
	var checkout *CheckoutResult
	var billErr error

	if s.identity != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			identityPassword = generatePassword()
			identityUserID, identityExisted, identityErr = s.identity.CreateUser(ctx, RegisterInput{
				Email:     in.ContactEmail,
				FirstName: in.FirstName,
				LastName:  in.LastName,
				Password:  identityPassword,
			})
		}()
	}
	if s.billing != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			checkout, billErr = s.billing.CreateCheckout(ctx, t.ID, t.OwnerEmail, in.RedirectURL)
		}()
	}
	wg.Wait()

	// Store Admin provisioning. The generated temp password IS the
	// one-time password the owner logs in with, so it goes back in the
	// signup response — which only means anything because CreateUser
	// inserts a real, immediately loggable-in users row.
	if s.identity != nil {
		if identityErr != nil {
			logger.LogWarn("", "signup-identity", fmt.Sprintf("tenant=%s email=%s: %v", t.ID, in.ContactEmail, identityErr))
			// Tenant record stays for support to unblock manually or the
			// sweep ticker to reap.
		} else {
			t.OwnerUserID = identityUserID
			// existed means identity kept the credential this person
			// already has and ignored the one generated above. Returning
			// that password would be actively harmful: it looks like the
			// way in, and it is not. Say so instead, and let the frontend
			// tell them their existing Defolt password (or Google) opens
			// the new store.
			out.OwnerExisting = identityExisted
			if !identityExisted {
				out.OneTimePassword = identityPassword
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
		if billErr != nil {
			logger.LogWarn("", "signup-billing", fmt.Sprintf("tenant=%s: checkout unavailable: %v", t.ID, billErr))
		} else {
			out.PaymentURL = checkout.PaymentURL
			out.AmountTZS = checkout.AmountTZS
		}
	}
	return out, nil
}

// resumeStalledSignup finds the tenant already squatting on this slug
// and returns it only when the retry is unmistakably the same abandoned
// attempt: still pending_payment, and the same contact email as the new
// submission. Anything else (a different email, or a slug that belongs
// to an active/suspended/archived tenant) stays a genuine conflict —
// this must never let a stranger hijack someone else's registration by
// guessing their slug.
func (s *TenantsService) resumeStalledSignup(ctx context.Context, in SignupInput) (*model.Tenant, error) {
	slug := strings.ToLower(strings.TrimSpace(in.Slug))
	existing, err := s.repo.FindBySlug(ctx, slug)
	if err != nil {
		return nil, ErrSlugTaken
	}
	if existing.Status != model.StatusPendingPayment {
		return nil, ErrSlugTaken
	}
	if !strings.EqualFold(strings.TrimSpace(existing.ContactEmail), strings.TrimSpace(in.ContactEmail)) {
		return nil, ErrSlugTaken
	}
	// Carry the phone across a resume. The abandoned row may predate the
	// column, or the merchant may have corrected the number on the retry;
	// either way the value they just typed is the current one. Persist
	// failures are swallowed on purpose — a resumed signup must still
	// complete, and billing falls back if the phone is missing.
	if p := NormalizePhone(in.Phone); p != "" && p != existing.Phone {
		existing.Phone = p
		if err := s.repo.Save(ctx, existing); err != nil {
			logger.LogWarn("", "signup-resume-phone", fmt.Sprintf("tenant=%s: %v", existing.ID, err))
		}
	}
	return existing, nil
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

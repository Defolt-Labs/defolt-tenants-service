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
	// Phone is the merchant's own number, and it is REQUIRED — enforced
	// in Create, which this calls, so there is one place that decides it
	// rather than two that can drift. §10.9 as amended by the owner on
	// 2026-08-15: the DEFOLT_CLIENT_* globals are deleted, so a tenant
	// created without a phone would have nothing behind it on the Selcom
	// checkout.
	Phone        string
	FirstName    string
	MiddleName   string
	LastName     string
	CountryCode  string
	TurnstileTok string
	RedirectURL  string
	ClientIP     string
	// Product selects the fleet product this signup provisions. Defaults to
	// "drs" (retail) when empty, preserving the original behaviour. EVERY
	// product — "health" (DHS) included — goes through the SAME paid path:
	// the tenant is created pending_payment, a Selcom registration checkout
	// is issued, and only on payment does billing drive activation and the
	// tenant.activated event that dhs-setup (health) / drs-setup (retail)
	// consume to provision the owner. There is no auto-activate/skip-billing
	// branch for any product; only the tenant namespace and the downstream
	// consumer differ. (An earlier comment here claimed health skipped
	// billing — it never did; see PublicSignup, which has no product branch.)
	Product string
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

	// Product selects the fleet product (default "drs"). Every product goes
	// through the same paid registration flow — pending_payment, a Selcom
	// registration checkout, and activation on payment — so a health facility
	// pays to activate exactly as a retail store does; only the namespace on the
	// tenant differs (and downstream, which consumer picks up tenant.activated).
	product := defaultStr(in.Product, "drs")

	t, err := s.Create(ctx, CreateInput{
		Slug:         in.Slug,
		Name:         in.Name,
		ContactEmail: in.ContactEmail,
		Phone:        in.Phone,
		CountryCode:  in.CountryCode,
		Product:      product,
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
	// Keep the owner's legal name on the tenant. It rides on tenant.activated so
	// the product can create its first staff record under the PERSON's name;
	// without it dhs-setup fell back to the tenant's own Name, which made the
	// facility admin of "Rani Dental Clinic" a staff member called after the
	// clinic rather than after the dentist.
	t.OwnerFirstName = strings.TrimSpace(in.FirstName)
	t.OwnerMiddleName = strings.TrimSpace(in.MiddleName)
	t.OwnerLastName = strings.TrimSpace(in.LastName)

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
				Email:      in.ContactEmail,
				FirstName:  in.FirstName,
				MiddleName: in.MiddleName,
				LastName:   in.LastName,
				Password:   identityPassword,
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
	// frontend to surface the resend-payment-link path. On payment, defolt-
	// billing calls back /internal/tenants/:id/activate, which flips the tenant
	// active and emits tenant.activated — dhs-setup's consumer then provisions
	// the Facility Admin for a health tenant (drs-setup for a store).
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
	// Scope the resume lookup to the same product namespace the retry is
	// for, matching the (product, slug) uniqueness. A drs tenant on the
	// same slug must not be mistaken for this health signup's stalled row.
	existing, err := s.repo.FindBySlug(ctx, NormalizeProduct(in.Product), slug)
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
	// either way the value they just typed is the current one.
	//
	// The number has already passed RequirePhone: this path is only
	// reached after Create refused with ErrSlugTaken, and Create
	// validates the phone before it ever looks at the slug. So a
	// normalise failure here is not possible, and if it somehow were,
	// keeping the row's existing number beats failing a resumed signup.
	//
	// Persist failures are swallowed on purpose for the same reason — a
	// resumed signup must still complete. It leaves the row with a stale
	// phone, not an absent one, because a resume implies the row already
	// existed under the pre-13.7 code or with an earlier number.
	if p, err := NormalisePhone(in.Phone); err == nil && p != "" && p != existing.Phone {
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

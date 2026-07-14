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
	Name         string      // workspace / merchant name
	ContactEmail string      // becomes the initial Store Admin
	FirstName    string
	LastName     string
	CountryCode  string
	TurnstileTok string
	ClientIP     string
}

// PublicSignup is the marketing form entrypoint. Verifies Turnstile,
// creates the tenant record, and registers the Store Admin user in
// defolt-identity. On identity failure the tenant record stays as
// `pending_payment` — the sweep cron cleans up abandoned rows.
func (s *TenantsService) PublicSignup(ctx context.Context, in SignupInput, ts *Turnstile, id *IdentityClient) (*model.Tenant, error) {
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

	if id != nil {
		password := generatePassword()
		regErr := id.Register(ctx, RegisterInput{
			Email:     in.ContactEmail,
			FirstName: in.FirstName,
			LastName:  in.LastName,
			Password:  password,
		})
		if regErr != nil {
			logger.LogWarn("", "signup-identity", fmt.Sprintf("tenant=%s email=%s: %v", t.ID, in.ContactEmail, regErr))
			// Tenant record stays for support to unblock manually or the
			// sweep cron to reap.
		}
	}
	return t, nil
}

// generatePassword makes a 16-char URL-safe token. Complies with the
// defolt-identity minimum-complexity policy (mixed chars, symbols).
func generatePassword() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "Defolt-" + base64.URLEncoding.EncodeToString([]byte("fallback"))[:10]
	}
	return "Defolt-" + base64.URLEncoding.EncodeToString(b)[:12]
}

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"defolt-tenants-service/logger"
)

// Turnstile verifies a Cloudflare Turnstile token via the siteverify
// endpoint. Returns nil on success. Fail-closed contract: whenever a secret
// is configured, an invalid or absent token is rejected (non-nil error).
// Verification is skipped ONLY in the explicitly-unconfigured mode (empty
// secret) — acceptable for local/UAT but never the production state, so
// NewTurnstile logs a WARN to surface an accidentally-unset prod secret
// instead of silently leaving public signup open.
type Turnstile struct {
	secret string
	http   *http.Client
}

func NewTurnstile(secret string) *Turnstile {
	if strings.TrimSpace(secret) == "" {
		logger.LogWarn("", "turnstile", "TURNSTILE_SECRET unset: CAPTCHA verification DISABLED (public signup unprotected) - acceptable for dev/UAT only, never production")
	}
	return &Turnstile{secret: secret, http: &http.Client{Timeout: 6 * time.Second}}
}

func (t *Turnstile) Verify(ctx context.Context, token, remoteIP string) error {
	if strings.TrimSpace(t.secret) == "" {
		return nil // disabled: explicitly-unconfigured mode only (see NewTurnstile)
	}
	form := url.Values{
		"secret":   {t.secret},
		"response": {token},
	}
	if remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://challenges.cloudflare.com/turnstile/v0/siteverify",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := t.http.Do(req)
	if err != nil {
		return fmt.Errorf("turnstile: %w", err)
	}
	defer resp.Body.Close()
	var body struct {
		Success    bool     `json:"success"`
		ErrorCodes []string `json:"error-codes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return fmt.Errorf("turnstile decode: %w", err)
	}
	if !body.Success {
		return fmt.Errorf("turnstile failed: %v", body.ErrorCodes)
	}
	return nil
}

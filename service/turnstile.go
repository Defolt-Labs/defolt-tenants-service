package service

import (
	"context"
	"encoding/json"
	"errors"
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
	secret    string
	verifyURL string
	http      *http.Client
}

const turnstileSiteverifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

func NewTurnstile(secret string) *Turnstile {
	if strings.TrimSpace(secret) == "" {
		logger.LogWarn("", "turnstile", "TURNSTILE_SECRET unset: CAPTCHA verification DISABLED (public signup unprotected) - acceptable for dev/UAT only, never production")
	}
	return &Turnstile{secret: secret, verifyURL: turnstileSiteverifyURL, http: &http.Client{Timeout: 6 * time.Second}}
}

// WithVerifyURL points the verifier at another siteverify endpoint. It exists
// for tests, which serve Cloudflare's answer from an httptest server.
func (t *Turnstile) WithVerifyURL(u string) *Turnstile {
	t.verifyURL = u
	return t
}

// ErrTurnstileUnavailable is the other half of a failed human check: the
// check could not be MADE, so the person did nothing wrong. Cloudflare was
// unreachable, answered something that is not its JSON, or refused OUR
// secret (invalid-input-secret, missing-input-secret). WP-SIGNUP1: the owner
// was shown "Forbidden." for exactly that last case, while the log said
// [invalid-input-secret]; telling a person to try the check again cannot fix a
// key the server holds.
var ErrTurnstileUnavailable = errors.New("turnstile verification unavailable")

// TurnstileError carries Cloudflare's error codes next to the sentinel it
// wraps, so every errors.Is on ErrTurnstile / ErrTurnstileUnavailable keeps
// working and the handler can still put the reason in details. The codes are
// Cloudflare's public vocabulary (invalid-input-response, timeout-or-duplicate),
// never the token or the secret.
type TurnstileError struct {
	Codes []string
	base  error
	cause error
}

func (e *TurnstileError) Error() string {
	msg := e.base.Error()
	if len(e.Codes) > 0 {
		msg += ": " + strings.Join(e.Codes, ",")
	}
	if e.cause != nil {
		msg += ": " + e.cause.Error()
	}
	return msg
}

func (e *TurnstileError) Unwrap() error { return e.base }

// turnstileSecretFault names the Cloudflare codes that mean the SERVER's
// secret is wrong, not the person's token.
func turnstileSecretFault(codes []string) bool {
	for _, c := range codes {
		if c == "invalid-input-secret" || c == "missing-input-secret" {
			return true
		}
	}
	return false
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.verifyURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return &TurnstileError{base: ErrTurnstileUnavailable, cause: err}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := t.http.Do(req)
	if err != nil {
		return &TurnstileError{base: ErrTurnstileUnavailable, cause: fmt.Errorf("turnstile: %w", err)}
	}
	defer resp.Body.Close()
	var body struct {
		Success    bool     `json:"success"`
		ErrorCodes []string `json:"error-codes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return &TurnstileError{base: ErrTurnstileUnavailable, cause: fmt.Errorf("turnstile decode: %w", err)}
	}
	if !body.Success {
		if turnstileSecretFault(body.ErrorCodes) {
			return &TurnstileError{Codes: body.ErrorCodes, base: ErrTurnstileUnavailable}
		}
		return &TurnstileError{Codes: body.ErrorCodes, base: ErrTurnstile}
	}
	return nil
}

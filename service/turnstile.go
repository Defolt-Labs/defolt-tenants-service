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

// Turnstile verifies Cloudflare Turnstile CAPTCHA tokens.
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

// WithVerifyURL overrides the verification endpoint for testing.
func (t *Turnstile) WithVerifyURL(u string) *Turnstile {
	t.verifyURL = u
	return t
}

// ErrTurnstileUnavailable is returned when token verification cannot be completed.
var ErrTurnstileUnavailable = errors.New("turnstile verification unavailable")

// TurnstileError wraps sentinel errors with Cloudflare error codes.
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

// turnstileSecretFault reports whether verification failed due to server secret misconfiguration.
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
		return nil
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

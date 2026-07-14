package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Turnstile verifies a Cloudflare Turnstile token via the siteverify
// endpoint. Returns nil on success. Empty secret = skipped (dev mode).
type Turnstile struct {
	secret string
	http   *http.Client
}

func NewTurnstile(secret string) *Turnstile {
	return &Turnstile{secret: secret, http: &http.Client{Timeout: 6 * time.Second}}
}

func (t *Turnstile) Verify(ctx context.Context, token, remoteIP string) error {
	if strings.TrimSpace(t.secret) == "" {
		return nil // disabled
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

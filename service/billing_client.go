package service

import (
	"bytes"
	"context"
	"defolt-tenants-service/reqid"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// BillingClient calls defolt-billing-service for registration checkout and lifecycle state.
type BillingClient struct {
	baseURL     string
	internalKey string
	http        *http.Client
}

func NewBillingClient(baseURL, internalKey string) *BillingClient {
	return &BillingClient{
		baseURL:     baseURL,
		internalKey: internalKey,
		http:        &http.Client{Timeout: 12 * time.Second},
	}
}

// CheckoutResult holds the registration checkout URL and invoice details.
type CheckoutResult struct {
	InvoiceID  string `json:"invoice_id"`
	AmountTZS  int64  `json:"amount_tzs"`
	PaymentURL string `json:"payment_url"`
	Reference  string `json:"reference"`
	ExpiresAt  string `json:"expires_at"`
	Owed       *bool  `json:"owed"`
	State      string `json:"state"`
}

// NothingOwed reports whether billing requires no payment step.
func (r *CheckoutResult) NothingOwed() bool {
	return r != nil && r.Owed != nil && !*r.Owed
}

// CreateCheckout requests a checkout URL for tenant registration.
func (c *BillingClient) CreateCheckout(ctx context.Context, tenantID uuid.UUID, ownerEmail string, redirectURL string) (*CheckoutResult, error) {
	if c == nil || c.baseURL == "" {
		return nil, fmt.Errorf("billing-client: baseURL not configured")
	}
	body, _ := json.Marshal(map[string]any{"owner_email": ownerEmail, "redirect_url": redirectURL})
	url := fmt.Sprintf("%s/billing/v1/internal/tenants/%s/checkout", c.baseURL, tenantID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	if rid := reqid.From(ctx); rid != "" {
		req.Header.Set("X-Request-ID", rid)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("billing CreateCheckout: %d %s", resp.StatusCode, string(raw))
	}
	var env struct {
		Data CheckoutResult `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("billing CreateCheckout: bad envelope: %w", err)
	}
	if env.Data.PaymentURL == "" && !env.Data.NothingOwed() {
		return nil, fmt.Errorf("billing CreateCheckout: envelope missing payment_url")
	}
	return &env.Data, nil
}

// ErrNoSubscription indicates billing has no subscription record for the tenant.
var ErrNoSubscription = errors.New("billing has no subscription for this tenant")

// SubscriptionState queries billing for current subscription entitlement state.
func (c *BillingClient) SubscriptionState(ctx context.Context, tenantID uuid.UUID) (string, error) {
	if c == nil || c.baseURL == "" {
		return "", fmt.Errorf("billing-client: baseURL not configured")
	}
	url := fmt.Sprintf("%s/billing/v1/internal/tenants/%s/entitlement", c.baseURL, tenantID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	if rid := reqid.From(ctx); rid != "" {
		req.Header.Set("X-Request-ID", rid)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusNotFound {
		return "", ErrNoSubscription
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("billing SubscriptionState: %d %s", resp.StatusCode, string(raw))
	}
	var env struct {
		Data struct {
			State string `json:"state"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", fmt.Errorf("billing SubscriptionState: bad envelope: %w", err)
	}
	if env.Data.State == "" {
		return "", fmt.Errorf("billing SubscriptionState: envelope missing state")
	}
	return env.Data.State, nil
}

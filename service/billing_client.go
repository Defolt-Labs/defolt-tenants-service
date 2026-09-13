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

// BillingClient talks to defolt-billing-service to mint the Selcom
// registration-checkout link for a fresh tenant (plan §5.11). The
// billing side owns the invoice, the amount and the Selcom order; we
// only carry the payment URL back to the signup caller.
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

// CheckoutResult mirrors the envelope data of
// POST {billing}/api/v1/internal/tenants/{id}/checkout.
//
// AmountTZS is int64 WHOLE SHILLINGS. It was float64 until 2026-09-04,
// which was the one float on a money path anywhere in this fleet.
// Billing produces it as int64 the whole way down -- plan.RegistrationCents
// / 100 into model.Invoice.AmountTZS int64, out through
// service.CheckoutView.AmountTZS int64 -- and
// Libraries/defolt-kit/money declares the fleet type as `Money int64`,
// whole shillings, naming these float64 amount_tzs DTO fields as the
// drift it retires. Libraries/defolt-contracts/payment already declares
// SelcomPaymentEvent.AmountTZS int64.
//
// The change is wire-compatible: JSON has one number type, so the field
// name and the emitted bytes are identical for any integral value and no
// deploy ordering is required. Billing stores int64 and so cannot emit a
// fraction; if it ever did, this now fails the unmarshal loudly instead
// of rounding silently, and CreateCheckout's error is already non-fatal.
type CheckoutResult struct {
	InvoiceID  string `json:"invoice_id"`
	AmountTZS  int64  `json:"amount_tzs"`
	PaymentURL string `json:"payment_url"`
	Reference  string `json:"reference"`
	ExpiresAt  string `json:"expires_at"`
}

// CreateCheckout asks billing for a Selcom checkout URL covering the
// tenant's registration invoice. Callers treat failure as non-fatal:
// the tenant stays `pending_payment` and the link can be re-issued
// via resend-payment-link.
func (c *BillingClient) CreateCheckout(ctx context.Context, tenantID uuid.UUID, ownerEmail string, redirectURL string) (*CheckoutResult, error) {
	if c == nil || c.baseURL == "" {
		return nil, fmt.Errorf("billing-client: baseURL not configured")
	}
	body, _ := json.Marshal(map[string]any{"owner_email": ownerEmail, "redirect_url": redirectURL})
	url := fmt.Sprintf("%s/api/v1/internal/tenants/%s/checkout", c.baseURL, tenantID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	// Same fleet requirement as the identity client: forward, never mint.
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
	if env.Data.PaymentURL == "" {
		return nil, fmt.Errorf("billing CreateCheckout: envelope missing payment_url")
	}
	return &env.Data, nil
}

// ErrNoSubscription means billing has no subscription row for this
// tenant. Distinct from a transport failure on purpose: the WP-B16
// activation sweep must do NOTHING when billing has never heard of a
// tenant, and must retry on the next tick when billing is merely
// unreachable. Collapsing the two would either activate a tenant no
// product is entitled to, or leave a live one stuck for ever.
var ErrNoSubscription = errors.New("billing has no subscription for this tenant")

// SubscriptionState reads the tenant's billing lifecycle state off the
// entitlement endpoint — the same read the products gate on, so there is
// one authority and not a second copy of the enum here.
//
// This is the PULL half of WP-B16. Billing pushes the state the moment it
// seeds a subscription (defolt-billing-service calls
// PUT /internal/tenants/:id/subscription-state), and that push is a single
// best-effort HTTP call that a restart, a rollout or a network blip can
// lose. A tenant that misses it is not merely mis-labelled: under WP-B15
// nothing ever pays at signup, so the tenant stays `pending_payment`, no
// `tenant.activated` is emitted, its product never provisions it, and the
// abandonment sweep would eventually delete it. The pull is what makes the
// push losable.
func (c *BillingClient) SubscriptionState(ctx context.Context, tenantID uuid.UUID) (string, error) {
	if c == nil || c.baseURL == "" {
		return "", fmt.Errorf("billing-client: baseURL not configured")
	}
	url := fmt.Sprintf("%s/api/v1/internal/tenants/%s/entitlement", c.baseURL, tenantID)
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

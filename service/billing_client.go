package service

import (
	"bytes"
	"context"
	"defolt-tenants-service/reqid"
	"encoding/json"
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

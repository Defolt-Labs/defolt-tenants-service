package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// WP-SIGNUP2. Billing answers a health signup's checkout with 200, an empty
// payment_url and owed false, because the clinic is exploring and owes
// nothing. That is an answer, not a failure; a real checkout with no link
// still is one.
func TestCreateCheckoutNothingOwedIsAnAnswer(t *testing.T) {
	for _, tc := range []struct {
		name, body  string
		wantErr     bool
		wantNothing bool
	}{
		{"nothing owed", `{"code":"DL_BILLING_NOTHING_OWED","data":{"payment_url":"","amount_tzs":0,"owed":false,"state":"exploring"}}`, false, true},
		{"a real checkout", `{"code":"DL_BILLING_PAY_INIT","data":{"payment_url":"https://pay.example/x","amount_tzs":2000}}`, false, false},
		{"a checkout with no link", `{"code":"DL_BILLING_PAY_INIT","data":{"payment_url":"","amount_tzs":2000}}`, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			res, err := NewBillingClient(srv.URL, "k").CreateCheckout(context.Background(), uuid.New(), "o@example.com", "")
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want error %t", err, tc.wantErr)
			}
			if err == nil && res.NothingOwed() != tc.wantNothing {
				t.Fatalf("NothingOwed = %t, want %t", res.NothingOwed(), tc.wantNothing)
			}
			if tc.wantNothing && res.State != "exploring" {
				t.Fatalf("state = %q, want exploring", res.State)
			}
		})
	}
}

package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"defolt-tenants-service/model"

	"github.com/google/uuid"
)

// TestExploringIsALiveTenant is WP-B16's whole point in one assertion.
//
// WP-B15 seeds every fresh subscription as `exploring`, and this service
// translated billing's enum with a switch whose default locks the gate. So
// `exploring` — the state of a tenant who has done nothing wrong — mapped to
// `suspended`, and more importantly never reached `active`, which is the edge
// that publishes tenant.activated and the only thing that makes dhs-setup or
// drs-setup provision the tenant at all.
func TestExploringIsALiveTenant(t *testing.T) {
	for _, tc := range []struct {
		subState string
		want     model.TenantStatus
	}{
		{"exploring", model.StatusActive},
		{"trial", model.StatusActive},
		{"active", model.StatusActive},
		{"awaiting_registration", model.StatusPendingPayment},
		{"grace", model.StatusGrace},
		{"suspended", model.StatusSuspended},
		{"cancelled", model.StatusSuspended},
		// A state this service has genuinely never heard of still locks the
		// gate. That default is right; `exploring` simply was not unknown.
		{"something_billing_invented_later", model.StatusSuspended},
		{"", model.StatusSuspended},
	} {
		if got := TenantStatusForSubscriptionState(tc.subState); got != tc.want {
			t.Errorf("subscription state %q maps to tenant status %q, want %q", tc.subState, got, tc.want)
		}
	}
}

// TestActivatedPayloadCarriesWhatTheConsumersNeed pins the two fields that
// decide whether the owner of a freshly provisioned facility can log in.
// dhs-setup refuses to create the staff mirror without owner_user_id and
// writes owner_email onto it as the address login matches against.
func TestActivatedPayloadCarriesWhatTheConsumersNeed(t *testing.T) {
	owner := uuid.New()
	tn := &model.Tenant{
		ID: uuid.New(), Slug: "defolt-labs", Name: "Defolt Labs",
		Product: "health", ContactEmail: "owner@example.com",
		OwnerUserID: &owner, OwnerFirstName: "Rachel", OwnerLastName: "Mhaville",
	}
	p := activatedPayload(context.Background(), tn)

	// OwnerEmail is set by the public signup form and by nothing else. A
	// tenant created through the internal route has only ContactEmail, and an
	// empty owner_email writes a staff row no login can ever match.
	if p["owner_email"] != "owner@example.com" {
		t.Fatalf("owner_email = %v, want the contact email as the fallback", p["owner_email"])
	}
	if p["owner_user_id"] != tn.OwnerUserID {
		t.Fatalf("owner_user_id missing; dhs-setup would log 'admin mirror NOT created'")
	}
	if p["name"] != "Defolt Labs" || p["slug"] != "defolt-labs" {
		t.Fatalf("the facility's own name and slug must ride on the event: %v", p)
	}
	// An exploring tenant is not on a trial clock. A zero time on the wire
	// would read as a window that had already closed.
	if _, ok := p["trial_ends_at"]; ok {
		t.Fatalf("trial_ends_at present for a tenant with no trial: %v", p)
	}

	// The explicit owner email still wins when signup recorded one.
	tn.OwnerEmail = "signed-up@example.com"
	if p := activatedPayload(context.Background(), tn); p["owner_email"] != "signed-up@example.com" {
		t.Fatalf("owner_email = %v, want the signup email", p["owner_email"])
	}

	// And the paid path's trial window still rides when there IS one.
	now := time.Now()
	end := now.Add(7 * 24 * time.Hour)
	tn.TrialStartsAt, tn.TrialEndsAt, tn.ActivatedAt = &now, &end, &now
	p = activatedPayload(context.Background(), tn)
	if p["trial_ends_at"] != end || p["activated_at"] != now {
		t.Fatalf("the paid path's trial window and activation moment must still ride: %v", p)
	}
}

// TestAbandonBlockedNeverDeletesOnAnUnansweredQuestion covers the second half
// of WP-B16, which is the dangerous half.
//
// SweepAbandoned hard-deletes the tenant row AND the owner's identity
// account after 24 hours in pending_payment. Under WP-B15 nobody pays at
// signup, so every live exploring facility became eligible for deletion on
// its second day. The bias here is one-directional on purpose: only a
// definite "billing has no subscription" allows the delete.
func TestAbandonBlockedNeverDeletesOnAnUnansweredQuestion(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		body      string
		wantBlock bool
	}{
		{"exploring is a live tenant", 200, `{"data":{"state":"exploring"}}`, true},
		{"so is one on trial", 200, `{"data":{"state":"trial"}}`, true},
		{"and one in grace", 200, `{"data":{"state":"grace"}}`, true},
		{"awaiting_registration is the abandoned signup this sweeper is for", 200, `{"data":{"state":"awaiting_registration"}}`, false},
		{"no subscription at all is the other case it is right about", 404, `{}`, false},
		{"billing erroring is not permission to delete", 500, `boom`, true},
		{"nor is billing answering nonsense", 200, `{"data":{}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			s := &TenantsService{billing: NewBillingClient(srv.URL, "k")}
			if got, why := s.abandonBlocked(context.Background(), uuid.New()); got != tc.wantBlock {
				t.Fatalf("blocked = %v (%s), want %v", got, why, tc.wantBlock)
			}
		})
	}
}

// TestSubscriptionStateDistinguishesGoneFromDown is the distinction the sweep
// above rests on: a 404 is terminal and everything else is retryable.
func TestSubscriptionStateDistinguishesGoneFromDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Service-Key") != "k" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c := NewBillingClient(srv.URL, "k")
	if _, err := c.SubscriptionState(context.Background(), uuid.New()); err != ErrNoSubscription {
		t.Fatalf("404 gave %v, want ErrNoSubscription", err)
	}
}

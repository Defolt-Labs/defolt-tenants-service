package service

import (
	"encoding/json"
	"testing"

	"defolt-tenants-service/model"

	"github.com/google/uuid"
)

// The owner registered "Rani Dental Clinic" on DHS and was welcomed by email as
// "Your". defolt-billing builds that mail from tenant.created, and the event
// carried five fields, none of which was a name.
//
// The consumer was blamed for it, and only half deserved it: a service cannot
// print a fact it was never sent. This test is the half that lives here. It
// asserts the CONTRACT of the event rather than the behaviour of a publish, so
// it needs no NATS connection and cannot be skipped in CI.
func TestTenantCreatedCarriesTheNames(t *testing.T) {
	tenant := &model.Tenant{
		ID:              uuid.MustParse("47c58b5b-9369-4149-8845-eeaf71184768"),
		Slug:            "rani-dental-clinic",
		Name:            "Rani Dental Clinic",
		ContactEmail:    "defoltlabs@gmail.com",
		Product:         "health",
		Status:          model.StatusPendingPayment,
		OwnerFirstName:  "Abel",
		OwnerMiddleName: "",
		OwnerLastName:   "Sekwala",
	}

	raw, err := json.Marshal(tenantCreatedPayload(tenant))
	if err != nil {
		t.Fatalf("marshal tenant.created: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal tenant.created: %v", err)
	}

	// The ORGANISATION's name. This is the field whose absence produced
	// "Welcome to DRS, Your".
	if got["name"] != "Rani Dental Clinic" {
		t.Errorf("name: want %q, got %v", "Rani Dental Clinic", got["name"])
	}

	// The PERSON's name, in parts. Kept separate from `name` on purpose: a
	// consumer that conflates the two makes the facility admin of a clinic a
	// staff member called after the clinic, which is what dhs-setup did before
	// tenant.activated carried these.
	if got["owner_first_name"] != "Abel" {
		t.Errorf("owner_first_name: want %q, got %v", "Abel", got["owner_first_name"])
	}
	if got["owner_last_name"] != "Sekwala" {
		t.Errorf("owner_last_name: want %q, got %v", "Sekwala", got["owner_last_name"])
	}
	// Present even when empty. A consumer that reads a missing key and one that
	// reads an empty string take different branches in some JSON libraries, and
	// "no middle name" is a fact, not an absence of one.
	if _, ok := got["owner_middle_name"]; !ok {
		t.Error("owner_middle_name: key missing entirely; it must be present even when empty")
	}

	// The five fields that were always there. Billing's consumer decodes these
	// by name, so renaming one silently stops a merchant being invoiced.
	for k, want := range map[string]any{
		"tenant_id":   "47c58b5b-9369-4149-8845-eeaf71184768",
		"slug":        "rani-dental-clinic",
		"owner_email": "defoltlabs@gmail.com",
		"product":     "health",
		"state":       "pending_payment",
	} {
		if got[k] != want {
			t.Errorf("%s: want %v, got %v", k, want, got[k])
		}
	}
}

// A tenant created by the admin path has no person attached. The event must
// still be well-formed: every key present, the name fields empty rather than
// absent, so a consumer branches on emptiness and never on a missing key.
func TestTenantCreatedSurvivesAnOwnerlessTenant(t *testing.T) {
	got := tenantCreatedPayload(&model.Tenant{
		ID:     uuid.New(),
		Slug:   "kariakoo-duka",
		Name:   "Kariakoo Duka",
		Status: model.StatusPendingPayment,
	})
	for _, k := range []string{
		"tenant_id", "slug", "name", "owner_email", "product", "state",
		"owner_first_name", "owner_middle_name", "owner_last_name",
	} {
		if _, ok := got[k]; !ok {
			t.Errorf("%s: key missing on an ownerless tenant", k)
		}
	}
	if got["name"] != "Kariakoo Duka" {
		t.Errorf("name: want %q, got %v", "Kariakoo Duka", got["name"])
	}
}

// The host a customer is sent to, per product. `mt.` must never appear in any
// of them: that is the legacy multi-tenant apex, and it went out in a real
// welcome email as https://mt.drs.uat.defoltlabs.com/billing.
func TestProductHomeNeverServesTheMultiTenantApex(t *testing.T) {
	cases := []struct {
		name, product, slug, root, want string
	}{
		{"a store gets its own host", "drs", "kariakoo-duka", "uat.defoltlabs.com",
			"https://kariakoo-duka.shop.uat.defoltlabs.com"},
		{"every clinic shares one host", "health", "rani-dental-clinic", "uat.defoltlabs.com",
			"https://dhs.uat.defoltlabs.com"},
		{"production drops the environment label", "drs", "kariakoo-duka", "defoltlabs.com",
			"https://kariakoo-duka.shop.defoltlabs.com"},
		{"a store with no slug gets the product apex, not an empty label", "drs", "", "uat.defoltlabs.com",
			"https://drs.uat.defoltlabs.com"},
		{"an unknown product gets the marketing site, never a guess", "vinono", "whatever", "uat.defoltlabs.com",
			"https://www.uat.defoltlabs.com"},
		{"an empty product is drs, matching NormalizeProduct", "", "kariakoo-duka", "uat.defoltlabs.com",
			"https://kariakoo-duka.shop.uat.defoltlabs.com"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ProductHome(c.product, c.slug, c.root)
			if got != c.want {
				t.Errorf("ProductHome(%q, %q, %q) = %q, want %q", c.product, c.slug, c.root, got, c.want)
			}
			if containsMT(got) {
				t.Errorf("ProductHome returned the mt. apex: %q", got)
			}
		})
	}
}

// ResendPaymentLink used to pass an empty redirect_url, so Selcom returned the
// payer nowhere at all. The return URL must be a real page on the product the
// person is actually buying.
func TestProductReturnURLLandsOnTheProductBeingBought(t *testing.T) {
	if got, want := ProductReturnURL("health", "rani-dental-clinic", "uat.defoltlabs.com"),
		"https://dhs.uat.defoltlabs.com/login"; got != want {
		t.Errorf("health return URL = %q, want %q", got, want)
	}
	if got, want := ProductReturnURL("drs", "kariakoo-duka", "uat.defoltlabs.com"),
		"https://kariakoo-duka.shop.uat.defoltlabs.com/login"; got != want {
		t.Errorf("drs return URL = %q, want %q", got, want)
	}
	// Never empty. An empty return URL is the defect this replaced, and it is
	// invisible: the checkout still succeeds and the payer simply never comes
	// back.
	if ProductReturnURL("", "", "") == "" {
		t.Error("return URL is empty even with nothing supplied; Selcom would strand the payer")
	}
}

func containsMT(s string) bool {
	for i := 0; i+4 <= len(s); i++ {
		if s[i:i+4] == "mt.d" || s[i:i+4] == "/mt." || s[i:i+4] == ".mt." {
			return true
		}
	}
	return false
}

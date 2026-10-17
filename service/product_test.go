package service

import "testing"

func TestNormalizeProduct(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty defaults drs", in: "", want: "drs"},
		{name: "whitespace defaults drs", in: "   ", want: "drs"},
		{name: "health passthrough", in: "health", want: "health"},
		{name: "drs passthrough", in: "drs", want: "drs"},
		{name: "uppercase health", in: "HEALTH", want: "health"},
		{name: "padded", in: "  Health  ", want: "health"},
		{name: "nyaraka passthrough", in: "nyaraka", want: "nyaraka"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := NormalizeProduct(c.in); got != c.want {
				t.Fatalf("NormalizeProduct(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

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

func TestProductReturnURLLandsOnTheProductBeingBought(t *testing.T) {
	if got, want := ProductReturnURL("health", "rani-dental-clinic", "uat.defoltlabs.com"),
		"https://dhs.uat.defoltlabs.com/login"; got != want {
		t.Errorf("health return URL = %q, want %q", got, want)
	}
	if got, want := ProductReturnURL("drs", "kariakoo-duka", "uat.defoltlabs.com"),
		"https://kariakoo-duka.shop.uat.defoltlabs.com/login"; got != want {
		t.Errorf("drs return URL = %q, want %q", got, want)
	}
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

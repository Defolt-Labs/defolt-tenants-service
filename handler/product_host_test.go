package handler

import "testing"

func TestProductFromHost(t *testing.T) {
	cases := []struct {
		name   string
		host   string
		header string
		want   string
	}{
		// The edge header wins when present — this is how a future DHS
		// (health) host family, or any per-product ingress, states its
		// product explicitly rather than relying on suffix parsing.
		{name: "header wins", host: "acme.mt.drs.uat.defoltlabs.com", header: "health", want: "health"},
		{name: "header normalized", host: "acme.shop.defoltlabs.com", header: "  HEALTH ", want: "health"},

		// No header: derive from the host suffix. All DRS host families
		// resolve to drs.
		{name: "drs staff uat", host: "acme.mt.drs.uat.defoltlabs.com", want: "drs"},
		{name: "drs staff prod", host: "acme.drs.defoltlabs.com", want: "drs"},
		{name: "drs storefront", host: "acme.shop.defoltlabs.com", want: "drs"},
		{name: "drs storefront uat", host: "acme.shop.uat.defoltlabs.com", want: "drs"},

		// Health host families (greenfield, but parse them now so the day
		// DHS gets per-slug hosts nothing silently resolves as drs).
		{name: "health dhs suffix", host: "acme.dhs.uat.defoltlabs.com", want: "health"},
		{name: "health afya suffix", host: "acme.afya.defoltlabs.com", want: "health"},

		// Unknown / bare host falls back to the fleet default rather than
		// guessing — keeps the pre-product-scoping behaviour.
		{name: "unknown suffix defaults drs", host: "acme.example.com", want: "drs"},
		{name: "empty host defaults drs", host: "", want: "drs"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := productFromHost(c.host, c.header); got != c.want {
				t.Fatalf("productFromHost(%q, %q) = %q, want %q", c.host, c.header, got, c.want)
			}
		})
	}
}

package service

import "testing"

func TestNormalizeProduct(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		// Empty defaults to drs, preserving every pre-product-scoping
		// caller (drs-vue signup sends no product field).
		{name: "empty defaults drs", in: "", want: "drs"},
		{name: "whitespace defaults drs", in: "   ", want: "drs"},
		{name: "health passthrough", in: "health", want: "health"},
		{name: "drs passthrough", in: "drs", want: "drs"},
		// Case/space are noise: the DB stores lowercase product literals.
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

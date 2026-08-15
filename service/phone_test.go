package service

import "testing"

func TestNormalizePhone(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		// The four ways the same real number gets typed.
		{"already e164", "+255785659580", "+255785659580"},
		{"country code no plus", "255785659580", "+255785659580"},
		{"local trunk zero", "0785659580", "+255785659580"},
		{"bare subscriber", "785659580", "+255785659580"},

		// Separators carry no information.
		{"spaces", "0785 659 580", "+255785659580"},
		{"hyphens", "+255-785-659-580", "+255785659580"},
		{"parens and dots", "(0785) 659.580", "+255785659580"},
		{"leading and trailing space", "  0785659580  ", "+255785659580"},

		// International access prefix.
		{"00 prefix", "00255785659580", "+255785659580"},

		// Lenient by design: unrecognised input is preserved, never
		// rejected, because a bad phone must not cost a signup.
		{"empty", "", ""},
		{"blank", "   ", ""},
		{"non tz kept", "+441632960961", "+441632960961"},
		{"garbage kept", "not a phone", "not a phone"},
		{"too short kept", "12345", "12345"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := NormalizePhone(c.in); got != c.want {
				t.Fatalf("NormalizePhone(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// The whole point of normalising is that the variants collapse to one
// key. Assert that directly rather than trusting the table above to
// stay consistent with itself.
func TestNormalizePhoneConverges(t *testing.T) {
	variants := []string{
		"+255785659580",
		"255785659580",
		"0785659580",
		"785659580",
		"0785 659 580",
		"+255 785 659 580",
		"00255785659580",
	}
	want := NormalizePhone(variants[0])
	for _, v := range variants {
		if got := NormalizePhone(v); got != want {
			t.Fatalf("variant %q normalised to %q, want %q — the variants do not converge", v, got, want)
		}
	}
}

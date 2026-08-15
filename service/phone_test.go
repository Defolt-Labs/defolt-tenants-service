package service

import (
	"errors"
	"testing"
)

func TestNormalisePhone(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr error
	}{
		// The four ways the same real number gets typed.
		{name: "already e164", in: "+255785659580", want: "+255785659580"},
		{name: "country code no plus", in: "255785659580", want: "+255785659580"},
		{name: "local trunk zero", in: "0785659580", want: "+255785659580"},
		{name: "bare subscriber", in: "785659580", want: "+255785659580"},

		// Separators carry no information.
		{name: "spaces", in: "0785 659 580", want: "+255785659580"},
		{name: "hyphens", in: "+255-785-659-580", want: "+255785659580"},
		{name: "parens and dots", in: "(0785) 659.580", want: "+255785659580"},
		{name: "leading and trailing space", in: "  0785659580  ", want: "+255785659580"},

		// `00` is how an older handset writes `+`.
		{name: "00 prefix", in: "00255785659580", want: "+255785659580"},

		// A number written with its own country code keeps it. This is
		// the case that makes the default region safe to have.
		{name: "kenyan kept", in: "+254712345678", want: "+254712345678"},
		{name: "uk kept", in: "+441632960961", want: "+441632960961"},

		// Empty is not an error: it is how an optional number is
		// cleared. RequirePhone is what refuses it where it matters.
		{name: "empty", in: "", want: ""},
		{name: "blank", in: "   ", want: ""},

		// Refusals. This is the half the previous implementation did not
		// have: it returned unparseable input verbatim, so a typo was
		// stored and later handed to Selcom as buyer_phone.
		{name: "letters only", in: "not a phone", wantErr: ErrPhoneInvalid},
		{name: "too short to place", in: "12345", wantErr: ErrPhoneAmbiguous},
		{name: "digits but no country", in: "1234567890", wantErr: ErrPhoneAmbiguous},
		{name: "trunk zero then junk", in: "0000", wantErr: ErrPhoneInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := NormalisePhone(c.in)
			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("NormalisePhone(%q) error = %v, want %v", c.in, err, c.wantErr)
				}
				if got != "" {
					t.Fatalf("NormalisePhone(%q) returned %q alongside an error — a refused number must never be stored", c.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalisePhone(%q) unexpected error: %v", c.in, err)
			}
			if got != c.want {
				t.Fatalf("NormalisePhone(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// The whole point of normalising is that the variants collapse to one
// key. Assert that directly rather than trusting the table above to
// stay consistent with itself.
func TestNormalisePhoneConverges(t *testing.T) {
	variants := []string{
		"+255785659580",
		"255785659580",
		"0785659580",
		"785659580",
		"0785 659 580",
		"+255 785 659 580",
		"00255785659580",
		"(0785) 659-580",
	}
	want, err := NormalisePhone(variants[0])
	if err != nil {
		t.Fatalf("baseline variant failed to normalise: %v", err)
	}
	for _, v := range variants {
		got, err := NormalisePhone(v)
		if err != nil {
			t.Fatalf("variant %q failed to normalise: %v", v, err)
		}
		if got != want {
			t.Fatalf("variant %q normalised to %q, want %q — the variants do not converge", v, got, want)
		}
	}
}

// RequirePhone is the signup contract: a missing or unusable number is
// a 400 the merchant can act on, never a silently empty column. The
// globals it replaces are deleted, so there is no fallback behind it.
func TestRequirePhone(t *testing.T) {
	t.Run("normalises a good number", func(t *testing.T) {
		got, err := RequirePhone(" 0785 659 580 ")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "+255785659580" {
			t.Fatalf("got %q, want %q", got, "+255785659580")
		}
	})

	refusals := []struct {
		name string
		in   string
		is   error
	}{
		{"empty", "", ErrPhoneRequired},
		{"whitespace", "   ", ErrPhoneRequired},
		{"letters", "call me", ErrPhoneInvalid},
		{"no country", "1234567890", ErrPhoneAmbiguous},
	}
	for _, c := range refusals {
		t.Run(c.name, func(t *testing.T) {
			got, err := RequirePhone(c.in)
			if err == nil {
				t.Fatalf("RequirePhone(%q) accepted it and returned %q", c.in, got)
			}
			// Both must hold: ErrValidation is what the handler switches
			// on to answer 400, and the specific cause is what the
			// merchant is shown.
			if !errors.Is(err, ErrValidation) {
				t.Fatalf("RequirePhone(%q) error %v does not wrap ErrValidation — the handler would answer 500, not 400", c.in, err)
			}
			if !errors.Is(err, c.is) {
				t.Fatalf("RequirePhone(%q) error %v, want it to wrap %v", c.in, err, c.is)
			}
		})
	}
}

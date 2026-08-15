package service

import (
	"errors"
	"fmt"
	"strings"
)

// Phone numbers are stored canonically, in E.164 (`+255785659580`).
//
// This exists because in this market the same number genuinely gets
// entered four different ways — `0785 659 580`, `255785659580`,
// `+255-785-659-580`, `785659580` — and every one of them is the same
// merchant. Storing them verbatim means the phone can never be used as
// a key, and §10.9 puts it on a Selcom payload where a stray space is a
// rejected checkout.
//
// **This is a faithful port of `NormalisePhone` in
// `drs-setup-service/service/phone.go`, which is the fleet's original
// and is the reference.** An earlier version of this file claimed to be
// the reference implementation for P2's customer-phone dedupe; that was
// wrong — drs-setup already had one, so there were two normalisers that
// disagreed (`07856595800` and `12345` were accepted here and rejected
// there). They agree now, deliberately, and the two services cannot
// share a package because they are separate Go modules. If either
// changes, change both.
//
// The default region is Tanzania. That is not a neutral choice, it is
// this product's market. It only ever applies to a number written with
// a trunk zero or with no prefix at all; anything written with `+` or
// `00` keeps the country the author gave it, so a Kenyan merchant's
// `+254…` is stored exactly as meant.
const defaultCallingCode = "255"

// ErrPhoneRequired is returned when no phone was supplied at all.
//
// Phone became required at signup on 2026-08-15 (owner, amending §10.9).
// The reason is not tidiness: it is what allows `DEFOLT_CLIENT_PHONE`
// and the rest of the `DEFOLT_CLIENT_*` globals to be deleted rather
// than kept as a permanent fallback. A global constant standing in for
// a per-merchant field is what put "UAT Demo Client" on real merchants'
// payment records.
var ErrPhoneRequired = errors.New("phone: a phone number is required")

// ErrPhoneInvalid is returned when the text cannot be read as a phone
// number at all.
var ErrPhoneInvalid = errors.New("phone: not a usable number")

// ErrPhoneAmbiguous is returned when the digits are plausible but the
// country is a guess. Better to ask for the `+` than to store a number
// that dials the wrong country.
var ErrPhoneAmbiguous = errors.New("phone: country is unclear, write it with a + and the country code")

// NormalisePhone turns what a person typed into E.164, or explains why
// it cannot. An empty (or whitespace-only) input is not an error here:
// it is how an optional number is cleared, and it returns "". Callers
// that require a phone check for "" themselves — see RequirePhone.
func NormalisePhone(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}

	// Keep a leading + and drop every other decoration people write:
	// spaces, dashes, dots, brackets, and the non-breaking space that
	// arrives when a number is pasted out of a spreadsheet.
	plus := strings.HasPrefix(s, "+")
	var digits strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	n := digits.String()
	if n == "" {
		return "", ErrPhoneInvalid
	}

	// `00` is the other way of writing `+`, and it is what an older
	// handset produces.
	if !plus && strings.HasPrefix(n, "00") {
		plus = true
		n = n[2:]
	}

	switch {
	case plus:
		// Already international. Nothing to infer.
	case strings.HasPrefix(n, "0"):
		// A trunk zero is local dialling, so the local country applies.
		n = defaultCallingCode + strings.TrimLeft(n, "0")
	case strings.HasPrefix(n, defaultCallingCode) && len(n) == len(defaultCallingCode)+9:
		// Written internationally but without the +.
	case len(n) == 9:
		// A Tanzanian mobile written without its trunk zero.
		n = defaultCallingCode + n
	default:
		return "", ErrPhoneAmbiguous
	}

	// E.164 is at most 15 digits. The floor is lower than the ITU
	// minimum on purpose: short national numbers exist, and refusing a
	// real number is worse here than accepting a short one, which the
	// sender will reject anyway with a clearer message than this can
	// give.
	if len(n) < 8 || len(n) > 15 {
		return "", ErrPhoneInvalid
	}
	if strings.HasPrefix(n, "0") {
		// No country code starts with zero, so this is a trunk prefix
		// that survived, which means the input was malformed.
		return "", ErrPhoneInvalid
	}
	return "+" + n, nil
}

// RequirePhone is NormalisePhone for the paths where a phone is
// mandatory — signup and tenant creation. It folds every refusal into
// ErrValidation so the handler answers 400 with the reason in the
// detail, the same shape ErrSlugInvalid already uses.
//
// The refusal text is written to be read by the merchant, not by us:
// "country is unclear" tells them what to type next, where "invalid
// phone" does not.
func RequirePhone(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("%w: %w", ErrValidation, ErrPhoneRequired)
	}
	p, err := NormalisePhone(raw)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrValidation, err)
	}
	if p == "" {
		// Unreachable given the guard above, but a normaliser that
		// silently returned "" would otherwise write an empty phone onto
		// a row the caller believes is populated.
		return "", fmt.Errorf("%w: %w", ErrValidation, ErrPhoneRequired)
	}
	return p, nil
}

package service

import "strings"

// NormalizePhone puts a Tanzanian mobile number into the single E.164
// form `+255XXXXXXXXX`.
//
// This exists because in this market the same number genuinely gets
// entered four different ways — `0785 659 580`, `255785659580`,
// `+255-785-659-580`, `785659580` — and every one of them is the same
// merchant. Storing them verbatim means the phone can never be used as
// a key, and §10.9 wants it on a Selcom payload where a stray space is
// a rejected checkout.
//
// It is deliberately lenient: anything it cannot recognise is returned
// trimmed rather than rejected. A phone that fails to normalise must
// never stop a signup or a checkout (§0.2 rule 3) — the worst case is
// that billing falls back to the global buyer identity, which is
// exactly what the fallback is for.
//
// This is the reference implementation for P2's customer-phone dedupe
// (§3.2 item 1). When P2 lands, use this shape rather than writing a
// second one — two normalisers that disagree are worse than none.
func NormalizePhone(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	// Keep digits only; remember a leading '+' so `+255…` and `255…`
	// converge. Separators (space, hyphen, parentheses, dots) carry no
	// information here.
	var digits strings.Builder
	for _, r := range trimmed {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	d := digits.String()
	if d == "" {
		return trimmed
	}

	switch {
	// 255785659580 — already the country code, 12 digits total.
	case len(d) == 12 && strings.HasPrefix(d, "255"):
		return "+" + d
	// 0785659580 — the local trunk form, 10 digits.
	case len(d) == 10 && strings.HasPrefix(d, "0"):
		return "+255" + d[1:]
	// 785659580 — bare subscriber number, 9 digits, no trunk zero.
	case len(d) == 9:
		return "+255" + d
	// 00255785659580 — the international access prefix.
	case len(d) == 14 && strings.HasPrefix(d, "00255"):
		return "+" + d[2:]
	}

	// Not a shape we recognise — a non-TZ number, or a typo. Preserve
	// what the merchant typed, with a '+' if they meant one, and let the
	// human who reads it decide. Refusing here would cost a signup.
	if strings.HasPrefix(trimmed, "+") {
		return "+" + d
	}
	return trimmed
}

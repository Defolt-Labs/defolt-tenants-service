package service

import (
	"errors"
	"fmt"
	"strings"
)

// defaultCallingCode is the default country calling code for local numbers.
const defaultCallingCode = "255"

// ErrPhoneRequired indicates an empty phone was provided where required.
var ErrPhoneRequired = errors.New("phone: a phone number is required")

// ErrPhoneInvalid indicates the value cannot be parsed as a phone number.
var ErrPhoneInvalid = errors.New("phone: not a usable number")

// ErrPhoneAmbiguous indicates international prefix is needed to identify country.
var ErrPhoneAmbiguous = errors.New("phone: country is unclear, write it with a + and the country code")

// NormalisePhone converts phone numbers to E.164 format.
func NormalisePhone(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}

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

	if !plus && strings.HasPrefix(n, "00") {
		plus = true
		n = n[2:]
	}

	switch {
	case plus:
	case strings.HasPrefix(n, "0"):
		n = defaultCallingCode + strings.TrimLeft(n, "0")
	case strings.HasPrefix(n, defaultCallingCode) && len(n) == len(defaultCallingCode)+9:
	case len(n) == 9:
		n = defaultCallingCode + n
	default:
		return "", ErrPhoneAmbiguous
	}

	if len(n) < 8 || len(n) > 15 {
		return "", ErrPhoneInvalid
	}
	if strings.HasPrefix(n, "0") {
		return "", ErrPhoneInvalid
	}
	return "+" + n, nil
}

// RequirePhone normalises phone number and returns validation error on invalid input.
func RequirePhone(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("%w: %w", ErrValidation, ErrPhoneRequired)
	}
	p, err := NormalisePhone(raw)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrValidation, err)
	}
	if p == "" {
		return "", fmt.Errorf("%w: %w", ErrValidation, ErrPhoneRequired)
	}
	return p, nil
}

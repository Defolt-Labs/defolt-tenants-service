package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTurnstileBlankSecret(t *testing.T) {
	cases := []struct {
		env     string
		secret  string
		refused bool
	}{
		{"development", "", false},
		{"development", "   ", false},
		{"staging", "", true},
		{"production", "  ", true},
		{"", "", true},
	}
	for _, tc := range cases {
		err := NewTurnstile(tc.secret, tc.env).Verify(context.Background(), "tok", "")
		if !tc.refused {
			if err != nil {
				t.Errorf("env %q secret %q: err %v, want nil", tc.env, tc.secret, err)
			}
			continue
		}
		var te *TurnstileError
		if !errors.Is(err, ErrTurnstileUnavailable) || !errors.As(err, &te) || len(te.Codes) != 1 || te.Codes[0] != "secret-not-configured" {
			t.Errorf("env %q secret %q: err %v, want unavailable with secret-not-configured", tc.env, tc.secret, err)
		}
	}
}

func TestTurnstileSetSecretStillChecksEverywhere(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":false,"error-codes":["invalid-input-response"]}`))
	}))
	defer srv.Close()
	for _, env := range []string{"development", "production"} {
		err := NewTurnstile("real", env).WithVerifyURL(srv.URL).Verify(context.Background(), "bad", "")
		if !errors.Is(err, ErrTurnstile) {
			t.Errorf("env %s: err %v, want ErrTurnstile", env, err)
		}
	}
}

package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"defolt-tenants-service/middleware"
	"defolt-tenants-service/response"
	"defolt-tenants-service/service"

	"github.com/gin-gonic/gin"
)

// WP-SIGNUP1. The public signup answered "Forbidden." for a refused human
// check, and "Request could not be validated." for every body it could not
// take. These tests hold each refusal to its own code, a sentence in en and
// sw, and the request id in the body and the header.

const signupRID = "hts-signup1-0f1e2d3c"

type signupAnswer struct {
	Code      string          `json:"code"`
	Meta      response.Meta   `json:"meta"`
	Details   json.RawMessage `json:"details"`
	RequestID string          `json:"request_id"`
}

// cloudflare serves a canned siteverify answer; a nil body closes the
// connection, which is Cloudflare being unreachable.
func cloudflare(t *testing.T, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if body == "" {
			hj, _ := w.(http.Hijacker)
			conn, _, _ := hj.Hijack()
			conn.Close()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func signupRoute(ts *service.Turnstile) *gin.Engine {
	gin.SetMode(gin.TestMode)
	svc := service.New(nil, nil, nil, nil, nil, []string{"reserved-clinic"}, "")
	h := New(svc, ts, nil, "test")
	r := gin.New()
	r.Use(middleware.RequestID())
	r.POST("/api/v1/public/signup", h.PublicSignup)
	return r
}

func goodSignup() map[string]any {
	return map[string]any{
		"slug": "hts-probe-clinic", "name": "HTS Probe Clinic",
		"contact_email": "owner@example.com", "phone": "+255712345678",
		"first_name": "Abel", "last_name": "Sekwala",
		"turnstile_token": "a-token", "product": "health",
	}
}

func postSignup(t *testing.T, r *gin.Engine, raw []byte) (int, signupAnswer, http.Header) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/public/signup", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(middleware.RequestIDHeader, signupRID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var a signupAnswer
	if err := json.Unmarshal(w.Body.Bytes(), &a); err != nil {
		t.Fatalf("answer is not an envelope: %v: %s", err, w.Body.String())
	}
	return w.Code, a, w.Header()
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// assertSentence checks the two things every refusal owes: a sentence in both
// languages that is not the bare status word, and the caller's request id in
// the body and the header.
func assertSentence(t *testing.T, a signupAnswer, h http.Header) {
	t.Helper()
	if a.Meta.EN == "" || a.Meta.SW == "" {
		t.Fatalf("%s: a sentence is missing: %+v", a.Code, a.Meta)
	}
	if a.Meta.EN == "Forbidden." || a.Code == response.ErrForbidden.Code {
		t.Fatalf("the signup answered the bare %q again", a.Meta.EN)
	}
	if strings.ContainsRune(a.Meta.EN+a.Meta.SW, '\u2014') {
		t.Fatalf("%s: an em dash in the copy", a.Code)
	}
	if a.RequestID != signupRID {
		t.Fatalf("%s: body request_id = %q, want %q", a.Code, a.RequestID, signupRID)
	}
	if got := h.Get(middleware.RequestIDHeader); got != signupRID {
		t.Fatalf("%s: X-Request-ID header = %q, want %q", a.Code, got, signupRID)
	}
}

func TestSignupRefusalsNameTheirReason(t *testing.T) {
	refused := service.NewTurnstile("real-secret").WithVerifyURL(
		cloudflare(t, `{"success":false,"error-codes":["invalid-input-response"]}`))
	badSecret := service.NewTurnstile("real-secret").WithVerifyURL(
		cloudflare(t, `{"success":false,"error-codes":["invalid-input-secret"]}`))
	unreachable := service.NewTurnstile("real-secret").WithVerifyURL(cloudflare(t, ""))
	passes := service.NewTurnstile("") // disabled: the request reaches the service checks

	with := func(k string, v any) []byte {
		b := goodSignup()
		b[k] = v
		return mustJSON(t, b)
	}
	without := func(k string) []byte {
		b := goodSignup()
		delete(b, k)
		return mustJSON(t, b)
	}

	cases := []struct {
		name   string
		ts     *service.Turnstile
		body   []byte
		status int
		code   string
		field  string
		reason string
	}{
		{"a refused token", refused, mustJSON(t, goodSignup()), 403, "DL_SIGNUP_HUMAN_CHECK_FAILED", "turnstile_token", "invalid-input-response"},
		{"our secret refused", badSecret, mustJSON(t, goodSignup()), 503, "DL_SIGNUP_HUMAN_CHECK_UNAVAILABLE", "turnstile_token", "invalid-input-secret"},
		{"cloudflare unreachable", unreachable, mustJSON(t, goodSignup()), 503, "DL_SIGNUP_HUMAN_CHECK_UNAVAILABLE", "turnstile_token", ""},
		{"no phone", passes, without("phone"), 400, "DL_SIGNUP_FIELD_REQUIRED", "phone", ""},
		{"no email", passes, without("contact_email"), 400, "DL_SIGNUP_FIELD_REQUIRED", "contact_email", ""},
		{"a blank first name", passes, with("first_name", "   "), 400, "DL_SIGNUP_FIELD_REQUIRED", "first_name", ""},
		{"a malformed email", passes, with("contact_email", "not-an-address"), 400, "DL_SIGNUP_EMAIL_INVALID", "contact_email", ""},
		{"an unreadable body", passes, []byte(`{"slug":`), 400, "DL_SIGNUP_BODY_UNREADABLE", "", ""},
		{"a malformed slug", passes, with("slug", "!!bad!!"), 400, "DL_TENANT_SLUG_INVALID", "slug", ""},
		{"a reserved slug", passes, with("slug", "reserved-clinic"), 409, "DL_TENANT_SLUG_RESERVED", "slug", ""},
		{"an unusable phone", passes, with("phone", "+12345"), 400, "DL_SIGNUP_PHONE_INVALID", "phone", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, a, h := postSignup(t, signupRoute(tc.ts), tc.body)
			if status != tc.status || a.Code != tc.code {
				t.Fatalf("answered %d %s (%s), want %d %s", status, a.Code, a.Meta.EN, tc.status, tc.code)
			}
			assertSentence(t, a, h)
			if tc.field != "" {
				var d signupFieldDetails
				_ = json.Unmarshal(a.Details, &d)
				if d.Field != tc.field {
					t.Fatalf("details.field = %q, want %q", d.Field, tc.field)
				}
				if tc.reason != "" && d.Reason != tc.reason {
					t.Fatalf("details.reason = %q, want %q", d.Reason, tc.reason)
				}
			}
		})
	}
}

// TestSignupRefusalsTheServiceDecides covers the refusals that need a database
// to reach through the route (slug taken is a unique-index violation), by
// handing the mapper the error the service returns.
func TestSignupRefusalsTheServiceDecides(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{service.ErrSlugTaken, 409, "DL_TENANT_SLUG_TAKEN"},
		{service.ErrPhoneAmbiguous, 400, "DL_SIGNUP_PHONE_COUNTRY_UNCLEAR"},
		{service.ErrPhoneRequired, 400, "DL_SIGNUP_FIELD_REQUIRED"},
	}
	for _, tc := range cases {
		r := gin.New()
		r.Use(middleware.RequestID())
		r.POST("/x", func(c *gin.Context) { writeSignupError(c, tc.err) })
		req := httptest.NewRequest(http.MethodPost, "/x", nil)
		req.Header.Set(middleware.RequestIDHeader, signupRID)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		var a signupAnswer
		_ = json.Unmarshal(w.Body.Bytes(), &a)
		if w.Code != tc.status || a.Code != tc.code {
			t.Fatalf("%v answered %d %s, want %d %s", tc.err, w.Code, a.Code, tc.status, tc.code)
		}
		assertSentence(t, a, w.Header())
	}
}

// TestTheCreatedAnswerCarriesTheRequestID holds the success half: the 201 is
// built by hand for idempotent replay, outside the writer, so it needs its own
// assertion.
func TestTheCreatedAnswerCarriesTheRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := New(nil, nil, nil, "test")
	r := gin.New()
	r.Use(middleware.RequestID())
	r.POST("/x", func(c *gin.Context) {
		h.respondCreatedIdempotent(c, response.OKTenantCreated.Code, response.OKTenantCreated.Meta, gin.H{"slug": "s"})
	})
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.Header.Set(middleware.RequestIDHeader, signupRID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var a signupAnswer
	_ = json.Unmarshal(w.Body.Bytes(), &a)
	if w.Code != 201 || a.RequestID != signupRID || w.Header().Get(middleware.RequestIDHeader) != signupRID {
		t.Fatalf("201 answer: status %d body request_id %q header %q", w.Code, a.RequestID, w.Header().Get(middleware.RequestIDHeader))
	}
}

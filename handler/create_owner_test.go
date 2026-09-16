package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"defolt-tenants-service/middleware"
	"defolt-tenants-service/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// TestCreateInputCarriesTheOwnerUserID asserts the one mapping that dropped it.
// The field was on the request struct and on the tenant model, and the
// createBody -> CreateInput step in between did not carry it.
func TestCreateInputCarriesTheOwnerUserID(t *testing.T) {
	owner := "22470793-0b1c-4d2e-8f3a-5c6d7e8f9a0b"
	in, err := createInputFrom(createBody{
		Slug: "rani-dental-clinic", Name: "Rani Dental Clinic",
		ContactEmail: "owner@example.com", Phone: "+255712345678",
		Product: "health", OwnerUserID: owner, OwnerEmail: "owner@example.com",
		OwnerFirstName: "Abel", OwnerLastName: "Sekwala",
	})
	if err != nil {
		t.Fatalf("createInputFrom: %v", err)
	}
	if in.OwnerUserID == nil {
		t.Fatalf("owner_user_id dropped between the body and the create input; the tenant is created ownerless")
	}
	if in.OwnerUserID.String() != owner {
		t.Fatalf("owner_user_id = %s, want %s", in.OwnerUserID, owner)
	}
	// The three names and the owner email travel with it, so the event that
	// announces the tenant can name the person rather than the clinic.
	if in.OwnerEmail != "owner@example.com" || in.OwnerFirstName != "Abel" || in.OwnerLastName != "Sekwala" {
		t.Fatalf("the owner's other fields were dropped: %+v", in)
	}

	// A body that names no owner leaves it nil rather than a zero UUID.
	if bare, err := createInputFrom(createBody{Slug: "s", Name: "n"}); err != nil || bare.OwnerUserID != nil {
		t.Fatalf("owner_user_id = %v (err %v) for a body that named none, want nil", bare.OwnerUserID, err)
	}

	// And a malformed one is refused rather than silently dropped: the caller
	// must not be left believing it named an owner who does not exist.
	if _, err := createInputFrom(createBody{Slug: "s", Name: "n", OwnerUserID: "not-a-uuid"}); err == nil {
		t.Fatalf("a malformed owner_user_id was accepted")
	}
}

// newCreateRoute wires the real route with a service whose repository is never
// reached. Every case below is decided before the insert: the owner refusal
// answers in the handler, and the control case is steered onto a reserved slug
// so the service refuses it without a database. That is the point of the
// control case — it proves the request got PAST the owner guard.
func newCreateRoute() *gin.Engine {
	gin.SetMode(gin.TestMode)
	svc := service.New(nil, nil, nil, nil, nil, []string{"reserved-clinic"}, "")
	h := New(svc, nil, nil, "test")
	r := gin.New()
	r.Use(middleware.RequestID())
	r.POST("/api/v1/tenants", h.Create)
	return r
}

func postCreate(t *testing.T, body map[string]any) (int, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tenants", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(middleware.RequestIDHeader, "req-0f1e2d3c-4b5a-6978")
	w := httptest.NewRecorder()
	newCreateRoute().ServeHTTP(w, req)
	var env map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal envelope (%d %s): %v", w.Code, w.Body.String(), err)
	}
	return w.Code, env
}

// TestHealthTenantWithNoOwnerIsRefused is the refusal on the real route, in the
// house envelope, naming which of two things is wrong.
func TestHealthTenantWithNoOwnerIsRefused(t *testing.T) {
	// The slug is a RESERVED one on purpose. It means the only thing that can
	// answer 400 DL_TENANT_OWNER_REQUIRED is the owner guard firing BEFORE the
	// service looks at anything else; without the guard this request is a 409
	// on the slug instead, which is a visibly different answer.
	status, env := postCreate(t, map[string]any{
		"slug": "reserved-clinic", "name": "Rani Dental Clinic",
		"contact_email": "owner@example.com", "phone": "+255712345678",
		"product": "health",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; an ownerless health tenant must not be created", status)
	}
	if env["code"] != "DL_TENANT_OWNER_REQUIRED" {
		t.Fatalf("code = %v, want DL_TENANT_OWNER_REQUIRED", env["code"])
	}
	meta, _ := env["meta"].(map[string]any)
	if meta == nil || meta["en"] == "" || meta["sw"] == "" {
		t.Fatalf("the refusal must carry both locales: %v", env["meta"])
	}
	// Both halves, in both locales: the product that demands an owner, and
	// the field that was not sent. A refusal that says only "validation
	// failed" costs the caller a guess it should not have to make.
	for locale, want := range map[string][]string{
		"en": {"health", "owner_user_id"},
		"sw": {"afya", "owner_user_id"},
	} {
		got, _ := meta[locale].(string)
		for _, needle := range want {
			if !bytes.Contains([]byte(got), []byte(needle)) {
				t.Fatalf("meta.%s = %q, must name %q", locale, got, needle)
			}
		}
	}
}

// TestHealthTenantWithAnOwnerGetsPastTheGuard is the other side of it. The
// reserved slug is what the service refuses on, which can only be reached if
// the owner guard let the request through.
func TestHealthTenantWithAnOwnerGetsPastTheGuard(t *testing.T) {
	status, env := postCreate(t, map[string]any{
		"slug": "reserved-clinic", "name": "Rani Dental Clinic",
		"contact_email": "owner@example.com", "phone": "+255712345678",
		"product": "health", "owner_user_id": uuid.New().String(),
	})
	if env["code"] == "DL_TENANT_OWNER_REQUIRED" {
		t.Fatalf("a health tenant that named its owner was refused for having none")
	}
	if status != http.StatusConflict || env["code"] != "DL_TENANT_SLUG_RESERVED" {
		t.Fatalf("status/code = %d/%v, want 409/DL_TENANT_SLUG_RESERVED — the request must reach the service", status, env["code"])
	}
}

// TestRetailTenantWithNoOwnerIsNotTightened pins the scope of the round. A drs
// tenant with no owner reaches the service exactly as it always has.
func TestRetailTenantWithNoOwnerIsNotTightened(t *testing.T) {
	status, env := postCreate(t, map[string]any{
		"slug": "reserved-clinic", "name": "A Shop",
		"contact_email": "owner@example.com", "phone": "+255712345678",
		"product": "drs",
	})
	if env["code"] == "DL_TENANT_OWNER_REQUIRED" {
		t.Fatalf("a retail tenant with no owner was refused; this round does not tighten that")
	}
	if status != http.StatusConflict || env["code"] != "DL_TENANT_SLUG_RESERVED" {
		t.Fatalf("status/code = %d/%v, want 409/DL_TENANT_SLUG_RESERVED", status, env["code"])
	}
}

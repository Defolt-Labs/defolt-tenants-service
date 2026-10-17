package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"defolt-tenants-service/middleware"
	"defolt-tenants-service/response"
	"defolt-tenants-service/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

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
	if in.OwnerEmail != "owner@example.com" || in.OwnerFirstName != "Abel" || in.OwnerLastName != "Sekwala" {
		t.Fatalf("the owner's other fields were dropped: %+v", in)
	}

	if bare, err := createInputFrom(createBody{Slug: "s", Name: "n"}); err != nil || bare.OwnerUserID != nil {
		t.Fatalf("owner_user_id = %v (err %v) for a body that named none, want nil", bare.OwnerUserID, err)
	}

	if _, err := createInputFrom(createBody{Slug: "s", Name: "n", OwnerUserID: "not-a-uuid"}); err == nil {
		t.Fatalf("a malformed owner_user_id was accepted")
	}
}

func newCreateRoute() *gin.Engine {
	gin.SetMode(gin.TestMode)
	svc := service.New(nil, nil, nil, nil, nil, []string{"reserved-clinic"}, "")
	h := New(svc, nil, nil, "test")
	r := gin.New()
	r.Use(middleware.RequestID())
	r.POST("/tenants/v1", h.Create)
	return r
}

func postCreate(t *testing.T, body map[string]any) (int, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/tenants/v1", bytes.NewReader(raw))
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

func TestHealthTenantWithNoOwnerIsRefused(t *testing.T) {
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

func TestTheCreatedAnswerCarriesTheRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := New(nil, nil, nil, "test")
	r := gin.New()
	r.Use(middleware.RequestID())
	r.POST("/x", func(c *gin.Context) {
		h.respondCreatedIdempotent(c, response.OKTenantCreated.Code, response.OKTenantCreated.Meta, gin.H{"slug": "s"})
	})
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	const rid = "test-rid-0f1e2d3c"
	req.Header.Set(middleware.RequestIDHeader, rid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var a struct {
		Code      string        `json:"code"`
		Meta      response.Meta `json:"meta"`
		RequestID string        `json:"request_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &a)
	if w.Code != 201 || a.RequestID != rid || w.Header().Get(middleware.RequestIDHeader) != rid {
		t.Fatalf("201 answer: status %d body request_id %q header %q", w.Code, a.RequestID, w.Header().Get(middleware.RequestIDHeader))
	}
}

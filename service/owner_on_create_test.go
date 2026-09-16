package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"defolt-tenants-service/middleware"
	"defolt-tenants-service/model"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// TestCreateCarriesTheOwnerOntoTheTenant is the P1 measured on production.
//
// The internal POST /tenants route accepted `owner_user_id`, the tenant model
// had the column, and the mapping between them dropped it. So the tenant was
// created ownerless, tenant.activated went out with no owner, and dhs-setup
// logged "activated with no owner_user_id; admin mirror NOT created" — a
// clinic that exists and that its own owner cannot sign into. Rani Dental
// Clinic went onto production DHS through this route on 2026-09-16.
//
// The assertion is on the ROW and on the READ shape, because those are the two
// places the field has to survive: the column it is written to and the JSON a
// caller reads back.
func TestCreateCarriesTheOwnerOntoTheTenant(t *testing.T) {
	owner := uuid.MustParse("22470793-0b1c-4d2e-8f3a-5c6d7e8f9a0b")
	in := CreateInput{
		Slug:         "rani-dental-clinic",
		Name:         "Rani Dental Clinic",
		ContactEmail: "owner@example.com",
		Phone:        "+255712345678",
		Product:      "health",
		OwnerUserID:  &owner,
		OwnerEmail:   "owner@example.com",
	}

	tn := newTenantFromCreate(in, in.Slug, in.Phone)
	if tn.OwnerUserID == nil {
		t.Fatalf("owner_user_id dropped by the mapper; the tenant is ownerless and no admin mirror is ever created")
	}
	if *tn.OwnerUserID != owner {
		t.Fatalf("owner_user_id = %v, want %v", *tn.OwnerUserID, owner)
	}

	// And it comes back on the read. model.Tenant is what GET /tenants/:id
	// answers with, so a field that is on the row and not on this shape is a
	// field the caller cannot confirm it set.
	raw, err := json.Marshal(tn)
	if err != nil {
		t.Fatalf("marshal tenant: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal tenant: %v", err)
	}
	if got["owner_user_id"] != owner.String() {
		t.Fatalf("owner_user_id on the read = %v, want %q", got["owner_user_id"], owner.String())
	}

	// A create that names no owner still leaves the column null rather than a
	// zero UUID. A nil owner and an all-zero owner are different bugs
	// downstream, and only one of them is honest.
	if bare := newTenantFromCreate(CreateInput{Slug: "s", Name: "n", ContactEmail: "e"}, "s", "p"); bare.OwnerUserID != nil {
		t.Fatalf("owner_user_id = %v for a create that named none, want nil", bare.OwnerUserID)
	}
}

// TestHealthTenantMustNameItsOwner covers the refusal. Which of two things is
// wrong has to be readable from the answer: WHICH product demands an owner,
// and that the request named none.
func TestHealthTenantMustNameItsOwner(t *testing.T) {
	owner := uuid.MustParse("22470793-0b1c-4d2e-8f3a-5c6d7e8f9a0b")
	zero := uuid.Nil
	for _, tc := range []struct {
		name    string
		product string
		owner   *uuid.UUID
		wantErr error
	}{
		{"health with no owner is refused", "health", nil, ErrOwnerRequired},
		{"health with an all-zero owner is refused too", "health", &zero, ErrOwnerRequired},
		{"health with an owner is allowed", "health", &owner, nil},
		// Not tightened in this round. A retail tenant has always been
		// creatable without an owner and support relies on that.
		{"drs with no owner keeps the behaviour it has", "drs", nil, nil},
		{"an unset product defaults to drs and is not tightened", "", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := RequireOwnerForProduct(tc.product, tc.owner); got != tc.wantErr {
				t.Fatalf("RequireOwnerForProduct(%q, %v) = %v, want %v", tc.product, tc.owner, got, tc.wantErr)
			}
		})
	}
}

// TestActivatedCarriesTheInboundRequestID is the third half of the same
// failure. dhs-setup's consumer lifts the request id off this event and puts
// it on its identity lookup; defolt-identity answers 400 DL_REQUEST_ID_REQUIRED
// to anything without one, so an event with no request id means every owner
// lookup is refused and the consumer names the facility admin from the event's
// own fallback parts instead.
//
// The id is taken from the INBOUND HTTP context and never minted here: a
// request id is born at the caller and a fabricated one looks like a trace and
// joins nothing.
func TestActivatedCarriesTheInboundRequestID(t *testing.T) {
	owner := uuid.MustParse("22470793-0b1c-4d2e-8f3a-5c6d7e8f9a0b")
	tn := &model.Tenant{
		ID: uuid.MustParse("47c58b5b-9369-4149-8845-eeaf71184768"),
		Slug: "rani-dental-clinic", Name: "Rani Dental Clinic",
		Product: "health", ContactEmail: "owner@example.com", OwnerUserID: &owner,
	}

	// The whole path, through the real middleware: a header arrives, the
	// middleware puts it on the request context, and the payload built from
	// that context carries it.
	gin.SetMode(gin.TestMode)
	const inbound = "req-0f1e2d3c-4b5a-6978"
	var got map[string]any
	r := gin.New()
	r.Use(middleware.RequestID())
	r.POST("/api/v1/internal/tenants/:id/activate", func(c *gin.Context) {
		got = activatedPayload(c.Request.Context(), tn)
		c.Status(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/tenants/"+tn.ID.String()+"/activate", nil)
	req.Header.Set(middleware.RequestIDHeader, inbound)
	r.ServeHTTP(httptest.NewRecorder(), req)

	if got == nil {
		t.Fatalf("the activate route never ran")
	}
	if got["request_id"] != inbound {
		t.Fatalf("request_id on tenant.activated = %v, want the inbound %q; identity 400s a lookup without one", got["request_id"], inbound)
	}

	// The field is present, and empty, when there genuinely is no id. An
	// absent key and an empty string take different branches in some JSON
	// libraries, and "this event carries no trace" is a fact worth sending.
	bare := activatedPayload(context.Background(), tn)
	rid, ok := bare["request_id"]
	if !ok {
		t.Fatalf("request_id missing from the payload entirely: %v", bare)
	}
	if rid != "" {
		t.Fatalf("request_id = %v for a context that carries none; nothing may mint one here", rid)
	}
}

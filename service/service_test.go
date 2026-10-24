package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"defolt-tenants-service/middleware"
	"defolt-tenants-service/model"
	"defolt-tenants-service/reqid"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func boolp(v bool) *bool { return &v }

func show(p *bool) string {
	if p == nil {
		return "null"
	}
	if *p {
		return "true"
	}
	return "false"
}

func activatedWire(t *testing.T, tn *model.Tenant) map[string]any {
	t.Helper()
	raw, err := json.Marshal(activatedPayload(context.Background(), tn))
	if err != nil {
		t.Fatalf("marshal tenant.activated: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal tenant.activated: %v", err)
	}
	return got
}

func TestActivatedPayloadRequiresOwnerUserID(t *testing.T) {
	ownerID := uuid.New()
	tenant := &model.Tenant{
		ID:             uuid.New(),
		Slug:           "health-clinic",
		Product:        "health",
		OwnerUserID:    &ownerID,
		OwnerEmail:     "owner@example.com",
		ContactEmail:   "contact@example.com",
		OwnerFirstName: "Jane",
		OwnerLastName:  "Doe",
	}

	payload := activatedPayload(context.Background(), tenant)
	if payload["owner_user_id"] == nil {
		t.Fatal("owner_user_id is nil in activatedPayload; dhs-setup will nak the activation")
	}
	gotID, ok := payload["owner_user_id"].(*uuid.UUID)
	if !ok || gotID == nil || *gotID != ownerID {
		t.Fatalf("owner_user_id = %v, want %v", payload["owner_user_id"], ownerID)
	}
}

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
		t.Fatalf("newTenantFromCreate dropped owner_user_id")
	}
	if *tn.OwnerUserID != owner {
		t.Fatalf("newTenantFromCreate owner_user_id = %v, want %v", *tn.OwnerUserID, owner)
	}

	raw, err := json.Marshal(tn)
	if err != nil {
		t.Fatalf("marshal tenant: %v", err)
	}
	var roundTrip map[string]any
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatalf("unmarshal tenant: %v", err)
	}
	if roundTrip["owner_user_id"] != owner.String() {
		t.Fatalf("JSON representation dropped owner_user_id: %s", raw)
	}
}

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

func TestActivatedCarriesTheInboundRequestID(t *testing.T) {
	owner := uuid.MustParse("22470793-0b1c-4d2e-8f3a-5c6d7e8f9a0b")
	tn := &model.Tenant{
		ID:           uuid.MustParse("47c58b5b-9369-4149-8845-eeaf71184768"),
		Slug:         "rani-dental-clinic",
		Name:         "Rani Dental Clinic",
		Product:      "health",
		ContactEmail: "owner@example.com",
		OwnerUserID:  &owner,
	}

	gin.SetMode(gin.TestMode)
	const inbound = "req-0f1e2d3c-4b5a-6978"
	var got map[string]any
	r := gin.New()
	r.Use(middleware.RequestID())
	r.POST("/tenants/v1/internal/tenants/:id/activate", func(c *gin.Context) {
		got = activatedPayload(c.Request.Context(), tn)
		c.Status(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodPost, "/tenants/v1/internal/tenants/"+tn.ID.String()+"/activate", nil)
	req.Header.Set(middleware.RequestIDHeader, inbound)
	r.ServeHTTP(httptest.NewRecorder(), req)

	if got == nil {
		t.Fatalf("the activate route never ran")
	}
	if got["request_id"] != inbound {
		t.Fatalf("request_id on tenant.activated = %v, want the inbound %q", got["request_id"], inbound)
	}

	bare := activatedPayload(context.Background(), tn)
	rid, ok := bare["request_id"]
	if !ok {
		t.Fatalf("request_id missing from payload: %v", bare)
	}
	if rid != "" {
		t.Fatalf("request_id = %v for a context that carries none", rid)
	}
}

func TestExploringIsALiveTenant(t *testing.T) {
	for _, tc := range []struct {
		subState string
		want     model.TenantStatus
	}{
		{"exploring", model.StatusActive},
		{"trial", model.StatusActive},
		{"active", model.StatusActive},
		{"awaiting_registration", model.StatusPendingPayment},
		{"grace", model.StatusGrace},
		{"suspended", model.StatusSuspended},
		{"cancelled", model.StatusSuspended},
		{"something_billing_invented_later", model.StatusSuspended},
		{"", model.StatusSuspended},
	} {
		if got := TenantStatusForSubscriptionState(tc.subState); got != tc.want {
			t.Errorf("subscription state %q maps to tenant status %q, want %q", tc.subState, got, tc.want)
		}
	}
}

func TestActivatedPayloadCarriesWhatTheConsumersNeed(t *testing.T) {
	owner := uuid.New()
	tn := &model.Tenant{
		ID:             uuid.New(),
		Slug:           "defolt-labs",
		Name:           "Defolt Labs",
		Product:        "health",
		ContactEmail:   "owner@example.com",
		OwnerUserID:    &owner,
		OwnerFirstName: "Rachel",
		OwnerLastName:  "Mhaville",
	}
	p := activatedPayload(context.Background(), tn)

	if p["owner_email"] != "owner@example.com" {
		t.Fatalf("owner_email = %v, want contact email fallback", p["owner_email"])
	}
	if p["owner_user_id"] != tn.OwnerUserID {
		t.Fatalf("owner_user_id missing: %v", p)
	}
	if p["name"] != "Defolt Labs" || p["slug"] != "defolt-labs" {
		t.Fatalf("name/slug missing: %v", p)
	}
	if _, ok := p["trial_ends_at"]; ok {
		t.Fatalf("trial_ends_at present for tenant with no trial: %v", p)
	}

	tn.OwnerEmail = "signed-up@example.com"
	if p := activatedPayload(context.Background(), tn); p["owner_email"] != "signed-up@example.com" {
		t.Fatalf("owner_email = %v, want signed-up email", p["owner_email"])
	}

	now := time.Now()
	end := now.Add(7 * 24 * time.Hour)
	tn.TrialStartsAt, tn.TrialEndsAt, tn.ActivatedAt = &now, &end, &now
	p = activatedPayload(context.Background(), tn)
	if p["trial_ends_at"] != end || p["activated_at"] != now {
		t.Fatalf("trial window missing: %v", p)
	}
}

func TestAbandonBlockedNeverDeletesOnAnUnansweredQuestion(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		body      string
		wantBlock bool
	}{
		{"exploring is a live tenant", 200, `{"data":{"state":"exploring"}}`, true},
		{"so is one on trial", 200, `{"data":{"state":"trial"}}`, true},
		{"and one in grace", 200, `{"data":{"state":"grace"}}`, true},
		{"awaiting_registration is abandoned", 200, `{"data":{"state":"awaiting_registration"}}`, false},
		{"no subscription allows delete", 404, `{}`, false},
		{"billing error blocks delete", 500, `boom`, true},
		{"nonsense body blocks delete", 200, `{"data":{}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			s := &TenantsService{billing: NewBillingClient(srv.URL, "k")}
			if got, why := s.abandonBlocked(context.Background(), uuid.New()); got != tc.wantBlock {
				t.Fatalf("blocked = %v (%s), want %v", got, why, tc.wantBlock)
			}
		})
	}
}

func TestSubscriptionStateDistinguishesGoneFromDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Service-Key") != "k" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c := NewBillingClient(srv.URL, "k")
	if _, err := c.SubscriptionState(context.Background(), uuid.New()); err != ErrNoSubscription {
		t.Fatalf("404 gave %v, want ErrNoSubscription", err)
	}
}

func TestActivatedCarriesOwnerExistedAlwaysAsABoolean(t *testing.T) {
	owner := uuid.New()
	for _, tc := range []struct {
		name    string
		existed *bool
		want    bool
	}{
		{"identity answered DL_USER_EXISTS", boolp(true), true},
		{"this signup created the identity", boolp(false), false},
		{"not known", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := activatedWire(t, &model.Tenant{
				ID:           uuid.New(),
				Slug:         "clinic",
				Product:      "health",
				OwnerUserID:  &owner,
				OwnerEmail:   "owner@example.com",
				OwnerExisted: tc.existed,
			})
			v, present := got["owner_existed"]
			if !present {
				t.Fatalf("tenant.activated has no owner_existed key")
			}
			b, isBool := v.(bool)
			if !isBool {
				t.Fatalf("owner_existed = %#v (%T), want a JSON boolean", v, v)
			}
			if b != tc.want {
				t.Fatalf("owner_existed = %v, want %v", b, tc.want)
			}
		})
	}
}

func TestSignupRecordsWhatIdentityAnswered(t *testing.T) {
	user := uuid.New()

	fresh := &model.Tenant{ID: uuid.New()}
	applyIdentityOwner(fresh, &user, false)
	if fresh.OwnerUserID == nil || *fresh.OwnerUserID != user {
		t.Fatalf("owner_user_id = %v, want %v", fresh.OwnerUserID, user)
	}
	if fresh.OwnerExisted == nil || *fresh.OwnerExisted {
		t.Fatalf("owner_existed = %s for newly created identity, want false", show(fresh.OwnerExisted))
	}
	if got := activatedWire(t, fresh)["owner_existed"]; got != false {
		t.Fatalf("wire owner_existed = %v, want false", got)
	}

	known := &model.Tenant{ID: uuid.New()}
	applyIdentityOwner(known, &user, true)
	if known.OwnerExisted == nil || !*known.OwnerExisted {
		t.Fatalf("owner_existed = %s for existing identity, want true", show(known.OwnerExisted))
	}
	if got := activatedWire(t, known)["owner_existed"]; got != true {
		t.Fatalf("wire owner_existed = %v, want true", got)
	}
}

func TestResumedSignupKeepsTheFirstAnswer(t *testing.T) {
	user := uuid.New()

	resumed := &model.Tenant{ID: uuid.New(), OwnerUserID: &user, OwnerExisted: boolp(false)}
	applyIdentityOwner(resumed, &user, true)
	if resumed.OwnerExisted == nil || *resumed.OwnerExisted {
		t.Fatalf("owner_existed = %s after resume, want false", show(resumed.OwnerExisted))
	}

	legacy := &model.Tenant{ID: uuid.New(), OwnerUserID: &user}
	applyIdentityOwner(legacy, &user, true)
	if legacy.OwnerExisted != nil {
		t.Fatalf("owner_existed = %v on pre-column resume, want nil", *legacy.OwnerExisted)
	}
	if got := activatedWire(t, legacy)["owner_existed"]; got != false {
		t.Fatalf("wire owner_existed = %v for unknown, want false", got)
	}

	failedFirst := &model.Tenant{ID: uuid.New()}
	applyIdentityOwner(failedFirst, &user, true)
	if failedFirst.OwnerExisted == nil || !*failedFirst.OwnerExisted {
		t.Fatalf("owner_existed = %s, want true", show(failedFirst.OwnerExisted))
	}
}

func TestCreateAttachingAnOwnerSaysItExisted(t *testing.T) {
	owner := uuid.New()
	in := CreateInput{Slug: "clinic", Name: "Clinic", ContactEmail: "o@example.com", Product: "health", OwnerUserID: &owner}
	tn := newTenantFromCreate(in, in.Slug, "+255712345678")
	if tn.OwnerExisted == nil || !*tn.OwnerExisted {
		t.Fatalf("owner_existed = %s for attached existing user, want true", show(tn.OwnerExisted))
	}
	if got := activatedWire(t, tn)["owner_existed"]; got != true {
		t.Fatalf("wire owner_existed = %v, want true", got)
	}

	bare := newTenantFromCreate(CreateInput{Slug: "s", Name: "n", ContactEmail: "e"}, "s", "p")
	if bare.OwnerExisted != nil {
		t.Fatalf("owner_existed = %v for create with no owner, want nil", *bare.OwnerExisted)
	}
	zero := uuid.Nil
	if z := newTenantFromCreate(CreateInput{Slug: "s", Name: "n", ContactEmail: "e", OwnerUserID: &zero}, "s", "p"); z.OwnerExisted != nil {
		t.Fatalf("owner_existed = %v for all-zero owner id, want nil", *z.OwnerExisted)
	}
}

func TestTenantCreatedCarriesTheNames(t *testing.T) {
	tenant := &model.Tenant{
		ID:              uuid.MustParse("47c58b5b-9369-4149-8845-eeaf71184768"),
		Slug:            "rani-dental-clinic",
		Name:            "Rani Dental Clinic",
		ContactEmail:    "defoltlabs@gmail.com",
		Product:         "health",
		Status:          model.StatusPendingPayment,
		OwnerFirstName:  "Abel",
		OwnerMiddleName: "",
		OwnerLastName:   "Sekwala",
	}

	raw, err := json.Marshal(tenantCreatedPayload(context.Background(), tenant))
	if err != nil {
		t.Fatalf("marshal tenant.created: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal tenant.created: %v", err)
	}

	if got["name"] != "Rani Dental Clinic" {
		t.Errorf("name: want %q, got %v", "Rani Dental Clinic", got["name"])
	}
	if got["owner_first_name"] != "Abel" {
		t.Errorf("owner_first_name: want %q, got %v", "Abel", got["owner_first_name"])
	}
	if got["owner_last_name"] != "Sekwala" {
		t.Errorf("owner_last_name: want %q, got %v", "Sekwala", got["owner_last_name"])
	}
	if _, ok := got["owner_middle_name"]; !ok {
		t.Error("owner_middle_name: key missing entirely")
	}

	for k, want := range map[string]any{
		"tenant_id":   "47c58b5b-9369-4149-8845-eeaf71184768",
		"slug":        "rani-dental-clinic",
		"owner_email": "defoltlabs@gmail.com",
		"product":     "health",
		"state":       "pending_payment",
	} {
		if got[k] != want {
			t.Errorf("%s: want %v, got %v", k, want, got[k])
		}
	}
}

func TestTenantCreatedCarriesTheInboundRequestID(t *testing.T) {
	tenant := &model.Tenant{ID: uuid.New(), Slug: "kariakoo-duka", Name: "Kariakoo Duka", Status: model.StatusPendingPayment}
	ctx := reqid.With(context.Background(), "req-created-1")

	if got := tenantCreatedPayload(ctx, tenant)["request_id"]; got != "req-created-1" {
		t.Fatalf("request_id on tenant.created = %v, want req-created-1", got)
	}
	if got, ok := tenantCreatedPayload(context.Background(), tenant)["request_id"]; !ok || got != "" {
		t.Fatalf("request_id = %v (present %v) for a context that carries none", got, ok)
	}
}

func TestTenantCreatedSurvivesAnOwnerlessTenant(t *testing.T) {
	got := tenantCreatedPayload(context.Background(), &model.Tenant{
		ID:     uuid.New(),
		Slug:   "kariakoo-duka",
		Name:   "Kariakoo Duka",
		Status: model.StatusPendingPayment,
	})
	for _, k := range []string{
		"tenant_id", "slug", "name", "owner_email", "product", "state",
		"owner_first_name", "owner_middle_name", "owner_last_name", "request_id",
	} {
		if _, ok := got[k]; !ok {
			t.Errorf("%s: key missing on ownerless tenant", k)
		}
	}
	if got["name"] != "Kariakoo Duka" {
		t.Errorf("name: want %q, got %v", "Kariakoo Duka", got["name"])
	}
}

func TestApplyUpdateNamesOnlyTheFieldsTheRequestSet(t *testing.T) {
	name, plan, phone := "New Name", "premium", "0712345678"
	cases := []struct {
		name string
		in   UpdateInput
		want []string
	}{
		{"name only", UpdateInput{Name: &name}, []string{"name"}},
		{"plan only", UpdateInput{Plan: &plan}, []string{"plan"}},
		{"phone only", UpdateInput{Phone: &phone}, []string{"phone"}},
		{"name and plan", UpdateInput{Name: &name, Plan: &plan}, []string{"name", "plan"}},
		{"nothing", UpdateInput{}, nil},
	}
	for _, tc := range cases {
		tn := &model.Tenant{Name: "Old", Plan: "standard", Phone: "+255700000000", ContactEmail: "o@example.com"}
		got, err := applyUpdate(tn, tc.in)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: columns %v, want %v", tc.name, got, tc.want)
		}
		if tn.ContactEmail != "o@example.com" {
			t.Errorf("%s: contact_email changed", tc.name)
		}
	}
}

func TestApplyUpdateLeavesAnUnchangedPhoneOut(t *testing.T) {
	same := "+255700000000"
	got, err := applyUpdate(&model.Tenant{Phone: same}, UpdateInput{Phone: &same})
	if err != nil || len(got) != 0 {
		t.Fatalf("columns %v err %v, want none", got, err)
	}
}

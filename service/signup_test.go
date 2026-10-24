package service

import (
	"context"
	"defolt-tenants-service/model"
	"defolt-tenants-service/repository"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// Measured on UAT 2026-10-03,
// tenant 5fb30935: PublicSignup published tenant.created straight after the
// insert, billing seeded the subscription exploring off it and pushed the
// state back, and SyncSubscriptionState published tenant.activated from the
// row while the signup was still inside identity CreateUser, so the event had
// no owner_user_id and dhs-setup parked it. The activation payload test asserted
// only the payload, never the order, so it stayed green.
//
// This test records ONE ordered log across the repository, identity, the
// publisher and billing, and asserts where tenant.created sits in it.

// orderLog is the single ordered log every fake writes to.
type orderLog struct {
	mu      sync.Mutex
	entries []string
	created [][]byte // the bytes of every tenant.created published
}

func (l *orderLog) add(e string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, e)
}

func (l *orderLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.entries...)
}

func (l *orderLog) index(e string) int {
	for i, x := range l.snapshot() {
		if x == e {
			return i
		}
	}
	return -1
}

func (l *orderLog) count(e string) int {
	n := 0
	for _, x := range l.snapshot() {
		if x == e {
			n++
		}
	}
	return n
}

const (
	logInsert   = "insert"
	logIdentity = "identity CreateUser"
	logOwner    = "SaveOwner"
	logName     = "SaveName"
	logPhone    = "SavePhone"
	logCreated  = "publish tenant.created"
	logCheckout = "billing checkout"
)

// recordingRows is signupRows in memory. insertTaken makes Insert refuse the
// slug, and existing is then what FindBySlug answers (the resumed path).
type recordingRows struct {
	log         *orderLog
	insertTaken bool
	existing    *model.Tenant
	byID        map[uuid.UUID]model.Tenant
	inserted    *model.Tenant // a copy of the row as Insert stored it
}

func (r *recordingRows) Insert(_ context.Context, t *model.Tenant) error {
	r.log.add(logInsert)
	if r.insertTaken {
		return repository.ErrSlugTaken
	}
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	cp := *t
	r.inserted = &cp
	r.byID[t.ID] = cp
	return nil
}

func (r *recordingRows) FindByID(_ context.Context, id uuid.UUID) (*model.Tenant, error) {
	t, ok := r.byID[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return &t, nil
}

func (r *recordingRows) FindBySlug(_ context.Context, product, slug string) (*model.Tenant, error) {
	if r.existing == nil || r.existing.Product != product || r.existing.Slug != slug {
		return nil, repository.ErrNotFound
	}
	cp := *r.existing
	return &cp, nil
}

// SaveName and SavePhone write one column each into the stored row, as the repository does.
func (r *recordingRows) SaveName(_ context.Context, t *model.Tenant) error {
	r.log.add(logName)
	stored := r.byID[t.ID]
	stored.Name = t.Name
	r.byID[t.ID] = stored
	return nil
}

func (r *recordingRows) SavePhone(_ context.Context, t *model.Tenant) error {
	r.log.add(logPhone)
	stored := r.byID[t.ID]
	stored.Phone = t.Phone
	r.byID[t.ID] = stored
	return nil
}

func (r *recordingRows) SaveOwner(_ context.Context, t *model.Tenant) error {
	r.log.add(logOwner)
	r.byID[t.ID] = *t
	return nil
}

type orderRig struct {
	log  *orderLog
	rows *recordingRows
	svc  *TenantsService
}

// newOrderRig wires the service to the recorder, a publisher that logs, and
// httptest servers on the real identity and billing clients. identityStatus
// is what identity answers (201 or 500).
func newOrderRig(t *testing.T, identityStatus int) *orderRig {
	t.Helper()
	log := &orderLog{}
	rows := &recordingRows{log: log, byID: map[uuid.UUID]model.Tenant{}}

	identity := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/identity/v1/internal/admin/users" {
			http.NotFound(w, r)
			return
		}
		log.add(logIdentity)
		w.Header().Set("Content-Type", "application/json")
		if identityStatus >= 400 {
			w.WriteHeader(identityStatus)
			_, _ = w.Write([]byte(`{"success":false,"code":"DL_INTERNAL"}`))
			return
		}
		w.WriteHeader(identityStatus)
		_, _ = w.Write([]byte(`{"success":true,"code":"DL_USER_CREATED","data":{"id":"` + uuid.NewString() + `"}}`))
	}))
	t.Cleanup(identity.Close)

	billing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/checkout") {
			http.NotFound(w, r)
			return
		}
		log.add(logCheckout)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"invoice_id":"inv-1","amount_tzs":2000,"payment_url":"https://pay.example/checkout/1","reference":"ref-1"}}`))
	}))
	t.Cleanup(billing.Close)

	svc := &TenantsService{
		rows:     rows,
		identity: NewIdentityClient(identity.URL, "k", "test"),
		billing:  NewBillingClient(billing.URL, "k"),
		reserved: map[string]struct{}{},
	}
	svc.publish = func(subject string, data []byte) error {
		if subject == "tenant.created" {
			log.add(logCreated)
			log.mu.Lock()
			log.created = append(log.created, append([]byte(nil), data...))
			log.mu.Unlock()
		} else {
			log.add("publish " + subject)
		}
		return nil
	}
	return &orderRig{log: log, rows: rows, svc: svc}
}

func signupInput(product, slug string) SignupInput {
	return SignupInput{
		Slug:         slug,
		Name:         "Order Test Clinic",
		ContactEmail: "owner-order@example.com",
		Phone:        "+255797140800",
		FirstName:    "Asha",
		LastName:     "Mtest",
		Product:      product,
	}
}

// assertCreatedAfterOwnerBeforeCheckout is the row's whole claim.
func assertCreatedAfterOwnerBeforeCheckout(t *testing.T, l *orderLog) {
	t.Helper()
	got := l.snapshot()
	t.Logf("ordered log: %v", got)
	if n := l.count(logCreated); n != 1 {
		t.Fatalf("tenant.created published %d times, want exactly 1; log %v", n, got)
	}
	created := l.index(logCreated)
	for _, before := range []string{logInsert, logIdentity, logOwner} {
		if i := l.index(before); i < 0 || i > created {
			t.Errorf("tenant.created at position %d of %v, want it after %q (position %d)", created+1, got, before, i+1)
		}
	}
	if i := l.index(logCheckout); i < 0 || i < created {
		t.Errorf("tenant.created at position %d of %v, want it before %q (position %d)", created+1, got, logCheckout, i+1)
	}
}

// assertPayloadUnchanged decodes the published bytes and holds them to the
// payload tenantCreatedPayload builds today for the row as inserted.
func assertPayloadUnchanged(t *testing.T, l *orderLog, row *model.Tenant) {
	t.Helper()
	if len(l.created) != 1 {
		t.Fatalf("captured %d tenant.created payloads, want 1", len(l.created))
	}
	want, err := json.Marshal(tenantCreatedPayload(context.Background(), row))
	if err != nil {
		t.Fatal(err)
	}
	if string(l.created[0]) != string(want) {
		t.Errorf("tenant.created bytes changed:\n got %s\nwant %s", l.created[0], want)
	}
	var gotMap map[string]any
	if err := json.Unmarshal(l.created[0], &gotMap); err != nil {
		t.Fatalf("published bytes are not JSON: %v", err)
	}
	keys := make([]string, 0, len(gotMap))
	for k := range gotMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	wantKeys := []string{"name", "owner_email", "owner_first_name", "owner_last_name", "owner_middle_name", "product", "request_id", "slug", "state", "tenant_id"}
	if !reflect.DeepEqual(keys, wantKeys) {
		t.Errorf("tenant.created keys = %v, want %v", keys, wantKeys)
	}
}

// (a) A health signup.
func TestHealthSignupPublishesTenantCreatedAfterOwnerSaved(t *testing.T) {
	rig := newOrderRig(t, http.StatusCreated)
	out, err := rig.svc.PublicSignup(context.Background(), signupInput("health", "order-health-1"), NewTurnstile(""))
	if err != nil {
		t.Fatalf("PublicSignup: %v", err)
	}
	if out.Tenant == nil || out.Tenant.OwnerUserID == nil {
		t.Fatalf("signup answered no owner_user_id: %+v", out.Tenant)
	}
	assertCreatedAfterOwnerBeforeCheckout(t, rig.log)
	assertPayloadUnchanged(t, rig.log, rig.rows.inserted)
}

// (b) DRS gets the same order.
func TestDRSSignupPublishesTenantCreatedAfterOwnerSaved(t *testing.T) {
	rig := newOrderRig(t, http.StatusCreated)
	if _, err := rig.svc.PublicSignup(context.Background(), signupInput("drs", "order-drs-1"), NewTurnstile("")); err != nil {
		t.Fatalf("PublicSignup: %v", err)
	}
	assertCreatedAfterOwnerBeforeCheckout(t, rig.log)
	assertPayloadUnchanged(t, rig.log, rig.rows.inserted)
}

// (c) Identity answers 500: the row has no owner, and tenant.created still
// goes out once, after the SaveOwner attempt and before the checkout.
func TestIdentityFailureStillPublishesOnceAfterSaveOwner(t *testing.T) {
	rig := newOrderRig(t, http.StatusInternalServerError)
	out, err := rig.svc.PublicSignup(context.Background(), signupInput("health", "order-idfail-1"), NewTurnstile(""))
	if err != nil {
		t.Fatalf("PublicSignup: %v", err)
	}
	if out.Tenant != nil && out.Tenant.OwnerUserID != nil {
		t.Fatalf("identity failed but the row carries owner_user_id %v", out.Tenant.OwnerUserID)
	}
	assertCreatedAfterOwnerBeforeCheckout(t, rig.log)
	assertPayloadUnchanged(t, rig.log, rig.rows.inserted)
}

// (d) A resumed signup: the slug is taken by the same email, still
// pending_payment. tenant.created is published once, after the owner.
func TestResumedSignupPublishesOnce(t *testing.T) {
	rig := newOrderRig(t, http.StatusCreated)
	existing := &model.Tenant{
		ID:             uuid.New(),
		Slug:           "order-resume-1",
		Name:           "Order Test Clinic",
		ContactEmail:   "owner-order@example.com",
		Phone:          "+255797140800",
		Product:        "health",
		Plan:           "standard",
		Status:         model.StatusPendingPayment,
		OwnerFirstName: "Asha",
		OwnerLastName:  "Mtest",
	}
	rig.rows.insertTaken = true
	rig.rows.existing = existing
	rig.rows.byID[existing.ID] = *existing

	out, err := rig.svc.PublicSignup(context.Background(), signupInput("health", "order-resume-1"), NewTurnstile(""))
	if err != nil {
		t.Fatalf("PublicSignup (resume): %v", err)
	}
	if out.Tenant == nil || out.Tenant.ID != existing.ID {
		t.Fatalf("resume answered another tenant: %+v", out.Tenant)
	}
	assertCreatedAfterOwnerBeforeCheckout(t, rig.log)
	assertPayloadUnchanged(t, rig.log, existing)
}

// A resumed signup that renames the clinic and changes the phone writes those two columns only: a plan or
// contact email changed on the stored row since the signup read it survives.
func TestResumedSignupWritesOnlyTheNameAndPhone(t *testing.T) {
	rig := newOrderRig(t, http.StatusCreated)
	read := &model.Tenant{
		ID: uuid.New(), Slug: "order-resume-2", Name: "Old Name", ContactEmail: "owner-order@example.com",
		Phone: "+255797140801", Product: "health", Plan: "standard", Status: model.StatusPendingPayment,
	}
	stored := *read
	stored.Plan = "premium"
	rig.rows.insertTaken = true
	rig.rows.existing = read
	rig.rows.byID[read.ID] = stored

	if _, err := rig.svc.resumeStalledSignup(context.Background(), signupInput("health", "order-resume-2")); err != nil {
		t.Fatalf("resumeStalledSignup: %v", err)
	}
	if rig.log.count(logName) != 1 || rig.log.count(logPhone) != 1 {
		t.Fatalf("log = %v, want one SaveName and one SavePhone", rig.log.snapshot())
	}
	got := rig.rows.byID[read.ID]
	if got.Name != "Order Test Clinic" || got.Phone != "+255797140800" {
		t.Fatalf("name %q phone %q, want the signup's values", got.Name, got.Phone)
	}
	if got.Plan != "premium" {
		t.Fatalf("plan = %q, want the stored premium to survive", got.Plan)
	}
}

// (e) Control: the internal POST /tenants route publishes tenant.created
// immediately after the insert, once, as it always has.
func TestInternalCreatePublishesImmediately(t *testing.T) {
	rig := newOrderRig(t, http.StatusCreated)
	if _, err := rig.svc.Create(context.Background(), CreateInput{
		Slug:         "order-internal-1",
		Name:         "Order Test Clinic",
		ContactEmail: "owner-order@example.com",
		Phone:        "+255797140800",
		Product:      "drs",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got := rig.log.snapshot()
	t.Logf("ordered log: %v", got)
	want := []string{logInsert, logCreated}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("internal Create log = %v, want %v", got, want)
	}
	assertPayloadUnchanged(t, rig.log, rig.rows.inserted)
}

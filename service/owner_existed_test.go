package service

import (
	"context"
	"encoding/json"
	"testing"

	"defolt-tenants-service/model"

	"github.com/google/uuid"
)

// WP-SIGNUP5. tenant.activated must say whether the owner's identity existed
// before the tenant was made, so dhs-setup can stop putting a
// must-change-password flag on a DRS shop owner's own password. These tests
// pin the producer half: the answer comes from what identity said, it
// survives a resumed signup, and it is on the wire as a boolean every time.

func boolp(v bool) *bool { return &v }

// show prints a nullable flag as a reader would say it.
func show(p *bool) string {
	if p == nil {
		return "null"
	}
	if *p {
		return "true"
	}
	return "false"
}

// activatedWire is the payload as a consumer reads it: JSON over NATS.
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

func TestActivatedCarriesOwnerExistedAlwaysAsABoolean(t *testing.T) {
	owner := uuid.New()
	for _, tc := range []struct {
		name    string
		existed *bool
		want    bool
	}{
		{"identity answered DL_USER_EXISTS", boolp(true), true},
		{"this signup created the identity", boolp(false), false},
		// A row older than the column, or an ownerless tenant. False is what
		// every consumer assumed before the key existed, so an unknown never
		// switches a must-change-password flag off.
		{"not known", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := activatedWire(t, &model.Tenant{
				ID: uuid.New(), Slug: "clinic", Product: "health",
				OwnerUserID: &owner, OwnerEmail: "owner@example.com",
				OwnerExisted: tc.existed,
			})
			v, present := got["owner_existed"]
			if !present {
				t.Fatalf("tenant.activated has no owner_existed key; dhs-setup cannot tell a DRS owner from a new one")
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

	// A brand-new address: identity created the account, the signup handed
	// out a one-time password, the owner must change it.
	fresh := &model.Tenant{ID: uuid.New()}
	applyIdentityOwner(fresh, &user, false)
	if fresh.OwnerUserID == nil || *fresh.OwnerUserID != user {
		t.Fatalf("owner_user_id = %v, want %v", fresh.OwnerUserID, user)
	}
	if fresh.OwnerExisted == nil || *fresh.OwnerExisted {
		t.Fatalf("owner_existed = %s for an identity this signup created, want false", show(fresh.OwnerExisted))
	}
	if got := activatedWire(t, fresh)["owner_existed"]; got != false {
		t.Fatalf("wire owner_existed = %v, want false", got)
	}

	// An address that already had a Defolt account: identity kept the
	// password and answered DL_USER_EXISTS.
	known := &model.Tenant{ID: uuid.New()}
	applyIdentityOwner(known, &user, true)
	if known.OwnerExisted == nil || !*known.OwnerExisted {
		t.Fatalf("owner_existed = %s for an identity that already existed, want true", show(known.OwnerExisted))
	}
	if got := activatedWire(t, known)["owner_existed"]; got != true {
		t.Fatalf("wire owner_existed = %v, want true", got)
	}
}

// The trap: a signup abandoned at payment and resumed. The first attempt
// created the identity; the retry registers the same address and identity
// now answers DL_USER_EXISTS, because of the first attempt. The first answer
// has to stand, or a one-time password is published as a chosen one.
func TestResumedSignupKeepsTheFirstAnswer(t *testing.T) {
	user := uuid.New()

	resumed := &model.Tenant{ID: uuid.New(), OwnerUserID: &user, OwnerExisted: boolp(false)}
	applyIdentityOwner(resumed, &user, true)
	if resumed.OwnerExisted == nil || *resumed.OwnerExisted {
		t.Fatalf("owner_existed = %s after a resume, want false: the first attempt minted this account", show(resumed.OwnerExisted))
	}

	// A row made before the column, resumed after the deploy: the first
	// answer was never recorded, so it stays unknown and is sent as false.
	legacy := &model.Tenant{ID: uuid.New(), OwnerUserID: &user}
	applyIdentityOwner(legacy, &user, true)
	if legacy.OwnerExisted != nil {
		t.Fatalf("owner_existed = %v on a pre-column resume, want nil (unknown, not true)", *legacy.OwnerExisted)
	}
	if got := activatedWire(t, legacy)["owner_existed"]; got != false {
		t.Fatalf("wire owner_existed = %v for an unknown, want false", got)
	}

	// A first attempt whose identity call failed has no owner on the row, so
	// the retry's answer is the first one this row hears.
	failedFirst := &model.Tenant{ID: uuid.New()}
	applyIdentityOwner(failedFirst, &user, true)
	if failedFirst.OwnerExisted == nil || !*failedFirst.OwnerExisted {
		t.Fatalf("owner_existed = %s, want true from the only answer identity gave", show(failedFirst.OwnerExisted))
	}
}

// The internal POST /tenants route is given an owner_user_id the caller
// already holds, so the tenant is attached to an identity that existed.
func TestCreateAttachingAnOwnerSaysItExisted(t *testing.T) {
	owner := uuid.New()
	in := CreateInput{Slug: "clinic", Name: "Clinic", ContactEmail: "o@example.com", Product: "health", OwnerUserID: &owner}
	tn := newTenantFromCreate(in, in.Slug, "+255712345678")
	if tn.OwnerExisted == nil || !*tn.OwnerExisted {
		t.Fatalf("owner_existed = %s for a create that attached an existing user, want true", show(tn.OwnerExisted))
	}
	if got := activatedWire(t, tn)["owner_existed"]; got != true {
		t.Fatalf("wire owner_existed = %v, want true", got)
	}

	// No owner named (PublicSignup's own Create call, before identity has
	// answered): not known yet.
	bare := newTenantFromCreate(CreateInput{Slug: "s", Name: "n", ContactEmail: "e"}, "s", "p")
	if bare.OwnerExisted != nil {
		t.Fatalf("owner_existed = %v for a create with no owner, want nil", *bare.OwnerExisted)
	}
	zero := uuid.Nil
	if z := newTenantFromCreate(CreateInput{Slug: "s", Name: "n", ContactEmail: "e", OwnerUserID: &zero}, "s", "p"); z.OwnerExisted != nil {
		t.Fatalf("owner_existed = %v for an all-zero owner id, want nil", *z.OwnerExisted)
	}
}

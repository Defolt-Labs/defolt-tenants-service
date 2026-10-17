package service

import (
	"context"
	"defolt-tenants-service/model"
	"testing"

	"github.com/google/uuid"
)

// TestActivatedPayloadRequiresOwnerUserID ensures that an activated payload
// for a health tenant carries a non-nil owner_user_id.
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

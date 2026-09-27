package repository

import (
	"context"
	"strings"
	"testing"

	"defolt-tenants-service/model"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// WP-SIGNUP5. SaveOwner writes an explicit column list. The signup learns
// owner_existed from identity in the same call as owner_user_id, and
// tenant.activated is published later by another request that reads the
// row, so a column missing from this list is a flag that is silently never
// stored and always published as false. A dry run renders the SQL without a
// database; nothing connects. SkipDefaultTransaction is load-bearing: DryRun
// alone still opens the transaction gorm wraps an Updates in, and that dials.
func TestSaveOwnerWritesOwnerExisted(t *testing.T) {
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: "host=127.0.0.1 user=dryrun dbname=dryrun sslmode=disable"}),
		&gorm.Config{DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true})
	if err != nil {
		t.Fatalf("open dry-run gorm: %v", err)
	}
	owner := uuid.New()
	existed := true
	tn := &model.Tenant{ID: uuid.New(), OwnerUserID: &owner, OwnerEmail: "o@example.com", OwnerExisted: &existed}

	q := New(db).saveOwnerQuery(context.Background(), tn)
	if q.Error != nil {
		t.Fatalf("dry-run SaveOwner: %v", q.Error)
	}
	sql := q.Statement.SQL.String()
	t.Logf("SaveOwner SQL: %s", sql)
	if !strings.Contains(sql, `"owner_existed"`) {
		t.Fatalf("SaveOwner does not write owner_existed; SQL = %s", sql)
	}
	// And still writes only the owner's fields: a stale copy of the row must
	// not carry its status back (WP-SIGNUP2).
	if strings.Contains(sql, `"status"`) {
		t.Fatalf("SaveOwner writes status; SQL = %s", sql)
	}
}

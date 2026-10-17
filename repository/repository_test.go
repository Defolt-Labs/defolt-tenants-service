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
	if strings.Contains(sql, `"status"`) {
		t.Fatalf("SaveOwner writes status; SQL = %s", sql)
	}
}

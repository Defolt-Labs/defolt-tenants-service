package repository

import (
	"context"
	"strings"
	"testing"
	"time"

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

func TestSaveActivationWritesOnlyStatusAndActivatedAt(t *testing.T) {
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: "host=127.0.0.1 user=dryrun dbname=dryrun sslmode=disable"}),
		&gorm.Config{DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true})
	if err != nil {
		t.Fatalf("open dry-run gorm: %v", err)
	}
	now := time.Now()
	tn := &model.Tenant{ID: uuid.New(), Status: model.StatusActive, ActivatedAt: &now, OwnerEmail: "stale@example.com"}

	q := New(db).saveActivationQuery(context.Background(), tn)
	if q.Error != nil {
		t.Fatalf("dry-run SaveActivation: %v", q.Error)
	}
	sql := q.Statement.SQL.String()
	t.Logf("SaveActivation SQL: %s", sql)
	set := sql[strings.Index(sql, "SET"):strings.Index(sql, "WHERE")]
	for _, col := range []string{`"status"`, `"activated_at"`} {
		if !strings.Contains(set, col) {
			t.Fatalf("SaveActivation does not write %s; SQL = %s", col, sql)
		}
	}
	for _, col := range []string{"owner_user_id", "owner_email", "owner_first_name", "owner_existed", "plan", "name", "phone", "trial_"} {
		if strings.Contains(set, col) {
			t.Fatalf("SaveActivation writes %s; SQL = %s", col, sql)
		}
	}
}

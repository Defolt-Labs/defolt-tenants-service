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

// writtenColumns answers the SET clause of the statement a Save* method would run, without a database.
func writtenColumns(t *testing.T, build func(r *Repo, tn *model.Tenant) *gorm.DB) string {
	t.Helper()
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: "host=127.0.0.1 user=dryrun dbname=dryrun sslmode=disable"}),
		&gorm.Config{DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true})
	if err != nil {
		t.Fatalf("open dry-run gorm: %v", err)
	}
	owner := uuid.New()
	existed := true
	now := time.Now()
	tn := &model.Tenant{
		ID: uuid.New(), Slug: "s", Name: "n", ContactEmail: "c@example.com", Phone: "+255712345678", Plan: "standard",
		Status: model.StatusActive, OwnerUserID: &owner, OwnerEmail: "o@example.com", OwnerFirstName: "A",
		OwnerMiddleName: "B", OwnerLastName: "C", OwnerExisted: &existed, ActivatedAt: &now, TrialStartsAt: &now, TrialEndsAt: &now,
	}
	q := build(New(db), tn)
	if q.Error != nil {
		t.Fatalf("dry-run: %v", q.Error)
	}
	sql := q.Statement.SQL.String()
	t.Logf("SQL: %s", sql)
	return sql[strings.Index(sql, "SET"):strings.Index(sql, "WHERE")]
}

// assertOnlyColumns fails when the SET clause names a column outside want, or misses one in it.
func assertOnlyColumns(t *testing.T, set string, want ...string) {
	t.Helper()
	all := []string{"slug", "name", "contact_email", "phone", "currency", "timezone", "country_code", "product", "plan", "status",
		"owner_user_id", "owner_email", "owner_first_name", "owner_middle_name", "owner_last_name", "owner_existed",
		"activated_at", "trial_starts_at", "trial_ends_at", "created_at", "deleted_at"}
	wanted := map[string]bool{"updated_at": true}
	for _, c := range want {
		wanted[c] = true
		if !strings.Contains(set, `"`+c+`"`) {
			t.Errorf("does not write %s; SET = %s", c, set)
		}
	}
	for _, c := range all {
		if !wanted[c] && strings.Contains(set, `"`+c+`"`) {
			t.Errorf("writes %s, which the path does not change; SET = %s", c, set)
		}
	}
}

func TestSaveDetailsWritesOnlyTheNamedColumns(t *testing.T) {
	for _, cols := range [][]string{{"name"}, {"plan"}, {"phone", "contact_email"}, {"name", "contact_email", "phone", "plan"}} {
		set := writtenColumns(t, func(r *Repo, tn *model.Tenant) *gorm.DB { return r.saveDetailsQuery(context.Background(), tn, cols) })
		assertOnlyColumns(t, set, cols...)
	}
}

func TestSaveDetailsRefusesAColumnOutsideTheEditableSet(t *testing.T) {
	if checkEditColumns([]string{"name", "status"}) == nil {
		t.Fatal("status accepted as an edit column")
	}
	if checkEditColumns(nil) == nil {
		t.Fatal("an edit with no columns was accepted")
	}
	if err := checkEditColumns([]string{"name", "plan"}); err != nil {
		t.Fatalf("editable columns refused: %v", err)
	}
}

func TestSaveNameWritesOnlyName(t *testing.T) {
	set := writtenColumns(t, func(r *Repo, tn *model.Tenant) *gorm.DB { return r.saveNameQuery(context.Background(), tn) })
	assertOnlyColumns(t, set, "name")
}

func TestSavePhoneWritesOnlyPhone(t *testing.T) {
	set := writtenColumns(t, func(r *Repo, tn *model.Tenant) *gorm.DB { return r.savePhoneQuery(context.Background(), tn) })
	assertOnlyColumns(t, set, "phone")
}

func TestSaveOwnerLinkWritesOnlyTheOwnerIDAndEmail(t *testing.T) {
	set := writtenColumns(t, func(r *Repo, tn *model.Tenant) *gorm.DB { return r.saveOwnerLinkQuery(context.Background(), tn) })
	assertOnlyColumns(t, set, "owner_user_id", "owner_email")
}

func TestSaveRegistrationActivationWritesOnlyStatusAndTheTrialWindow(t *testing.T) {
	set := writtenColumns(t, func(r *Repo, tn *model.Tenant) *gorm.DB {
		return r.saveRegistrationActivationQuery(context.Background(), tn)
	})
	assertOnlyColumns(t, set, "status", "activated_at", "trial_starts_at", "trial_ends_at")
}

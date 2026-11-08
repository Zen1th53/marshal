package store

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestCommunityArchitectureReceiverColdReadDoesNotMigrateOrEnroll(t *testing.T) {
	ctx := t.Context()
	s := openEmptyTestStore(t)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if got := queryInt(t, s.db, "SELECT max(version) FROM schema_migrations"); got != LatestSchemaVersion {
		t.Fatalf("cold schema=%d, want %d", got, LatestSchemaVersion)
	}
	if got := queryInt(t, s.db, "SELECT count(*) FROM schema_migrations WHERE version BETWEEN 104 AND 107"); got != 4 {
		t.Fatalf("canonical receiver migrations=%d, want 4", got)
	}
	ledger := ledgerVersions(t, s)
	schema := queryInt(t, s.db, "PRAGMA schema_version")
	changes := queryInt(t, s.db, "SELECT total_changes()")
	// Migrate prepares the receiver schema. A cold read must never perform
	// further DDL, register an owner, or persist any acceptance authority.
	if _, err := s.ArchitectureAcceptanceFor(ctx, "PROJECT-cold"); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("cold receipt lookup did not deny: %v", err)
	}
	if got := ledgerVersions(t, s); !reflect.DeepEqual(got, ledger) {
		t.Fatalf("cold read changed migration ledger: got %v, want %v", got, ledger)
	}
	if got := queryInt(t, s.db, "PRAGMA schema_version"); got != schema {
		t.Fatalf("cold read changed schema: got %d, want %d", got, schema)
	}
	if got := queryInt(t, s.db, "SELECT total_changes()"); got != changes {
		t.Fatalf("cold read wrote database: changes=%d, want %d", got, changes)
	}
	for _, table := range []string{"architecture_target_generations", "architecture_owner_namespaces", "architecture_owner_nonces", "architecture_accepted_revisions", "architecture_owner_audit", "architecture_fact_bindings"} {
		if got := queryInt(t, s.db, "SELECT count(*) FROM "+table); got != 0 {
			t.Fatalf("cold read populated %s: %d rows", table, got)
		}
	}
}

func TestCommunityArchitectureReceiverUnmigratedReadDoesNotWrite(t *testing.T) {
	ctx := t.Context()
	s := openEmptyTestStore(t)
	changes := queryInt(t, s.db, "SELECT total_changes()")
	if _, err := s.ArchitectureAcceptanceFor(ctx, "PROJECT-cold"); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("unmigrated receipt lookup did not deny: %v", err)
	}
	if got := queryInt(t, s.db, "SELECT count(*) FROM sqlite_master WHERE type='table'"); got != 0 {
		t.Fatalf("cold read created %d tables", got)
	}
	if got := queryInt(t, s.db, "SELECT total_changes()"); got != changes {
		t.Fatalf("cold read wrote database: changes=%d, want %d", got, changes)
	}
}

func TestCommunityArchitectureReceiptRequiresRegisteredOwnerBrowser(t *testing.T) {
	ctx := context.Background()
	s := openEmptyTestStore(t)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.MigrateArchitectureReceiver(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ArchitectureTargetGenerationFor(ctx, "PROJECT-unregistered"); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("unregistered issuer was not denied: %v", err)
	}
	if _, err := s.ArchitectureAcceptanceFor(ctx, "PROJECT-unregistered"); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("unregistered acceptance was not denied: %v", err)
	}
	if _, err := s.ImportObservedArchitectureReceipt(ctx, SignedArchitectureReceipt{}); err == nil {
		t.Fatal("empty browser receipt was accepted")
	}
}

func TestCommunityArchitectureMigrationPreservesLegacyHistoryWithoutBackfill(t *testing.T) {
	ctx := context.Background()
	s := openEmptyTestStore(t)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO projects(project_id,repository,default_branch,pack_version,created_at) VALUES('PROJECT-legacy','/tmp/legacy','main','6.0','2026-01-01')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO decisions(decision_id,project_id,owner_role,status,body,source,created_at,updated_at) VALUES('DEC-legacy','PROJECT-legacy','architect','accepted','historical decision','legacy','2026-01-01','2026-01-01')`); err != nil {
		t.Fatal(err)
	}
	if got := queryInt(t, s.db, "SELECT max(version) FROM schema_migrations"); got != LatestSchemaVersion {
		t.Fatalf("baseline schema=%d", got)
	}
	if err := s.MigrateArchitectureReceiver(ctx); err != nil {
		t.Fatal(err)
	}
	if got := queryInt(t, s.db, "SELECT count(*) FROM schema_migrations WHERE version BETWEEN 92 AND 96"); got != 0 {
		t.Fatalf("legacy divergent migrations recorded=%d", got)
	}
	ledger := ledgerVersions(t, s)
	for _, version := range registeredCanonicalVersions() {
		if !ledger[version] {
			t.Fatalf("canonical migration %d missing", version)
		}
	}
	for _, version := range []int{104, 105, 106, 107, 108} {
		if got := queryInt(t, s.db, "SELECT count(*) FROM schema_migrations WHERE version=?", version); got != 1 {
			t.Fatalf("schema version %d recorded %d times", version, got)
		}
	}
	if got := queryInt(t, s.db, "SELECT count(*) FROM decisions WHERE decision_id='DEC-legacy' AND body='historical decision'"); got != 1 {
		t.Fatalf("legacy history count=%d", got)
	}
	for _, table := range []string{"architecture_target_generations", "architecture_owner_namespaces", "architecture_accepted_revisions"} {
		if got := queryInt(t, s.db, "SELECT count(*) FROM "+table); got != 0 {
			t.Fatalf("%s backfilled %d rows", table, got)
		}
	}
}

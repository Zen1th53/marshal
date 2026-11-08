package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// insertOwnerAcceptance records a signed owner acceptance of an
// architecture revision for a project, as the owner tables hold it. The
// signature is not checked by the overview, which only reads.
func insertOwnerAcceptance(t *testing.T, s *Store, project string, n int, revision int64, digest, origin string, at time.Time) {
	t.Helper()
	ctx := t.Context()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT OR IGNORE INTO architecture_target_generations(project_id,generation,enrollment_id,enrollment) VALUES(?,1,?,x'7b7d')`, project, "enrol-"+project)
	nonce, receipt := fmt.Sprintf("nonce-%s-%d", project, n), fmt.Sprintf("rcpt-%s-%d", project, n)
	exec(`INSERT INTO architecture_owner_nonces(nonce,project_id,generation,payload_digest,expires_unix) VALUES(?,?,1,'pd',0)`, nonce, project)
	var r SignedArchitectureReceipt
	r.Challenge.Binding.ArchitectureID, r.Challenge.Binding.Revision, r.Challenge.Binding.Digest = "ARCH-1", revision, digest
	r.Challenge.Message = "accept architecture"
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO architecture_accepted_revisions(receipt_id,receipt_digest,project_id,generation,nonce,receipt) VALUES(?,?,?,1,?,?)`, receipt, "d-"+receipt, project, nonce, raw)
	exec(`INSERT INTO architecture_owner_audit(receipt_id,project_id,generation,message_id,session_id,origin,accepted_at) VALUES(?,?,1,'m','s',?,?)`, receipt, project, origin, at.Format(time.RFC3339Nano))
}

func TestArchitectureOverviewOwnerAcceptance(t *testing.T) {
	s := openTestStore(t)
	marshalTestProject(t, s)
	ctx := t.Context()
	if _, err := s.ArchitectureOverview(ctx, ""); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("no project: %v", err)
	}
	got, err := s.ArchitectureOverview(ctx, "p")
	if err != nil || got.Accepted != nil {
		t.Fatalf("before an acceptance: %+v %v", got, err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO projects(project_id,repository,default_branch,pack_version,created_at) VALUES('q','other/repo','main','v1','2026-11-03T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 2, 13, 38, 0, 0, time.UTC)
	insertOwnerAcceptance(t, s, "p", 1, 6, "d6", "owner_command", at.Add(-time.Hour))
	insertOwnerAcceptance(t, s, "p", 2, 7, "a3f1c9e2", "owner_browser", at)
	insertOwnerAcceptance(t, s, "q", 3, 9, "other", "owner_command", at)
	got, err = s.ArchitectureOverview(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if editionContract && got.Accepted != nil && got.Accepted.Record == ArchitectureRecordContract {
		t.Fatalf("a project contract without one: %+v", got.Accepted)
	}
	want := ArchitectureAccepted{Record: ArchitectureRecordOwner, ID: "ARCH-1", Revision: 7, Digest: "a3f1c9e2", By: "owner in the browser", Instruction: "accept architecture", At: at}
	if got.Accepted == nil || *got.Accepted != want {
		t.Fatalf("accepted %+v, want %+v", got.Accepted, want)
	}
	if got.Comparable || len(got.Changes) != 0 {
		t.Fatalf("owner acceptances are not compared: %+v", got)
	}
}

func TestArchitectureChanges(t *testing.T) {
	got, err := architectureChanges(`{"a":1,"b":{"x":[1, 2]},"c":"same"}`, `{"a":2,"b":{"x":[1,2]},"c":"same","d":true}`)
	if err != nil || !slices.Equal(got, []string{"a", "d"}) {
		t.Fatalf("changes %v %v", got, err)
	}
	if got, err := architectureChanges(`{"a":1}`, `{"a":1}`); err != nil || len(got) != 0 {
		t.Fatalf("same %v %v", got, err)
	}
	if _, err := architectureChanges(`[`, `{}`); err == nil {
		t.Fatal("a broken body compared")
	}
}

// The owner acceptance read walks the sequence newest first and stops at
// its limit, with no sort, and reaches its audit row by key.
func TestArchitectureOverviewQueryPlans(t *testing.T) {
	s := openTestStore(t)
	text := storeQueryPlan(t, s, `SELECT r.project_id, r.receipt, a.origin, a.accepted_at FROM architecture_accepted_revisions r
		JOIN architecture_owner_audit a ON a.receipt_id = r.receipt_id ORDER BY r.sequence DESC LIMIT 20`)
	if strings.Contains(text, "TEMP B-TREE") || strings.Contains(text, "json_extract") || !strings.Contains(text, "SEARCH a USING INDEX") {
		t.Errorf("owner acceptance read sorts, filters by JSON or scans the audit: %q", text)
	}
}

//go:build !governed

package store

import (
	"slices"
	"strings"
	"testing"
	"time"
)

const editionContract = true

func TestArchitectureOverviewContract(t *testing.T) {
	s := openTestStore(t)
	marshalTestProject(t, s)
	ctx := t.Context()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	at := time.Date(2026, 11, 2, 13, 38, 0, 0, time.UTC)
	revision := func(rev int64, digest, body, occurrence string) {
		t.Helper()
		exec(`INSERT INTO extreme_project_contract_revisions(project_id,contract_id,revision,digest,contract_json,acceptance_occurrence,created_at) VALUES('PROJECT-c','CONTRACT-1',?,?,?,?,?)`, rev, digest, body, occurrence, at.Add(time.Duration(rev)*time.Minute).Format(time.RFC3339Nano))
	}
	revision(6, "d6", `{"rules":["a"],"components":["tui"]}`, "DEC-6")
	revision(7, "a3f1c9e2", `{"rules":["a","b"],"components":["tui"]}`, "DEC-7")
	exec(`INSERT INTO extreme_project_contract_active(project_id,storage_project_id,contract_id,revision,digest,cas_version,bootstrap_state) VALUES('PROJECT-c','p','CONTRACT-1',7,'a3f1c9e2',2,'accepted')`)

	// Without the instruction tables the Marshal's decision accepted it.
	got, err := s.ArchitectureOverview(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	want := ArchitectureAccepted{Record: ArchitectureRecordContract, ID: "CONTRACT-1", Revision: 7, Digest: "a3f1c9e2", By: "Marshal", At: at.Add(7 * time.Minute)}
	if got.Accepted == nil || *got.Accepted != want || !got.Comparable || got.Current != 7 || len(got.Changes) != 0 {
		t.Fatalf("accepted %+v %+v", got.Accepted, got)
	}

	// The owner's "accept architecture" instruction that the decision
	// consumed names the owner.
	exec(projectContractInstructionSchema)
	exec(`INSERT INTO extreme_project_contract_instructions(project_id,instruction_ref,session_id,launch_id,instruction,proposal_occurrence,proposal_digest,contract_id,revision,contract_digest,inventory_digest,expected_version,message_at,binding_digest) VALUES('p','INS-7','s','l','accept architecture','PROP','pd','CONTRACT-1',7,'a3f1c9e2','inv',1,?,'bd')`, at.Format(time.RFC3339Nano))
	exec(`INSERT INTO extreme_project_contract_instruction_receipts(project_id,instruction_ref,decision_occurrence,contract_id,revision,contract_digest) VALUES('p','INS-7','DEC-7','CONTRACT-1',7,'a3f1c9e2')`)
	got, err = s.ArchitectureOverview(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if a := got.Accepted; a == nil || a.By != "owner" || a.Instruction != "accept architecture" || !a.At.Equal(at) {
		t.Fatalf("owner acceptance %+v", a)
	}
	// A contract read wins over an owner architecture acceptance.
	insertOwnerAcceptance(t, s, "p", 1, 3, "x", "owner_command", at)
	if got, err = s.ArchitectureOverview(ctx, "p"); err != nil || got.Accepted.Record != ArchitectureRecordContract {
		t.Fatalf("record %+v %v", got.Accepted, err)
	}

	// A stored revision newer than the accepted one is compared with it.
	revision(8, "d8", `{"rules":["a","b"],"components":["tui","broker"],"owner":"x"}`, "DEC-8")
	got, err = s.ArchitectureOverview(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if got.Accepted.Revision != 7 || got.Current != 8 || !slices.Equal(got.Changes, []string{"components", "owner"}) {
		t.Fatalf("changes %+v", got)
	}

	// Another project's storage id reads no contract.
	if got, err = s.ArchitectureOverview(ctx, "other"); err != nil || got.Accepted != nil {
		t.Fatalf("another project: %+v %v", got.Accepted, err)
	}
}

// The contract revisions and the owner instruction are read by key; the
// active pointer table holds one row per project.
func TestArchitectureOverviewContractQueryPlans(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.db.ExecContext(t.Context(), projectContractInstructionSchema); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`SELECT contract_json, acceptance_occurrence, created_at FROM extreme_project_contract_revisions WHERE project_id = 'c' AND contract_id = 'k' AND revision = 7`,
		`SELECT revision, contract_json FROM extreme_project_contract_revisions WHERE project_id = 'c' AND contract_id = 'k' ORDER BY revision DESC LIMIT 1`,
		`SELECT i.instruction, i.message_at FROM extreme_project_contract_instruction_receipts r JOIN extreme_project_contract_instructions i ON i.project_id = r.project_id AND i.instruction_ref = r.instruction_ref WHERE r.project_id = 'p' AND r.decision_occurrence = 'd'`,
	} {
		text := storeQueryPlan(t, s, q)
		if strings.Contains(text, "SCAN") || strings.Contains(text, "TEMP B-TREE") || strings.Contains(text, "json_extract") {
			t.Errorf("%s: plan %q does not read by key", q, text)
		}
	}
	text := storeQueryPlan(t, s, `SELECT project_id, contract_id, revision, digest FROM extreme_project_contract_active WHERE storage_project_id = 'p' AND bootstrap_state = 'accepted' LIMIT 1`)
	if strings.Contains(text, "TEMP B-TREE") || strings.Contains(text, "json_extract") {
		t.Errorf("active pointer read sorts or filters by JSON: %q", text)
	}
}

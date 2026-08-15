package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/policy"
	"github.com/Zen1th53/marshal/internal/policytest"
)

func TestPolicyTestRunPersistsAndReloadsExactBinding(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir()+"/state.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	p := policy.Policy{ID: "policy-1", Version: 1, Default: policy.EffectDeny}
	digest, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	run := policytest.TestRun{
		ID: "run-1", PolicyID: p.ID,
		Binding:        policy.PolicyBinding{Version: p.Version, Digest: digest, Generation: 7},
		TestFileDigest: digest,
		Cases:          []policytest.TestCaseResult{{ID: "case-1", Result: policytest.Result{Name: "case-1", Status: policytest.StatusPass}}},
	}
	if err := st.PutPolicyTestRun(ctx, run); err != nil {
		t.Fatalf("PutPolicyTestRun: %v", err)
	}
	var integrity string
	if err := st.db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity_check = %q, err=%v", integrity, err)
	}
	var foreignKeys int
	if err := st.db.QueryRowContext(ctx, "SELECT count(*) FROM pragma_foreign_key_check").Scan(&foreignKeys); err != nil || foreignKeys != 0 {
		t.Fatalf("foreign_key_check rows = %d, err=%v", foreignKeys, err)
	}
	secondRun := run
	secondRun.ID = "run-2"
	if err := st.PutPolicyTestRun(ctx, secondRun); err != nil {
		t.Fatalf("same content with distinct run ID: %v", err)
	}
	got, err := st.GetPolicyTestRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetPolicyTestRun: %v", err)
	}
	if got.PolicyID != run.PolicyID || got.Binding != run.Binding || got.TestFileDigest != run.TestFileDigest || len(got.Cases) != 1 || got.Cases[0].ID != "case-1" || got.Cases[0].Result.Status != policytest.StatusPass {
		t.Fatalf("round trip mismatch: got %#v", got)
	}

	if err := st.PutPolicyTestRun(ctx, run); err != nil {
		t.Fatalf("identical retry: %v", err)
	}
	conflict := run
	conflict.Cases = []policytest.TestCaseResult{{ID: "case-1", Result: policytest.Result{Name: "case-1", Status: policytest.StatusFail, Diff: "different"}}}
	if err := st.PutPolicyTestRun(ctx, conflict); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("conflicting retry error = %v, want conflict", err)
	}
}

func TestPolicyTestRunLoadIsDefensiveAndReopenable(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/state.db"
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	digest := testPolicyDigest(t)
	run := policytest.TestRun{ID: "run-reopen", PolicyID: "policy-1", Binding: policy.PolicyBinding{Version: 1, Digest: digest, Generation: 1}, TestFileDigest: digest, Cases: []policytest.TestCaseResult{{ID: "case-1", Result: policytest.Result{Name: "case-1", Status: policytest.StatusPass}}}}
	if err := first.PutPolicyTestRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	loaded, err := first.GetPolicyTestRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	loaded.Cases[0].Result.Status = policytest.StatusFail
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := second.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	reopened, err := second.GetPolicyTestRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Cases[0].Result.Status != policytest.StatusPass {
		t.Fatalf("loaded mutation changed durable result: %#v", reopened)
	}
}

func TestPolicyTestRunRejectsSecretBearingAndCorruptDurableValues(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	marker := "MARSHAL_TEST_SECRET_T49_A02_7f3a"
	digest := testPolicyDigest(t)
	run := policytest.TestRun{ID: marker + " ", PolicyID: "policy-1", Binding: policy.PolicyBinding{Version: 1, Digest: digest, Generation: 1}, TestFileDigest: digest, Cases: []policytest.TestCaseResult{{ID: "case-1", Result: policytest.Result{Name: "case-1", Status: policytest.StatusPass}}}}
	if err := st.PutPolicyTestRun(ctx, run); !errors.Is(err, model.ErrInvalid) || strings.Contains(err.Error(), marker) {
		t.Fatalf("secret-bearing error = %v", err)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM policy_test_runs"); got != 0 {
		t.Fatalf("rejected run rows = %d, want 0", got)
	}
	valid := run
	valid.ID = "run-corrupt"
	if err := st.PutPolicyTestRun(ctx, valid); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, "UPDATE policy_test_runs SET policy_digest = 'corrupt' WHERE run_id = ?", valid.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetPolicyTestRun(ctx, valid.ID); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("corrupt digest error = %v, want invalid", err)
	}
}

func TestPolicyTestRunTwoStoresObserveCanonicalTruth(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/state.db"
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := second.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	digest := testPolicyDigest(t)
	run := policytest.TestRun{ID: "run-two-store", PolicyID: "policy-1", Binding: policy.PolicyBinding{Version: 1, Digest: digest, Generation: 1}, TestFileDigest: digest, Cases: []policytest.TestCaseResult{{ID: "case-1", Result: policytest.Result{Name: "case-1", Status: policytest.StatusPass}}}}
	if err := first.PutPolicyTestRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if _, err := second.GetPolicyTestRun(ctx, run.ID); err != nil {
		t.Fatalf("second store did not observe durable run: %v", err)
	}
}

func testPolicyDigest(t *testing.T) policy.PolicyDigest {
	t.Helper()
	digest, err := (policy.Policy{ID: "policy-1", Version: 1, Default: policy.EffectDeny}).Digest()
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

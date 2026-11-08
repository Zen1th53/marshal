//go:build !governed && unix

package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Zen1th53/code-evidence/evidence"
	"github.com/Zen1th53/marshal/internal/codeevidence"
	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
)

const ceLedgerV1 = "package ledger\n\n// Balance returns the account balance in cents.\nfunc Balance(entries []int) int {\n\ttotal := 0\n\tfor _, e := range entries {\n\t\ttotal += e\n\t}\n\treturn total\n}\n"
const ceLedgerV2 = "package ledger\n\n// Balance returns the account balance in cents, ignoring refunds.\nfunc Balance(entries []int) int {\n\ttotal := 0\n\tfor _, e := range entries {\n\t\tif e > 0 {\n\t\t\ttotal += e\n\t\t}\n\t}\n\treturn total\n}\n"

// codeEvidenceRun is a two-task Marshal run over the ledger package with a
// fake governed provider:
//
//   - task "a" (worker B) rewrites ledger/ledger.go and is merged first;
//   - task "b" (worker A), independent, starts from the run base, asks
//     code_context about Balance, which records a receipt on
//     ledger/ledger.go, then writes ledger/notes.go only.
//
// It returns the service and a function that drives the run to b's hand-in.
func codeEvidenceRun(t *testing.T, settings string, currentEvidence ...bool) (*MarshalService, func() marshal.HandIn) {
	t.Helper()
	ctx := context.Background()
	s, repo := marshalFixture(t, 2)
	if err := os.MkdirAll(filepath.Join(repo, "ledger"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "ledger", "ledger.go"), []byte(ceLedgerV1), 0o600); err != nil {
		t.Fatal(err)
	}
	marshalGit(t, repo, "add", "-A")
	marshalGit(t, repo, "commit", "-m", "ledger")
	state := filepath.Join(t.TempDir(), ".marshal-extreme")
	s.codeEvidence = &codeevidence.Host{StateDir: state, Repository: repo}
	if settings != "" {
		if err := os.MkdirAll(s.codeEvidence.Dir(), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(s.codeEvidence.Dir(), codeevidence.SettingsFile), []byte(settings), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	draft := s.Model.(marshalFakeModel).draft
	for i := range draft.Tasks {
		draft.Tasks[i].Files = []string{"ledger"}
		draft.Tasks[i].Checks = []marshal.Check{{Command: "test -d ledger", Criteria: draft.Tasks[i].Criteria}}
		draft.Tasks[i].DependsOn = nil
		draft.Plan.Tasks[i].Paths = []string{"ledger"}
		draft.Plan.Tasks[i].DependsOn = nil
		draft.Plan.Checks[draft.Tasks[i].PlanTaskID] = []string{"test -d ledger"}
	}
	draft.Plan.Graph, _ = plan.BuildGraph(draft.Plan.Tasks)
	s.Model = marshalFakeModel{draft: draft, review: marshal.Review{Verdict: marshal.VerdictAccept, Reviewer: "marshal"}}
	recorded := make(chan error, 1)
	s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(runCtx context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		switch req.Task.PlanTaskID {
		case "b":
			// What the relay does for code_context on this task's binding.
			evidenceRoot := req.Worktree
			if len(currentEvidence) > 0 && currentEvidence[0] {
				run, _, err := s.load(runCtx, req.RunID)
				if err != nil {
					return nil, err
				}
				evidenceRoot = filepath.Join(s.Worktrees, integrationTaskID(req.RunID, run))
			}
			d, err := s.codeEvidence.Context(runCtx, evidenceRoot, req.Task.Files, "change how Balance handles refunds", "", "", 0)
			if err == nil {
				err = s.recordCodeEvidenceReceipt(runCtx, req.RunID, req.Task.PlanTaskID, "TASK-worker-a", d.Receipt.ID, "code_context")
			}
			recorded <- err
			if err != nil {
				return nil, err
			}
			return nil, os.WriteFile(filepath.Join(req.Worktree, "ledger", "notes.go"), []byte("package ledger\n\n// Notes is unrelated.\nconst Notes = 1\n"), 0o600)
		default:
			return nil, os.WriteFile(filepath.Join(req.Worktree, "ledger", "ledger.go"), []byte(ceLedgerV2), 0o600)
		}
	}}
	if _, err := s.StartPlanning(ctx, "run", "ledger work", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	return s, func() marshal.HandIn {
		t.Helper()
		// Task B changes ledger.go and merges...
		dA, err := s.Dispatch(ctx, "run", "a", "ignore refunds")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.CollectHandIn(ctx, "run", dA); err != nil {
			t.Fatal(err)
		}
		if v, err := s.Review(ctx, "run", "a", knownCharge()); err != nil || v != marshal.VerdictAccept {
			t.Fatalf("review a: %s %v", v, err)
		}
		if err := s.Merge(ctx, "run", "a"); err != nil {
			t.Fatal(err)
		}
		// ...while worker A, an independent task, works from the run base
		// (as in a parallel lane or a stale rework base) and takes its
		// evidence on ledger.go there...
		dB, err := s.Dispatch(ctx, "run", "b", "ledger notes")
		if err != nil {
			t.Fatal(err)
		}
		// ...then A hands in.
		h, err := s.CollectHandIn(ctx, "run", dB)
		if err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-recorded:
			if err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatalf("worker did not record evidence; hand-in: %+v run: %+v", h, mustLoad(t, s))
		}
		if h.BaseCommit == s.integrationHead(ctx, "run", mustLoad(t, s)) {
			t.Fatal("fixture: A must work from a base without B's change")
		}
		return h
	}
}

func mustLoad(t *testing.T, s *MarshalService) marshal.Run {
	t.Helper()
	run, _, err := s.load(context.Background(), "run")
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func lastEvidenceEvent(t *testing.T, s *MarshalService, task string) events.Event {
	t.Helper()
	history, err := s.Store.MarshalDecisions(context.Background(), "run")
	if err != nil {
		t.Fatal(err)
	}
	var found *events.Event
	for i := range history {
		if history[i].Type == events.EventTypeMarshalEvidenceValidated && history[i].TaskID == task {
			found = &history[i]
		}
	}
	if found == nil {
		t.Fatalf("no evidence validation event for %s", task)
	}
	return *found
}

// TestCodeEvidenceHandInReportsEvidenceChangedByMergedTask is the end-to-end
// check: worker A's evidence on ledger.go; task B changes ledger.go and
// merges; A's hand-in validation reports the evidence invalidated, names the
// file, records the audit event, adds the advisory review note and shows in
// the inspector counts. Advisory is the default: A is not returned.
func TestCodeEvidenceHandInReportsEvidenceChangedByMergedTask(t *testing.T) {
	ctx := context.Background()
	s, drive := codeEvidenceRun(t, "")
	h := drive()
	if !slices.Contains(h.FilesTouched, "ledger/notes.go") || slices.Contains(h.FilesTouched, "ledger/ledger.go") {
		t.Fatalf("worker A touched %v", h.FilesTouched)
	}
	e := lastEvidenceEvent(t, s, "b")
	if e.Data["phase"] != "hand-in" || e.Data["policy"] != codeevidence.OnInvalidatedAdvisory {
		t.Fatalf("event: %+v", e.Data)
	}
	if n, _ := e.Data["invalidated"].(float64); n < 1 {
		t.Fatalf("expected invalidated evidence, got %+v", e.Data)
	}
	paths, _ := e.Data["paths"].([]any)
	if len(paths) != 1 || paths[0] != "ledger/ledger.go" {
		t.Fatalf("paths: %+v", e.Data["paths"])
	}
	run, _, err := s.load(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	if st := run.Tasks[taskIndex(run, "b")].State; st != marshal.HandedIn {
		t.Fatalf("advisory policy changed the task state: %s", st)
	}
	if note := s.codeEvidenceReviewNote(ctx, "run", "b"); !strings.Contains(note, "advisory: code evidence hand-in:") || !strings.Contains(note, "ledger/ledger.go") {
		t.Fatalf("review note: %q", note)
	}
	if v, err := s.Review(ctx, "run", "b", knownCharge()); err != nil || v != marshal.VerdictAccept {
		t.Fatalf("review b: %s %v", v, err)
	}
	review, err := s.Store.GetMarshalReview(ctx, "run", "b", 1)
	if err != nil || !strings.Contains(strings.Join(review.Value.Reasons, "\n"), "advisory: code evidence hand-in:") {
		t.Fatalf("review reasons: %+v %v", review.Value.Reasons, err)
	}
	// Verification re-validates against the verified head (advisory).
	if err := s.Merge(ctx, "run", "b"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.VerifyMerged(ctx, "run", knownCharge()); err != nil {
		t.Fatal(err)
	}
	if v := lastEvidenceEvent(t, s, "b"); v.Data["phase"] != "verification" {
		t.Fatalf("verification event: %+v", v.Data)
	} else if n, _ := v.Data["invalidated"].(float64); n < 1 {
		t.Fatalf("verification: %+v", v.Data)
	}
	// The worker whose own evidence was untouched validates clean.
	if note := s.codeEvidenceReviewNote(ctx, "run", "a"); note != "" {
		t.Fatalf("task a has no receipts, note %q", note)
	}
}

// TestCodeEvidenceReturnPolicyReturnsTask: with on_invalidated=return the
// same run returns A for rework with a reason naming the changed file.
func TestCodeEvidenceReturnPolicyReturnsTask(t *testing.T) {
	ctx := context.Background()
	s, drive := codeEvidenceRun(t, `{"on_invalidated":"return"}`)
	drive()
	run, _, err := s.load(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	task := run.Tasks[taskIndex(run, "b")]
	if task.State != marshal.Returned {
		t.Fatalf("state %s", task.State)
	}
	attempt := 1 + task.EvidenceAttemptBase
	review, err := s.Store.GetMarshalReview(ctx, "run", "b", attempt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(review.Value.Reasons, "\n"), "code evidence: other merged work changed evidence this task relied on (ledger/ledger.go)") {
		t.Fatalf("reasons: %q", review.Value.Reasons)
	}
	if e := lastEvidenceEvent(t, s, "b"); e.Data["policy"] != codeevidence.OnInvalidatedReturn {
		t.Fatalf("event: %+v", e.Data)
	}

	// A new attempt reads current integration evidence; the first attempt's
	// stale receipt must no longer return the task.
	s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(runCtx context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		current := mustLoad(t, s)
		root := filepath.Join(s.Worktrees, integrationTaskID(req.RunID, current))
		d, err := s.codeEvidence.Context(runCtx, root, req.Task.Files, "Balance", "", "", 0)
		if err != nil {
			return nil, err
		}
		if err := s.recordCodeEvidenceReceipt(runCtx, req.RunID, "b", "TASK-rework", d.Receipt.ID, "code_context"); err != nil {
			return nil, err
		}
		return nil, os.WriteFile(filepath.Join(req.Worktree, "ledger", "notes.go"), []byte("package ledger\nconst Notes = 2\n"), 0600)
	}}
	dispatch, err := s.Dispatch(ctx, "run", "b", "read current evidence")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CollectHandIn(ctx, "run", dispatch); err != nil {
		t.Fatal(err)
	}
	current := mustLoad(t, s)
	if got := current.Tasks[taskIndex(current, "b")].State; got != marshal.HandedIn {
		t.Fatalf("fresh attempt returned by old evidence: %s", got)
	}
	if note := s.codeEvidenceReviewNote(ctx, "run", "b"); note != "" {
		t.Fatalf("stale attempt note: %s", note)
	}
}

// TestCodeEvidenceDisabledWithoutHost: without the Extreme host wiring the
// hooks do nothing (no events, no notes, no returns).
func TestCodeEvidenceDisabledWithoutHost(t *testing.T) {
	ctx := context.Background()
	s, _ := marshalFixture(t, 1)
	if err := s.codeEvidenceHandIn(ctx, "run", marshal.Run{}, marshal.Task{Mode: marshal.Governed}, marshal.HandIn{}); err != nil {
		t.Fatal(err)
	}
	if note := s.codeEvidenceReviewNote(ctx, "run", "a"); note != "" {
		t.Fatal(note)
	}
	s.codeEvidenceVerify(ctx, "run", marshal.Run{}, "")
	if got := withCodeEvidenceTask(ctx, s, "run", marshal.Task{PlanTaskID: "a"}, "brief"); codeEvidenceFrom(got) != nil {
		t.Fatal("binding without host")
	}
}

// TestCodeEvidenceScopeRequiresAdmittedBinding: the runtime serves code
// evidence only with the live admitted worker proof; a bare context, another
// task or another agent is denied, and receipts are task-bound.
func TestCodeEvidenceScopeRequiresAdmittedBinding(t *testing.T) {
	r := &Runtime{}
	if _, err := r.codeEvidenceScope(context.Background(), "agent", "TASK-1"); err == nil {
		t.Fatal("scope without proof")
	}
	proof := workerSourceReadProof{runtime: r, task: "TASK-1", agent: "agent", worktree: t.TempDir(), validate: func(context.Context) error { return nil }}
	ctx := context.WithValue(context.Background(), workerSourceReadKey{}, proof)
	for _, c := range []struct{ agent, task string }{{"other", "TASK-1"}, {"agent", "TASK-2"}} {
		if _, err := r.codeEvidenceScope(ctx, c.agent, c.task); err == nil {
			t.Fatalf("scope for %+v", c)
		}
	}
	if !ledgerAdd("TASK-ledger", "rcpt_0123456789abcdef0123456789abcdef") || ledgerAdd("TASK-ledger", "rcpt_0123456789abcdef0123456789abcdef") {
		t.Fatal("ledger add")
	}
	if !ledgerHas("TASK-ledger", "rcpt_0123456789abcdef0123456789abcdef") || !ledgerHas("TASK-ledger", "rcpt_01234567") || ledgerHas("TASK-ledger", "rcpt_") || ledgerHas("TASK-other", "rcpt_0123456789abcdef0123456789abcdef") {
		t.Fatal("ledger membership")
	}
}

func TestCodeEvidenceUncertainHandInReturnsTask(t *testing.T) {
	for _, failure := range []string{"missing_receipt", "store_error", "settings_error"} {
		t.Run(failure, func(t *testing.T) {
			s, drive := codeEvidenceRun(t, `{"on_invalidated":"return"}`, true)
			original := s.Drivers["worker"].(driver.Governed)
			s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(ctx context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
				records, err := original.Run(ctx, req)
				if err != nil || req.Task.PlanTaskID != "b" {
					return records, err
				}
				switch failure {
				case "missing_receipt":
					err = s.recordCodeEvidenceReceipt(ctx, req.RunID, "b", "TASK-worker-a", "rcpt_00000000000000000000000000000000", "code_context")
				case "store_error":
					// A file in place of the store directory makes the real host open fail.
					err = os.RemoveAll(s.codeEvidence.StoreDir())
					if err == nil {
						err = os.WriteFile(s.codeEvidence.StoreDir(), []byte("not a directory"), 0600)
					}
				case "settings_error":
					err = os.WriteFile(filepath.Join(s.codeEvidence.Dir(), codeevidence.SettingsFile), []byte(`{"on_invalidated":"reject"}`), 0600)
				}
				return records, err
			}}
			drive()
			task := mustLoad(t, s).Tasks[1]
			if task.State != marshal.Returned {
				t.Fatalf("uncertain evidence accepted: %s", task.State)
			}
			e := lastEvidenceEvent(t, s, "b")
			if e.Data["unknown"] == float64(0) && e.Data["settings_error"] == nil {
				t.Fatalf("uncertainty not recorded: %+v", e.Data)
			}
		})
	}
}

func TestCodeEvidenceUnknownAdvisoryAndCanceledValidation(t *testing.T) {
	ctx := context.Background()
	s, drive := codeEvidenceRun(t, "", true)
	drive()
	run := mustLoad(t, s)
	task := run.Tasks[taskIndex(run, "b")]
	if err := s.recordCodeEvidenceReceipt(ctx, "run", "b", "TASK-worker-a", "rcpt_00000000000000000000000000000000", "code_context"); err != nil {
		t.Fatal(err)
	}
	sum, block := s.validateCodeEvidence(ctx, "run", task, run.BaseCommit, nil, "hand-in")
	if block || sum.Unknown == 0 || !strings.Contains(s.codeEvidenceReviewNote(ctx, "run", "b"), "unknown") {
		t.Fatalf("unknown advisory lost: %+v block=%v", sum, block)
	}
	if err := os.WriteFile(filepath.Join(s.codeEvidence.Dir(), codeevidence.SettingsFile), []byte(`{"on_invalidated":"return"}`), 0600); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	sum, block = s.validateCodeEvidence(canceled, "run", task, run.BaseCommit, nil, "hand-in")
	if !block || sum.Unknown == 0 {
		t.Fatalf("canceled validation passed: %+v %v", sum, block)
	}
	if e := lastEvidenceEvent(t, s, "b"); e.Data["error"] == nil {
		t.Fatalf("cancellation not durable: %+v", e.Data)
	}
}

func TestCodeEvidencePushedLocatorWorkerOwnership(t *testing.T) {
	for _, push := range []string{"locator", "gated"} {
		t.Run(push, func(t *testing.T) {
			ctx := context.Background()
			repo := runtimeRepo(t)
			if _, err := Bootstrap(ctx, repo.Path()); err != nil {
				t.Fatal(err)
			}
			r, err := Open(ctx, repo.Path())
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			agent, err := r.RegisterAgent(ctx, RegisterAgentRequest{Name: "Code evidence worker", Role: model.RoleDeveloper, ModelProvider: "codex"})
			if err != nil {
				t.Fatal(err)
			}
			s, drive := codeEvidenceRun(t, fmt.Sprintf(`{"push":%q}`, push))
			original := s.Drivers["worker"].(driver.Governed)
			s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(runCtx context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
				if req.Task.PlanTaskID == "b" {
					bindingCtx := withCodeEvidenceTask(runCtx, s, req.RunID, req.Task, "Balance refunds")
					proof := workerSourceReadProof{runtime: r, task: "TASK-pushed", agent: agent.ID, provider: "codex", root: req.Worktree, worktree: req.Worktree, paths: req.Task.Files, evidence: codeEvidenceFrom(bindingCtx), validate: func(context.Context) error { return nil }}
					workerCtx := context.WithValue(bindingCtx, workerSourceReadKey{}, proof)
					text := r.codeEvidencePush(workerCtx, agent.ID, proof.task)
					if !strings.Contains(text, "Code evidence locator") {
						return nil, fmt.Errorf("no pushed locator: %q", text)
					}
					owned, err := s.codeEvidenceReceipts(runCtx, req.RunID, "b")
					if err != nil || len(owned) != 1 {
						return nil, fmt.Errorf("pushed receipts %v: %v", owned, err)
					}
					id := owned[0]
					// Durable events authorize the receipt without the process ledger.
					if _, err := r.CodeEvidenceExpand(workerCtx, agent.ID, proof.task, evidence.ExpandRequest{Receipt: id[:13], Select: "ledger/ledger.go"}); err != nil {
						return nil, err
					}
					if _, err := r.CodeEvidenceValidate(workerCtx, agent.ID, proof.task, []string{id[:13]}); err != nil {
						return nil, err
					}
					other := proof
					other.task = "TASK-other"
					otherCtx := context.WithValue(bindingCtx, workerSourceReadKey{}, other)
					if _, err := r.CodeEvidenceExpand(otherCtx, agent.ID, other.task, evidence.ExpandRequest{Receipt: id, Select: "ledger/ledger.go"}); err == nil {
						return nil, fmt.Errorf("another canonical task used pushed receipt")
					}
					for _, change := range []string{"run", "plan_task"} {
						other = proof
						copied := *proof.evidence
						if change == "run" {
							copied.runID = "another-run"
						} else {
							copied.planTaskID = "a"
						}
						other.evidence = &copied
						if _, err := r.CodeEvidenceValidate(context.WithValue(bindingCtx, workerSourceReadKey{}, other), agent.ID, other.task, []string{id}); err == nil {
							return nil, fmt.Errorf("another %s used pushed receipt", change)
						}
					}
					other = proof
					copied := *proof.evidence
					copied.attempt++
					other.evidence = &copied
					if _, err := r.CodeEvidenceValidate(context.WithValue(bindingCtx, workerSourceReadKey{}, other), agent.ID, other.task, []string{id}); err == nil {
						return nil, fmt.Errorf("another attempt used pushed receipt")
					}
				}
				return original.Run(runCtx, req)
			}}
			drive()
			if e := lastEvidenceEvent(t, s, "b"); e.Data["invalidated"] == float64(0) {
				t.Fatalf("pushed expanded evidence not checked at hand-in: %+v", e.Data)
			}
		})
	}
}

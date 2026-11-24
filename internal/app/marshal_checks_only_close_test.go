//go:build !governed

package app

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/statepath"
	"github.com/Zen1th53/marshal/internal/verification"
)

// checksOnlyRecorder counts approved check runs and the head each ran on.
type checksOnlyRecorder struct {
	mu    sync.Mutex
	heads []string
	fail  bool
}

func (c *checksOnlyRecorder) run(ctx context.Context, a, b, w, dir, command string) marshal.CommandRecord {
	c.mu.Lock()
	head, _ := gitMarshal(ctx, dir, "rev-parse", "HEAD")
	c.heads = append(c.heads, head)
	fail := c.fail
	c.mu.Unlock()
	if fail {
		return marshal.CommandRecord{Command: command, ExitCode: 1, Output: "forced failure\n"}
	}
	return fixtureCheckRunner(ctx, a, b, w, dir, command)
}

func (c *checksOnlyRecorder) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.heads)
}

// checksOnlyFixture is a one-task run whose approved review policy opts out
// of the final model check, with every verifier role unavailable.
func checksOnlyFixture(t *testing.T, accept marshal.AcceptanceMode, policy marshal.ReviewPolicy) (*MarshalService, string, string, *checksOnlyRecorder) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "state.db")
	s, repo := marshalFixture(t, 1, dbPath)
	ctx := t.Context()
	settings := marshal.DefaultSettings()
	settings.AcceptanceMode = accept
	settings.ReviewPolicy = policy
	if _, err := s.Store.SetMarshalSettings(ctx, s.ProjectID, settings, 0); err != nil {
		t.Fatal(err)
	}
	s.Verify, s.VerifierProvider, s.IndependentVerify = nil, nil, nil
	recorder := &checksOnlyRecorder{}
	s.GovernedCheck = recorder.run
	if _, err := s.StartPlanning(ctx, "run", "write file", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	return s, repo, dbPath, recorder
}

var finalModelOff = marshal.ReviewPolicy{FinalModel: marshal.FinalModelOff}

func countAttestations(t *testing.T, dbPath string) int {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM completion_attestations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func countRunEvents(t *testing.T, s *MarshalService, kind events.EventType) int {
	t.Helper()
	list, err := s.Store.MarshalDecisions(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range list {
		if e.Type == kind {
			n++
		}
	}
	return n
}

func assertChecksOnlyDelivered(t *testing.T, s *MarshalService, repo, dbPath string, recorder *checksOnlyRecorder, run marshal.Run) {
	t.Helper()
	if run.State != marshal.Closed || run.DeliveryKind != verification.DeliveredChecksOnly || run.ChecksOnly == nil || !run.ChecksOnly.Passed() {
		t.Fatalf("closed run = %s kind=%q receipt=%+v", run.State, run.DeliveryKind, run.ChecksOnly)
	}
	delivered := marshalGit(t, repo, "rev-parse", "refs/heads/main")
	if run.ChecksOnly.Head != delivered || run.ChecksOnly.TargetBefore != run.BaseCommit {
		t.Fatalf("receipt head %s before %s, delivered %s", run.ChecksOnly.Head, run.ChecksOnly.TargetBefore, delivered)
	}
	if recorder.count() == 0 || recorder.heads[len(recorder.heads)-1] != delivered {
		t.Fatalf("approved checks did not run on the delivered commit: %v, delivered %s", recorder.heads, delivered)
	}
	if got := countAttestations(t, dbPath); got != 0 {
		t.Fatalf("checks-only delivery minted %d canonical attestations", got)
	}
	if got := countRunEvents(t, s, events.EventTypeMarshalVerifierRecorded); got != 0 {
		t.Fatalf("checks-only delivery recorded %d verifier events", got)
	}
	list, err := s.Store.MarshalDecisions(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range list {
		if e.Type == events.EventTypeMarshalRunClosed {
			found = e.Data["delivery_kind"] == verification.DeliveredChecksOnly && e.Data["checks_only_receipt_digest"] == run.ChecksOnly.Digest
		}
	}
	if !found {
		t.Fatal("close event does not name the checks-only delivery and receipt")
	}
}

// Both finish branches deliver an opted-out run with every verifier role
// unavailable: the standing-authorization close on a current-runtime run, and
// VerifyMerged followed by the authorized close on a legacy run.
func TestExecuteChecksOnlyWithoutIndependentVerifier(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "current"
		if legacy {
			name = "legacy"
		}
		t.Run(name, func(t *testing.T) {
			s, repo, dbPath, recorder := checksOnlyFixture(t, marshal.AcceptMarshal, finalModelOff)
			ctx := t.Context()
			if legacy {
				stored, err := s.Store.GetMarshalRun(ctx, s.ProjectID, "run")
				if err != nil {
					t.Fatal(err)
				}
				run := stored.Value
				run.Compatibility = nil
				if _, err = s.Store.SetMarshalRun(ctx, s.ProjectID, "run", run, stored.Revision); err != nil {
					t.Fatal(err)
				}
			}
			run, err := s.Execute(ctx, "run", marshalBrief, nil)
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			assertChecksOnlyDelivered(t, s, repo, dbPath, recorder, run)
			stored, err := s.Store.GetMarshalRun(ctx, s.ProjectID, "run")
			if err != nil || stored.Value.DeliveryKind != verification.DeliveredChecksOnly {
				t.Fatalf("persisted delivery kind = %q %v", stored.Value.DeliveryKind, err)
			}
		})
	}

	// A full-proof run with the same verifier outage still stops for an
	// independent checker and never delivers.
	t.Run("full-proof-outage", func(t *testing.T) {
		s, repo, _, _ := checksOnlyFixture(t, marshal.AcceptMarshal, marshal.ReviewPolicy{})
		base := marshalGit(t, repo, "rev-parse", "refs/heads/main")
		run, _ := s.Execute(t.Context(), "run", marshalBrief, nil)
		if run.State == marshal.Closed || run.DeliveryKind != "" {
			t.Fatalf("full-proof run delivered without a verifier: %s %q", run.State, run.DeliveryKind)
		}
		if got := marshalGit(t, repo, "rev-parse", "refs/heads/main"); got != base {
			t.Fatal("full-proof run moved the target without a verifier")
		}
	})
}

// VerifyMerged on an opted-out run reruns the approved checks, returns its own
// pass decision, keeps the run verifying with a receipt, and an owner Close
// then delivers. Failed and unknown checks block.
func TestVerifierOffStillRunsApprovedChecks(t *testing.T) {
	s, repo, dbPath, recorder := checksOnlyFixture(t, marshal.AcceptMarshalThenUser, finalModelOff)
	ctx := t.Context()
	run, err := s.Execute(ctx, "run", marshalBrief, nil)
	if err != nil || run.State != marshal.Verifying {
		t.Fatalf("execute: %s %v", run.State, err)
	}
	if run.ChecksOnly == nil || !run.ChecksOnly.Passed() || recorder.count() == 0 {
		t.Fatalf("verification left no passing receipt: %+v checks=%d", run.ChecksOnly, recorder.count())
	}
	decision, err := s.VerifyMerged(ctx, "run", knownCharge())
	if err != nil || decision != verification.ChecksOnlyPassed {
		t.Fatalf("VerifyMerged = %s %v", decision, err)
	}
	if run, _ = s.Snapshot(ctx, "run"); run.State != marshal.Verifying {
		t.Fatalf("pass left run %s", run.State)
	}
	if err = s.Close(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	run, _ = s.Snapshot(ctx, "run")
	assertChecksOnlyDelivered(t, s, repo, dbPath, recorder, run)

	for _, mode := range []string{"failed", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			s, repo, _, recorder := checksOnlyFixture(t, marshal.AcceptMarshalThenUser, finalModelOff)
			ctx := t.Context()
			if _, err := s.Execute(ctx, "run", marshalBrief, nil); err != nil {
				t.Fatal(err)
			}
			if mode == "failed" {
				recorder.fail = true
			} else {
				s.GovernedCheck = nil
			}
			decision, err := s.VerifyMerged(ctx, "run", knownCharge())
			if err != nil || decision != verification.VerificationFailed {
				t.Fatalf("VerifyMerged = %s %v", decision, err)
			}
			run, _ := s.Snapshot(ctx, "run")
			if run.State != marshal.AwaitingUser || run.Pause == nil || run.Pause.ResumeState != marshal.Verifying || run.ChecksOnly != nil {
				t.Fatalf("failed checks did not pause at verification: %s %+v", run.State, run.Pause)
			}
			base := marshalGit(t, repo, "rev-parse", "refs/heads/main")
			if _, err := s.Resume(ctx, "run"); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(ctx, "run"); err == nil {
				t.Fatal("close delivered on failing checks")
			}
			if got := marshalGit(t, repo, "rev-parse", "refs/heads/main"); got != base {
				t.Fatal("failing checks moved the target")
			}
		})
	}
}

// The checks-only path never touches the canonical verification route: no
// session, no attestation, no verifier event, and the receipt names the
// omitted role.
func TestCanonicalOptOutProducesChecksOnlyReceiptAndNoAttestation(t *testing.T) {
	s, repo, dbPath, recorder := checksOnlyFixture(t, marshal.AcceptMarshal, marshal.ReviewPolicy{FinalModel: marshal.FinalModelOff, CrossReview: marshal.CrossReviewOff})
	canonical := 0
	s.requiredCanonicalVerification = func(context.Context, string, marshal.Run, string) (verification.Session, verification.Binding, error) {
		canonical++
		return verification.Session{}, verification.Binding{}, errors.New("canonical verification must not run")
	}
	run, err := s.Execute(t.Context(), "run", marshalBrief, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertChecksOnlyDelivered(t, s, repo, dbPath, recorder, run)
	if canonical != 0 {
		t.Fatalf("canonical verification ran %d times", canonical)
	}
	roles := strings.Join(run.ChecksOnly.OmittedRoles, ",")
	if roles != "independent-verifier,cross-review" || run.ChecksOnly.PolicyDigest == "" || run.ChecksOnly.ChecksDigest == "" || run.ChecksOnly.Authority != "standing-close-authorization" {
		t.Fatalf("receipt binding incomplete: %+v", run.ChecksOnly)
	}
}

// A retained receipt is dropped on resume once it no longer binds the run,
// and an amendment clears it; the close always takes a fresh receipt.
func TestChecksOnlyResumeAmendCloseBinding(t *testing.T) {
	s, _, _, _ := checksOnlyFixture(t, marshal.AcceptMarshalThenUser, finalModelOff)
	ctx := t.Context()
	run, err := s.Execute(ctx, "run", marshalBrief, nil)
	if err != nil || run.ChecksOnly == nil {
		t.Fatalf("execute: %v %+v", err, run.ChecksOnly)
	}
	first := run.ChecksOnly.ID
	stored, err := s.Store.GetMarshalRun(ctx, s.ProjectID, "run")
	if err != nil {
		t.Fatal(err)
	}
	stale := stored.Value
	receipt := *stale.ChecksOnly
	receipt.PlanVersion++
	receipt.Digest = receipt.ComputeDigest()
	stale.ChecksOnly = &receipt
	if _, err = s.Store.SetMarshalRun(ctx, s.ProjectID, "run", stale, stored.Revision); err != nil {
		t.Fatal(err)
	}
	if run, err = s.Resume(ctx, "run"); err != nil || run.ChecksOnly != nil {
		t.Fatalf("resume kept a stale receipt: %v %+v", err, run.ChecksOnly)
	}
	if _, err = s.VerifyMerged(ctx, "run", knownCharge()); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	run, _ = s.Snapshot(ctx, "run")
	if run.ChecksOnly == nil || run.ChecksOnly.ID == first || run.ChecksOnly.Authority != "owner-close" {
		t.Fatalf("close did not bind a fresh receipt: %+v", run.ChecksOnly)
	}
	if !checksOnlyReceiptCurrent(run, *run.ChecksOnly) {
		t.Fatal("closed receipt does not bind the closed run")
	}

	t.Run("amend-clears", func(t *testing.T) {
		s, _, _, _ := checksOnlyFixture(t, marshal.AcceptMarshalThenUser, finalModelOff)
		ctx := t.Context()
		if run, err := s.Execute(ctx, "run", marshalBrief, nil); err != nil || run.ChecksOnly == nil {
			t.Fatalf("execute: %v", err)
		}
		current, _ := s.Snapshot(ctx, "run")
		approvedPlan, err := s.Store.GetPlan(ctx, current.PlanID, current.PlanVersion)
		if err != nil {
			t.Fatal(err)
		}
		draft := s.Model.(marshalFakeModel).draft
		draft.Plan = approvedPlan
		draft.Plan.State = plan.StateReady
		draft.Plan.Routes = map[string]plan.Route{"a": {Provider: "test", Governance: constitution.GovernanceVerified}}
		draft.Plan.Tasks = append([]plan.Task(nil), approvedPlan.Tasks...)
		draft.Plan.Tasks[0].Title = "write a (reworded)"
		if draft.Plan.Graph, err = plan.BuildGraph(draft.Plan.Tasks); err != nil {
			t.Fatal(err)
		}
		draft.Tasks = append([]marshal.Task(nil), draft.Tasks...)
		draft.Tasks[0].Title = "write a (reworded)"
		amended, err := s.ApplyAmendDraft(ctx, "run", "reword the task title", draft)
		if err != nil {
			t.Fatal(err)
		}
		if amended.ChecksOnly != nil {
			t.Fatal("amendment kept the checks-only receipt")
		}
		stored, err := s.Store.GetMarshalRun(ctx, s.ProjectID, "run")
		if err != nil || stored.Value.ChecksOnly != nil {
			t.Fatalf("persisted receipt after amendment: %v", err)
		}
	})
}

// pendingChecksOnlyClose leaves the exact authorized journal a crash before
// the target moved would leave: the hook copies it, the close aborts, and the
// copy is written back.
func pendingChecksOnlyClose(t *testing.T, s *MarshalService) marshal.Run {
	t.Helper()
	ctx := t.Context()
	var pending marshal.Run
	s.requiredContextCloseEffect = func(ctx context.Context, id string, _ marshal.Run, _ *marshal.LifecycleOperation, _ func() error) error {
		stored, err := s.Store.GetMarshalRun(ctx, s.ProjectID, id)
		if err != nil {
			return err
		}
		pending = stored.Value
		return errors.New("simulated crash before the target moved")
	}
	if err := s.Close(ctx, "run"); err == nil {
		t.Fatal("close did not stop")
	}
	s.requiredContextCloseEffect = nil
	if pending.Operation == nil || !pending.Operation.Authorized || pending.Operation.DeliveryKind != verification.DeliveredChecksOnly {
		t.Fatalf("no authorized checks-only intent captured: %+v", pending.Operation)
	}
	// The aborted attempt's refusal is already on record under its
	// operation ID; the crashed journal carries an operation of its own.
	op := *pending.Operation
	op.ID, op.Event.ID = "marshal-op-crashed", "marshal-op-crashed"
	op.Event.Data = copyMarshalEventData(op.Event.Data)
	op.Event.Data["operation_id"] = op.ID
	pending.Operation = &op
	stored, err := s.Store.GetMarshalRun(ctx, s.ProjectID, "run")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Store.SetMarshalRun(ctx, s.ProjectID, "run", pending, stored.Revision); err != nil {
		t.Fatal(err)
	}
	return pending
}

func TestChecksOnlyCloseFenceAndGitRecovery(t *testing.T) {
	verified := func(t *testing.T) (*MarshalService, string, *checksOnlyRecorder) {
		s, repo, _, recorder := checksOnlyFixture(t, marshal.AcceptMarshalThenUser, finalModelOff)
		if run, err := s.Execute(t.Context(), "run", marshalBrief, nil); err != nil || run.State != marshal.Verifying {
			t.Fatalf("execute: %s %v", run.State, err)
		}
		return s, repo, recorder
	}

	t.Run("required-context-fence", checksOnlyRequiredContextCloseFence)

	t.Run("crash-before-intent", func(t *testing.T) {
		s, repo, _ := verified(t)
		base := marshalGit(t, repo, "rev-parse", "refs/heads/main")
		s.ApprovalActor = func(context.Context, string, string) (string, error) { return "", errors.New("crash before intent") }
		if err := s.Close(t.Context(), "run"); err == nil {
			t.Fatal("close did not stop")
		}
		run, _ := s.Snapshot(t.Context(), "run")
		if run.Operation != nil || run.State != marshal.Verifying || marshalGit(t, repo, "rev-parse", "refs/heads/main") != base {
			t.Fatalf("refused close left %s %+v", run.State, run.Operation)
		}
	})

	t.Run("unchanged-target-unapplied", func(t *testing.T) {
		s, repo, recorder := verified(t)
		ctx := t.Context()
		base := marshalGit(t, repo, "rev-parse", "refs/heads/main")
		pending := pendingChecksOnlyClose(t, s)
		run, err := s.Resume(ctx, "run")
		if err != nil {
			t.Fatal(err)
		}
		if run.Operation != nil || run.State == marshal.Closed || marshalGit(t, repo, "rev-parse", "refs/heads/main") != base {
			t.Fatalf("unapplied recovery: %s %+v", run.State, run.Operation)
		}
		list, _ := s.Store.MarshalDecisions(ctx, "run")
		unapplied := 0
		for _, e := range list {
			if e.Data["close_outcome"] == "unapplied" && e.Data["unapplied_operation_id"] == pending.Operation.ID {
				unapplied++
			}
		}
		if unapplied != 1 {
			t.Fatalf("unapplied events = %d", unapplied)
		}
		// A second resume finds no intent to clear again.
		if _, err = s.Resume(ctx, "run"); err != nil {
			t.Fatal(err)
		}
		before := recorder.count()
		if _, err = s.VerifyMerged(ctx, "run", knownCharge()); err != nil {
			t.Fatal(err)
		}
		if err = s.Close(ctx, "run"); err != nil {
			t.Fatal(err)
		}
		run, _ = s.Snapshot(ctx, "run")
		if run.State != marshal.Closed || run.ChecksOnly == nil || run.ChecksOnly.ID == pending.Operation.ReceiptID || recorder.count() <= before {
			t.Fatalf("fresh close reused the old proof: %+v", run.ChecksOnly)
		}
		list, _ = s.Store.MarshalDecisions(ctx, "run")
		for _, e := range list {
			if e.Type == events.EventTypeMarshalRunClosed && e.Data["operation_id"] == pending.Operation.ID {
				t.Fatal("fresh close reused the unapplied operation")
			}
		}
		if marshalGit(t, repo, "rev-parse", "refs/"+statepath.RefName+"/run/pre-close") != base {
			t.Fatal("checkpoint audit evidence lost")
		}
	})

	t.Run("before-equals-after-unapplied", func(t *testing.T) {
		s, repo, _ := verified(t)
		ctx := t.Context()
		base := marshalGit(t, repo, "rev-parse", "refs/heads/main")
		pendingChecksOnlyClose(t, s)
		stored, _ := s.Store.GetMarshalRun(ctx, s.ProjectID, "run")
		forged := stored.Value
		op := *forged.Operation
		op.After = op.Before
		forged.Operation = &op
		if _, err := s.Store.SetMarshalRun(ctx, s.ProjectID, "run", forged, stored.Revision); err != nil {
			t.Fatal(err)
		}
		run, err := s.Resume(ctx, "run")
		if err != nil || run.State == marshal.Closed || run.Operation != nil || marshalGit(t, repo, "rev-parse", "refs/heads/main") != base {
			t.Fatalf("ref equality alone delivered: %s %v", run.State, err)
		}
	})

	t.Run("after-git-applied", func(t *testing.T) {
		s, repo, recorder := verified(t)
		ctx := t.Context()
		head := marshalGit(t, repo, "rev-parse", "refs/heads/"+statepath.RefName+"/run/integration")
		s.AfterLifecycleEffect = func(kind, _ string) error {
			if kind == "close" {
				return errors.New("simulated crash after Git")
			}
			return nil
		}
		if err := s.Close(ctx, "run"); err == nil {
			t.Fatal("close did not stop")
		}
		s.AfterLifecycleEffect = nil
		if marshalGit(t, repo, "rev-parse", "refs/heads/main") != head {
			t.Fatal("target did not move before the simulated crash")
		}
		checks := recorder.count()
		run, err := s.Resume(ctx, "run")
		if err != nil {
			t.Fatal(err)
		}
		if run.State != marshal.Closed || run.DeliveryKind != verification.DeliveredChecksOnly || recorder.count() != checks {
			t.Fatalf("applied recovery: %s %q checks %d->%d", run.State, run.DeliveryKind, checks, recorder.count())
		}
		// The already-written acknowledgement is not repeated.
		if _, err = s.Resume(ctx, "run"); err != nil {
			t.Fatal(err)
		}
		if got := countRunEvents(t, s, events.EventTypeMarshalRunClosed); got != 1 {
			t.Fatalf("close acknowledged %d times", got)
		}
		if got := countRunEvents(t, s, events.EventTypeMarshalCloseFencePassed); got != 1 {
			t.Fatalf("close fence passage events = %d, want 1", got)
		}
	})

	t.Run("after-git-missing-fence-proof-held", func(t *testing.T) {
		s, repo, _ := verified(t)
		ctx := t.Context()
		pending := pendingChecksOnlyClose(t, s)
		// Advance the target to op.After without persisting a durable fence
		// proof-of-passage record: recovery must refuse to complete the close
		// and hold the intent for inspection.
		marshalGit(t, repo, "update-ref", pending.Operation.Target, pending.Operation.After, pending.Operation.Before)
		if _, err := s.Resume(ctx, "run"); err == nil || !strings.Contains(err.Error(), "missing durable close fence proof") {
			t.Fatalf("recovery completed without durable fence proof: %v", err)
		}
		run, _ := s.Snapshot(ctx, "run")
		if run.State == marshal.Closed || run.Operation == nil {
			t.Fatalf("unproven advancement was not held: state=%s op=%+v", run.State, run.Operation)
		}
	})

	t.Run("fence-marker-unchanged-target-unapplied-and-retired", func(t *testing.T) {
		s, repo, _ := verified(t)
		ctx := t.Context()
		base := marshalGit(t, repo, "rev-parse", "refs/heads/main")
		pending := pendingChecksOnlyClose(t, s)
		if err := s.recordChecksOnlyCloseFence(ctx, "run", pending.Operation); err != nil {
			t.Fatal(err)
		}
		run, err := s.Resume(ctx, "run")
		if err != nil {
			t.Fatal(err)
		}
		if run.Operation != nil || run.State == marshal.Closed || marshalGit(t, repo, "rev-parse", "refs/heads/main") != base {
			t.Fatalf("unchanged target with fence marker did not clear as unapplied: %s %+v", run.State, run.Operation)
		}
		if got := countRunEvents(t, s, events.EventTypeMarshalCloseFencePassed); got != 1 {
			t.Fatalf("fence marker audit evidence = %d, want 1", got)
		}
		// Once cleared as unapplied, the old operation's fence marker is
		// ineligible for reuse even if replayed with Git later at op.After.
		stored, err := s.Store.GetMarshalRun(ctx, s.ProjectID, "run")
		if err != nil {
			t.Fatal(err)
		}
		replayed := stored.Value
		replayed.Operation = pending.Operation
		if _, err = s.Store.SetMarshalRun(ctx, s.ProjectID, "run", replayed, stored.Revision); err != nil {
			t.Fatal(err)
		}
		marshalGit(t, repo, "update-ref", pending.Operation.Target, pending.Operation.After, pending.Operation.Before)
		if _, err := s.Resume(ctx, "run"); err == nil || !strings.Contains(err.Error(), "missing durable close fence proof") {
			t.Fatalf("retired fence marker was reused: %v", err)
		}
	})

	t.Run("external-divergence-held", func(t *testing.T) {
		s, repo, _ := verified(t)
		ctx := t.Context()
		pendingChecksOnlyClose(t, s)
		marshalGit(t, repo, "commit", "--allow-empty", "-m", "someone else")
		if _, err := s.Resume(ctx, "run"); err == nil || !strings.Contains(err.Error(), "target moved") {
			t.Fatalf("divergent target recovered: %v", err)
		}
		run, _ := s.Snapshot(ctx, "run")
		if run.Operation == nil || run.State == marshal.Closed {
			t.Fatal("divergent journal was not held for inspection")
		}
	})
}

func seedChecksOnlyAcceptedReview(t *testing.T, r *Runtime, runID string, run marshal.Run) {
	t.Helper()
	for _, task := range run.Tasks {
		review := marshal.Review{
			ArchitectureVerdict: "conforms",
			Verdict:             marshal.VerdictAccept,
			Reviewer:            "marshal",
			Independent: &marshal.IndependentReview{
				ArchitectureVerdict: "conforms",
				Verdict:             marshal.VerdictAccept,
				Reviewer:            "cross-review:other",
				Provider:            "other",
				EvidenceRefs:        []string{"result:" + task.ResultCommit},
				SessionID:           "independent-review",
				FreshSession:        true,
			},
		}
		if _, err := r.store.SetMarshalReview(t.Context(), runID, task.PlanTaskID, 1, review); err != nil {
			t.Fatal(err)
		}
	}
}

// At the required-context close fence a checks-only close presents its sealed
// receipt and live one-shot capability in place of the canonical session and
// attestation; without the capability, or on replay, the target never moves.
func checksOnlyRequiredContextCloseFence(t *testing.T) {
	r, ctx, _, _, id, _ := completionRuntimeFixture(t, "complete")
	session, err := r.store.GetVerificationSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	r.requiredIntegrationSources.mu.Lock()
	source := r.requiredIntegrationSources.byRun[session.Binding.RunID]
	r.requiredIntegrationSources.mu.Unlock()
	service := r.Marshal()
	service.GovernedCheck = fixtureCheckRunner
	runID := source.marshalRunID
	stored, err := r.store.GetMarshalRun(ctx, r.ProjectID(), runID)
	if err != nil {
		t.Fatal(err)
	}
	run := stored.Value
	seedChecksOnlyAcceptedReview(t, r, runID, run)
	run.Settings.ReviewPolicy.FinalModel = marshal.FinalModelOff
	run.ReviewPolicy = marshal.NewReviewPolicySnapshot(run.Settings.ReviewPolicy, "", "owner", time.Now().UTC())
	if _, err = r.store.SetMarshalRun(ctx, r.ProjectID(), runID, run, stored.Revision); err != nil {
		t.Fatal(err)
	}
	if err = r.observeRequiredMarshalIntegrationSource(ctx, service, runID, run, source.head); err != nil {
		t.Fatal(err)
	}
	r.requiredIntegrationSources.mu.Lock()
	for _, candidate := range r.requiredIntegrationSources.byRun {
		if candidate.marshalRunID == runID {
			source = candidate
		}
	}
	r.requiredIntegrationSources.mu.Unlock()
	receipt, err := service.runChecksOnlyReceipt(ctx, runID, run, source.head, "owner-close")
	if err != nil || !receipt.Passed() {
		t.Fatalf("receipt: %v %+v", err, receipt)
	}
	attestations := 0
	if _, err := r.store.LatestCompletionAttestation(ctx, session.ID); err == nil {
		attestations = 1
	}
	next := source.marshalRun
	next.State, next.ChecksOnly, next.DeliveryKind = marshal.Closed, &receipt, verification.DeliveredChecksOnly
	op := &marshal.LifecycleOperation{Kind: "close", Before: run.BaseCommit, After: source.head, Target: run.TargetRef, Next: &next, DeliveryKind: verification.DeliveredChecksOnly, ReceiptID: receipt.ID, ReceiptDigest: receipt.Digest, Event: events.Event{Type: events.EventTypeMarshalRunClosed}}
	revision, err := service.beginOperation(ctx, runID, source.marshalRun, source.marshalRevision, op)
	if err != nil {
		t.Fatal(err)
	}
	op.Authorized = true
	pending := source.marshalRun
	pending.Operation = op
	if err = service.save(ctx, runID, pending, revision); err != nil {
		t.Fatal(err)
	}
	called := 0
	effect := func() error {
		called++
		_, err := gitMarshal(ctx, r.ProjectRoot(), "update-ref", op.Target, op.After, op.Before)
		return err
	}
	if err = r.withRequiredContextCloseEffect(ctx, service, runID, source.marshalRun, op, effect); !errors.Is(err, projectcontext.ErrReviewPending) || called != 0 {
		t.Fatalf("fence passed without a live capability: %v called=%d", err, called)
	}
	if err = r.retainRequiredChecksOnlyCloseProof(ctx, runID, source.marshalRun, source.head, receipt); err != nil {
		t.Fatal(err)
	}
	if err = r.withRequiredContextCloseEffect(ctx, service, runID, source.marshalRun, op, effect); err != nil || called != 1 {
		t.Fatalf("checks-only fence refused: %v called=%d", err, called)
	}
	if after, _ := gitMarshal(ctx, r.ProjectRoot(), "rev-parse", op.Target); strings.TrimSpace(after) != source.head {
		t.Fatal("target did not move to the bound head")
	}
	if passed, err := service.hasChecksOnlyCloseFence(ctx, runID, op); err != nil || !passed {
		t.Fatalf("checks-only close fence did not persist proof-of-passage: passed=%v err=%v", passed, err)
	}
	if err = r.withRequiredContextCloseEffect(ctx, service, runID, source.marshalRun, op, effect); err == nil || called != 1 {
		t.Fatal("checks-only capability replayed", err)
	}
	now := 0
	if _, err := r.store.LatestCompletionAttestation(ctx, session.ID); err == nil {
		now = 1
	}
	if now != attestations {
		t.Fatal("checks-only fence minted a canonical attestation")
	}
}

// The runtime-wired owner Close of an opted-out run delivers through the
// required source and close fence with every verifier role unavailable and
// mints no canonical attestation.
func TestCanonicalOptOutRuntimeWiredClose(t *testing.T) {
	r, ctx, cli, _, verificationID, _ := completionRuntimeFixture(t, "complete")
	session, err := r.store.GetVerificationSession(ctx, verificationID)
	if err != nil {
		t.Fatal(err)
	}
	r.requiredIntegrationSources.mu.Lock()
	source := r.requiredIntegrationSources.byRun[session.Binding.RunID]
	r.requiredIntegrationSources.mu.Unlock()
	binary, err := os.ReadFile(cli.Binary)
	if err != nil {
		t.Fatal(err)
	}
	tools := t.TempDir()
	if err = os.WriteFile(filepath.Join(tools, "claude"), binary, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	service, err := r.MarshalWired(MarshalWiring{Provider: "claude", Approver: func(context.Context, string, string) (string, error) { return "explicit-host-fixture-consent", nil }})
	if err != nil {
		t.Fatal(err)
	}
	service.HandInGuard = nil
	service.Verify, service.VerifierProvider, service.IndependentVerify = nil, nil, nil
	service.GateState = service.observedGateState
	recorder := &checksOnlyRecorder{}
	service.GovernedCheck = recorder.run
	stored, err := r.store.GetMarshalRun(ctx, r.ProjectID(), source.marshalRunID)
	if err != nil {
		t.Fatal(err)
	}
	run := stored.Value
	seedChecksOnlyAcceptedReview(t, r, source.marshalRunID, run)
	run.Settings.ReviewPolicy.FinalModel = marshal.FinalModelOff
	run.ReviewPolicy = marshal.NewReviewPolicySnapshot(run.Settings.ReviewPolicy, "", "owner", time.Now().UTC())
	run.Repository, err = service.repositoryIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := r.store.GetPlan(ctx, run.PlanID, run.PlanVersion)
	if err != nil {
		t.Fatal(err)
	}
	run.ApprovalScopeDigest = marshalApprovalDigest(p.ApprovalScopeDigest, run)
	run.CloseAuthorization = closeAuthorization(run, "explicit-host-fixture-consent")
	if _, err = r.store.SetMarshalRun(ctx, r.ProjectID(), source.marshalRunID, run, stored.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = gitMarshal(ctx, r.ProjectRoot(), "switch", "--detach", run.BaseCommit); err != nil {
		t.Fatal(err)
	}
	attested := 0
	if _, err := r.store.LatestCompletionAttestation(ctx, session.ID); err == nil {
		attested = 1
	}
	if err = service.Close(ctx, source.marshalRunID); err != nil {
		t.Fatal("runtime-wired checks-only close refused: ", err)
	}
	closed, err := r.store.GetMarshalRun(ctx, r.ProjectID(), source.marshalRunID)
	if err != nil || closed.Value.State != marshal.Closed || closed.Value.Operation != nil || closed.Value.DeliveryKind != verification.DeliveredChecksOnly || closed.Value.ChecksOnly == nil {
		t.Fatalf("close journal: %s kind=%q %v", closed.Value.State, closed.Value.DeliveryKind, err)
	}
	after, err := gitMarshal(ctx, r.ProjectRoot(), "rev-parse", run.TargetRef)
	if err != nil || strings.TrimSpace(after) != source.head || closed.Value.ChecksOnly.Head != source.head {
		t.Fatal("checks-only close did not deliver the bound head", after, err)
	}
	if recorder.count() == 0 || recorder.heads[len(recorder.heads)-1] != source.head {
		t.Fatalf("approved checks did not run on the delivered head: %v", recorder.heads)
	}
	latest, err := r.Verification().CurrentForRun(ctx, session.Binding.RunID)
	if err != nil || latest.ID != session.ID {
		t.Fatal("checks-only close created a verification session", latest.ID, err)
	}
	now := 0
	if _, err := r.store.LatestCompletionAttestation(ctx, session.ID); err == nil {
		now = 1
	}
	if now != attested {
		t.Fatal("checks-only close minted a canonical attestation")
	}
}

// Turning cross-review off by itself (leaving final-model at its default)
// selects checks-only proof when the approved policy allows it, in both
// VerifyMerged and Close/Execute, with zero canonical attestations.
func TestCrossReviewOffAloneSelectsChecksOnlyProof(t *testing.T) {
	policy := marshal.ReviewPolicy{CrossReview: marshal.CrossReviewOff}
	t.Run("execute-standing-close", func(t *testing.T) {
		s, repo, dbPath, recorder := checksOnlyFixture(t, marshal.AcceptMarshal, policy)
		s.CrossReview = nil
		run, err := s.Execute(t.Context(), "run", marshalBrief, nil)
		if err != nil {
			t.Fatalf("execute with cross-review off: %v", err)
		}
		assertChecksOnlyDelivered(t, s, repo, dbPath, recorder, run)
		if roles := strings.Join(run.ChecksOnly.OmittedRoles, ","); !strings.Contains(roles, "cross-review") {
			t.Fatalf("omitted roles = %q, want cross-review listed", roles)
		}
	})
	t.Run("verify-merged-and-owner-close", func(t *testing.T) {
		s, repo, dbPath, recorder := checksOnlyFixture(t, marshal.AcceptMarshalThenUser, policy)
		s.CrossReview = nil
		ctx := t.Context()
		run, err := s.Execute(ctx, "run", marshalBrief, nil)
		if err != nil || run.State != marshal.Verifying {
			t.Fatalf("execute: state=%s err=%v", run.State, err)
		}
		decision, err := s.VerifyMerged(ctx, "run", knownCharge())
		if err != nil || decision != verification.ChecksOnlyPassed {
			t.Fatalf("VerifyMerged = %s err=%v, want CHECKS_ONLY_PASSED", decision, err)
		}
		if err := s.Close(ctx, "run"); err != nil {
			t.Fatalf("owner close: %v", err)
		}
		run, _ = s.Snapshot(ctx, "run")
		assertChecksOnlyDelivered(t, s, repo, dbPath, recorder, run)
	})
}

// Missing or disagreeing mandatory review (primary review, mandatory
// independent cross-review, or mandatory architecture verdict) refuses
// checks-only delivery without moving the target.
func TestChecksOnlyRefusesMissingOrDisagreeingMandatoryReview(t *testing.T) {
	t.Run("missing-or-disagreeing-task-review", func(t *testing.T) {
		cases := []struct {
			name   string
			mutate func(t *testing.T, s *MarshalService, dbPath string)
		}{
			{
				name: "missing-review",
				mutate: func(t *testing.T, _ *MarshalService, dbPath string) {
					db, err := sql.Open("sqlite", dbPath)
					if err != nil {
						t.Fatal(err)
					}
					defer db.Close()
					if _, err := db.Exec(`DELETE FROM marshal_reviews WHERE run_id = 'run' AND task_id = 'a'`); err != nil {
						t.Fatal(err)
					}
				},
			},
			{
				name: "disagreeing-primary-review",
				mutate: func(t *testing.T, s *MarshalService, dbPath string) {
					db, err := sql.Open("sqlite", dbPath)
					if err != nil {
						t.Fatal(err)
					}
					defer db.Close()
					if _, err := db.Exec(`DELETE FROM marshal_reviews WHERE run_id = 'run' AND task_id = 'a'`); err != nil {
						t.Fatal(err)
					}
					if _, err := s.Store.SetMarshalReview(t.Context(), "run", "a", 1, marshal.Review{
						Verdict:  marshal.VerdictReturn,
						Reviewer: "marshal",
						Reasons:  []string{"primary disagreed"},
					}); err != nil {
						t.Fatal(err)
					}
				},
			},
			{
				name: "missing-mandatory-independent-review",
				mutate: func(t *testing.T, s *MarshalService, dbPath string) {
					db, err := sql.Open("sqlite", dbPath)
					if err != nil {
						t.Fatal(err)
					}
					defer db.Close()
					if _, err := db.Exec(`DELETE FROM marshal_reviews WHERE run_id = 'run' AND task_id = 'a'`); err != nil {
						t.Fatal(err)
					}
					if _, err := s.Store.SetMarshalReview(t.Context(), "run", "a", 1, marshal.Review{
						Verdict:     marshal.VerdictAccept,
						Reviewer:    "marshal",
						Independent: nil,
					}); err != nil {
						t.Fatal(err)
					}
				},
			},
			{
				name: "disagreeing-mandatory-independent-review",
				mutate: func(t *testing.T, s *MarshalService, dbPath string) {
					db, err := sql.Open("sqlite", dbPath)
					if err != nil {
						t.Fatal(err)
					}
					defer db.Close()
					if _, err := db.Exec(`DELETE FROM marshal_reviews WHERE run_id = 'run' AND task_id = 'a'`); err != nil {
						t.Fatal(err)
					}
					if _, err := s.Store.SetMarshalReview(t.Context(), "run", "a", 1, marshal.Review{
						Verdict:  marshal.VerdictAccept,
						Reviewer: "marshal",
						Independent: &marshal.IndependentReview{
							Verdict:      marshal.VerdictReturn,
							Reviewer:     "cross-review:other",
							Provider:     "other",
							EvidenceRefs: []string{"check:ok"},
						},
					}); err != nil {
						t.Fatal(err)
					}
				},
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				s, repo, dbPath, _ := checksOnlyFixture(t, marshal.AcceptMarshalThenUser, finalModelOff)
				ctx := t.Context()
				if run, err := s.Execute(ctx, "run", marshalBrief, nil); err != nil || run.State != marshal.Verifying {
					t.Fatalf("execute: state=%s err=%v", run.State, err)
				}
				base := marshalGit(t, repo, "rev-parse", "refs/heads/main")
				tc.mutate(t, s, dbPath)
				if decision, err := s.VerifyMerged(ctx, "run", knownCharge()); err == nil || decision == verification.ChecksOnlyPassed {
					t.Fatalf("VerifyMerged passed with %s: decision=%s err=%v", tc.name, decision, err)
				}
				if _, err := s.Resume(ctx, "run"); err != nil {
					t.Fatal(err)
				}
				if err := s.Close(ctx, "run"); err == nil {
					t.Fatalf("Close delivered despite %s", tc.name)
				}
				if got := marshalGit(t, repo, "rev-parse", "refs/heads/main"); got != base {
					t.Fatalf("target moved despite %s", tc.name)
				}
			})
		}
	})

	t.Run("missing-or-disagreeing-mandatory-architecture-review", func(t *testing.T) {
		for _, scenario := range []string{"missing-verdict", "mismatch-verdict"} {
			t.Run(scenario, func(t *testing.T) {
				r, ctx, _, _, id, _ := completionRuntimeFixture(t, "complete")
				session, err := r.store.GetVerificationSession(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				r.requiredIntegrationSources.mu.Lock()
				source := r.requiredIntegrationSources.byRun[session.Binding.RunID]
				r.requiredIntegrationSources.mu.Unlock()
				service := r.Marshal()
				service.GovernedCheck = fixtureCheckRunner
				runID := source.marshalRunID
				stored, err := r.store.GetMarshalRun(ctx, r.ProjectID(), runID)
				if err != nil {
					t.Fatal(err)
				}
				run := stored.Value
				run.Settings.ReviewPolicy.FinalModel = marshal.FinalModelOff
				run.ReviewPolicy = marshal.NewReviewPolicySnapshot(run.Settings.ReviewPolicy, "", "owner", time.Now().UTC())
				if _, err = r.store.SetMarshalRun(ctx, r.ProjectID(), runID, run, stored.Revision); err != nil {
					t.Fatal(err)
				}
				review := marshal.Review{
					Verdict:  marshal.VerdictAccept,
					Reviewer: "marshal",
					Independent: &marshal.IndependentReview{
						Verdict:      marshal.VerdictAccept,
						Reviewer:     "cross-review:other",
						Provider:     "other",
						EvidenceRefs: []string{"result:" + run.Tasks[0].ResultCommit},
					},
				}
				if scenario == "mismatch-verdict" {
					review.ArchitectureVerdict = "mismatches"
					review.ArchitectureMismatches = []string{"README.md:doc: disagrees with contract"}
					review.Independent.ArchitectureVerdict = "conforms"
				}
				if _, err := r.store.SetMarshalReview(ctx, runID, run.Tasks[0].PlanTaskID, 1, review); err != nil {
					t.Fatal(err)
				}
				if _, err := service.runChecksOnlyReceipt(ctx, runID, run, source.head, "owner-close"); err == nil || !strings.Contains(err.Error(), "mandatory architecture review") {
					t.Fatalf("expected mandatory architecture review refusal (%s), got %v", scenario, err)
				}
			})
		}
	})
}

// At the required-context close fence, any change to the owner or project
// context (mandatory memory, freshness, context packet, or SQL authority)
// between check and close refuses the checks-only close before any Git effect.
func TestChecksOnlyCloseFenceRefusesChangedOwnerAndProjectContext(t *testing.T) {
	scenarios := []string{
		"foreign-owner-context",
		"changed-owner-principal",
		"revoked-memory",
		"stale-memory",
		"changed-sql-authority",
		"changed-context-packet",
	}
	for _, scenario := range scenarios {
		t.Run(scenario, func(t *testing.T) {
			r, ctx, _, _, id, _ := completionRuntimeFixture(t, "complete")
			session, err := r.store.GetVerificationSession(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			r.requiredIntegrationSources.mu.Lock()
			source := r.requiredIntegrationSources.byRun[session.Binding.RunID]
			r.requiredIntegrationSources.mu.Unlock()
			service := r.Marshal()
			service.GovernedCheck = fixtureCheckRunner
			runID := source.marshalRunID
			stored, err := r.store.GetMarshalRun(ctx, r.ProjectID(), runID)
			if err != nil {
				t.Fatal(err)
			}
			run := stored.Value
			seedChecksOnlyAcceptedReview(t, r, runID, run)
			run.Settings.ReviewPolicy.FinalModel = marshal.FinalModelOff
			run.ReviewPolicy = marshal.NewReviewPolicySnapshot(run.Settings.ReviewPolicy, "", "owner", time.Now().UTC())
			if _, err = r.store.SetMarshalRun(ctx, r.ProjectID(), runID, run, stored.Revision); err != nil {
				t.Fatal(err)
			}
			if err = r.observeRequiredMarshalIntegrationSource(ctx, service, runID, run, source.head); err != nil {
				t.Fatal(err)
			}
			r.requiredIntegrationSources.mu.Lock()
			for _, candidate := range r.requiredIntegrationSources.byRun {
				if candidate.marshalRunID == runID {
					source = candidate
				}
			}
			r.requiredIntegrationSources.mu.Unlock()
			receipt, err := service.runChecksOnlyReceipt(ctx, runID, run, source.head, "owner-close")
			if err != nil || !receipt.Passed() {
				t.Fatalf("receipt: %v %+v", err, receipt)
			}
			if err = r.retainRequiredChecksOnlyCloseProof(ctx, runID, source.marshalRun, source.head, receipt); err != nil {
				t.Fatal(err)
			}
			next := source.marshalRun
			next.State, next.ChecksOnly, next.DeliveryKind = marshal.Closed, &receipt, verification.DeliveredChecksOnly
			op := &marshal.LifecycleOperation{Kind: "close", Before: run.BaseCommit, After: source.head, Target: run.TargetRef, Next: &next, DeliveryKind: verification.DeliveredChecksOnly, ReceiptID: receipt.ID, ReceiptDigest: receipt.Digest, Event: events.Event{Type: events.EventTypeMarshalRunClosed}}
			revision, err := service.beginOperation(ctx, runID, source.marshalRun, source.marshalRevision, op)
			if err != nil {
				t.Fatal(err)
			}
			op.Authorized = true
			pending := source.marshalRun
			pending.Operation = op
			if err = service.save(ctx, runID, pending, revision); err != nil {
				t.Fatal(err)
			}
			closeCtx := ctx
			switch scenario {
			case "foreign-owner-context":
				closeCtx = t.Context()
			case "changed-owner-principal":
				r.requiredIntegrationSources.mu.Lock()
				source.checksOnly.principal = "local-uid:other"
				r.requiredIntegrationSources.mu.Unlock()
			case "revoked-memory":
				if _, err := r.store.UpdateMemory(t.Context(), r.ProjectID(), "MEM-required-resolver", 1, func(record *model.MemoryRecordV2) error {
					record.ACLScope = "foreign"
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			case "stale-memory":
				if _, err := r.store.UpdateMemory(t.Context(), r.ProjectID(), "MEM-required-resolver", 1, func(record *model.MemoryRecordV2) error {
					record.BranchName = "stale-branch"
					record.HeadCommit = strings.Repeat("b", 40)
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			case "changed-sql-authority":
				owner, ok := auth.LocalFromContext(ctx)
				if !ok {
					t.Fatal("missing local owner in fixture context")
				}
				if err := r.store.PutCapabilityGrant(t.Context(), capability.Grant{
					ID:        capability.GrantID("GRANT-late-checks-only"),
					Subject:   capability.SubjectID(owner.ID()),
					TaskID:    capability.TaskID(r.ProjectIdentity()),
					Kind:      capability.KindFilesystemRead,
					Scope:     capability.Scope{Resource: "README.md", Actions: []string{"read"}},
					Issuer:    capability.SubjectID(owner.ID()),
					IssuedAt:  time.Now().UTC().Add(-time.Minute),
					ExpiresAt: time.Now().UTC().Add(time.Hour),
				}); err != nil {
					t.Fatal(err)
				}
			case "changed-context-packet":
				r.requiredIntegrationSources.mu.Lock()
				source.checksOnly.admissions[0].ExpectedPacketDigest = "sha256:" + strings.Repeat("0", 64)
				r.requiredIntegrationSources.mu.Unlock()
			}
			called := false
			err = r.withRequiredContextCloseEffect(closeCtx, service, runID, source.marshalRun, op, func() error {
				called = true
				return nil
			})
			if err == nil || called {
				t.Fatalf("checks-only close passed despite %s: err=%v called=%v", scenario, err, called)
			}
			if after, gitErr := gitMarshal(t.Context(), r.ProjectRoot(), "rev-parse", op.Target); gitErr != nil || strings.TrimSpace(after) != op.Before {
				t.Fatalf("target moved despite %s: after=%s err=%v", scenario, after, gitErr)
			}
		})
	}
}

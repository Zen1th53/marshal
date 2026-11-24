package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
)

// reworkFixture approves a one-task run whose worker appends a line to a.txt,
// so a carried-forward attempt sees the previous attempt's line.
func reworkFixture(t *testing.T) (*MarshalService, string) {
	t.Helper()
	s, repo := marshalFixture(t, 1)
	s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		path := filepath.Join(req.Worktree, "a.txt")
		prior, _ := os.ReadFile(path)
		return nil, os.WriteFile(path, append(prior, []byte("attempt\n")...), 0600)
	}}
	if _, err := s.StartPlanning(t.Context(), "run", "write file", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(t.Context(), "run"); err != nil {
		t.Fatal(err)
	}
	return s, repo
}

func reworkAttempt(t *testing.T, s *MarshalService) marshal.HandIn {
	t.Helper()
	d, err := s.Dispatch(t.Context(), "run", "a", "write")
	if err != nil {
		t.Fatal(err)
	}
	h, err := s.CollectHandIn(t.Context(), "run", d)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func setMarshalReview(s *MarshalService, review marshal.Review) {
	s.Model = marshalFakeModel{draft: s.Model.(marshalFakeModel).draft, review: review}
}

func setCrossReview(s *MarshalService, verdict marshal.Verdict, reasons ...string) {
	s.CrossReview = func(_ context.Context, _ marshal.Task, h marshal.HandIn, _ marshal.Control) (marshal.Review, string, error) {
		return marshal.Review{ArchitectureVerdict: "conforms", Verdict: verdict, Reasons: reasons, Reviewer: "cross-review:other", FreshSession: true, SessionID: "independent-review", EvidenceRefs: []string{"result:" + h.ResultCommit}}, "other", nil
	}
}

func lastMarshalEvent(t *testing.T, s *MarshalService, kind events.EventType) events.Event {
	t.Helper()
	history, err := s.Store.MarshalDecisions(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Type == kind {
			full, err := s.fullMarshalEvent(history[i])
			if err != nil {
				t.Fatal(err)
			}
			return full
		}
	}
	t.Fatalf("no %s event", kind)
	return events.Event{}
}

// A returned attempt resumes at its verified prior result, pinned by ref,
// while the approved base and the attempt base stay separate.
func TestReworkStartsAtVerifiedPriorResult(t *testing.T) {
	s, repo := reworkFixture(t)
	setMarshalReview(s, marshal.Review{Verdict: marshal.VerdictReturn, Reviewer: "marshal", Reasons: []string{"add a second line"}})
	first := reworkAttempt(t, s)
	if v, err := s.Review(t.Context(), "run", "a", knownCharge()); err != nil || v != marshal.VerdictReturn {
		t.Fatalf("review = %s %v", v, err)
	}
	if got := marshalGit(t, repo, "rev-parse", priorResultRef("run", "a", first.ResultCommit)); got != first.ResultCommit {
		t.Fatalf("prior result pin = %s, want %s", got, first.ResultCommit)
	}
	setMarshalReview(s, marshal.Review{Verdict: marshal.VerdictAccept, Reviewer: "marshal"})
	second := reworkAttempt(t, s)
	run, _, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if second.BaseCommit != run.BaseCommit {
		t.Fatalf("attempt base = %s, want approved base %s", second.BaseCommit, run.BaseCommit)
	}
	if _, err := gitMarshal(t.Context(), repo, "merge-base", "--is-ancestor", first.ResultCommit, second.ResultCommit); err != nil {
		t.Fatalf("rework did not build on the prior result: %v", err)
	}
	if got := marshalGit(t, repo, "show", second.ResultCommit+":a.txt"); got != "attempt\nattempt" {
		t.Fatalf("a.txt = %q, want both attempts", got)
	}
	e := lastMarshalEvent(t, s, events.EventTypeMarshalTaskDispatched)
	if e.Data["prior_result"] != first.ResultCommit || e.Data["attempt_base"] != run.BaseCommit || e.Data["approved_base_commit"] != run.BaseCommit || fmt.Sprint(e.Data["attempt"]) != "2" {
		t.Fatalf("dispatch evidence = %v", e.Data)
	}
}

// A prior result that does not descend from the approved base is refused as
// stale_base with recovery commands; nothing is reset and the pin stays.
func TestStaleBaseRefusesWithRecovery(t *testing.T) {
	s, repo := reworkFixture(t)
	setMarshalReview(s, marshal.Review{Verdict: marshal.VerdictReturn, Reviewer: "marshal", Reasons: []string{"again"}})
	reworkAttempt(t, s)
	if _, err := s.Review(t.Context(), "run", "a", knownCharge()); err != nil {
		t.Fatal(err)
	}
	tree := marshalGit(t, repo, "rev-parse", "HEAD^{tree}")
	unrelated := marshalGit(t, repo, "commit-tree", tree, "-m", "unrelated")
	run, rev, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	run.Tasks[0].ResultCommit = unrelated
	if err := s.save(t.Context(), "run", run, rev); err != nil {
		t.Fatal(err)
	}
	_, err = s.Dispatch(t.Context(), "run", "a", "write")
	if !errors.Is(err, ErrStaleBase) {
		t.Fatalf("dispatch = %v, want stale_base", err)
	}
	receipt, ok := marshal.RefusalOf(err)
	if !ok || len(receipt.NextCommands) == 0 || !strings.Contains(err.Error(), unrelated) {
		t.Fatalf("refusal lacks recovery: %+v %v", receipt, err)
	}
	after, _, err := s.load(t.Context(), "run")
	if err != nil || after.Tasks[0].ResultCommit != unrelated || after.Tasks[0].State != marshal.Returned || after.Operation != nil {
		t.Fatalf("refusal changed the task: %+v %v", after.Tasks[0], err)
	}
	if got := marshalGit(t, repo, "rev-parse", priorResultRef("run", "a", unrelated)); got != unrelated {
		t.Fatalf("refused prior result was not pinned: %s", got)
	}
}

// A disagreement returns the task with both verdicts; the Marshal's accept
// never replaces the independent reviewer's failing verdict.
func TestDisagreementKeepsBothVerdicts(t *testing.T) {
	s, _ := reworkFixture(t)
	setCrossReview(s, marshal.VerdictReturn, "edge case missing")
	h := reworkAttempt(t, s)
	v, err := s.Review(t.Context(), "run", "a", knownCharge())
	if err != nil || v == marshal.VerdictAccept {
		t.Fatalf("review = %s %v, want a return", v, err)
	}
	run, _, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	task := run.Tasks[0]
	if task.State != marshal.Returned || task.ResultCommit != h.ResultCommit {
		t.Fatalf("task = %s %s", task.State, task.ResultCommit)
	}
	d := task.ReviewDisagreement
	if d == nil || d.Count != 1 || d.MarshalVerdict != marshal.VerdictAccept || d.IndependentVerdict != marshal.VerdictReturn || !strings.Contains(strings.Join(d.IndependentReasons, ";"), "edge case missing") {
		t.Fatalf("disagreement = %+v", d)
	}
	stored, err := s.Store.GetMarshalReview(t.Context(), "run", "a", 1)
	if err != nil || stored.Value.Verdict != marshal.VerdictAccept || stored.Value.Independent == nil || stored.Value.Independent.Verdict != marshal.VerdictReturn {
		t.Fatalf("stored review lost a verdict: %+v %v", stored.Value, err)
	}
	e := lastMarshalEvent(t, s, events.EventTypeMarshalTaskReturned)
	data, ok := e.Data["review_disagreement"].(map[string]any)
	if !ok || data["marshal_verdict"] != string(marshal.VerdictAccept) || data["independent_verdict"] != string(marshal.VerdictReturn) || data["next_action"] == "" {
		t.Fatalf("returned event = %v", e.Data)
	}
	if run.State == marshal.AwaitingUser {
		t.Fatalf("a first disagreement paused the run: %+v", run.Pause)
	}
}

// The same conflict on the next attempt stops automatic rework with one
// durable escalation carrying both verdicts and the owner's next action.
func TestRepeatedDisagreementStopsWithBothVerdicts(t *testing.T) {
	s, _ := reworkFixture(t)
	setMarshalReview(s, marshal.Review{Verdict: marshal.VerdictReturn, Reviewer: "marshal", Reasons: []string{"wrong file layout"}})
	for attempt := 1; attempt <= 2; attempt++ {
		reworkAttempt(t, s)
		if _, err := s.Review(t.Context(), "run", "a", knownCharge()); err != nil {
			t.Fatal(err)
		}
	}
	run, _, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	task := run.Tasks[0]
	if task.State != marshal.Escalated || task.EscalationCode != marshal.CodeReviewDisagreement || isPermanentEscalation(task) {
		t.Fatalf("task = %s %s", task.State, task.EscalationCode)
	}
	if run.State != marshal.AwaitingUser || run.Pause == nil || run.Pause.Code != marshal.CodeReviewDisagreement || run.Pause.Action != marshal.PauseRetryTask {
		t.Fatalf("pause = %s %+v", run.State, run.Pause)
	}
	if !strings.Contains(run.Pause.Reason, "wrong file layout") || !strings.Contains(run.Pause.Reason, string(marshal.VerdictAccept)) || len(run.Pause.Resolutions) == 0 {
		t.Fatalf("pause lacks both verdicts or next action: %+v", run.Pause)
	}
	if d := task.ReviewDisagreement; d == nil || d.Count != 2 || d.Attempt != 2 {
		t.Fatalf("disagreement = %+v", d)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		stored, err := s.Store.GetMarshalReview(t.Context(), "run", "a", attempt)
		if err != nil || stored.Value.Verdict != marshal.VerdictReturn || stored.Value.Independent == nil || stored.Value.Independent.Verdict != marshal.VerdictAccept {
			t.Fatalf("attempt %d review = %+v %v", attempt, stored.Value, err)
		}
	}
	if _, err := s.Dispatch(t.Context(), "run", "a", "write"); err == nil {
		t.Fatal("an escalated disagreement was retried automatically")
	}
	e := lastMarshalEvent(t, s, events.EventTypeMarshalEscalated)
	if _, ok := e.Data["review_disagreement"].(map[string]any); !ok {
		t.Fatalf("escalation event = %v", e.Data)
	}
}

// A serial Process 05 rework keeps the prior result pinned. The default
// edition stops for a new approved plan naming the retained result and both
// bases; the governed edition stays bound to its approved task.
func TestSerialReworkRecoveryKeepsPriorResultAndNeedsApprovedBinding(t *testing.T) {
	s, repo := reworkFixture(t)
	setMarshalReview(s, marshal.Review{Verdict: marshal.VerdictReturn, Reviewer: "marshal", Reasons: []string{"redo"}})
	h := reworkAttempt(t, s)
	run, rev, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	run.Process05Bound = true
	if err := s.save(t.Context(), "run", run, rev); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Review(t.Context(), "run", "a", knownCharge()); err != nil {
		t.Fatal(err)
	}
	if got := marshalGit(t, repo, "rev-parse", priorResultRef("run", "a", h.ResultCommit)); got != h.ResultCommit {
		t.Fatalf("prior result pin = %s", got)
	}
	run, _, err = s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if run.Tasks[0].ResultCommit != h.ResultCommit || run.Tasks[0].State != marshal.Returned {
		t.Fatalf("task = %s %s", run.Tasks[0].State, run.Tasks[0].ResultCommit)
	}
	if !isExtremeBuild {
		return
	}
	if run.Pause == nil || run.Pause.Code != marshal.CodeGovernedRework || run.Pause.Action != marshal.PauseUsePlan || !strings.Contains(run.Pause.Detail, h.ResultCommit) || !strings.Contains(run.Pause.Detail, run.BaseCommit) {
		t.Fatalf("pause = %+v", run.Pause)
	}
	if _, err := s.Dispatch(t.Context(), "run", "a", "write"); err == nil {
		t.Fatal("serial rework reran without a new approved binding")
	}
}

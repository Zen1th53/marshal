//go:build !governed

package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/verification"
)

// A verifier answer of the wrong shape is the verifier's fault, so it must
// not be reported as a project context change; a different HEAD still is.
func TestRequiredVerifierMalformedOutputIsNotAContextChange(t *testing.T) {
	head, tree := strings.Repeat("a", 40), strings.Repeat("b", 40)
	b := verification.Binding{ProjectID: "project", GoalID: "goal", GoalRevision: 1, PlanID: "plan", PlanVersion: 1, RunID: "run", RunVersion: 1, TreeDigest: strings.Repeat("c", 64), EnvironmentDigest: "environment"}
	goal := model.GoalContract{SuccessCriteria: []string{"Add numbers"}}
	run := marshal.Run{Tasks: []marshal.Task{{PlanTaskID: "one", Criteria: goal.SuccessCriteria, Checks: []marshal.Check{{Command: "python3 -m unittest", Criteria: goal.SuccessCriteria}}}}}
	observed := verification.Session{RequiredChecks: map[string]verification.Status{"one#0": verification.StatusPass}, RuntimeProof: &verification.RuntimeProof{Head: head, Tree: tree, Checks: []verification.RuntimeCheckProof{{ID: "one#0", Task: "one", Head: head, Tree: tree, Command: "python3 -m unittest", Criteria: goal.SuccessCriteria}}}}
	initial, criteria, err := requiredVerifierSkeleton(goal, run, observed, b, "verification", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	capture := &requiredMarshalTurnCapture{consumed: true, provider: "claude", observedModel: "host-model", session: "host-session", startedAt: time.Now().UTC(), finishedAt: time.Now().UTC()}
	capture.preparation.head, capture.preparation.tree = head, b.TreeDigest
	good := func() requiredVerifierResult {
		return requiredVerifierResult{Head: head, Assessments: []requiredVerifierAssessment{{ID: criteria[0].ID, Status: verification.StatusPass, Reason: "The add test passes", CheckIDs: []string{"one#0"}}}}
	}
	for name, mutate := range map[string]func(*requiredVerifierResult){
		"no assessments":  func(r *requiredVerifierResult) { r.Assessments = nil },
		"blank reason":    func(r *requiredVerifierResult) { r.Assessments[0].Reason = " " },
		"foreign check":   func(r *requiredVerifierResult) { r.Assessments[0].CheckIDs = []string{"two#0"} },
		"no check ids":    func(r *requiredVerifierResult) { r.Assessments[0].CheckIDs = nil },
		"foreign id":      func(r *requiredVerifierResult) { r.Assessments[0].ID = "foreign" },
		"duplicate check": func(r *requiredVerifierResult) { r.Assessments[0].CheckIDs = []string{"one#0", "one#0"} },
	} {
		t.Run(name, func(t *testing.T) {
			result := good()
			mutate(&result)
			_, _, _, err := requiredVerifierEvidence(initial, criteria, result, capture)
			if !errors.Is(err, errRequiredVerifierOutput) || errors.Is(err, projectcontext.ErrChanged) {
				t.Fatalf("malformed verifier output reported as %v", err)
			}
		})
	}
	wrongHead := good()
	wrongHead.Head = tree
	if _, _, _, err := requiredVerifierEvidence(initial, criteria, wrongHead, capture); !errors.Is(err, projectcontext.ErrChanged) {
		t.Fatalf("a different verified HEAD was not a context change: %v", err)
	}
}

func TestRequiredSourceChangeCauseKeepsTheRefusal(t *testing.T) {
	if err := requiredSourceChangeCause(projectcontext.ErrReviewPending); !errors.Is(err, projectcontext.ErrReviewPending) {
		t.Fatalf("review pending lost: %v", err)
	}
	if err := requiredSourceChangeCause(projectcontext.ErrChanged); !errors.Is(err, projectcontext.ErrChanged) {
		t.Fatalf("change lost: %v", err)
	}
	err := requiredSourceChangeCause(errors.New("git status: exit 128"))
	if !errors.Is(err, projectcontext.ErrChanged) || !strings.Contains(err.Error(), "exit 128") {
		t.Fatalf("other failure not named as a change with its cause: %v", err)
	}
}

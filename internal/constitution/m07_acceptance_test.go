package constitution_test

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/marshal"
)

func taskCase() (constitution.Envelope, constitution.RuntimeState, constitution.TaskAcceptance) {
	env, state := cleanEnvelope(), cleanState()
	input := constitution.TaskAcceptance{Mode: marshal.AcceptMarshal, MarshalVerdictAccept: true,
		Executor: "worker", Reviewer: "marshal", ResultCommit: "abc", EvidenceCommit: "abc",
		CriteriaTotal: 2, CriteriaMet: 2}
	return env, state, input
}

func taskVerdict(env constitution.Envelope, state constitution.RuntimeState, input constitution.TaskAcceptance) constitution.Verdict {
	return constitution.EvaluateTaskAcceptance(constitution.Default(), env, state, input)
}

func TestM07FailingCriterionDeniesMarshalAccept(t *testing.T) {
	e, s, i := taskCase()
	i.CriteriaMet--
	if v := taskVerdict(e, s, i); !v.Blocked() || !hasReason(v, constitution.ReasonCompletionNotAuthorized) {
		t.Fatalf("unmet criterion: %+v", v)
	}
}

func TestM07ReviewerExecutorInvariant(t *testing.T) {
	e, s, i := taskCase()
	i.Reviewer = i.Executor
	if v := taskVerdict(e, s, i); !hasInvariant(v, constitution.InvNoSelfAcceptance) {
		t.Fatalf("self review: %+v", v)
	}
}

func TestM07MarshalExecutorCannotReview(t *testing.T) {
	e, s, i := taskCase()
	i.Executor, i.Reviewer = "marshal", "marshal"
	if v := taskVerdict(e, s, i); !hasInvariant(v, constitution.InvNoSelfAcceptance) {
		t.Fatalf("marshal self review: %+v", v)
	}
}

func TestM07UltraRequiresCrossReview(t *testing.T) {
	e, s, i := taskCase()
	s.EntitlementValid = true
	if v := taskVerdict(e, s, i); !v.Outcome.Permits() {
		t.Fatalf("standard: %+v", v)
	}
	e.Mode = constitution.ModeUltra
	if v := taskVerdict(e, s, i); !v.Blocked() {
		t.Fatalf("ultra without review: %+v", v)
	}
	i.IndependentReviewDone = true
	if v := taskVerdict(e, s, i); !v.Outcome.Permits() {
		t.Fatalf("ultra with review: %+v", v)
	}
}

func TestM07EvidenceBoundToResultCommit(t *testing.T) {
	e, s, i := taskCase()
	i.EvidenceCommit = "older"
	if v := taskVerdict(e, s, i); !hasInvariant(v, constitution.InvEvidenceFreshness) {
		t.Fatalf("stale evidence: %+v", v)
	}
}

func TestM07UserModeRequiresPerson(t *testing.T) {
	e, s, i := taskCase()
	i.Mode = marshal.AcceptUser
	if v := taskVerdict(e, s, i); v.Outcome.Permits() {
		t.Fatalf("marshal alone accepted: %+v", v)
	}
	i.UserApprovalActor = "user"
	if v := taskVerdict(e, s, i); !v.Outcome.Permits() {
		t.Fatalf("user approval refused: %+v", v)
	}
}

func TestM07MarshalVerdictNeverSetsApprovalActor(t *testing.T) {
	for _, mode := range []marshal.AcceptanceMode{marshal.AcceptMarshal, marshal.AcceptMarshalThenUser, marshal.AcceptUser} {
		t.Run(string(mode), func(t *testing.T) {
			e, s, i := taskCase()
			i.Mode = mode
			i.UserApprovalActor = "user"
			// A model identity as the approving actor must never trigger the
			// self-approval invariant for a runtime acceptance decision.
			e.Actor = "marshal"
			if v := taskVerdict(e, s, i); hasInvariant(v, constitution.InvNoSelfApproval) {
				t.Fatalf("model verdict became approval: %+v", v)
			}
		})
	}
}

func TestM07CloseRequiresCurrentUserAuthorization(t *testing.T) {
	e, s, _ := taskCase()
	e.Reversibility = constitution.ReversibilityUnknown
	e.CheckpointID = "checkpoint-1"
	run := marshal.Run{Repository: "repo", BaseCommit: "base", TargetRef: "refs/heads/main", ApprovalScopeDigest: "scope", Settings: marshal.Settings{AcceptanceMode: marshal.AcceptMarshal}}
	close := func() constitution.Verdict {
		return constitution.EvaluateMarshalClose(constitution.Default(), e, s, run, "")
	}
	if v := close(); v.Outcome.Permits() {
		t.Fatalf("no authorization: %+v", v)
	}
	run.CloseAuthorization = &marshal.CloseAuthorization{User: "user", ApprovalScopeDigest: "scope", Repository: "repo", BaseCommit: "base", TargetRef: "refs/heads/main"}
	if v := close(); !v.Outcome.Permits() {
		t.Fatalf("standing authorization: %+v", v)
	}
	run.CloseAuthorization.Voided = true
	if v := close(); v.Outcome.Permits() {
		t.Fatalf("voided authorization: %+v", v)
	}
}

func TestM07CloseRequiresCheckpointOrIrreversibilityStatement(t *testing.T) {
	e, s, _ := taskCase()
	e.Reversibility = constitution.ReversibilityUnknown
	run := marshal.Run{Repository: "repo", BaseCommit: "base", TargetRef: "refs/heads/main", ApprovalScopeDigest: "scope", Settings: marshal.Settings{AcceptanceMode: marshal.AcceptMarshal},
		CloseAuthorization: &marshal.CloseAuthorization{User: "user", ApprovalScopeDigest: "scope", Repository: "repo", BaseCommit: "base", TargetRef: "refs/heads/main"}}
	v := constitution.EvaluateMarshalClose(constitution.Default(), e, s, run, "")
	if v.Outcome != constitution.OutcomeRequireApproval || !hasInvariant(v, constitution.InvRollbackTruthful) {
		t.Fatalf("close without checkpoint or disclosure: %+v", v)
	}
}

func TestM07CloseRequiresFreshIntegratedEvidence(t *testing.T) {
	e, s, _ := taskCase()
	e.Reversibility = constitution.ReversibilityUnknown
	e.CheckpointID = "checkpoint-1"
	run := marshal.Run{Repository: "repo", BaseCommit: "base", TargetRef: "refs/heads/main", ApprovalScopeDigest: "scope", Settings: marshal.Settings{AcceptanceMode: marshal.AcceptMarshal},
		CloseAuthorization: &marshal.CloseAuthorization{User: "user", ApprovalScopeDigest: "scope", Repository: "repo", BaseCommit: "base", TargetRef: "refs/heads/main"}}
	close := func() constitution.Verdict {
		return constitution.EvaluateMarshalClose(constitution.Default(), e, s, run, "")
	}
	if v := close(); !v.Outcome.Permits() {
		t.Fatalf("authorized close with fresh evidence: %+v", v)
	}
	s.EvidenceFresh = false
	if v := close(); v.Outcome.Permits() || v.Reason != constitution.ReasonEvidenceStale {
		t.Fatalf("stale integrated evidence: %+v", v)
	}
	s.EvidencePresent = false
	if v := close(); v.Outcome.Permits() || v.Reason != constitution.ReasonEvidenceInsufficient {
		t.Fatalf("absent integrated evidence: %+v", v)
	}
}

func hasInvariant(v constitution.Verdict, id constitution.InvariantID) bool {
	for _, f := range v.Findings {
		if f.Invariant == id {
			return true
		}
	}
	return false
}

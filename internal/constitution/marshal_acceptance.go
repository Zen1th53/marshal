package constitution

import "github.com/Zen1th53/marshal/internal/marshal"

// TaskAcceptance holds runtime observations. The verdict is a recorded claim,
// while identities and check results must come from the driver and store.
type TaskAcceptance struct {
	Mode                 marshal.AcceptanceMode
	MarshalVerdictAccept bool
	// UserApprovalActor comes from a person's approval action recorded by MARSHAL.
	UserApprovalActor     string
	Executor              string
	Reviewer              string
	ResultCommit          string
	EvidenceCommit        string
	CriteriaTotal         int
	CriteriaMet           int
	IndependentReviewDone bool
}

// EvaluateTaskAcceptance decides a task hand-in through the completion gate.
func EvaluateTaskAcceptance(registry *Registry, envelope Envelope, state RuntimeState, input TaskAcceptance) Verdict {
	envelope.Domain = DomainCompletion
	state.TaskExecutor, state.TaskReviewer = input.Executor, input.Reviewer
	state.ResultCommit, state.EvidenceCommit = input.ResultCommit, input.EvidenceCommit
	state.AcceptanceCriteriaTotal, state.AcceptanceCriteriaMet = input.CriteriaTotal, input.CriteriaMet
	state.EvidencePresent = input.EvidenceCommit != ""
	state.EvidenceFresh = input.ResultCommit != "" && input.EvidenceCommit == input.ResultCommit
	state.IndependentReviewRequired = envelope.Mode == ModeUltra
	state.IndependentReviewDone = input.IndependentReviewDone
	state.ApprovalActor = ""
	if input.Mode == marshal.AcceptUser {
		state.ApprovalActor = input.UserApprovalActor
	}
	verdict := Evaluate(registry, envelope, state, nil)
	if verdict.Outcome != OutcomeAllow {
		return verdict
	}
	if input.Mode != marshal.AcceptMarshal && input.Mode != marshal.AcceptMarshalThenUser && input.Mode != marshal.AcceptUser ||
		(input.Mode == marshal.AcceptUser && state.ApprovalActor == "") ||
		(input.Mode != marshal.AcceptUser && !input.MarshalVerdictAccept) ||
		input.Executor == "" || input.Reviewer == "" {
		verdict.Outcome, verdict.Reason = OutcomeBlock, ReasonCompletionNotAuthorized
	}
	return verdict
}

// EvaluateMarshalClose requires current user consent before the target branch moves.
// userApprovalActor must come from a person's approval action recorded by MARSHAL.
func EvaluateMarshalClose(registry *Registry, envelope Envelope, state RuntimeState, run marshal.Run, userApprovalActor string) Verdict {
	envelope.Domain = DomainProjectMutation
	state.ApprovalActor = ""
	if run.Settings.AcceptanceMode == marshal.AcceptMarshal && run.ValidCloseAuthorization() {
		state.ApprovalActor = run.CloseAuthorization.User
	} else if run.Settings.AcceptanceMode != marshal.AcceptMarshal {
		state.ApprovalActor = userApprovalActor
	}
	verdict := Evaluate(registry, envelope, state, nil)
	// The project mutation domain does not generally require evidence, but a
	// close must be verified against the integrated result.
	if verdict.Outcome == OutcomeAllow && (!state.EvidencePresent || !state.EvidenceFresh) {
		verdict.Outcome = OutcomeBlock
		if !state.EvidencePresent {
			verdict.Reason = ReasonEvidenceInsufficient
		} else {
			verdict.Reason = ReasonEvidenceStale
		}
	}
	if verdict.Outcome == OutcomeAllow && state.ApprovalActor == "" {
		verdict.Outcome, verdict.Reason = OutcomeRequireApproval, ReasonApprovalRequired
	}
	return verdict
}

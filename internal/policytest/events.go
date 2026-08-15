package policytest

import (
	"math"

	"github.com/Zen1th53/marshal/internal/policy"
)

// EventType is the closed T49 audit vocabulary. Case-level events are
// reserved for the later runner; A05 emits only lifecycle facts created here.
type EventType string

const (
	EventStarted    EventType = "policytest.started"
	EventCasePassed EventType = "policytest.case.passed"
	EventCaseFailed EventType = "policytest.case.failed"
	EventFinished   EventType = "policytest.finished"
)

func (e EventType) Valid() bool {
	switch e {
	case EventStarted, EventCasePassed, EventCaseFailed, EventFinished:
		return true
	default:
		return false
	}
}

// EventFact contains only bounded, canonical facts. It deliberately excludes
// fixtures, evaluator output, authorization objects, and backend details.
type EventFact struct {
	Type           EventType
	RunID          string
	CaseID         CaseID
	PolicyID       policy.PolicyID
	PolicyVersion  policy.PolicyVersion
	PolicyDigest   policy.PolicyDigest
	Generation     uint64
	TestFileDigest policy.PolicyDigest
	PreviousState  RunState
	TargetState    RunState
	Action         Action
	SubjectID      string
	SessionID      string
	TaskID         string
	ChangeID       string
	Result         string
	ReasonCode     policy.ErrorCode
}

func (f EventFact) Validate() error {
	if !f.Type.Valid() || !validBoundedID(f.RunID, maxSuiteID) || !validBoundedID(string(f.PolicyID), maxSuiteID) ||
		f.PolicyVersion <= 0 || f.Generation > math.MaxInt64 || !f.PreviousState.Valid() || !f.TargetState.Valid() ||
		!f.Action.Valid() || !validBoundedID(f.SubjectID, maxSuiteID) || !validBoundedID(f.SessionID, maxSuiteID) ||
		!validBoundedID(f.TaskID, maxSuiteID) || !validBoundedID(f.ChangeID, maxSuiteID) ||
		f.PolicyDigest.Validate() != nil || f.TestFileDigest.Validate() != nil {
		return policy.ErrAuthorizationInvalid
	}
	if f.Result != "allowed" && f.Result != "passed" && f.Result != "failed" {
		return policy.ErrAuthorizationInvalid
	}
	if (f.Type == EventCasePassed || f.Type == EventCaseFailed) && !validBoundedID(string(f.CaseID), maxCaseID) {
		return policy.ErrAuthorizationInvalid
	}
	if (f.Type != EventCasePassed && f.Type != EventCaseFailed) && f.CaseID != "" {
		return policy.ErrAuthorizationInvalid
	}
	if f.Type == EventCasePassed && f.Result != "passed" || f.Type == EventCaseFailed && f.Result != "failed" {
		return policy.ErrAuthorizationInvalid
	}
	if (f.Type == EventCasePassed || f.Type == EventCaseFailed) && f.ReasonCode == "" {
		return policy.ErrAuthorizationInvalid
	}
	if f.Type != EventCasePassed && f.Type != EventCaseFailed && f.ReasonCode != policy.CodeAuthorizationAllowed && f.ReasonCode != policy.CodeAuthorizationDenied {
		return policy.ErrAuthorizationInvalid
	}
	return nil
}

func (f EventFact) Data() map[string]any {
	return map[string]any{
		"run_id": string(f.RunID), "case_id": string(f.CaseID), "policy_id": string(f.PolicyID), "policy_version": int64(f.PolicyVersion),
		"policy_digest": string(f.PolicyDigest), "generation": f.Generation, "test_file_digest": string(f.TestFileDigest),
		"previous_state": string(f.PreviousState), "target_state": string(f.TargetState), "action": string(f.Action),
		"subject_id": f.SubjectID, "session_id": f.SessionID, "task_id": f.TaskID, "change_id": f.ChangeID,
		"result": f.Result, "reason_code": string(f.ReasonCode),
	}
}

func EventForTransition(request AuthorizationRequest) (EventFact, bool) {
	fact := EventFact{
		RunID: request.RunID, PolicyID: request.PolicyID, PolicyVersion: request.Binding.Version,
		PolicyDigest: request.Binding.Digest, Generation: request.Binding.Generation,
		TestFileDigest: request.TestFileDigest, PreviousState: request.ExpectedState,
		TargetState: request.TargetState, Action: request.Action, SubjectID: request.SubjectID,
		SessionID: request.SessionID, TaskID: request.TaskID, ChangeID: request.ChangeID,
		Result: "allowed", ReasonCode: policy.CodeAuthorizationAllowed,
	}
	switch {
	case request.TargetState == StateValidated || request.TargetState == StateExecuted:
		fact.Type = EventStarted
	case request.ExpectedState == StateExecuted && request.TargetState == StatePassed:
		fact.Type, fact.Result = EventFinished, "passed"
	case request.ExpectedState == StateExecuted && request.TargetState == StateFailed:
		fact.Type, fact.Result = EventFinished, "failed"
	default:
		return EventFact{}, false
	}
	return fact, true
}

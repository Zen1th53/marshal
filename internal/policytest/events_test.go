package policytest

import (
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/policy"
)

func validEventFact() EventFact {
	digest := policy.PolicyDigest("sha256:" + strings.Repeat("a", 64))
	return EventFact{
		Type: EventStarted, RunID: "run-1", PolicyID: "policy-1", PolicyVersion: 1,
		PolicyDigest: digest, Generation: 1, TestFileDigest: digest,
		PreviousState: StateLoaded, TargetState: StateValidated, Action: ActionTransition,
		SubjectID: "subject-1", SessionID: "session-1", TaskID: "task-1", ChangeID: "change-1",
		Result: "allowed", ReasonCode: policy.CodeAuthorizationAllowed,
	}
}

func TestEventFactRejectsUnknownVocabulary(t *testing.T) {
	fact := validEventFact()
	fact.Type = EventType("policytest.unknown")
	if err := fact.Validate(); !errors.Is(err, policy.ErrAuthorizationInvalid) {
		t.Fatalf("unknown event error=%v", err)
	}
	fact = validEventFact()
	fact.ReasonCode = policy.ErrorCode("POLICY_UNKNOWN_REASON")
	if err := fact.Validate(); !errors.Is(err, policy.ErrAuthorizationInvalid) {
		t.Fatalf("unknown reason error=%v", err)
	}
}

func TestEventFactRejectsUnboundedFields(t *testing.T) {
	fact := validEventFact()
	fact.RunID = strings.Repeat("x", maxSuiteID+1)
	if err := fact.Validate(); !errors.Is(err, policy.ErrAuthorizationInvalid) {
		t.Fatalf("oversized run error=%v", err)
	}
}

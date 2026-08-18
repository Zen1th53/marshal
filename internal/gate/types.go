package gate

import (
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/policy"
)

type GatePoint string

const (
	GatePointPreExecution GatePoint = "pre-execution"
	GatePointPreCommit    GatePoint = "pre-commit"
	GatePointPrePush      GatePoint = "pre-push"
	GatePointPreMerge     GatePoint = "pre-merge"
	GatePointPreRelease   GatePoint = "pre-release"
)

type CheckStatus string

const (
	CheckStatusPass    CheckStatus = "PASS"
	CheckStatusFail    CheckStatus = "FAIL"
	CheckStatusBlocked CheckStatus = "BLOCKED"
	CheckStatusMissing CheckStatus = "MISSING"
	CheckStatusInvalid CheckStatus = "INVALID"
)

type DecisionState string

const (
	DecisionStateRequested   DecisionState = "requested"
	DecisionStateEvaluating  DecisionState = "evaluating"
	DecisionStateAllowed     DecisionState = "allowed"
	DecisionStateDenied      DecisionState = "denied"
	DecisionStateBlocked     DecisionState = "blocked"
	DecisionStateConsumed    DecisionState = "consumed"
	DecisionStateInvalidated DecisionState = "invalidated"
)

type ErrorCode string

const (
	CodeAllowed              ErrorCode = "GATE_ALLOWED"
	CodeRequiredCheckMissing ErrorCode = "GATE_REQUIRED_CHECK_MISSING"
	CodeStaleEvidence        ErrorCode = "GATE_STALE_EVIDENCE"
	CodePolicyDeny           ErrorCode = "GATE_POLICY_DENY"
	CodeQuorumUnmet          ErrorCode = "GATE_QUORUM_UNMET"
	CodeUnknownCheck         ErrorCode = "GATE_UNKNOWN_CHECK"
	CodeUnknownGatePoint     ErrorCode = "GATE_UNKNOWN_POINT"
	CodeInvalidCheckStatus   ErrorCode = "GATE_INVALID_CHECK_STATUS"
	CodeInvalidDecision      ErrorCode = "GATE_INVALID_DECISION"
)

type Error struct {
	Code    ErrorCode
	message string
}

func (e *Error) Error() string { return e.message }
func (e *Error) Is(target error) bool {
	other, ok := target.(*Error)
	return ok && e != nil && other != nil && e.Code == other.Code
}

var (
	ErrRequiredCheckMissing = &Error{Code: CodeRequiredCheckMissing, message: "required gate check is missing"}
	ErrStaleEvidence        = &Error{Code: CodeStaleEvidence, message: "gate evidence is stale"}
	ErrPolicyDeny           = &Error{Code: CodePolicyDeny, message: "gate policy denied the operation"}
	ErrQuorumUnmet          = &Error{Code: CodeQuorumUnmet, message: "gate quorum is unmet"}
	ErrUnknownCheck         = &Error{Code: CodeUnknownCheck, message: "gate check is unknown"}
	ErrUnknownGatePoint     = &Error{Code: CodeUnknownGatePoint, message: "gate point is unknown"}
	ErrInvalidCheckStatus   = &Error{Code: CodeInvalidCheckStatus, message: "gate check status is invalid"}
	ErrInvalidDecision      = &Error{Code: CodeInvalidDecision, message: "gate decision is invalid"}
)

type CheckResult struct {
	CheckID    string      `json:"check_id"`
	Status     CheckStatus `json:"status"`
	EvidenceID string      `json:"evidence_id,omitempty"`
	Reason     ErrorCode   `json:"reason"`
}

type Decision struct {
	DecisionID   string
	Point        GatePoint
	Subject      string
	Resource     string
	Allowed      bool
	State        DecisionState
	Checks       []CheckResult
	PolicyIDs    []string
	PolicyDigest policy.PolicyDigest
	ChangeDigest string
	CreatedAt    time.Time
}

func (d Decision) Validate() error {
	if strings.TrimSpace(d.DecisionID) == "" || strings.TrimSpace(d.Subject) == "" || strings.TrimSpace(d.Resource) == "" || !validGatePoint(d.Point) || d.CreatedAt.IsZero() {
		if !validGatePoint(d.Point) {
			return ErrUnknownGatePoint
		}
		return ErrInvalidDecision
	}
	if d.State != "" && !validDecisionState(d.State) {
		return ErrInvalidDecision
	}
	if err := d.PolicyDigest.Validate(); err != nil {
		return ErrInvalidDecision
	}
	if d.ChangeDigest != "" && len(d.ChangeDigest) != len("sha256:")+64 {
		return ErrInvalidDecision
	}
	if len(d.Checks) == 0 {
		return ErrRequiredCheckMissing
	}
	for _, check := range d.Checks {
		if strings.TrimSpace(check.CheckID) == "" {
			return ErrUnknownCheck
		}
		if !validCheckStatus(check.Status) {
			return ErrInvalidCheckStatus
		}
	}
	return nil
}

func validDecisionState(state DecisionState) bool {
	switch state {
	case DecisionStateRequested, DecisionStateEvaluating, DecisionStateAllowed, DecisionStateDenied, DecisionStateBlocked, DecisionStateConsumed, DecisionStateInvalidated:
		return true
	default:
		return false
	}
}

func ValidDecisionTransition(from, to DecisionState) bool {
	return (from == DecisionStateRequested && to == DecisionStateEvaluating) ||
		(from == DecisionStateEvaluating && (to == DecisionStateAllowed || to == DecisionStateDenied || to == DecisionStateBlocked)) ||
		((from == DecisionStateAllowed || from == DecisionStateDenied || from == DecisionStateBlocked) && (to == DecisionStateConsumed || to == DecisionStateInvalidated))
}

func validGatePoint(point GatePoint) bool {
	switch point {
	case GatePointPreExecution, GatePointPreCommit, GatePointPrePush, GatePointPreMerge, GatePointPreRelease:
		return true
	default:
		return false
	}
}

func validCheckStatus(status CheckStatus) bool {
	switch status {
	case CheckStatusPass, CheckStatusFail, CheckStatusBlocked, CheckStatusMissing, CheckStatusInvalid:
		return true
	default:
		return false
	}
}

package policy

// PolicyEventType is the closed event vocabulary owned by T48 A05.
type PolicyEventType string

const (
	EventPolicyLoaded           PolicyEventType = "policy.loaded"
	EventPolicyValidationFailed PolicyEventType = "policy.validation.failed"
	EventPolicyActivated        PolicyEventType = "policy.activated"
	EventPolicyDecisionAllowed  PolicyEventType = "policy.decision.allowed"
	EventPolicyDecisionDenied   PolicyEventType = "policy.decision.denied"
)

func (e PolicyEventType) Valid() bool {
	switch e {
	case EventPolicyLoaded, EventPolicyValidationFailed, EventPolicyActivated,
		EventPolicyDecisionAllowed, EventPolicyDecisionDenied:
		return true
	default:
		return false
	}
}

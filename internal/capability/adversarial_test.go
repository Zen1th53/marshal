package capability

import "testing"

func FuzzCapabilityScopeValidation(f *testing.F) {
	f.Add("/workspace", "read")
	f.Add("../escape", "write")
	f.Add("", "")
	f.Fuzz(func(t *testing.T, resource, action string) {
		scope := Scope{Resource: resource, Actions: []string{action}}
		if err := scope.Validate(); err != nil && err != ErrInvalidScope {
			t.Fatalf("unexpected validation error: %v", err)
		}
	})
}

func FuzzCapabilityDecisionValidation(f *testing.F) {
	f.Add(true, string(ReasonAllowed), "cap-1")
	f.Add(false, string(ReasonDenied), "")
	f.Add(true, "UNKNOWN", "")
	f.Fuzz(func(t *testing.T, allowed bool, reason, grantID string) {
		decision := Decision{Allowed: allowed, Reason: Reason(reason), GrantID: grantID}
		_ = decision.Validate()
	})
}

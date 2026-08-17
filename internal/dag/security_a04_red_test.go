package dag

import "testing"

func TestA04AuthorizationBoundaryExists(t *testing.T) {
	if (AuthorizationRequest{}).valid() {
		t.Fatal("empty authorization request must fail closed")
	}
}

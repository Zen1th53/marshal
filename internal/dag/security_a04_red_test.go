package dag

import "testing"

func TestA04AuthorizationBoundaryExists(t *testing.T) {
	if NewAuthorizer(nil) == nil {
		t.Fatal("expected authorization boundary")
	}
}

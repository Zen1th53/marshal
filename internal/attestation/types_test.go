package attestation

import "testing"

func TestReportStruct(t *testing.T) {
	r := Report{NodeID: "n-1", MarshalVersion: "v1.0", Nonce: "nonce-1"}
	if r.NodeID != "n-1" {
		t.Fatalf("expected n-1, got %s", r.NodeID)
	}
}

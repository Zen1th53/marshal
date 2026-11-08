//go:build governed

package app

import (
	"encoding/json"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/verification"
	"strings"
	"testing"
)

func TestCommunityVerifierRuntimeProofRemainsAbsent(t *testing.T) {
	s := &MarshalService{}
	proof, err := s.beginMarshalRuntimeProof(t.Context(), "missing", marshal.Run{}, "head", "tree", true)
	if err != nil || proof != nil {
		t.Fatalf("community semantics changed %v %v", proof, err)
	}
	session := verification.Session{}
	data, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "runtime_proof") || marshalRuntimeProofInstruction(session) != "" {
		t.Fatal("community verifier input changed")
	}
}

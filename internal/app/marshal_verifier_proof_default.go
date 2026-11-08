//go:build governed

package app

import (
	"context"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/verification"
)

func (s *MarshalService) beginMarshalRuntimeProof(context.Context, string, marshal.Run, string, string, bool) (*verification.RuntimeProof, error) {
	return nil, nil
}
func (s *MarshalService) appendMarshalRuntimeCheck(context.Context, *verification.RuntimeProof, string, marshal.Task, marshal.Check, *marshal.CommandRecord, error) error {
	return nil
}
func finishMarshalRuntimeProof(context.Context, string, *verification.RuntimeProof, map[string]verification.Status) error {
	return nil
}

func marshalRuntimeProofInstruction(verification.Session) string { return "" }
func (s *MarshalService) validateVerifierRuntimeProof(context.Context, marshal.Run, string, verification.Session) error {
	return nil
}

// verifierSchemaReadsFiles reports a verification turn whose provider may use
// read-only tools to inspect the repository and its evidence files.
func verifierSchemaReadsFiles(schema string) bool { return schema == marshalVerifySchema }

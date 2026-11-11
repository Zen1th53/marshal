//go:build governed

package app

import (
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// Community keeps human-only acceptance: an owner chat message never lets
// the model accept a contract or an architecture.
func TestCommunityOwnerChatInstructionCannotAuthorizeModelAcceptance(t *testing.T) {
	rt := browserContextRuntime(t)
	if _, _, err := rt.RecordProjectContractOperatorInstruction(t.Context(), "chat", "launch", "operator:1", "accept architecture", time.Now()); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("Community recorded a contract instruction: %v", err)
	}
}

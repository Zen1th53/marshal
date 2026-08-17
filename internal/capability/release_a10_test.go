package capability

import (
	"errors"
	"testing"
)

func TestCapabilityReleaseContractHasStableKindsAndReasons(t *testing.T) {
	kinds := []CapabilityKind{KindFilesystemRead, KindFilesystemWrite, KindShellExec, KindGitCommit, KindGitPush, KindNetworkEgress, KindSecretUse, KindMCPCall, KindDeployExecute}
	for _, kind := range kinds {
		if !knownKind(kind) {
			t.Fatalf("known capability kind rejected: %s", kind)
		}
	}
	for _, code := range []ErrorCode{CodeInvalidScope, CodeDenied, CodeExpired, CodeRevoked, CodeSubjectMismatch, CodeTaskMismatch} {
		if errors.Is(NewError(code, "secret detail"), ErrInvalidScope) && code != CodeInvalidScope {
			t.Fatalf("error code %s collapsed into invalid scope", code)
		}
	}
}

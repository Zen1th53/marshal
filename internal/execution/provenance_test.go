package execution

import (
	"errors"
	"testing"
)

func TestProvenance_ValidateAllowedBinary_ShadowingProtection(t *testing.T) {
	// Valid system binary
	if err := ValidateAllowedBinary("/usr/bin/git", nil); err != nil {
		t.Errorf("expected /usr/bin/git to be allowed: %v", err)
	}

	// Blocked: /tmp execution
	err := ValidateAllowedBinary("/tmp/malicious_git", nil)
	if err == nil {
		t.Errorf("expected error for /tmp binary, got nil")
	}
	if !errors.Is(err, ErrUnauthorizedWorker) {
		t.Errorf("expected ErrUnauthorizedWorker, got: %v", err)
	}

	// Blocked: ./ local directory shadowing
	err = ValidateAllowedBinary("./git", nil)
	if err == nil {
		t.Errorf("expected error for relative ./ binary, got nil")
	}
	if !errors.Is(err, ErrUnauthorizedWorker) {
		t.Errorf("expected ErrUnauthorizedWorker, got: %v", err)
	}
}

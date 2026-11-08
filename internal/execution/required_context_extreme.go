//go:build !governed

package execution

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/projectcontext"
)

func requiredConstraintDigest(pkg ConstraintPackage) (string, bool) {
	if pkg.RequiredContext == nil {
		return "", false
	}
	pkg.Digest = ""
	// Canonical JSON binds every field, including capability limits and
	// expected outputs omitted by the historical nil-context digest.
	return projectcontext.Digest("marshal.required.constraint-package.v2", pkg), true
}

func verifyRequiredConstraintBinding(pkg ConstraintPackage) error {
	b := pkg.RequiredContext
	if b == nil {
		return nil
	}
	if b.FormatVersion != projectcontext.FormatVersion || b.Project.CanonicalID == "" || b.Project.StorageID == "" || b.Contract.ID == "" || b.Contract.Revision < 1 || b.GoalID == "" || b.GoalRevision < 1 || b.PlanID == "" || b.PlanVersion < 1 || b.RunID == "" || b.RunVersion < 1 || b.TaskID != pkg.TaskID || b.CanonicalTaskID == "" || b.Principal == "" || b.Session == "" || b.Provider == "" || b.Model == "" || b.Attempt < 1 || b.Base == "" || b.Tree == "" {
		return fmt.Errorf("%w: incomplete required context binding", ErrConstraintViolation)
	}
	for _, digest := range []string{b.Contract.Digest, b.InventoryDigest, b.ManifestDigest, b.RequiredStateDigest} {
		if !requiredDigestHex(digest, 32) {
			return fmt.Errorf("%w: incomplete required context digest", ErrConstraintViolation)
		}
	}
	// plan.ApprovalScope.Digest historically carries 16 bytes of SHA-256.
	// Preserve its actual canonical format; the complete scope is still bound
	// by this package's full v2 digest and revalidated at runtime admission.
	if !requiredDigestHex(b.ApprovalScopeDigest, 16) {
		return fmt.Errorf("%w: incomplete required approval scope digest", ErrConstraintViolation)
	}
	return nil
}

func requiredDigestHex(digest string, bytes int) bool {
	if !strings.HasPrefix(digest, "sha256:") || len(digest) != 7+bytes*2 {
		return false
	}
	raw, err := hex.DecodeString(digest[7:])
	return err == nil && len(raw) == bytes && strings.ToLower(digest) == digest
}

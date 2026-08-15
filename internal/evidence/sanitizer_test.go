package evidence

import (
	"context"
	"errors"
	"testing"
)

func TestStrictSanitizerRejectsConfiguredSecretLiteral(t *testing.T) {
	const secret = "MARSHAL_TEST_SECRET_T06_A02_7c8b"
	sanitizer := NewStrictSanitizer(SanitizerConfig{LiteralSecrets: []string{secret}})
	_, err := sanitizer.SanitizeNode(context.Background(), Node{
		ID: "EVIDENCE-001", Type: NodeTypeClaim,
		Digest:   "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Metadata: map[string]string{"detail": secret},
	})
	if !errors.Is(err, ErrSecretRejected) {
		t.Fatalf("SanitizeNode() error = %v, want %v", err, ErrSecretRejected)
	}
}

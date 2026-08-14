package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestA07SecretMarkersDoNotReachSQLiteOrPublicErrors(t *testing.T) {
	const metadataMarker = "MARSHAL_TEST_SECRET_T06_A07_METADATA_8c2a"
	const authMarker = "MARSHAL_TEST_SECRET_T06_A07_AUTH_6d91"
	const auditMarker = "MARSHAL_TEST_SECRET_T06_A07_AUDIT_4c8e"
	path := filepath.Join(t.TempDir(), "state.db")
	st, err := OpenWithSecurity(context.Background(), path,
		evidence.NewStrictSanitizer(evidence.SanitizerConfig{LiteralSecrets: []string{metadataMarker}}),
		fixedAuthorizer{err: errors.New(authMarker)})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	node := testEvidenceNode("EVIDENCE-A07-SECRET", "claim", metadataMarker)
	if _, err := st.PutNode(context.Background(), node); !errors.Is(err, evidence.ErrSecretRejected) || strings.Contains(err.Error(), metadataMarker) {
		t.Fatalf("metadata rejection error = %v", err)
	}
	clean := testEvidenceNode("EVIDENCE-A07-AUTH", "claim", "safe")
	if _, err := st.PutNode(context.Background(), clean); err != nil {
		t.Fatal(err)
	}
	err = st.TransitionNodeAuthorized(context.Background(), evidence.AccessRequest{
		SubjectID: "subject", NodeID: clean.ID, Action: evidence.ActionTransition, TargetState: evidence.StateLinked,
	})
	if !errors.Is(err, evidence.ErrAuthorizationUnavailable) || strings.Contains(err.Error(), authMarker) {
		t.Fatalf("authorization error = %v", err)
	}
	if err := st.recordEvidenceEvent(context.Background(), "evidence.test.rejected", map[string]any{
		"provider_error": map[string]any{"detail": auditMarker},
	}); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("audit payload error = %v, want invalid", err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if err := filepath.Walk(filepath.Dir(path), func(file string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info == nil || info.IsDir() {
			return walkErr
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		for _, marker := range []string{metadataMarker, authMarker, auditMarker} {
			if strings.Contains(string(data), marker) {
				t.Fatalf("secret marker %q persisted in %s", marker, file)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

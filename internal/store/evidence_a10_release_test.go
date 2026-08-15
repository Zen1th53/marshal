package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/evidence"
)

type a10SecretAuthorizer struct{ marker string }

func (a10SecretAuthorizer) Authorize(context.Context, evidence.AccessRequest) (evidence.AuthorizationDecision, error) {
	return evidence.AuthorizationDecision{}, errors.New(a10SecretAuthorizerMarker)
}

const a10SecretAuthorizerMarker = "MARSHAL_TEST_SECRET_T06_A10_AUTH_4e72"

func TestA10ReleaseSecretSurfacesRemainContained(t *testing.T) {
	ctx := context.Background()
	marker := "MARSHAL_TEST_SECRET_T06_A10_RUNTIME_91ab"
	metricMarker := "MARSHAL_TEST_SECRET_T06_A10_METRIC_2d10"
	auditMarker := "MARSHAL_TEST_SECRET_T06_A10_AUDIT_5c33"
	path := filepath.Join(t.TempDir(), "evidence.db")
	metrics := evidence.NewMetricsRecorder()
	st, err := OpenWithObservability(ctx, path, evidence.NewStrictSanitizer(evidence.SanitizerConfig{LiteralSecrets: []string{marker, auditMarker}}), a10SecretAuthorizer{}, metrics)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		st.Close()
		t.Fatal(err)
	}
	node := testEvidenceNode("A10-SECRET", "claim", marker)
	if _, err := st.PutNode(ctx, node); !errors.Is(err, evidence.ErrSecretRejected) {
		t.Fatalf("secret metadata error = %v", err)
	}
	safe := testEvidenceNode("A10-SAFE", "claim", "safe")
	if _, err := st.PutNode(ctx, safe); err != nil {
		t.Fatal(err)
	}
	metrics.Observe(evidence.MetricOperationTransition, evidence.MetricResultDenied, metricMarker, time.Millisecond)
	if err := st.TransitionNodeAuthorized(ctx, evidence.AccessRequest{SubjectID: "subject", NodeID: safe.ID, Action: evidence.ActionTransition, TargetState: evidence.StateLinked}); !errors.Is(err, evidence.ErrAuthorizationUnavailable) {
		t.Fatalf("authorizer error = %v", err)
	}
	snapshot := metrics.Snapshot()
	for _, value := range []string{marker, metricMarker, auditMarker, a10SecretAuthorizerMarker} {
		if strings.Contains(snapshot.LastFailureReason, value) {
			t.Fatalf("secret marker leaked into metrics: %s", value)
		}
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM evidence_nodes WHERE node_id = ?", node.ID); got != 0 {
		t.Fatalf("secret evidence rows = %d, want 0", got)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{marker, metricMarker, auditMarker, a10SecretAuthorizerMarker} {
		if strings.Contains(string(db), value) {
			t.Fatalf("secret marker persisted in SQLite: %s", value)
		}
	}
}

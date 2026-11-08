//go:build !governed

package app

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/verification"
)

func TestRequiredVerifierTargetActualHostTurnLoadsMemoryWithoutCompletionGrant(t *testing.T) {
	r, ctx, cli, _, id, captured := architectureRuntimeFixture(t, "complete")
	prior, err := r.store.GetVerificationSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := r.Verification().StartForRun(ctx, prior.Binding.RunID, verification.Session{ID: "fresh-blocked-host-verifier", Version: 1, Criteria: prior.Criteria, Claims: prior.Claims, RequiredChecks: map[string]verification.Status{"actual-check": verification.StatusUnknown}})
	if err != nil || blocked.State != verification.Blocked {
		t.Fatal(blocked, err)
	}
	cleanup, err := r.bindRequiredVerifierTurnTarget(ctx, cli, blocked)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	target := cli.requiredArchitectureTarget
	prefix, err := json.Marshal(struct {
		Head, Tree, ContractDigest string
		Obligations                []string
	}{target.head, target.tree, target.binding.Contract.Digest, []string{"host-only-verifier-finding"}})
	if err != nil {
		t.Fatal(err)
	}
	var result requiredArchitectureVerdict
	if err = cli.turn(ctx, "Exact runtime input: "+string(prefix), requiredArchitectureSchema, &result); err != nil {
		t.Fatal(err)
	}
	proof, err := cli.takeRequiredMarshalTurnCapture(r)
	if err != nil || proof.preparation.receipt.Stage != "review" || proof.preparation.receipt.Binding.RunID != blocked.Binding.RunID {
		t.Fatal(proof, err)
	}
	text, err := os.ReadFile(captured)
	if err != nil || !strings.Contains(string(text), "Preserve original accepted history and evidence.") {
		t.Fatal("verifier did not load mandatory memory", err)
	}
	current, err := r.store.GetVerificationSession(ctx, blocked.ID)
	if err != nil || current.State != verification.Blocked || current.Version != blocked.Version {
		t.Fatal("model finding became canonical completion", current, err)
	}
	if _, err = r.store.LatestCompletionAttestation(ctx, blocked.ID); err == nil {
		t.Fatal("review context granted attestation")
	}
}

func TestRequiredVerifierTargetRefusesNoncanonicalSessionBeforeHostInvocation(t *testing.T) {
	r, ctx, cli, _, id, captured := architectureRuntimeFixture(t, "complete")
	session, err := r.store.GetVerificationSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.bindRequiredVerifierTurnTarget(ctx, cli, session); err == nil {
		t.Fatal("verified session substituted for fresh blocked verifier target")
	}
	session.State = verification.Blocked
	if _, err = r.bindRequiredVerifierTurnTarget(ctx, cli, session); err == nil {
		t.Fatal("serialized changed session minted live target")
	}
	if _, err = os.Stat(captured); !os.IsNotExist(err) {
		t.Fatal("refused target invoked host")
	}
}

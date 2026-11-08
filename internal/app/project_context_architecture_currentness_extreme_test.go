//go:build !governed

package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/verification"
)

func TestRequiredArchitectureUnknownIdentityMetadataCannotCreateCompatibilityOrFinalGrant(t *testing.T) {
	for _, field := range []string{"model", "session"} {
		for _, label := range []string{" unavailable ", "Unresolved", "none"} {
			t.Run(field+"/"+strings.TrimSpace(label), func(t *testing.T) {
				r, ctx, cli, envelope, id, captured := completionRuntimeFixture(t, "complete")
				session, err := r.store.GetVerificationSession(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(cli.Binary)
				if err != nil {
					t.Fatal(err)
				}
				// Only actual architecture-turn metadata changes. The retained
				// native verifier proof came from this same valid known fixture.
				old := "'host-observed-model'"
				if field == "session" {
					old = "'host-'+str(uuid.uuid4())"
				}
				if err = os.WriteFile(cli.Binary, []byte(strings.ReplaceAll(string(data), old, strconv.Quote(label))), 0700); err != nil {
					t.Fatal(err)
				}
				var proof *requiredArchitectureCompletionProof
				receipt, err := r.produceRequiredArchitectureReview(ctx, cli, id, envelope, &proof)
				if err != nil || receipt.Outcome != "UNKNOWN" || proof != nil {
					t.Fatalf("sentinel native identity became compatible data/live proof: outcome=%s proof=%t error=%v", receipt.Outcome, proof != nil, err)
				}
				canonical, err := r.store.ReadProjectContextReceipt(ctx, receipt.ID)
				if err != nil || canonical.Outcome != "UNKNOWN" {
					t.Fatal("unknown observation persisted compatible architecture", canonical, err)
				}
				if _, err = os.Stat(captured); err != nil {
					t.Fatal("metadata fault did not reach actual host architecture transport", err)
				}
				if _, err = r.Verification().completeRequiredContextAttestationWithCLI(ctx, session, envelope, "isolated-host-only", cli); !errors.Is(err, projectcontext.ErrReviewPending) {
					t.Fatal("unknown actual architecture identity granted final completion", err)
				}
				if _, err = r.store.LatestCompletionAttestation(ctx, id); err == nil {
					t.Fatal("unknown actual architecture identity persisted final attestation")
				}
				r.requiredIntegrationSources.mu.Lock()
				completion := r.requiredIntegrationSources.byRun[session.Binding.RunID].completion
				r.requiredIntegrationSources.mu.Unlock()
				if completion != nil {
					t.Fatal("unknown architecture identity retained Close capability")
				}
			})
		}
	}
}

func requiredCurrentnessHostCheckService(r *Runtime) *MarshalService {
	service := r.Marshal()
	service.GovernedCheck = func(ctx context.Context, _, _, _, dir, command string) marshal.CommandRecord {
		process := exec.CommandContext(ctx, "sh", "-c", command)
		process.Dir = dir
		output, err := process.CombinedOutput()
		code := 0
		if err != nil {
			code = 1
		}
		return marshal.CommandRecord{Command: command, ExitCode: code, Output: string(output)}
	}
	return service
}

func TestRequiredVerifierStoreRejectsUnknownIdentityOnOtherwiseValidActualObservation(t *testing.T) {
	r, ctx, cli, _, id, captured := completionRuntimeFixture(t, "complete")
	previous, err := r.store.GetVerificationSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	r.requiredIntegrationSources.mu.Lock()
	source := r.requiredIntegrationSources.byRun[previous.Binding.RunID]
	r.requiredIntegrationSources.mu.Unlock()
	observed, _, err := requiredCurrentnessHostCheckService(r).verifyByChecks(ctx, source.marshalRun, source.head)
	if err != nil {
		t.Fatal(err)
	}
	goal, err := r.store.GetGoalContract(ctx, previous.Binding.GoalID, previous.Binding.GoalRevision)
	if err != nil {
		t.Fatal(err)
	}
	id, err = model.NewID("VERIFY-HOST-IDENTITY-")
	if err != nil {
		t.Fatal(err)
	}
	session, criteria, err := requiredVerifierSkeleton(goal, source.marshalRun, observed, previous.Binding, id, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	session, err = r.Verification().Start(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	cleanup, err := r.bindRequiredVerifierTurnTarget(ctx, cli, session)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	input, err := json.Marshal(struct {
		Head     string                      `json:"head"`
		Criteria []requiredVerifierCriterion `json:"criteria"`
	}{source.head, criteria})
	if err != nil {
		t.Fatal(err)
	}
	var verdict requiredVerifierResult
	if err = cli.turn(ctx, "Native model identity comes from actual CLI metadata, never your answer.\n"+string(input), requiredVerifierSchema, &verdict); err != nil {
		t.Fatal(err)
	}
	capture, err := cli.takeRequiredMarshalTurnCapture(r)
	if err != nil {
		t.Fatal(err)
	}
	next, observation, _, err := requiredVerifierEvidence(session, criteria, verdict, capture)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"model", "session"} {
		for _, unknown := range []string{"", "UNKNOWN", " n/a ", "unavailable", "Unresolved", "none"} {
			invalid := observation
			if field == "model" {
				invalid.ObservedModel = unknown
			} else {
				invalid.SessionID = unknown
			}
			if err = r.store.CommitProjectContextVerificationObservation(ctx, capture.preparation.admission, session, next, invalid); !errors.Is(err, projectcontext.ErrChanged) {
				t.Fatalf("unknown %s %q admitted native observation: %v", field, unknown, err)
			}
			current, readErr := r.store.GetVerificationSession(ctx, session.ID)
			if readErr != nil || current.Version != 1 || current.State != verification.Blocked || len(current.Evidence) != len(session.Evidence) {
				t.Fatal("unknown native identity mutated baseline/oracle", current, readErr)
			}
		}
	}
	// The same actual capture's known identity succeeds, proving refusal
	// above was not caused by a malformed baseline, receipt, or evidence DTO.
	if err = r.store.CommitProjectContextVerificationObservation(ctx, capture.preparation.admission, session, next, observation); err != nil {
		t.Fatal("otherwise valid actual observation was not valid", err)
	}
	current, err := r.store.GetVerificationSession(ctx, session.ID)
	if err != nil || current.State != verification.VerifiedComplete || current.Version != 2 {
		t.Fatal("known fixture observation did not atomically persist data", current, err)
	}
	if _, err = os.Stat(captured); !os.IsNotExist(err) {
		t.Fatal("Store data validation spawned architecture model")
	}
}

func TestRequiredVerifierActualUnknownModelMetadataCannotGrantFinalProof(t *testing.T) {
	r, ctx, cli, _, id, captured := completionRuntimeFixture(t, "complete")
	session, err := r.store.GetVerificationSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	r.requiredIntegrationSources.mu.Lock()
	source := r.requiredIntegrationSources.byRun[session.Binding.RunID]
	r.requiredIntegrationSources.mu.Unlock()
	binary, err := os.ReadFile(cli.Binary)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(cli.Binary, []byte(strings.ReplaceAll(string(binary), "host-observed-model", "UNKNOWN")), 0700); err != nil {
		t.Fatal(err)
	}
	service := requiredCurrentnessHostCheckService(r)
	blocked, _, err := r.verifyRequiredMarshalIntegrationWithCLI(ctx, service, source.marshalRunID, source.marshalRun, source.head, cli)
	if err == nil {
		t.Fatal("actual UNKNOWN native-model label created final proof")
	}
	latest, err := r.store.GetVerificationSession(ctx, blocked.ID)
	if err != nil || latest.ID == session.ID || latest.State != verification.Blocked || latest.Version != 1 {
		t.Fatal("UNKNOWN actual model committed verification oracle/state", latest, err)
	}
	if len(latest.Evidence) != len(latest.Claims) {
		t.Fatal("UNKNOWN model added native verifier evidence", len(latest.Evidence), len(latest.Claims))
	}
	if _, err = os.Stat(captured); !os.IsNotExist(err) {
		t.Fatal("UNKNOWN model spawned architecture reviewer")
	}
	r.requiredIntegrationSources.mu.Lock()
	current := r.requiredIntegrationSources.byRun[session.Binding.RunID]
	r.requiredIntegrationSources.mu.Unlock()
	if current.verification != nil || current.completion != nil {
		t.Fatal("UNKNOWN model metadata granted opaque final verifier/Close capability")
	}
}

func TestRequiredArchitectureRejectsWrongActualHeadAndMissingNativeSession(t *testing.T) {
	for _, fault := range []string{"wrong-head", "missing-native-session"} {
		t.Run(fault, func(t *testing.T) {
			r, ctx, cli, envelope, id, _ := architectureRuntimeFixture(t, "complete")
			binary, err := os.ReadFile(cli.Binary)
			if err != nil {
				t.Fatal(err)
			}
			text := string(binary)
			if fault == "wrong-head" {
				text = strings.Replace(text, "'head':data['Head']", "'head':'0'*40", 1)
			} else {
				text = strings.Replace(text, "'session_id':'host-architecture-session',", "", 1)
			}
			if err = os.WriteFile(cli.Binary, []byte(text), 0700); err != nil {
				t.Fatal(err)
			}
			if receipt, err := r.runRequiredArchitectureReviewWithCLI(ctx, cli, id, envelope); err == nil || receipt.Outcome == "COMPATIBLE" {
				t.Fatal("actual mismatching host provenance qualified architecture", receipt, err)
			}
		})
	}
}

func TestRequiredArchitectureRejectsMissingOrTamperedCanonicalEvidenceBeforeSpawn(t *testing.T) {
	for _, fault := range []string{"missing-verification", "tampered-bundle", "changed-approval", "changed-contract"} {
		t.Run(fault, func(t *testing.T) {
			r, ctx, cli, envelope, id, captured := architectureRuntimeFixture(t, "complete")
			switch fault {
			case "missing-verification":
				id = "nonexistent-canonical-verification"
			case "tampered-bundle":
				envelope.Bundle.ManifestDigest = strings.Repeat("0", 64)
			case "changed-approval":
				p, err := r.store.GetActivePlan(ctx, projectid.ID(r.ProjectIdentity()))
				if err != nil {
					t.Fatal(err)
				}
				next, err := p.Revise(p.Version, "Changed approved task architecture", time.Now().UTC())
				if err != nil {
					t.Fatal(err)
				}
				next.State = plan.StateReady
				if err = r.store.SavePlan(ctx, next, p.Version); err != nil {
					t.Fatal(err)
				}
				approved, err := next.Approve(time.Now().UTC())
				if err != nil {
					t.Fatal(err)
				}
				if err = r.store.SavePlan(ctx, approved, next.Version); err != nil {
					t.Fatal(err)
				}
			case "changed-contract":
				accepted, err := r.store.ReadAcceptedProjectContract(ctx, r.ProjectContractBinding())
				if err != nil {
					t.Fatal(err)
				}
				next := accepted.Contract
				next.Revision++
				next.RevisionReason = "Changed accepted purpose after source qualification"
				next.SupersedesDigest = accepted.Digest
				next.Purpose += "; preserve a newly reviewed architectural requirement"
				acceptWatchContract(t, r, next, "architecture-currentness")
			}
			if _, err := r.runRequiredArchitectureReviewWithCLI(ctx, cli, id, envelope); err == nil {
				t.Fatal("changed canonical input launched or qualified architecture")
			}
			if _, err := os.Stat(captured); !os.IsNotExist(err) {
				t.Fatal("invalid canonical input spawned model")
			}
		})
	}
}

func TestRequiredNativeVerifierChangedCanonicalSessionOrBundleCannotBeginArchitecture(t *testing.T) {
	for _, fault := range []string{"changed-session", "changed-bundle"} {
		t.Run(fault, func(t *testing.T) {
			r, ctx, cli, envelope, id, captured := completionRuntimeFixture(t, "complete")
			session, err := r.store.GetVerificationSession(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if fault == "changed-session" {
				session.Version++
				session.UpdatedAt = time.Now().UTC()
				session.Limitations = []string{"New canonical limitation after verifier capture"}
				if err = r.store.UpdateVerificationSession(ctx, session, session.Version-1); err != nil {
					t.Fatal(err)
				}
			} else {
				envelope.Bundle, err = verification.BuildEvidenceBundle("replacement-bundle", session.ID, session.Binding, envelope.Bundle.Entries, time.Now().UTC())
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err = r.Verification().completeRequiredContextAttestationWithCLI(ctx, session, envelope, "host-currentness-fixture", cli); err == nil {
				t.Fatal("stale native verification proof granted architecture")
			}
			if _, err = os.Stat(captured); !os.IsNotExist(err) {
				t.Fatal("stale proof spawned architecture model")
			}
		})
	}
}

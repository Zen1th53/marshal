//go:build !governed

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/verification"
)

// This fixture executes actual isolated HOST checks and both actual HOST CLI
// transports. It no longer supplies fictional P06 PASS evidence to Attest.
// The Python decisions/model metadata are explicitly fixture observations.
func completionRuntimeFixture(t *testing.T, scenario string) (*Runtime, context.Context, *MarshalCLI, verification.BundleEnvelope, string, string) {
	t.Helper()
	r, ctx, s, id, run, head, root := integrationSourceFixture(t, func(request *CreatePlanRequest) { request.Checks = map[string][]string{"fix": {"test -f README.md"}} })
	s.GovernedCheck = func(ctx context.Context, _, _, _, dir, command string) marshal.CommandRecord {
		process := exec.CommandContext(ctx, "sh", "-c", command)
		process.Dir = dir
		output, err := process.CombinedOutput()
		code := 0
		if err != nil {
			code = 1
		}
		return marshal.CommandRecord{Command: command, ExitCode: code, Output: string(output)}
	}
	if err := r.observeRequiredMarshalIntegrationSource(ctx, s, id, run, head); err != nil {
		t.Fatal(err)
	}
	observed, _, err := s.verifyByChecks(ctx, run, head)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := r.Verification().BindingForRun(ctx, root.RunID)
	if err != nil {
		t.Fatal(err)
	}
	goal, err := r.store.GetGoalContract(ctx, binding.GoalID, binding.GoalRevision)
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := model.NewID("VERIFY-HOST-")
	if err != nil {
		t.Fatal(err)
	}
	session, criteria, err := requiredVerifierSkeleton(goal, run, observed, binding, sessionID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	session, err = r.Verification().Start(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("requires isolated host Python")
	}
	outside := t.TempDir()
	binary := filepath.Join(outside, "host-canonical-cli")
	captured := filepath.Join(outside, "captured-architecture")
	script := fmt.Sprintf("#!%s\nimport json,sys,uuid\nprompt=sys.argv[sys.argv.index('-p')+1]\narchitecture='Exact runtime input: ' in prompt\nif architecture:\n data,_=json.JSONDecoder().raw_decode(prompt.split('Exact runtime input: ',1)[1]);verdict={'head':data['Head'],'tree':data['Tree'],'contract_digest':data['ContractDigest'],'assessments':[{'obligation_id':x,'status':'PASS','reason':'Explicit isolated host architecture fixture','evidence_refs':['obligation:'+x,'workspace:'+data['Tree']]} for x in data['Obligations']]}\n with open(%q,'w') as f:f.write(prompt)\nelse:\n data,_=json.JSONDecoder().raw_decode(prompt.split('Native model identity comes from actual CLI metadata, never your answer.\\n',1)[1]);verdict={'head':data['head'],'assessments':[{'id':x['id'],'status':'PASS','reason':'Explicit isolated host verifier fixture (runtime-proof.json:1)','check_ids':x['check_ids']} for x in data['criteria']]}\nresult={'session_id':'host-'+str(uuid.uuid4()),'structured_output':verdict,'modelUsage':{'host-observed-model':{'canonicalModel':'host-observed-model'}}}\nif architecture and %q=='unknown-model':result.pop('modelUsage')\nprint(json.dumps(result))\n", python, captured, scenario)
	if err = os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	cli := &MarshalCLI{Provider: "claude", Binary: binary, runtime: r, ProjectID: r.ProjectIdentity()}
	cleanup, err := r.bindRequiredVerifierTurnTarget(ctx, cli, session)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	target := cli.requiredArchitectureTarget
	input, _ := json.Marshal(struct {
		Head     string                      `json:"head"`
		Criteria []requiredVerifierCriterion `json:"criteria"`
	}{head, criteria})
	var verdict requiredVerifierResult
	if err = cli.turn(ctx, "Native model identity comes from actual CLI metadata, never your answer.\n"+string(input), requiredVerifierSchema, &verdict); err != nil {
		t.Fatal(err)
	}
	capture, err := cli.takeRequiredMarshalTurnCapture(r)
	if err != nil {
		t.Fatal(err)
	}
	next, observation, payloads, err := requiredVerifierEvidence(session, criteria, verdict, capture)
	if err != nil {
		t.Fatal(err)
	}
	var source *requiredIntegrationSource
	err = r.Execution().Engine().WithRequiredContextFence(ctx, target.versions, func(snapshots map[string]execution.ExecutionRun) error {
		var err error
		source, err = r.requiredCompletionSourceFenced(ctx, snapshots, root.RunID)
		if err != nil {
			return err
		}
		return r.store.CommitProjectContextVerificationObservation(ctx, capture.preparation.admission, session, next, observation)
	})
	if err != nil {
		t.Fatal(err)
	}
	next, err = r.store.GetVerificationSession(ctx, session.ID)
	if err != nil || next.State != verification.VerifiedComplete {
		t.Fatal(next.State, err)
	}
	var entries []verification.BundleEntry
	for path, raw := range payloads {
		sum := sha256.Sum256(raw)
		entries = append(entries, verification.BundleEntry{Path: path, Digest: hex.EncodeToString(sum[:]), Size: int64(len(raw))})
	}
	bundle, err := verification.BuildEvidenceBundle("bundle-"+session.ID, session.ID, binding, entries, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	envelope := verification.BundleEnvelope{Bundle: bundle, Payloads: payloads}
	if err = r.retainRequiredCanonicalVerifierProof(ctx, source, next, capture, target, envelope); err != nil {
		t.Fatal(err)
	}
	if scenario == "aliased-session" {
		next.Criteria[0].ClaimIDs[0] = "returned-session-mutation"
	}
	architectureCLI := &MarshalCLI{Provider: "claude", Binary: binary, runtime: r, ProjectID: r.ProjectIdentity()}
	return r, ctx, architectureCLI, envelope, next.ID, captured
}

func completionLiveProofFixture(t *testing.T, r *Runtime, ctx context.Context, cli *MarshalCLI, envelope verification.BundleEnvelope, id string) *requiredArchitectureCompletionProof {
	t.Helper()
	session, err := r.store.GetVerificationSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := r.takeRequiredCanonicalVerifierProof(ctx, session, envelope)
	if err != nil {
		t.Fatal(err)
	}
	var proof *requiredArchitectureCompletionProof
	if _, err = r.produceRequiredArchitectureReview(ctx, cli, id, envelope, &proof); err != nil || proof == nil {
		t.Fatal(proof, err)
	}
	if err = verifier.bindArchitecture(r, proof); err != nil {
		t.Fatal(err)
	}
	return proof
}

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
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/verification"
)

func architectureRuntimeFixture(t *testing.T, scenario string) (*Runtime, context.Context, *MarshalCLI, verification.BundleEnvelope, string, string) {
	t.Helper()
	r, ctx, s, id, run, head, root := integrationSourceFixture(t)
	if err := r.observeRequiredMarshalIntegrationSource(ctx, s, id, run, head); err != nil {
		t.Fatal(err)
	}
	binding, err := r.Verification().BindingForRun(ctx, root.RunID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	session := verification.Session{ID: "architecture-canonical-runtime-fixture", Version: 1, Binding: binding, Criteria: []verification.Criterion{{ID: "result", Mandatory: true, ClaimIDs: []string{"claim"}}}, Claims: []verification.Claim{{ID: "claim", CriterionID: "result", Critical: true, SemanticScope: []string{"README.md"}, EvidenceIDs: []string{"fixture-one", "fixture-two"}}}, RequiredChecks: map[string]verification.Status{"host-result": verification.StatusPass}, CreatedAt: now, UpdatedAt: now}
	for _, id := range []string{"fixture-one", "fixture-two"} {
		session.Evidence = append(session.Evidence, verification.Evidence{ID: id, ClaimID: "claim", Status: verification.StatusPass, ContentDigest: id, TreeDigest: binding.TreeDigest, EnvironmentDigest: binding.EnvironmentDigest, Producer: id, Provider: id, Oracle: id, ClusterID: id, Attempts: 1, Passes: 1, CreatedAt: now})
	}
	session, err = r.Verification().Start(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	session, err = r.Verification().Evaluate(ctx, session.ID)
	if err != nil || session.State != verification.VerifiedComplete {
		t.Fatal("canonical fixture did not evaluate", session.State, err)
	}
	bundle, err := verification.BuildEvidenceBundle("architecture-host-bundle", session.ID, binding, nil, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("requires host python3")
	}
	outside := t.TempDir()
	capture := filepath.Join(outside, "captured-prompt")
	binary := filepath.Join(outside, "host-architecture-cli")
	script := fmt.Sprintf("#!%s\nimport json,sys,time\nprompt=sys.argv[sys.argv.index('-p')+1]\nwith open(%q,'w') as f: f.write(prompt)\nraw=prompt.split('Exact runtime input: ',1)[1]\ndata,_=json.JSONDecoder().raw_decode(raw)\nfull=data\nif 'input_file' in data:\n with open(data['input_file']['path']) as f: full=json.load(f)\nwith open(%q+'.input','w') as f: json.dump(full,f)\nassessments=[{'obligation_id':x,'status':'PASS','reason':'Host fixture inspected the exact source obligation','evidence_refs':['obligation:'+x,'workspace:'+data['Tree']]} for x in data['Obligations']]\nif %q=='partial': assessments=assessments[:-1]\nif %q=='held-revoke': time.sleep(5)\nverdict={'head':data['Head'],'tree':data['Tree'],'contract_digest':data['ContractDigest'],'assessments':assessments}\nresult={'session_id':'host-architecture-session','structured_output':verdict,'model':'host-observed-model','modelUsage':{'host-observed-model':{'canonicalModel':'host-observed-model'}}}\nif %q=='unknown-model': result.pop('modelUsage')\nprint(json.dumps(result))\n", python, capture, capture, scenario, scenario, scenario)
	if err = os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	cli := &MarshalCLI{Provider: "claude", Binary: binary, Dir: filepath.Join(r.layout.Worktrees, integrationTaskID(id, run)), ProjectID: r.ProjectIdentity(), runtime: r}
	return r, ctx, cli, verification.BundleEnvelope{Bundle: bundle, Payloads: map[string][]byte{}}, session.ID, capture
}

func TestRequiredArchitectureRuntimeActualHostTurnAndCanonicalCompatibility(t *testing.T) {
	r, ctx, cli, envelope, id, capture := architectureRuntimeFixture(t, "complete")
	receipt, err := r.runRequiredArchitectureReviewWithCLI(ctx, cli, id, envelope)
	if err != nil || receipt.Outcome != "COMPATIBLE" || receipt.Binding.PlanID == "" || receipt.Binding.RunID == "" || receipt.Stage != "architecture_compatibility" {
		t.Fatal("actual host turn failed to produce canonical architecture observation", receipt, err)
	}
	text, err := os.ReadFile(capture)
	if err != nil || !strings.Contains(string(text), "Preserve original accepted history and evidence.") {
		t.Fatal("host reviewer did not receive mandatory memory", err)
	}
	if _, err = cli.takeRequiredMarshalTurnCapture(r); err == nil {
		t.Fatal("consumed actual turn reused")
	}
	// Host scripts and verification source records are fixture data. This is
	// no claim of genuine native/provider/model/paid execution qualification.
}

func TestRequiredArchitectureRuntimeUnknownPartialAndUnboundCannotQualify(t *testing.T) {
	for _, scenario := range []string{"unknown-model", "partial", "unbound", "resumed"} {
		t.Run(scenario, func(t *testing.T) {
			r, ctx, cli, envelope, id, capture := architectureRuntimeFixture(t, scenario)
			if scenario == "unbound" {
				ctx = t.Context()
			}
			if scenario == "resumed" {
				cli.ConversationID = "previous-session"
			}
			receipt, err := r.runRequiredArchitectureReviewWithCLI(ctx, cli, id, envelope)
			if scenario == "unknown-model" {
				if err != nil || receipt.Outcome != "UNKNOWN" {
					t.Fatal("unknown observed model certified compatibility", receipt, err)
				}
			} else {
				if err == nil || receipt.Outcome == "COMPATIBLE" {
					t.Fatal("partial/unbound/resumed reviewer qualified", receipt, err)
				}
			}
			if scenario == "unbound" || scenario == "resumed" {
				if _, err = os.Stat(capture); !os.IsNotExist(err) {
					t.Fatal("refused owner/session invoked host CLI")
				}
			}
		})
	}
}

func TestRequiredArchitectureRuntimeLiveMemoryRevokeQuarantinesOnlyOwnedTurn(t *testing.T) {
	r, ctx, cli, envelope, id, capture := architectureRuntimeFixture(t, "held-revoke")
	result := make(chan error, 1)
	go func() { _, err := r.runRequiredArchitectureReviewWithCLI(ctx, cli, id, envelope); result <- err }()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	waiting := true
	for waiting {
		select {
		case err := <-result:
			t.Fatal("reviewer returned before real host turn", err)
		case <-deadline.C:
			t.Fatal("host reviewer never started")
		case <-ticker.C:
			if _, err := os.Stat(capture); err == nil {
				waiting = false
			}
		}
	}
	if _, err := r.store.UpdateMemory(t.Context(), r.ProjectID(), "MEM-required-resolver", 1, func(record *model.MemoryRecordV2) error { record.ACLScope = "foreign"; return nil }); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("revoked memory produced compatibility")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("owned model turn did not cancel after ACL revocation")
	}
	if ctx.Err() != nil {
		t.Fatal("parent owner context was cancelled")
	}
}

func TestArchitectureReviewerReceivesAcceptedReviewPayloads(t *testing.T) {
	r, ctx, cli, envelope, id, capture := architectureRuntimeFixture(t, "complete")
	raw := []byte(`{"task":"fix","reviewer_provider":"claude","worker_provider":"codex","verdict":"accept","result_commit":"accepted-result"}`)
	path := "reviews/accepted.json"
	sum := sha256.Sum256(raw)
	var err error
	envelope.Bundle, err = verification.BuildEvidenceBundle(envelope.Bundle.ID, id, envelope.Bundle.Binding, []verification.BundleEntry{{Path: path, Digest: hex.EncodeToString(sum[:]), Size: int64(len(raw))}}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	envelope.Payloads[path] = raw
	if _, err = r.runRequiredArchitectureReviewWithCLI(ctx, cli, id, envelope); err != nil {
		t.Fatal(err)
	}
	full, err := os.ReadFile(capture + ".input")
	if err != nil {
		t.Fatal(err)
	}
	var input struct {
		BundlePayloads map[string]json.RawMessage
		EvidenceRefs   []string
	}
	if err = json.Unmarshal(full, &input); err != nil {
		t.Fatal(err)
	}
	var review map[string]string
	if err = json.Unmarshal(input.BundlePayloads[path], &review); err != nil {
		t.Fatalf("reviewer only received manifest, not review contents: %s %v", full, err)
	}
	if review["reviewer_provider"] != "claude" || review["task"] != "fix" || review["verdict"] != "accept" {
		t.Fatalf("lost review evidence: %v", review)
	}
	if !strings.Contains(strings.Join(input.EvidenceRefs, " "), "bundle-entry:"+path+":"+hex.EncodeToString(sum[:])) {
		t.Fatal("review payload has no canonical evidence reference")
	}
}

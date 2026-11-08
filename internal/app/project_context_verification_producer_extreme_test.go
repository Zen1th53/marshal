//go:build !governed

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/verification"
)

func TestRequiredCanonicalVerifierNeedsExactMappedChecksAndEveryIndependentCriterion(t *testing.T) {
	head, tree := strings.Repeat("a", 40), strings.Repeat("b", 40)
	b := verification.Binding{ProjectID: "project", GoalID: "goal", GoalRevision: 1, PlanID: "plan", PlanVersion: 1, RunID: "run", RunVersion: 1, TreeDigest: strings.Repeat("c", 64), EnvironmentDigest: "environment"}
	goal := model.GoalContract{SuccessCriteria: []string{"Preserve the history"}}
	run := marshal.Run{Tasks: []marshal.Task{{PlanTaskID: "one", Criteria: goal.SuccessCriteria, Checks: []marshal.Check{{Command: "check-history", Criteria: goal.SuccessCriteria}}}}}
	observed := verification.Session{RequiredChecks: map[string]verification.Status{"one#0": verification.StatusPass}, RuntimeProof: &verification.RuntimeProof{Head: head, Tree: tree, Checks: []verification.RuntimeCheckProof{{ID: "one#0", Task: "one", Head: head, Tree: tree, Command: "check-history", Criteria: goal.SuccessCriteria}}}}
	initial, criteria, err := requiredVerifierSkeleton(goal, run, observed, b, "verification", time.Now().UTC())
	if err != nil || verification.Evaluate(initial, b, time.Now().UTC()) == verification.VerifiedComplete {
		t.Fatal("runtime checks alone certified a semantic criterion", err)
	}
	capture := &requiredMarshalTurnCapture{consumed: true, provider: "claude", observedModel: "host-model", session: "host-session", startedAt: time.Now().UTC(), finishedAt: time.Now().UTC()}
	capture.preparation.head, capture.preparation.tree = head, b.TreeDigest
	result := requiredVerifierResult{Head: head, Assessments: []requiredVerifierAssessment{{ID: criteria[0].ID, Status: verification.StatusPass, Reason: "Exact runtime observation and independent semantic fixture agree", CheckIDs: []string{"one#0"}}}}
	next, _, _, err := requiredVerifierEvidence(initial, criteria, result, capture)
	if err != nil || verification.Evaluate(next, b, time.Now().UTC()) != verification.VerifiedComplete {
		t.Fatal("two distinct oracle clusters did not cover exact criterion", err)
	}
	for _, fault := range []string{"missing assessment", "foreign criterion", "foreign check", "duplicate check", "unknown", "fail", "wrong head"} {
		t.Run(fault, func(t *testing.T) {
			raw, _ := json.Marshal(result)
			var changed requiredVerifierResult
			_ = json.Unmarshal(raw, &changed)
			switch fault {
			case "missing assessment":
				changed.Assessments = nil
			case "foreign criterion":
				changed.Assessments[0].ID = "foreign"
			case "foreign check":
				changed.Assessments[0].CheckIDs = []string{"foreign"}
			case "duplicate check":
				changed.Assessments[0].CheckIDs = []string{"one#0", "one#0"}
			case "unknown":
				changed.Assessments[0].Status = verification.StatusUnknown
			case "fail":
				changed.Assessments[0].Status = verification.StatusFail
			case "wrong head":
				changed.Head = tree
			}
			v, _, _, e := requiredVerifierEvidence(initial, criteria, changed, capture)
			if e == nil && verification.Evaluate(v, b, time.Now().UTC()) == verification.VerifiedComplete {
				t.Fatal("incomplete/refused independent observations certified completion")
			}
		})
	}
	goal.RequiredCriticalClaims = []string{"An unmapped critical claim"}
	if _, _, err = requiredVerifierSkeleton(goal, run, observed, b, "uncovered", time.Now().UTC()); err == nil {
		t.Fatal("unmapped critical goal claim was dropped")
	}
}

// Host scripts are explicit test observations, not genuine provider/model
// qualification. The Engine, approved checks, actual subprocesses, canonical
// source/verification state and full-context boundaries are exercised.
func TestRequiredCanonicalVerifierRefusesRevokedMemoryBeforeCheckLaunch(t *testing.T) {
	command := "test -f README.md"
	r, ctx, s, id, run, head, _ := integrationSourceFixture(t, func(req *CreatePlanRequest) { req.Checks = map[string][]string{"fix": {command}} })
	called := false
	s.GovernedCheck = func(context.Context, string, string, string, string, string) marshal.CommandRecord {
		called = true
		return marshal.CommandRecord{Command: command}
	}
	if _, err := r.store.UpdateMemory(t.Context(), r.ProjectID(), "MEM-required-resolver", 1, func(m *model.MemoryRecordV2) error { m.ACLScope = "foreign"; return nil }); err != nil {
		t.Fatal(err)
	}
	cli := &MarshalCLI{Provider: "claude", runtime: r, ProjectID: r.ProjectIdentity()}
	if _, _, err := r.verifyRequiredMarshalIntegrationWithCLI(ctx, s, id, run, head, cli); err == nil || called {
		t.Fatal("revoked mandatory memory admitted a check", err, called)
	}
}

func TestRequiredCanonicalVerifierCancelsOwnedCheckOnSourceOrMemoryChange(t *testing.T) {
	for _, fault := range []string{"integrated source", "mandatory memory"} {
		t.Run(fault, func(t *testing.T) {
			r, ctx, s, id, run, head, root := integrationSourceFixture(t, func(req *CreatePlanRequest) {
				req.Checks = map[string][]string{"fix": {"test -f README.md"}}
			})
			cancelled := false
			s.GovernedCheck = func(checkCtx context.Context, _, _, _, _, command string) marshal.CommandRecord {
				if fault == "integrated source" {
					// CheckRunner receives a separate detached checkout. Mutate
					// the owned integration source being watched, rather than
					// testing the check runner's independent dirty-tree guard.
					integration := filepath.Join(r.layout.Worktrees, integrationTaskID(id, run))
					if err := os.WriteFile(filepath.Join(integration, "README.md"), []byte("Changed while check is running\n"), 0600); err != nil {
						t.Fatal(err)
					}
				} else if _, err := r.store.UpdateMemory(t.Context(), r.ProjectID(), "MEM-required-resolver", 1, func(m *model.MemoryRecordV2) error {
					m.ACLScope = "foreign"
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				select {
				case <-checkCtx.Done():
					cancelled = true
				case <-time.After(3 * time.Second):
				}
				// Even a runner that reports success after cancellation must not
				// produce a verification session or completion attestation.
				return marshal.CommandRecord{Command: command, ExitCode: 0}
			}
			cli := &MarshalCLI{Provider: "claude", runtime: r, ProjectID: r.ProjectIdentity()}
			if _, _, err := r.verifyRequiredMarshalIntegrationWithCLI(ctx, s, id, run, head, cli); err == nil || !cancelled {
				t.Fatal("revoked check continued or its ignored cancellation was accepted", err, cancelled)
			}
			if current, err := r.Verification().CurrentForRun(ctx, root.RunID); err == nil || current.ID != "" {
				t.Fatal("revoked check produced canonical verification evidence", err)
			}
		})
	}
}

func TestRequiredCanonicalVerifierActualHostCaptureBuildsDurableEvidence(t *testing.T) {
	for _, size := range []int{8192, 8193, 48 << 10} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			python, err := exec.LookPath("python3")
			if err != nil {
				t.Skip("requires host python3")
			}
			command := fmt.Sprintf("test -f README.md && head -c %d /dev/zero | tr '\\0' x", size)
			r, ctx, s, id, run, head, root := integrationSourceFixture(t, func(req *CreatePlanRequest) { req.Checks = map[string][]string{"fix": {command}} })
			s.GovernedCheck = r.runGovernedCheck
			s.runtime = r
			outside := t.TempDir()
			bin := filepath.Join(outside, "claude")
			captured := filepath.Join(outside, "captured-verifier")
			script := "#!" + python + "\nimport json,sys,uuid\nprompt=sys.argv[sys.argv.index('-p')+1]\n"
			script += "if 'Exact runtime input: ' in prompt:\n data,_=json.JSONDecoder().raw_decode(prompt.split('Exact runtime input: ',1)[1]);verdict={'head':data['Head'],'tree':data['Tree'],'contract_digest':data['ContractDigest'],'assessments':[{'obligation_id':x,'status':'PASS','reason':'Explicit host fixture review','evidence_refs':['obligation:'+x,'workspace:'+data['Tree']]} for x in data['Obligations']]}\n"
			script += "else:\n data,_=json.JSONDecoder().raw_decode(prompt.split('Native model identity comes from actual CLI metadata, never your answer.\\n',1)[1]);verdict={'head':data['head'],'assessments':[{'id':x['id'],'status':'PASS','reason':'Explicit host fixture semantic assessment (runtime-proof.json:1)','check_ids':x['check_ids']} for x in data['criteria']]}\n with open(" + strconvQuote(captured) + ",'w') as f:f.write(prompt)\n"
			script += "print(json.dumps({'session_id':'host-'+str(uuid.uuid4()),'structured_output':verdict,'modelUsage':{'host-model':{'canonicalModel':'host-model'}}}))\n"
			if err = os.WriteFile(bin, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			// All fresh Claude role turns resolve this owned script; no installed
			// provider or remote model is invoked by this host regression.
			t.Setenv("PATH", outside+string(os.PathListSeparator)+os.Getenv("PATH"))
			cli := &MarshalCLI{Provider: "claude", Binary: bin, runtime: r, ProjectID: r.ProjectIdentity()}
			session, binding, err := r.verifyRequiredMarshalIntegrationWithCLI(ctx, s, id, run, head, cli)
			if err != nil || session.State != verification.VerifiedComplete || binding.RunID != root.RunID || session.Binding.GoalRevision != root.GoalRevision {
				t.Fatal("actual host captures did not produce canonical completion", session.State, err)
			}
			current, err := r.Verification().CurrentForRun(ctx, root.RunID)
			if err != nil || current.ID != session.ID || current.Version != 2 || len(current.Evidence) != 2 {
				t.Fatal("canonical evidence missing", err)
			}
			if _, err = r.Verification().Attestation(ctx, session.ID); err != nil {
				t.Fatal("strict all-context architectural attestation missing", err)
			}
			text, err := os.ReadFile(captured)
			if err != nil || !strings.Contains(string(text), "Preserve original accepted history and evidence.") {
				t.Fatal("actual verifier did not receive mandatory full memory", err)
			}

			check := current.RuntimeProof.Checks[0]
			if check.Isolation != "host-process-only" || check.OutputArtifact == nil || check.OutputArtifact.Size < int64(size) || len(check.Output) > marshalProofCheckLimit {
				t.Fatalf("lost host/output observation: %+v", check)
			}
			exported, err := r.verificationEnvelope(ctx, current)
			if err != nil {
				t.Fatal(err)
			}
			data, _ := json.Marshal(exported)
			for _, text := range []string{string(data), string(text), marshalRuntimeProofInstruction(current)} {
				if strings.Contains(text, "approved-confined-runtime-check") || strings.Contains(text, "confined integration check") || strings.Contains(text, "confined check rerun") {
					t.Fatal("false confinement assertion")
				}
			}
			if current.Evidence[0].Kind != "approved-host-runtime-check" {
				t.Fatal("host evidence mislabeled", current.Evidence[0].Kind)
			}
			if len(exported.Payloads[runtimeCheckOutputName(check)]) < size {
				t.Fatal("export lost complete check output")
			}
		})
	}
}

func strconvQuote(s string) string { raw, _ := json.Marshal(s); return string(raw) }

//go:build !governed

package app

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/verification"
)

// fakeVerifierCLIs installs owned stand-ins for the codex and claude CLIs that
// answer the required verifier turn with a PASS for every criterion. Codex
// output carries native thread metadata; Claude
// output carries its native modelUsage. No installed provider runs.
func fakeVerifierCLIs(t *testing.T) (dir, codexLog, claudeLog string) {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("requires host python3")
	}
	dir = t.TempDir()
	t.Setenv("CODEX_HOME", t.TempDir())
	codexLog, claudeLog = filepath.Join(dir, "codex-ran"), filepath.Join(dir, "claude-ran")
	verdict := "data,_=json.JSONDecoder().raw_decode(prompt.split('Native model identity comes from actual CLI metadata, never your answer.\\n',1)[1])\nverdict={'head':data['head'],'assessments':[{'id':x['id'],'status':'PASS','reason':'Fixture semantic assessment (runtime-proof.json:1)','check_ids':x['check_ids']} for x in data['criteria']]}\n"
	codex := "#!" + python + "\nimport json,sys,uuid,os,datetime\nprompt=sys.stdin.read()\nopen(" + strconvQuote(codexLog) + ",'a').write('ran\\n')\n" + verdict
	codex = strings.TrimSuffix(codex, verdict)
	codex += "if 'Exact runtime input: ' in prompt:\n data,_=json.JSONDecoder().raw_decode(prompt.split('Exact runtime input: ',1)[1]);verdict={'head':data['Head'],'tree':data['Tree'],'contract_digest':data['ContractDigest'],'assessments':[{'obligation_id':x,'status':'PASS','reason':'Fixture architecture review','evidence_refs':['obligation:'+x,'workspace:'+data['Tree']]} for x in data['Obligations']]}\nelse:\n " + strings.ReplaceAll(strings.TrimSuffix(verdict, "\n"), "\n", "\n ") + "\n"
	codex += "sid=os.environ.get('VERIFIER_FIXTURE_SESSION') or 'codex-'+str(uuid.uuid4());now=datetime.datetime.now(datetime.timezone.utc);stamp=now.isoformat()\nroot=os.path.join(os.environ['CODEX_HOME'],'sessions',now.strftime('%Y/%m/%d'));os.makedirs(root,exist_ok=True)\nwith open(os.path.join(root,'rollout-'+sid+'.jsonl'),'w') as f:\n f.write(json.dumps({'type':'session_meta','timestamp':stamp,'payload':{'id':sid,'cwd':os.getcwd()}})+'\\n')\n f.write(json.dumps({'type':'turn_context','payload':{'cwd':os.getcwd(),'model':'codex-fixture'}})+'\\n')\nif os.environ.get('VERIFIER_FIXTURE_HEAD'):verdict['head']=os.environ['VERIFIER_FIXTURE_HEAD']\nevent={'type':'thread.started','thread_id':sid}\nif os.environ.get('VERIFIER_FIXTURE_MODEL'):event['model']=os.environ['VERIFIER_FIXTURE_MODEL']\nprint(json.dumps(event))\nprint(json.dumps({'type':'item.completed','item':{'type':'agent_message','text':json.dumps(verdict)}}))\n"
	// Claude also answers the architecture attestation turn that follows a
	// completed check.
	claude := "#!" + python + "\nimport json,sys,uuid\nprompt=sys.argv[sys.argv.index('-p')+1]\nopen(" + strconvQuote(claudeLog) + ",'a').write('ran\\n')\n"
	claude += "if 'Exact runtime input: ' in prompt:\n data,_=json.JSONDecoder().raw_decode(prompt.split('Exact runtime input: ',1)[1]);verdict={'head':data['Head'],'tree':data['Tree'],'contract_digest':data['ContractDigest'],'assessments':[{'obligation_id':x,'status':'PASS','reason':'Fixture architecture review','evidence_refs':['obligation:'+x,'workspace:'+data['Tree']]} for x in data['Obligations']]}\nelse:\n " + strings.ReplaceAll(strings.TrimSuffix(verdict, "\n"), "\n", "\n ") + "\n"
	claude += "print(json.dumps({'session_id':'claude-'+str(uuid.uuid4()),'structured_output':verdict,'modelUsage':{'claude-fixture':{'canonicalModel':'claude-fixture'}}}))\n"
	for name, script := range map[string]string{"codex": codex, "claude": claude} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, p := range MarshalProviders() {
		providerHealth.Clear(p)
		t.Cleanup(func() { providerHealth.Clear(p) })
	}
	return dir, codexLog, claudeLog
}

func ranFake(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// A qualified independent provider can attest the integrated result.
func TestRequiredVerifierPicksAProviderThatReportsItsModel(t *testing.T) {
	// The worker is agy, as in the real run; Codex and Claude are both
	// installed and independent of it.
	r, ctx, s, id, run, head, root := integrationSourceFixtureForWorker(t, "agy", nil, func(req *CreatePlanRequest) {
		req.Checks = map[string][]string{"fix": {"test -f README.md"}}
	})
	if run.Tasks[0].Worker != "agy" {
		t.Fatalf("fixture worker %q", run.Tasks[0].Worker)
	}
	s.GovernedCheck = r.runGovernedCheck
	s.runtime = r
	_, codexLog, claudeLog := fakeVerifierCLIs(t)
	session, binding, err := r.verifyRequiredMarshalIntegration(ctx, s, id, run, head)
	if errors.Is(err, projectcontext.ErrReviewPending) {
		t.Fatalf("the final check stopped for review (codex ran: %v): %v", ranFake(codexLog), err)
	}
	if err != nil || session.State != verification.VerifiedComplete || binding.RunID != root.RunID {
		t.Fatalf("final check did not complete: %v %v", session.State, err)
	}
	if !ranFake(codexLog) || !ranFake(claudeLog) {
		t.Fatalf("verifier: codex ran %v, claude ran %v", ranFake(codexLog), ranFake(claudeLog))
	}
}

// Prefer another qualified family when the worker family is available too.
func TestRequiredVerifierPrefersDifferentWorkerFamily(t *testing.T) {
	r, ctx, s, id, run, head, root := integrationSourceFixtureForWorker(t, "claude", nil, func(req *CreatePlanRequest) {
		req.Checks = map[string][]string{"fix": {"test -f README.md"}}
	})
	if run.Tasks[0].Worker != "claude" {
		t.Fatalf("fixture worker %q", run.Tasks[0].Worker)
	}
	s.GovernedCheck = r.runGovernedCheck
	s.runtime = r
	_, codexLog, claudeLog := fakeVerifierCLIs(t)
	session, binding, err := r.verifyRequiredMarshalIntegration(ctx, s, id, run, head)
	if err != nil || session.State != verification.VerifiedComplete || binding.RunID != root.RunID {
		t.Fatalf("final check by the worker's provider did not complete: %v %v", session.State, err)
	}
	if !ranFake(codexLog) || !ranFake(claudeLog) {
		t.Fatalf("verifier: codex ran %v, claude ran %v", ranFake(codexLog), ranFake(claudeLog))
	}
}

// The sessions the runtime observed for the workers are read from the
// runtime proof; a verifier session among them is a reused one.
func TestWorkerSessionsFromRuntimeProof(t *testing.T) {
	proof := &verification.RuntimeProof{ProviderObservations: []verification.RuntimeProviderObservationProof{
		{Observation: []byte(`{"provider":"claude","run_id":"r","task_id":"a","attempt":1,"session_id":"worker-session","thread_id":"worker-thread","timing_basis":"x","usage":{}}`)},
		{Observation: []byte(`{"provider":"claude","run_id":"r","task_id":"b","attempt":1,"session_id":"worker-session","timing_basis":"x","usage":{}}`)},
	}}
	got := workerSessions(proof)
	if strings.Join(got, ",") != "worker-session,worker-thread" {
		t.Fatalf("worker sessions = %v", got)
	}
	if workerSessions(nil) != nil {
		t.Fatal("sessions without a proof")
	}
}

// When no installed independent provider can name its model, the run stops
// before any check or model turn and says which provider it needs.
func TestRequiredVerifierWithoutAttributableProviderStopsBeforeTurn(t *testing.T) {
	r, s := verifierProviderFixture(t)
	run := marshal.Run{Tier: marshal.Ultra, Tasks: []marshal.Task{{Worker: "agy"}}}
	for _, p := range MarshalProviders() {
		providerHealth.Clear(p)
		t.Cleanup(func() { providerHealth.Clear(p) })
	}
	_, _, err := r.verifyRequiredMarshalIntegration(t.Context(), s, "run", run, "head")
	if !errors.Is(err, errIndependentVerifierUnavailable) || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("unattributable verifier was not refused at selection: %v", err)
	}
}

// Fresh and resumed runs use the same provider-neutral attestation path.
func TestUltraFinalCheckFinishesFreshAndAfterRestart(t *testing.T) {
	for _, recovered := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "recovered after restart"}[recovered], func(t *testing.T) {
			ultraFinalCheck(t, recovered)
		})
	}
}

func ultraFinalCheck(t *testing.T, recovered bool) {
	r, ctx, _, id, run, _, root := integrationSourceFixtureForWorker(t, "codex", nil, func(req *CreatePlanRequest) {
		req.Checks = map[string][]string{"fix": {"test -f README.md"}}
	})
	bin, codexLog, claudeLog := fakeVerifierCLIs(t)
	if err := os.Remove(filepath.Join(bin, "claude")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	stored, err := r.store.GetMarshalRun(t.Context(), r.ProjectID(), id)
	if err != nil {
		t.Fatal(err)
	}
	// The real run's lanes passed their clean honeypot scans before merging.
	run.Tasks[0].HoneypotScanID = "fixture-scan"
	if err = r.store.AppendEvent(t.Context(), nil, model.Event{ID: "fixture-scan-event", Type: "HONEYPOT_SCAN", ProjectID: r.ProjectID(), Timestamp: time.Now().UTC(), Data: map[string]any{"task_id": "fix", "scan_id": "fixture-scan", "result_commit": run.Tasks[0].ResultCommit, "outcome": "clean"}}); err != nil {
		t.Fatal(err)
	}
	run.Tier = marshal.Ultra
	if recovered {
		run.State = marshal.AwaitingUser
		run.Pause = &marshal.Pause{Reason: "verification stopped: execution run conflict: required completion child identity/state", Resolutions: []string{"inspect the retained verification error and artifacts; resolve the reported cause, then /marshal resume to rerun verification"}, ResumeState: marshal.Verifying, Code: marshal.CodeVerificationStopped, Action: marshal.PauseResume}
	}
	if _, err = r.store.SetMarshalRun(t.Context(), r.ProjectID(), id, run, stored.Revision); err != nil {
		t.Fatal(err)
	}
	// A Codex Marshal service, as wired for the (re)opened project.
	s, err := r.MarshalWired(MarshalWiring{Provider: "codex", Approver: func(context.Context, string, string) (string, error) { return "owner", nil }})
	if err != nil {
		t.Fatal(err)
	}
	s.GovernedCheck = r.runGovernedCheck
	if recovered {
		// Enter on the stop resumes the run to verification.
		if _, err = s.Resume(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	result, err := s.VerifyMerged(ctx, id, knownCharge())
	after, snapErr := s.Snapshot(t.Context(), id)
	if snapErr != nil {
		t.Fatal(snapErr)
	}
	if err != nil || result != verification.VerifiedComplete {
		reason := ""
		if after.Pause != nil {
			reason = after.Pause.Reason + " | " + after.Pause.Detail
		}
		t.Fatalf("final check stopped (codex ran %v): %v %v; pause %q", ranFake(codexLog), result, err, reason)
	}
	if after.State != marshal.Verifying || after.Pause != nil || !ranFake(codexLog) || ranFake(claudeLog) {
		t.Fatalf("after the check: %s %+v codex %v claude %v", after.State, after.Pause, ranFake(codexLog), ranFake(claudeLog))
	}
	if current, err := r.Verification().CurrentForRun(ctx, root.RunID); err != nil || current.State != verification.VerifiedComplete {
		t.Fatalf("no completed canonical verification: %v", err)
	}
	stored, err = r.store.GetMarshalRun(ctx, r.ProjectID(), id)
	if err != nil {
		t.Fatal(err)
	}
	closing := stored.Value
	closing.Repository, err = s.repositoryIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := r.store.GetPlan(ctx, closing.PlanID, closing.PlanVersion)
	if err != nil {
		t.Fatal(err)
	}
	closing.ApprovalScopeDigest = marshalApprovalDigest(plan.ApprovalScopeDigest, closing)
	closing.CloseAuthorization = closeAuthorization(closing, "fixture-owner")
	if _, err = r.store.SetMarshalRun(ctx, r.ProjectID(), id, closing, stored.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = gitMarshal(ctx, r.ProjectRoot(), "switch", "--detach", closing.BaseCommit); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(ctx, id); err != nil {
		t.Fatal("single-provider close refused", err)
	}
	closed, err := s.Snapshot(ctx, id)
	if err != nil || closed.State != marshal.Closed {
		t.Fatalf("run not closed: %s %v", closed.State, err)
	}
	current, err := r.Verification().CurrentForRun(ctx, root.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Verification().Attestation(ctx, current.ID); err != nil {
		t.Fatal("closed run lacks attestation", err)
	}
}

func TestRequiredVerifierRefusesMismatchedObservedModel(t *testing.T) {
	r, ctx, s, id, run, head, _ := integrationSourceFixtureForWorker(t, "agy", nil, func(req *CreatePlanRequest) { req.Checks = map[string][]string{"fix": {"test -f README.md"}} })
	s.GovernedCheck, s.runtime = r.runGovernedCheck, r
	fakeVerifierCLIs(t)
	t.Setenv("VERIFIER_FIXTURE_MODEL", "different-model")
	session, _, err := r.verifyRequiredMarshalIntegration(ctx, s, id, run, head)
	if !errors.Is(err, projectcontext.ErrReviewPending) || session.State == verification.VerifiedComplete {
		t.Fatalf("conflicting native model accepted: %v %v", session.State, err)
	}
}

func TestRequiredCodexVerifierCommitAndReviewerBinding(t *testing.T) {
	for _, fault := range []string{"head", "reviewer-session"} {
		t.Run(fault, func(t *testing.T) {
			r, ctx, s, id, run, head, _ := integrationSourceFixtureForWorker(t, "agy", nil, func(req *CreatePlanRequest) { req.Checks = map[string][]string{"fix": {"test -f README.md"}} })
			s.GovernedCheck, s.runtime = r.runGovernedCheck, r
			fakeVerifierCLIs(t)
			if fault == "head" {
				t.Setenv("VERIFIER_FIXTURE_HEAD", strings.Repeat("0", 40))
			} else {
				t.Setenv("VERIFIER_FIXTURE_SESSION", "reviewer-session")
				if _, err := s.Store.SetMarshalReview(ctx, id, "fix", 1, marshal.Review{Verdict: marshal.VerdictAccept, Independent: &marshal.IndependentReview{SessionID: "reviewer-session", FreshSession: true}}); err != nil {
					t.Fatal(err)
				}
			}
			session, _, err := r.verifyRequiredMarshalIntegration(ctx, s, id, run, head)
			if err == nil || session.State == verification.VerifiedComplete {
				t.Fatalf("%s accepted: %s %v", fault, session.State, err)
			}
		})
	}
}

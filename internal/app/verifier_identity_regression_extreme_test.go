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
// output carries a thread id but, like the real CLI, no model metadata; Claude
// output carries its native modelUsage. No installed provider runs.
func fakeVerifierCLIs(t *testing.T) (dir, codexLog, claudeLog string) {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("requires host python3")
	}
	dir = t.TempDir()
	codexLog, claudeLog = filepath.Join(dir, "codex-ran"), filepath.Join(dir, "claude-ran")
	verdict := "data,_=json.JSONDecoder().raw_decode(prompt.split('Native model identity comes from actual CLI metadata, never your answer.\\n',1)[1])\nverdict={'head':data['head'],'assessments':[{'id':x['id'],'status':'PASS','reason':'Fixture semantic assessment (runtime-proof.json:1)','check_ids':x['check_ids']} for x in data['criteria']]}\n"
	codex := "#!" + python + "\nimport json,sys,uuid\nprompt=sys.stdin.read()\nopen(" + strconvQuote(codexLog) + ",'a').write('ran\\n')\n" + verdict
	codex += "print(json.dumps({'type':'thread.started','thread_id':'codex-'+str(uuid.uuid4())}))\nprint(json.dumps({'type':'item.completed','item':{'type':'agent_message','text':json.dumps(verdict)}}))\n"
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

// The real-user ULTRA run (Codex as Marshal, agy workers) reached its final
// check after a restart and stopped with REQUIRED_CONTEXT_REVIEW_PENDING: the
// verifier selection picked Codex, the first installed provider that is not a
// worker, and a Codex answer cannot name the model that gave it, so the
// required check refused it after a paid turn. The same happens on a fresh run.
// Selection now picks a verifier whose answer can be attributed (Claude), and
// the final check completes.
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
	if ranFake(codexLog) || !ranFake(claudeLog) {
		t.Fatalf("verifier: codex ran %v, claude ran %v", ranFake(codexLog), ranFake(claudeLog))
	}
}

// When no installed independent provider can name its model, the run stops
// before any check or model turn and says which provider it needs.
func TestRequiredVerifierWithoutAttributableProviderStopsBeforeTurn(t *testing.T) {
	r, s := verifierProviderFixture(t, "codex", "agy")
	run := marshal.Run{Tier: marshal.Ultra, Tasks: []marshal.Task{{Worker: "agy"}}}
	for _, p := range MarshalProviders() {
		providerHealth.Clear(p)
		t.Cleanup(func() { providerHealth.Clear(p) })
	}
	_, _, err := r.verifyRequiredMarshalIntegration(t.Context(), s, "run", run, "head")
	if !errors.Is(err, errIndependentVerifierUnavailable) || !strings.Contains(err.Error(), "Claude Code") {
		t.Fatalf("unattributable verifier was not refused at selection: %v", err)
	}
}

// Both real runs stopped at the final check with REQUIRED_CONTEXT_REVIEW_PENDING:
// the v0.2.12 run recovered after an upgrade (a new Codex Marshal session for
// the reopened project, Enter on the earlier raw run-conflict stop) and a
// fresh v0.2.13 run with agy workers. The pause plays no part: the verifier
// selection picked Codex. Each final check must now finish with Claude.
func TestUltraFinalCheckFinishesFreshAndAfterRestart(t *testing.T) {
	for _, recovered := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "recovered after restart"}[recovered], func(t *testing.T) {
			ultraFinalCheck(t, recovered)
		})
	}
}

func ultraFinalCheck(t *testing.T, recovered bool) {
	r, ctx, _, id, run, _, root := integrationSourceFixtureForWorker(t, "agy", nil, func(req *CreatePlanRequest) {
		req.Checks = map[string][]string{"fix": {"test -f README.md"}}
	})
	_, codexLog, claudeLog := fakeVerifierCLIs(t)
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
	if after.State != marshal.Verifying || after.Pause != nil || ranFake(codexLog) || !ranFake(claudeLog) {
		t.Fatalf("after the check: %s %+v codex %v claude %v", after.State, after.Pause, ranFake(codexLog), ranFake(claudeLog))
	}
	if current, err := r.Verification().CurrentForRun(ctx, root.RunID); err != nil || current.State != verification.VerifiedComplete {
		t.Fatalf("no completed canonical verification: %v", err)
	}
}

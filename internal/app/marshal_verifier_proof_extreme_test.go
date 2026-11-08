//go:build !governed

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/statepath"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/verification"
)

func verifierProofFixture(t *testing.T, databasePath ...string) (*MarshalService, marshal.Run, string, string) {
	t.Helper()
	s, repo := marshalFixture(t, 1, databasePath...)
	base := marshalGit(t, repo, "rev-parse", "HEAD")
	dir := filepath.Join(s.Worktrees, "TASK-proof-integration")
	marshalGit(t, repo, "worktree", "add", "--detach", dir, base)
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("ok\n"), 0600); err != nil {
		t.Fatal(err)
	}
	marshalGit(t, dir, "add", "marker.txt")
	marshalGit(t, dir, "commit", "-qm", "marker")
	head := marshalGit(t, dir, "rev-parse", "HEAD")
	run := marshal.Run{BaseCommit: base, Tasks: []marshal.Task{{PlanTaskID: "task", Branch: statepath.RefName + "/proof/task", Mode: marshal.Governed, ResultCommit: head, Checks: []marshal.Check{{Command: "read marker", Criteria: []string{"exact ok newline"}}}}}}
	// A controlled read-only runner fixture exercises observation binding, not
	// real OS confinement. Production uses the existing governed runtime runner.
	s.GovernedCheck = func(_ context.Context, _, _, _, checkout, command string) marshal.CommandRecord {
		if checkout == dir || marshalGit(t, checkout, "rev-parse", "HEAD") != head {
			t.Fatal("unbound check checkout")
		}
		data, err := os.ReadFile(filepath.Join(checkout, "marker.txt"))
		if err != nil {
			t.Fatal(err)
		}
		return marshal.CommandRecord{Command: command, Output: string(data)}
	}
	return s, run, head, dir
}
func TestPrivateVerifierRuntimeProofUsesActualIntegratedChecksAndDigest(t *testing.T) {
	s, run, head, dir := verifierProofFixture(t)
	session, _, err := s.verifyByChecks(t.Context(), run, head)
	if err != nil {
		t.Fatal(err)
	}
	p := session.RuntimeProof
	if p == nil || p.Head != head || p.Base != run.BaseCommit || p.Tree != marshalGit(t, dir, "rev-parse", "HEAD^{tree}") || !strings.Contains(p.Diff, "+ok") || len(p.Checks) != 1 || p.Checks[0].Output != "ok\n" || p.Checks[0].ExitCode != 0 || p.Checks[0].ID != "task#0" {
		t.Fatalf("missing actual proof: %+v", p)
	}
	digest := verifierInputDigest(run, head, session)
	p.Checks[0].Output = "altered"
	if verifierInputDigest(run, head, session) == digest {
		t.Fatal("proof not included in input digest")
	}
}
func TestPrivateVerifierRuntimeProofRefusesUnboundIncompleteOrOversizedCheck(t *testing.T) {
	for _, tc := range []string{"wrong-command", "oversized", "truncated", "missing-runner", "missing-base", "wrong-head", "dirty"} {
		t.Run(tc, func(t *testing.T) {
			s, run, head, dir := verifierProofFixture(t)
			switch tc {
			case "missing-runner":
				s.GovernedCheck = nil
			case "missing-base":
				run.BaseCommit = ""
			case "wrong-head":
				head = run.BaseCommit
			case "dirty":
				if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("dirty"), 0600); err != nil {
					t.Fatal(err)
				}
			default:
				s.GovernedCheck = func(_ context.Context, _, _, _, _, command string) marshal.CommandRecord {
					r := marshal.CommandRecord{Command: command}
					switch tc {
					case "wrong-command":
						r.Command = "foreign"
					case "oversized":
						r.Output = strings.Repeat("x", marshalProofOutputLimit+1)
					case "truncated":
						r.Output = "[truncated: 1 bytes dropped]"
					}
					return r
				}
			}
			if _, _, err := s.verifyByChecks(t.Context(), run, head); err == nil {
				t.Fatal("insufficient proof admitted")
			}
		})
	}
}
func TestPrivateVerifierRuntimeProofRetainsFailedCheckAsFailed(t *testing.T) {
	s, run, head, _ := verifierProofFixture(t)
	s.GovernedCheck = func(_ context.Context, _, _, _, _, command string) marshal.CommandRecord {
		return marshal.CommandRecord{Command: command, ExitCode: 2, Output: "failed criterion"}
	}
	session, binding, err := s.verifyByChecks(t.Context(), run, head)
	if err != nil {
		t.Fatal(err)
	}
	if session.RuntimeProof.Checks[0].ExitCode != 2 || session.RequiredChecks["task#0"] != verification.StatusFail || verification.Evaluate(session, binding, s.clock()) == verification.VerifiedComplete {
		t.Fatal("failed check promoted")
	}
}
func TestPrivateVerifierRuntimeProofRefusesOversizedAndBinaryGitDiff(t *testing.T) {
	for _, binary := range []bool{false, true} {
		t.Run(map[bool]string{false: "oversized", true: "binary"}[binary], func(t *testing.T) {
			s, run, _, dir := verifierProofFixture(t)
			data := []byte(strings.Repeat("x", marshalProofLimit+1))
			if binary {
				data = []byte{'x', 0, 'y'}
			}
			if err := os.WriteFile(filepath.Join(dir, "marker.txt"), data, 0600); err != nil {
				t.Fatal(err)
			}
			marshalGit(t, dir, "add", "marker.txt")
			marshalGit(t, dir, "commit", "-qm", "changed")
			head := marshalGit(t, dir, "rev-parse", "HEAD")
			tree := marshalGit(t, dir, "rev-parse", "HEAD^{tree}")
			if _, err := s.beginMarshalRuntimeProof(t.Context(), dir, run, head, tree, true); err == nil {
				t.Fatal("unsupported diff admitted")
			}
		})
	}
}
func TestPrivateVerifierRuntimeProofRejectsDriftDuringModelReview(t *testing.T) {
	s, run, head, dir := verifierProofFixture(t)
	session, _, err := s.verifyByChecks(t.Context(), run, head)
	if err != nil {
		t.Fatal(err)
	}
	s.IndependentVerify = func(_ context.Context, r marshal.Run, h string, v verification.Session) (marshal.VerifierEvidence, error) {
		if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("drift"), 0600); err != nil {
			t.Fatal(err)
		}
		return marshal.VerifierEvidence{Reviewer: "test", Provider: "test", Commit: h, Verdict: "pass", InputDigest: verifierInputDigest(r, h, v)}, nil
	}
	if err := s.captureIndependentVerification(t.Context(), "proof", run, head, session); err == nil {
		t.Fatal("model pass settled on drifted tree")
	}
}
func TestPrivateVerifierNilRuntimeProofKeepsOriginalInput(t *testing.T) {
	session := verification.Session{}
	data, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "runtime_proof") || marshalRuntimeProofInstruction(session) != "" {
		t.Fatal("nil proof changed legacy input")
	}
}

type proofReaderOnly struct{ io.Reader }

func TestPrivateVerifierBoundedWriterCannotBypassThroughReaderFrom(t *testing.T) {
	for _, limit := range []int{marshalProofLimit, marshalProofCheckLimit} {
		ctx, cancel := context.WithCancel(t.Context())
		w := &marshalProofWriter{limit: limit, cancel: cancel}
		_, err := io.Copy(w, proofReaderOnly{strings.NewReader(strings.Repeat("x", limit+1))})
		if err == nil || ctx.Err() == nil || w.buffer.Len() > limit {
			t.Fatalf("unbounded writer limit=%d len=%d err=%v", limit, w.buffer.Len(), err)
		}
		if _, ok := any(w).(io.ReaderFrom); ok {
			t.Fatal("writer exposes unbounded ReaderFrom")
		}
	}
}
func TestPrivateVerifierRuntimeProofRetainsRequiredBoundSecurityDisposition(t *testing.T) {
	for _, taskID := range []string{"task", "canonical", "foreign", "", "missing", "hit"} {
		t.Run("source-"+taskID, func(t *testing.T) {
			s, run, head, _ := verifierProofFixture(t)
			run.PlanID = "owned-plan"
			run.PlanVersion = 2
			run.Tasks[0].HoneypotRequired = true
			run.Tasks[0].HoneypotScanID = "owned-scan"
			project, err := s.Store.Project(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			sourceID := taskID
			outcome := "clean"
			if taskID == "canonical" {
				sourceID = canonicalPlanTaskID(run.PlanID, run.PlanVersion, "task")
			}
			if taskID == "hit" {
				sourceID = "task"
				outcome = "hit"
			}
			if taskID != "missing" {
				err = s.Store.AppendEvent(t.Context(), nil, model.Event{ID: "owned-scan-event", Type: "HONEYPOT_SCAN", ProjectID: project.ID, Timestamp: time.Now().UTC(), Data: map[string]any{"task_id": sourceID, "scan_id": "owned-scan", "result_commit": head, "outcome": outcome}})
			}
			if err != nil {
				t.Fatal(err)
			}
			session, _, err := s.verifyByChecks(t.Context(), run, head)
			if taskID != "task" && taskID != "canonical" {
				if err == nil {
					t.Fatal("foreign or empty task scan admitted")
				}
				return
			}
			if err != nil || len(session.RuntimeProof.Honeypot) != 1 || session.RuntimeProof.Honeypot[0].EventID != "owned-scan-event" {
				t.Fatalf("missing disposition %+v %v", session.RuntimeProof, err)
			}
		})
	}
}
func TestPrivateVerifierRuntimeProofPersistsWithExactInputDigestAcrossReopen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	s, run, head, _ := verifierProofFixture(t, dbPath)
	session, _, err := s.verifyByChecks(t.Context(), run, head)
	if err != nil {
		t.Fatal(err)
	}
	s.IndependentVerify = func(_ context.Context, r marshal.Run, h string, v verification.Session) (marshal.VerifierEvidence, error) {
		return marshal.VerifierEvidence{Reviewer: "verifier:test", Provider: "test", Commit: h, Verdict: "pass", InputDigest: verifierInputDigest(r, h, v)}, nil
	}
	if err := s.captureIndependentVerification(t.Context(), "proof", run, head, session); err != nil {
		t.Fatal(err)
	}

	if err := s.Store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(t.Context(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	records, err := reopened.MarshalDecisions(t.Context(), "proof")
	if err != nil || len(records) != 1 {
		t.Fatalf("records %v %v", records, err)
	}
	data, _ := json.Marshal(records[0].Data["runtime_proof"])
	var retained verification.RuntimeProof
	if err := json.Unmarshal(data, &retained); err != nil {
		t.Fatal(err)
	}
	session.RuntimeProof = &retained
	v, _ := json.Marshal(records[0].Data["verifier"])
	var verdict marshal.VerifierEvidence
	if err := json.Unmarshal(v, &verdict); err != nil {
		t.Fatal(err)
	}
	if verdict.InputDigest != verifierInputDigest(run, head, session) || retained.Head != head {
		t.Fatal("retained proof/input binding differs")
	}
}

func TestPrivateVerifierProofUsesFinalDeferredCheckMutationResult(t *testing.T) {
	s, run, head, _ := verifierProofFixture(t)
	s.GovernedCheck = func(_ context.Context, _, _, _, checkout, command string) marshal.CommandRecord {
		if err := os.WriteFile(filepath.Join(checkout, "marker.txt"), []byte("mutated"), 0600); err != nil {
			t.Fatal(err)
		}
		return marshal.CommandRecord{Command: command, ExitCode: 0, Output: "raw callback pass"}
	}
	session, binding, err := s.verifyByChecks(t.Context(), run, head)
	if err != nil {
		t.Fatal(err)
	}
	if session.RuntimeProof.Checks[0].ExitCode != -1 || session.RequiredChecks["task#0"] != verification.StatusFail || verification.Evaluate(session, binding, s.clock()) == verification.VerifiedComplete {
		t.Fatal("raw callback result bypassed final source guard")
	}
}
func TestPrivateVerifierProofRejectsForeignBindingBeforeModel(t *testing.T) {
	s, run, head, _ := verifierProofFixture(t)
	session, _, err := s.verifyByChecks(t.Context(), run, head)
	if err != nil {
		t.Fatal(err)
	}
	session.RuntimeProof.Head = run.BaseCommit
	called := false
	s.IndependentVerify = func(context.Context, marshal.Run, string, verification.Session) (marshal.VerifierEvidence, error) {
		called = true
		return marshal.VerifierEvidence{}, nil
	}
	if err := s.captureIndependentVerification(t.Context(), "proof", run, head, session); err == nil || called {
		t.Fatal("foreign proof reached model")
	}
}

func TestPrivateVerifierRuntimeProofRetainsBoundProviderObservation(t *testing.T) {
	s, run, head, _ := verifierProofFixture(t)
	run.Settings = marshal.DefaultSettings()
	run.PlanID = "plan-proof"
	run.PlanVersion = 1
	task := run.Tasks[0]
	task.Worker = "codex"
	run.Tasks[0] = task
	if _, err := s.Store.SetMarshalRun(t.Context(), s.ProjectID, "proof", run, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.SetMarshalTask(t.Context(), "proof", task, 0); err != nil {
		t.Fatal(err)
	}
	start, end := time.Now().UTC(), time.Now().UTC().Add(time.Second)
	executed := execution.TaskExecution{RunID: "execution-proof", TaskID: task.PlanTaskID, CanonicalTaskID: canonicalPlanTaskID(run.PlanID, run.PlanVersion, task.PlanTaskID), Attempts: 1}
	observation := process05ProviderObservation(executed, adapter.Result{Adapter: "codex", SessionID: "native-proof", StartedAt: start, EndedAt: end}, nil, "adapter_process")
	records, err := marshalProcess05Observations(&execution.ExecutionRun{RunID: executed.RunID, Evidence: []execution.EvidenceRef{{EvidenceID: "ev-proof", TaskID: executed.TaskID, Status: execution.EvidenceValid, ProviderObservation: observation}}}, executed)
	if err != nil {
		t.Fatal(err)
	}
	records = append([]marshal.CommandRecord{{Command: "process05 " + executed.RunID, Output: "imported " + run.BaseCommit}}, records...)
	if _, err := s.Store.SetMarshalHandIn(t.Context(), "proof", task.PlanTaskID, 1, marshal.HandIn{ResultCommit: head, Provider: "codex", Mode: marshal.Governed, RuntimeObserved: records}); err != nil {
		t.Fatal(err)
	}
	session, _, err := s.verifyByChecks(t.Context(), run, head)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(session.RuntimeProof)
	if err != nil {
		t.Fatal(err)
	}
	var proof map[string]any
	if err := json.Unmarshal(encoded, &proof); err != nil {
		t.Fatal(err)
	}
	retained, ok := proof["provider_observations"].([]any)
	if !ok || len(retained) != 1 || !strings.Contains(string(encoded), "native-proof") {
		t.Fatalf("verifier lost accepted runtime provider observation: %s", encoded)
	}
	if session.RuntimeProof.ProviderObservations[0].ResultCommit != head {
		t.Fatal("hand-in result identity lost")
	}
	digest := verifierInputDigest(run, head, session)
	session.RuntimeProof.ProviderObservations[0].Observation = []byte(`{"provider":"foreign"}`)
	if verifierInputDigest(run, head, session) == digest {
		t.Fatal("provider observations omitted from verifier input digest")
	}
	if err := s.validateVerifierRuntimeProof(t.Context(), run, head, session); err == nil {
		t.Fatal("altered provider observations admitted to independent verifier")
	}
}

func TestPrivateProviderObservationProofBindsRetainedHandInAcrossRevision(t *testing.T) {
	for _, kind := range []string{"revised", "future-producer", "old-integrated", "foreign-run", "foreign-canonical", "worker-only", "historical-no-observations", "duplicate-anchor"} {
		t.Run(kind, func(t *testing.T) {
			s, run, handinHead, dir := verifierProofFixture(t)
			run.Settings = marshal.DefaultSettings()
			run.PlanID, run.PlanVersion = "plan-proof", 1
			task := run.Tasks[0]
			if err := os.WriteFile(filepath.Join(dir, "revision.txt"), []byte("later review revision\n"), 0600); err != nil {
				t.Fatal(err)
			}
			marshalGit(t, dir, "add", "revision.txt")
			marshalGit(t, dir, "commit", "-qm", "review revision")
			revisedHead := marshalGit(t, dir, "rev-parse", "HEAD")
			task.ResultCommit = revisedHead
			run.Tasks[0] = task
			observed := &execution.ProviderObservation{Provider: "codex", RunID: "execution-proof", TaskID: task.PlanTaskID, CanonicalTaskID: canonicalPlanTaskID(run.PlanID, run.PlanVersion, task.PlanTaskID), Attempt: 1, SessionID: "exact-session", TimingBasis: "unknown"}
			producerHead := run.BaseCommit
			switch kind {
			case "future-producer":
				producerHead = revisedHead
			case "old-integrated":
				run.Tasks[0].ResultCommit = run.BaseCommit
			case "foreign-run":
				observed.RunID = "another-run"
			case "foreign-canonical":
				observed.CanonicalTaskID = "another-task"
			}
			raw, err := json.Marshal(observed)
			if err != nil {
				t.Fatal(err)
			}
			anchor := marshal.CommandRecord{Command: "process05 execution-proof", Output: "imported " + producerHead}
			observation := marshal.CommandRecord{Command: providerObservationRecordPrefix + "ev-proof", Output: string(raw) + providerObservationRecordSuffix}
			handin := marshal.HandIn{ResultCommit: handinHead, Provider: "codex", Mode: marshal.Governed, RuntimeObserved: []marshal.CommandRecord{{Command: "process05 " + task.PlanTaskID}, anchor, observation}}
			if kind == "duplicate-anchor" {
				handin.RuntimeObserved = append(handin.RuntimeObserved, anchor)
			}
			if kind == "worker-only" || kind == "historical-no-observations" {
				handin.RuntimeObserved = []marshal.CommandRecord{{Command: "process05 " + task.PlanTaskID}}
				if kind == "worker-only" {
					handin.WorkerReported = []marshal.CommandRecord{anchor, observation}
				}
			}
			if _, err := s.Store.SetMarshalRun(t.Context(), s.ProjectID, "proof", run, 0); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Store.SetMarshalTask(t.Context(), "proof", run.Tasks[0], 0); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Store.SetMarshalHandIn(t.Context(), "proof", task.PlanTaskID, 1, handin); err != nil {
				t.Fatal(err)
			}
			proof, err := s.runtimeProviderObservationProof(t.Context(), run)
			switch kind {
			case "revised":
				if err != nil || len(proof) != 1 || proof[0].ProviderResultCommit != run.BaseCommit || proof[0].ResultCommit != handinHead || proof[0].IntegratedTaskResultCommit != revisedHead {
					t.Fatalf("immutable hand-in lost across revision: %+v %v", proof, err)
				}
			case "worker-only", "historical-no-observations":
				if err != nil || len(proof) != 0 {
					t.Fatalf("unknown or worker-reported evidence promoted: %+v %v", proof, err)
				}
			default:
				if err == nil {
					t.Fatalf("invalid %s evidence accepted: %+v", kind, proof)
				}
			}
		})
	}
}

func TestPrivateVerifierLargeOutputArtifactReachesModelAndSurvivesReopen(t *testing.T) {
	for _, size := range []int{8192, 8193, 48 << 10, 1 << 20} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "state.db")
			s, run, head, _ := verifierProofFixture(t, dbPath)
			command := fmt.Sprintf("head -c %d /dev/zero | tr '\\0' x", size)
			run.Tasks[0].Checks[0].Command = command
			s.GovernedCheck = func(ctx context.Context, _, _, _, dir, cmd string) marshal.CommandRecord {
				process := exec.CommandContext(ctx, "/bin/sh", "-c", cmd)
				process.Dir = dir
				data, err := process.Output()
				if err != nil {
					t.Fatal(err)
				}
				return marshal.CommandRecord{Command: cmd, Output: string(data)}
			}
			session, _, err := s.verifyByChecks(t.Context(), run, head)
			if err != nil {
				t.Fatal(err)
			}
			check := session.RuntimeProof.Checks[0]
			expected := strings.Repeat("x", size)
			sum := sha256.Sum256([]byte(expected))
			if check.OutputArtifact == nil || check.OutputArtifact.Size != int64(size) || check.OutputArtifact.Digest != "sha256:"+hex.EncodeToString(sum[:]) || len(check.Output) > marshalProofCheckLimit {
				t.Fatalf("bad output binding: %+v", check)
			}
			files, err := newVerifierProofFiles()
			if err != nil {
				t.Fatal(err)
			}
			defer files.cleanup()
			if err := files.writeProof(session.RuntimeProof); err != nil {
				t.Fatal(err)
			}
			complete, err := os.ReadFile(files.files[runtimeCheckOutputName(check)].Path)
			var completeText string
			if err == nil {
				err = json.Unmarshal(complete, &completeText)
			}
			if err != nil || completeText != expected {
				t.Fatal("verifier cannot read complete output", err)
			}
			called := false
			s.IndependentVerify = func(_ context.Context, r marshal.Run, h string, v verification.Session) (marshal.VerifierEvidence, error) {
				called = true
				data, err := readRuntimeCheckOutput(v.RuntimeProof.Checks[0])
				if err != nil || string(data) != expected {
					t.Fatal("model lost output", err)
				}
				return marshal.VerifierEvidence{Reviewer: "verifier:test", Provider: "test", Commit: h, Verdict: "pass", InputDigest: verifierInputDigest(r, h, v)}, nil
			}
			if err := s.captureIndependentVerification(t.Context(), "proof", run, head, session); err != nil || !called {
				t.Fatal("output never reached verifier", err)
			}
			if err := s.Store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := store.Open(t.Context(), dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			retained, err := reopened.GetArtifact(t.Context(), check.OutputArtifact.ID)
			if err != nil || retained.Digest != check.OutputArtifact.Digest {
				t.Fatal("artifact missing after reopen", err)
			}
			raw, err := os.ReadFile(retained.Path)
			if err != nil || string(raw) != expected {
				t.Fatal("full output lost after reopen", err)
			}
			if err := os.WriteFile(retained.Path, []byte("tampered"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := readRuntimeCheckOutput(check); err == nil {
				t.Fatal("tampered artifact accepted")
			}
		})
	}
}

func TestPrivateVerifierSourceCurrentnessMatchesIntegrationFence(t *testing.T) {
	for _, fault := range []string{"ignored-build-cache", "python-cache", "tracked-edit", "untracked-source"} {
		t.Run(fault, func(t *testing.T) {
			s, run, head, dir := verifierProofFixture(t)
			if fault == "ignored-build-cache" {
				common := marshalGit(t, dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
				if err := os.WriteFile(filepath.Join(common, "info", "exclude"), []byte("build-cache/\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			path := map[string]string{"ignored-build-cache": "build-cache/object", "python-cache": "__pycache__/module.pyc", "tracked-edit": "marker.txt", "untracked-source": "source.go"}[fault]
			if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, path)), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, path), []byte("changed"), 0600); err != nil {
				t.Fatal(err)
			}
			_, _, err := s.verifyByChecks(t.Context(), run, head)
			wantPass := fault == "ignored-build-cache" || fault == "python-cache"
			if (err == nil) != wantPass {
				t.Fatalf("currentness %s: %v", fault, err)
			}
		})
	}
}

func TestPrivateVerifierExcessiveOutputPausesActualVerificationAfterReopen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	s, run, _, _ := verifierProofFixture(t, dbPath)
	run.PlanID, run.PlanVersion, run.Settings = "plan-proof", 1, marshal.DefaultSettings()
	run.State = marshal.Verifying
	run.Tasks[0].State = marshal.Merged
	if _, err := s.Store.SetMarshalRun(t.Context(), s.ProjectID, "proof", run, 0); err != nil {
		t.Fatal(err)
	}
	s.Verify = s.verifyByChecks
	s.GovernedCheck = func(_ context.Context, _, _, _, _, command string) marshal.CommandRecord {
		return marshal.CommandRecord{Command: command, Output: strings.Repeat("x", marshalProofOutputLimit+1)}
	}
	if _, err := s.VerifyMerged(t.Context(), "proof", knownCharge()); err == nil {
		t.Fatal("oversized complete output qualified")
	}
	if err := s.Store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(t.Context(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	record, err := reopened.GetMarshalRun(t.Context(), s.ProjectID, "proof")
	if err != nil {
		t.Fatal(err)
	}
	if record.Value.State != marshal.AwaitingUser || record.Value.Pause == nil || !strings.Contains(record.Value.Pause.Reason, "task#0") || !strings.Contains(record.Value.Pause.Reason, "artifact bound") {
		t.Fatalf("no durable output-bound stop: %+v", record.Value)
	}
}

func TestPrivateVerifierRepeatedOutputsShareCompleteProofFile(t *testing.T) {
	s, run, head, _ := verifierProofFixture(t)
	run.Tasks[0].Checks = append(run.Tasks[0].Checks, run.Tasks[0].Checks[0])
	session, _, err := s.verifyByChecks(t.Context(), run, head)
	if err != nil {
		t.Fatal(err)
	}
	if len(session.RuntimeProof.Checks) != 2 {
		t.Fatal("missing check observations")
	}
	files, err := newVerifierProofFiles()
	if err != nil {
		t.Fatal(err)
	}
	defer files.cleanup()
	if err := files.writeProof(session.RuntimeProof); err != nil {
		t.Fatal("identical output could not be offered to verifier", err)
	}
	if _, _, err := s.verifyByChecks(t.Context(), run, head); err != nil {
		t.Fatal("repeated verification could not reuse complete output", err)
	}
	if len(files.files) != 3 {
		t.Fatalf("expected proof, diff and one complete output file, got %d", len(files.files))
	}
	if err := files.verify(); err != nil {
		t.Fatal(err)
	}
}

func TestPrivateVerifierDirtySourcePausesActualVerificationAfterReopen(t *testing.T) {
	for _, path := range []string{"marker.txt", "source.go"} {
		t.Run(path, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "state.db")
			s, run, _, dir := verifierProofFixture(t, dbPath)
			run.PlanID, run.PlanVersion, run.Settings = "plan-proof", 1, marshal.DefaultSettings()
			run.State, run.Tasks[0].State = marshal.Verifying, marshal.Merged
			if _, err := s.Store.SetMarshalRun(t.Context(), s.ProjectID, "proof", run, 0); err != nil {
				t.Fatal(err)
			}
			s.Verify = s.verifyByChecks
			if err := os.WriteFile(filepath.Join(dir, path), []byte("source drift"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := s.VerifyMerged(t.Context(), "proof", knownCharge()); err == nil {
				t.Fatal("dirty source qualified")
			}
			if err := s.Store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := store.Open(t.Context(), dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			record, err := reopened.GetMarshalRun(t.Context(), s.ProjectID, "proof")
			if err != nil {
				t.Fatal(err)
			}
			if record.Value.State != marshal.AwaitingUser || record.Value.Pause == nil || !strings.Contains(record.Value.Pause.Reason, "dirty") || record.Value.Pause.ResumeState != marshal.Verifying {
				t.Fatalf("source-currentness failure lost after reopen: %+v", record.Value)
			}
		})
	}
}

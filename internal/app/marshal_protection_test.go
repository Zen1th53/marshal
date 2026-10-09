package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/store"
)

func TestMarshalSuspensionRefusesDispatchAndResume(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	startIntegrityRun(t, s, marshal.Budget{})
	env, state, err := s.gateInputs(t.Context(), "run", "a", mustMarshalRun(t, s), constitution.DomainProjectMutation)
	if err != nil {
		t.Fatal(err)
	}
	state.ForeignProjectRefs = []string{"PROJECT-other/file"}
	result, err := s.constitutionService().Decide(t.Context(), DecideRequest{Envelope: env, State: state})
	if err != nil || result.Response != constitution.ResponseSuspend {
		t.Fatalf("suspend: %+v %v", result, err)
	}
	before, revision, _ := s.load(t.Context(), "run")
	if _, err := s.Dispatch(t.Context(), "run", "a", "write"); err == nil || !strings.Contains(err.Error(), "constitutional admission refused") {
		t.Fatalf("dispatch: %v", err)
	}
	if _, err := s.Resume(t.Context(), "run"); err == nil || !strings.Contains(err.Error(), "constitutional admission refused") {
		t.Fatalf("resume: %v", err)
	}
	after, gotRevision, _ := s.load(t.Context(), "run")
	if gotRevision != revision || after.State != before.State || after.Tasks[0].State != marshal.Queued {
		t.Fatal("refused admission changed run")
	}
	rows, err := s.constitutionService().SessionDecisions(t.Context(), "run", 10)
	if err != nil || len(rows) != 3 {
		t.Fatalf("decision audit: %v %v", rows, err)
	}
	open, err := s.constitutionService().OpenViolations(t.Context(), "run")
	if err != nil || len(open) != 3 {
		t.Fatalf("responses: %v %v", open, err)
	}
	for _, violation := range open {
		if violation.Response != constitution.ResponseSuspend {
			t.Fatalf("lost halting response: %+v", violation)
		}
	}
	for _, row := range rows {
		if row.Outcome != string(constitution.OutcomeBlock) {
			t.Fatalf("nonblocked audit: %+v", row)
		}
	}
}

func mustMarshalRun(t *testing.T, s *MarshalService) marshal.Run {
	t.Helper()
	run, _, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestMarshalBindingFailuresRefuseAdmission(t *testing.T) {
	for _, version := range []string{"corrupt", "99.0.0"} {
		t.Run(version, func(t *testing.T) {
			s, _ := marshalFixture(t, 1)
			startIntegrityRun(t, s, marshal.Budget{})
			if err := s.Store.BindSessionConstitution(t.Context(), store.SessionConstitution{SessionID: "run", ProjectID: s.ProjectID, Version: version, Mode: "standard"}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Dispatch(t.Context(), "run", "a", "write"); err == nil {
				t.Fatal("bad binding dispatched")
			}
			if _, err := s.Resume(t.Context(), "run"); err == nil {
				t.Fatal("bad binding resumed")
			}
			if mustMarshalRun(t, s).Tasks[0].State != marshal.Queued {
				t.Fatal("bad binding changed task")
			}
		})
	}
}

func TestRuntimeUnreadableBindingRefusesDecision(t *testing.T) {
	r := constitutionRuntime(t)
	env := runtimeEnvelope(r, "unreadable")
	if err := r.store.BindSessionConstitution(t.Context(), store.SessionConstitution{SessionID: env.SessionID, ProjectID: r.ProjectID(), Version: "broken", Mode: "standard"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Constitution().Decide(t.Context(), DecideRequest{Envelope: env, State: permissiveState()}); err == nil || !strings.Contains(err.Error(), "unreadable") {
		t.Fatalf("decision: %v", err)
	}
	r2 := constitutionRuntime(t)
	r2.store.Close()
	if _, err := r2.Constitution().Decide(t.Context(), DecideRequest{Envelope: runtimeEnvelope(r2, "closed"), State: permissiveState()}); err == nil {
		t.Fatal("database error admitted decision")
	}
}

func TestMarshalReservedMergeRequiresSeparateCommitBoundApproval(t *testing.T) {
	s, repo := marshalFixture(t, 1)
	fake := s.Model.(marshalFakeModel)
	fake.draft.Plan.Tasks[0].Paths = append(fake.draft.Plan.Tasks[0].Paths, "AGENTS.md")
	fake.draft.Tasks[0].Files = append(fake.draft.Tasks[0].Files, "AGENTS.md")
	graph, err := plan.BuildGraph(fake.draft.Plan.Tasks)
	if err != nil {
		t.Fatal(err)
	}
	fake.draft.Plan.Graph = graph
	s.Model = fake
	original := s.Drivers["worker"].(driver.Governed)
	originalRun := original.Run
	original.Run = func(ctx context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		records, err := originalRun(ctx, req)
		if err != nil {
			return records, err
		}
		return records, os.WriteFile(filepath.Join(req.Worktree, "AGENTS.md"), []byte("new instructions"), 0600)
	}
	s.Drivers["worker"] = original
	startIntegrityRun(t, s, marshal.Budget{})
	h := acceptIntegrityTask(t, s, "a")
	var purpose string
	requests := 0
	s.ApprovalActor = func(_ context.Context, _ string, p string) (string, error) {
		if p == purpose && purpose != "" {
			purpose = ""
			return "operator", nil
		}
		return "", os.ErrPermission
	}
	s.ReservedMergeRequest = func(_ context.Context, runID, taskID string, rev int64, result string, files []string) {
		requests++
		if len(files) != 1 || files[0] != "AGENTS.md" || result != h.ResultCommit {
			t.Fatalf("request: %v %s", files, result)
		}
	}
	if err := s.Merge(t.Context(), "run", "a"); err == nil || !strings.Contains(err.Error(), "popup") {
		t.Fatalf("merge: %v", err)
	}
	if requests != 1 || mustMarshalRun(t, s).Tasks[0].State != marshal.Accepted {
		t.Fatal("no separate request or merged without consent")
	}
	if _, err := os.Stat(filepath.Join(repo, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatal("refused merge changed repository")
	}
	_, rev, _ := s.load(t.Context(), "run")
	purpose = ReservedMergePurpose("a", h.ResultCommit, rev)
	if err := s.Merge(t.Context(), "run", "a"); err != nil {
		t.Fatal(err)
	}
	if mustMarshalRun(t, s).Tasks[0].State != marshal.Merged {
		t.Fatal("approved merge failed")
	}
}

func TestMarshalGoverningFileTamperAlertsBeforeExecution(t *testing.T) {
	s, repo := marshalFixture(t, 1)
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	startIntegrityRun(t, s, marshal.Budget{})
	if mustMarshalRun(t, s).GoverningDigest == "" {
		t.Fatal("missing baseline")
	}
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Dispatch(t.Context(), "run", "a", "write"); err == nil || !strings.Contains(err.Error(), "governing files changed unexpectedly") {
		t.Fatalf("dispatch: %v", err)
	}
	if _, err := s.Resume(t.Context(), "run"); err == nil {
		t.Fatal("tampered run resumed")
	}
	events, err := s.Store.MarshalDecisions(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	alerts := 0
	for _, event := range events {
		if reason, _ := event.Data["reason"].(string); strings.Contains(reason, "governing files changed unexpectedly") {
			alerts++
		}
	}
	if alerts != 2 {
		t.Fatalf("alerts=%d", alerts)
	}
}

func TestMarshalReviewRecordsHaltingConstitutionalResponse(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	startIntegrityRun(t, s, marshal.Budget{})
	dispatch, err := s.Dispatch(t.Context(), "run", "a", "write")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CollectHandIn(t.Context(), "run", dispatch); err != nil {
		t.Fatal(err)
	}
	s.GateState = func(context.Context, string, string) (constitution.RuntimeState, error) {
		state := permissiveState()
		state.ForeignProjectRefs = []string{"PROJECT-other/file"}
		return state, nil
	}
	verdict, err := s.Review(t.Context(), "run", "a", knownCharge())
	if err != nil || verdict != marshal.VerdictReturn {
		t.Fatalf("review: %s %v", verdict, err)
	}
	open, err := s.constitutionService().OpenViolations(t.Context(), "run")
	if err != nil || len(open) != 1 || open[0].Response != constitution.ResponseSuspend {
		t.Fatalf("review response: %v %v", open, err)
	}
	s.GateState = func(context.Context, string, string) (constitution.RuntimeState, error) {
		return permissiveState(), nil
	}
	if _, err := s.Dispatch(t.Context(), "run", "a", "retry"); err == nil {
		t.Fatal("review suspension allowed retry")
	}
}

func TestGoverningDigestDetectsSymlinkTargetChange(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "instructions")
	if err := os.WriteFile(target, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	before, err := governingDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	after, err := governingDigest(root)
	if err != nil || after == before {
		t.Fatalf("target change undetected: %s %v", after, err)
	}
}

func TestUnapprovedResumePreservesPauseExplanationAndSuspension(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	if _, err := s.StartPlanning(t.Context(), "run", "write", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Escalate(t.Context(), "run", "", "security suspension"); err != nil {
		t.Fatal(err)
	}
	s.GateState = s.observedGateState
	if _, err := s.Resume(t.Context(), "run"); err == nil || !strings.Contains(err.Error(), "security suspension") || !strings.Contains(err.Error(), "next step") {
		t.Fatalf("pause explanation: %v", err)
	}
	service := s.constitutionService()
	env := constitution.Envelope{DecisionID: "preapproval-suspend", ConstitutionVersion: constitution.Current, Process: 6, ProjectID: s.ProjectID, SessionID: "run", Actor: "runtime", Surface: constitution.SurfaceCore, Mode: constitution.ModeStandard, Domain: constitution.DomainProjectMutation, Action: "test", Reversibility: constitution.ReversibleInternal, StateDigest: "sha256:state", RequestedAt: s.clock()}
	state := permissiveState()
	state.ForeignProjectRefs = []string{"PROJECT-other/file"}
	if _, err := service.Decide(t.Context(), DecideRequest{Envelope: env, State: state}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resume(t.Context(), "run"); err == nil || !strings.Contains(err.Error(), "constitutional admission refused") {
		t.Fatalf("preapproval suspension: %v", err)
	}
}

func TestGoverningDigestRefusesSymlinkToPipe(t *testing.T) {
	root := t.TempDir()
	pipe := filepath.Join(t.TempDir(), "instructions-pipe")
	if err := syscall.Mkfifo(pipe, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(pipe, filepath.Join(root, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := governingDigest(root); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("unsafe governing target: %v", err)
	}
}

func TestRuntimeImportedMemoryRequestKeepsUnverifiedProvenance(t *testing.T) {
	runtime := constitutionRuntime(t)
	var requests []permission.Request
	runtime.SetPermissionSink(func(req permission.Request) { requests = append(requests, req) })
	_, _, err := runtime.ProposeContinuation(t.Context(), importer.SessionTranscript{SessionID: "external-chat", Provider: "codex", CWD: runtime.ProjectRoot(), Messages: []importer.Message{{Role: "assistant", Content: "Pending work"}}})
	if err != nil || len(requests) != 1 || requests[0].Who != "Local request (unverified)" {
		t.Fatalf("imported candidate request: %v %v", requests, err)
	}
}

func TestGoverningDigestExcludesTransientBriefingDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("project instructions"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("claude instructions"), 0600); err != nil {
		t.Fatal(err)
	}
	initialDigest, err := governingDigest(root)
	if err != nil {
		t.Fatal(err)
	}

	briefingDir := filepath.Join(root, ".marshal", "briefing", "antigravity-123456")
	if err := os.MkdirAll(briefingDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(briefingDir, "AGENTS.md"), []byte("briefing instructions"), 0600); err != nil {
		t.Fatal(err)
	}

	digestWithBriefing, err := governingDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	if digestWithBriefing != initialDigest {
		t.Fatalf("expected digest to remain unchanged when briefing files are added: got %s, want %s", digestWithBriefing, initialDigest)
	}

	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("modified instructions"), 0600); err != nil {
		t.Fatal(err)
	}
	digestAfterMod, err := governingDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	if digestAfterMod == initialDigest {
		t.Fatal("expected digest to change when project AGENTS.md is modified")
	}
}

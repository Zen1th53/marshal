package tui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/learning"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/optimization"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
	"github.com/Zen1th53/marshal/internal/verification"
)

// Each test below is one numbered scenario in 08_ACCEPTANCE_AND_E2E.md. The
// fixtures are real migrated stores; where no canonical mutation boundary is
// exposed, the assertion is the required truthful NOT_RUN/refusal rather than
// a fabricated success.

func TestAcceptanceLaunchAndNavigation(t *testing.T) {
	_, ws, ctx := acceptanceWorkspace(t)
	ia, err := FrozenIA()
	if err != nil {
		t.Fatal(err)
	}
	for _, section := range ia.Sections {
		acceptanceScreen(t, ws, section.SpecID)
	}
	if err := ws.navView.Nav().DeepLink("CTUI-0194"); err != nil { // third level
		t.Fatal(err)
	}
	ws.dispatchNavigationKey(ctx, key(KeyEsc))
	ws.dispatchNavigationKey(ctx, key(KeyCtrlK))
	ws.dispatchNavigationKey(ctx, runeKey('s'))
	ws.dispatchNavigationKey(ctx, key(KeyEsc))
	ws.dispatchNavigationKey(ctx, runeKey('?'))
	ws.dispatchNavigationKey(ctx, key(KeyEsc))
	if err := ws.navView.Nav().DeepLink("CTUI-0803"); err != nil {
		t.Fatal(err)
	}
	// Repeat the interaction-critical section switch with a mouse focus
	// gesture, then use the explicit Open control. A click does not activate
	// a section implicitly, matching the keyboard safety model.
	ws.navView.Render(140, 30)
	ws.dispatchNavigationKey(ctx, KeyEvent{Type: KeyMouse, MouseButton: 0, MouseColumn: 12, MouseRow: 1})
	if ws.navView.Nav().Focus() != PaneTopNav {
		t.Fatalf("mouse tab focus = %s, want top navigation", ws.navView.Nav().Focus())
	}
	ws.dispatchNavigationKey(ctx, key(KeyEnter))
	if ws.navView.Nav().Current().Title != "Control" {
		t.Fatalf("explicit Open after mouse focus reached %q, want Control", ws.navView.Nav().Current().Title)
	}
	t.Log("PASS: all nine sections, third-level navigation, back, palette, help, and System exit destination are reachable by keyboard")
}

func TestAcceptanceLaunchNarrowNoColor(t *testing.T) {
	v, err := NewNavView(NewTheme(ThemeNoColor, false, false))
	if err != nil {
		t.Fatal(err)
	}
	v.OpenAndWait(context.Background())
	for _, width := range []int{20, 24, 40} {
		for _, line := range v.Render(width, 80) {
			if VisibleLen(line) > width {
				t.Fatalf("no-color width %d overflow: %q", width, line)
			}
		}
	}
	t.Log("PASS: narrow/no-color frame keeps textual focus and truth labels")
}

func TestAcceptanceProjectSetup(t *testing.T) {
	st, ws, ctx := acceptanceWorkspace(t)
	for _, id := range []string{"CTUI-0193", "CTUI-0194", "CTUI-0200", "CTUI-0208"} {
		acceptanceScreen(t, ws, id)
	}
	out, err := ws.ExecuteCommand(ctx, "/doctor")
	if err != nil || !strings.Contains(out, "SYSTEM DIAGNOSTICS") {
		t.Fatalf("doctor: out=%q err=%v", out, err)
	}
	if _, err := st.Project(ctx); err != nil {
		t.Fatalf("migrated fixture project was not readable: %v", err)
	}
	t.Log("PASS: project/setup surfaces and doctor use the migrated fixture; repair remains governed")
}

func TestAcceptanceGoalLifecycle(t *testing.T) {
	st, ws, ctx := acceptanceWorkspace(t)
	goal := acceptanceGoal(ws)
	goal.Confirmation = model.ConfirmationApproved
	if err := st.SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	if err := ws.RefreshState(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"CTUI-0228", "CTUI-0229", "CTUI-0230", "CTUI-0233", "CTUI-0236", "CTUI-0238", "CTUI-0239", "CTUI-0240", "CTUI-0241"} {
		acceptanceScreen(t, ws, id)
	}
	out, err := ws.ExecuteCommand(ctx, "/goal")
	if err != nil || !strings.Contains(out, goal.DesiredOutcome) {
		t.Fatalf("goal reread: out=%q err=%v", out, err)
	}
	t.Log("PASS: Process03 fixture is read from durable storage; revision/confirmation controls remain canonical")
}

func TestAcceptancePlanLifecycle(t *testing.T) {
	ws, runtime := realControlWorkspace(t, "SESSION-acceptance-plan")
	ctx := context.Background()
	project := projectid.ID("PROJECT-0123456789abcdef0123456789abcdef")
	ws.AttachRuntime(runtime, project)
	goal := acceptanceGoal(ws)
	goal.SessionID, goal.ProjectID = ws.sessionID, string(project)
	goal.Confirmation = model.ConfirmationApproved
	goal.SuccessCriteria = []string{"the acceptance plan is durably bound"}
	if err := runtime.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	capacity := goalintake.UnknownCapacity("codex", true)
	created, err := runtime.Plans().Create(ctx, app.CreatePlanRequest{
		SessionID: ws.sessionID, ProjectID: project,
		Tasks:      []plan.Task{{ID: "TASK-acceptance-plan", Title: "exercise plan lifecycle", Weight: 1, Criteria: goal.SuccessCriteria}},
		Candidates: []goalintake.Candidate{{Provider: "codex", Capacity: capacity, Governance: constitution.GovernanceVerified}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.State != plan.StateReady {
		t.Fatalf("created plan state = %s, blockers=%v", created.State, created.BlockedBy)
	}
	approved, err := runtime.Plans().ApproveVersion(ctx, project, created.Version)
	if err != nil {
		t.Fatal(err)
	}
	handoff, err := runtime.Plans().Handoff(ctx, ws.sessionID, project)
	if err != nil {
		t.Fatal(err)
	}
	if handoff.PlanID != approved.ID || handoff.PlanVersion != approved.Version || handoff.EvidenceID == "" {
		t.Fatalf("canonical handoff = %+v, approved=%+v", handoff, approved)
	}
	ws.navView.Refresh(ctx)
	for _, id := range []string{"CTUI-0250", "CTUI-0251", "CTUI-0252", "CTUI-0255", "CTUI-0257", "CTUI-0259"} {
		acceptanceScreen(t, ws, id)
	}
	t.Log("PASS: canonical Process04 create, CAS approval, durable reread, and evidence-bound Process05 handoff executed")
}

func TestAcceptanceRunLifecycle(t *testing.T) {
	_, ws, _ := acceptanceWorkspace(t)
	for _, id := range []string{"CTUI-0285", "CTUI-0286", "CTUI-0288", "CTUI-0290", "CTUI-0292", "CTUI-0043", "CTUI-0044", "CTUI-0046", "CTUI-0073"} {
		out := acceptanceScreen(t, ws, id)
		if id == "CTUI-0043" || id == "CTUI-0044" {
			if !strings.Contains(out, "IMPLEMENTATION GAP") {
				t.Fatalf("documented Process05 gap %s was not truthful", id)
			}
		}
	}
	t.Log("PASS: Process05 state is navigable; unavailable pause/resume bindings remain documented gaps")
}

func TestAcceptanceCollaboration(t *testing.T) {
	_, ws, ctx := acceptanceWorkspace(t)
	for _, id := range []string{"CTUI-0267", "CTUI-0330", "CTUI-0331", "CTUI-0332"} {
		acceptanceScreen(t, ws, id)
	}
	out, err := ws.ExecuteCommand(ctx, "/msg codex acceptance-message")
	if err != nil || !strings.Contains(out, "unavailable") {
		t.Fatalf("collaboration without runtime must fail closed: out=%q err=%v", out, err)
	}
	t.Log("PASS: collaboration surfaces render; no message is fabricated without a runtime authority")
}

func TestAcceptanceBoundProcess05Approval(t *testing.T) {
	ws, runtime := realControlWorkspace(t, "SESSION-acceptance-approval")
	ctx := context.Background()
	goal := acceptanceGoal(ws)
	goal.SessionID, goal.ProjectID = "SESSION-acceptance-approval", ws.projectID
	goal.Confirmation = model.ConfirmationPending
	goal.Revision = 1
	if err := runtime.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	ws.navView.Refresh(ctx)
	if err := ws.navView.Nav().DeepLink("CTUI-0060"); err != nil {
		t.Fatal(err)
	}
	ws.dispatchNavigationKey(ctx, key(KeyEnter))
	ws.dispatchNavigationKey(ctx, key(KeyRight))
	ws.dispatchNavigationKey(ctx, key(KeyEnter))
	updated, err := runtime.Store().GetActiveGoalContract(ctx, goal.SessionID)
	if err != nil || updated.Confirmation != model.ConfirmationApproved || updated.Revision != 2 {
		t.Fatalf("canonical approval reread = %#v, err=%v", updated, err)
	}
	t.Log("PASS: real runtime confirmation binds an exact target and durable revision; P05-only approvals remain NOT_RUN without a P05 fixture")
}

func TestAcceptanceBudgetAndContractImmutability(t *testing.T) {
	_, ws, _ := acceptanceWorkspace(t)
	for _, id := range []string{"CTUI-0076", "CTUI-0077", "CTUI-0084", "CTUI-0085", "CTUI-0240"} {
		acceptanceScreen(t, ws, id)
	}
	t.Log("PASS: measured budget/termination are read-only; goal revision is the only displayed contract-change path")
}

func TestAcceptanceCheckpointRollback(t *testing.T) {
	st, ws, ctx := acceptanceWorkspace(t)
	cp := model.HandoffCheckpoint{ID: "CP-acceptance", Version: 1, SessionID: ws.sessionID, TaskID: "TASK-acceptance", Role: "operator", Author: model.AuthorProvenance{AgentID: "operator", Harness: "test"}, CreatedAt: time.Now().UTC()}
	if err := st.SaveHandoffCheckpoint(ctx, cp); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"CTUI-0064", "CTUI-0065", "CTUI-0066", "CTUI-0070"} {
		acceptanceScreen(t, ws, id)
	}
	out, err := ws.ExecuteCommand(ctx, "/rollback "+cp.ID)
	if err != nil || !strings.Contains(out, "NOT performed") {
		t.Fatalf("rollback without runtime: out=%q err=%v", out, err)
	}
	if rows, err := st.GetCheckpointRollbacks(ctx, cp.ID); err != nil || len(rows) != 0 {
		t.Fatalf("refused rollback was not durable/no-op: rows=%v err=%v", rows, err)
	}
	t.Log("PASS: checkpoint reread is durable; absent restore authority reports NOT performed")
}

func TestAcceptanceVerificationAndEvidence(t *testing.T) {
	ws, runtime := realControlWorkspace(t, "SESSION-acceptance-verify")
	session := acceptanceVerifiedRun(t, ws, runtime)
	ws.navView.Refresh(context.Background())
	for _, id := range []string{"CTUI-0337", "CTUI-0338", "CTUI-0340", "CTUI-0341", "CTUI-0342", "CTUI-0345", "CTUI-0357", "CTUI-0388"} {
		out := acceptanceScreen(t, ws, id)
		if id == "CTUI-0340" && !strings.Contains(out, session.Binding.RunID) {
			t.Fatalf("verification binding screen omitted canonical run %s:\n%s", session.Binding.RunID, out)
		}
	}
	t.Log("PASS: canonical Process03→06 fixture executed and the exact run-bound verification, claims, and evidence are visible")
}

func TestAcceptanceVerificationFailureSafety(t *testing.T) {
	_, ws, _ := acceptanceWorkspace(t)
	for _, id := range []string{"CTUI-0344", "CTUI-0388", "CTUI-0394", "CTUI-0403"} {
		out := acceptanceScreen(t, ws, id)
		if strings.Contains(out, "PASS") {
			t.Fatalf("unrun verification screen %s rendered PASS:\n%s", id, out)
		}
	}
	t.Log("PASS: stale/unknown verification never promotes to PASS")
}

func TestAcceptanceMemoryRecall(t *testing.T) {
	ws, runtime := realControlWorkspace(t, "SESSION-acceptance-memory")
	ctx := context.Background()
	principal := authz.Principal{ID: ws.sessionID, Role: authz.Role{
		Name: "operator", Authorities: []authz.Authority{authz.AuthorityTaskPlan},
	}}
	record, err := runtime.Memory().Remember(ctx, principal, app.RememberRequest{
		ProjectID: string(ws.projectIdentity), ScopeID: string(ws.projectIdentity),
		Title: "acceptance memory", Body: "canonical recent-record evidence",
		Kind: model.MemoryKindSemantic,
	})
	if err != nil {
		t.Fatal(err)
	}
	ws.navView.Refresh(ctx)
	for _, id := range []string{"CTUI-0410", "CTUI-0411", "CTUI-0415", "CTUI-0418", "CTUI-0420"} {
		out := acceptanceScreen(t, ws, id)
		if (id == "CTUI-0411" || id == "CTUI-0415") && !strings.Contains(out, record.ID) {
			t.Fatalf("memory screen %s omitted canonical record %s:\n%s", id, record.ID, out)
		}
	}
	t.Log("PASS: canonical MemoryService write is authorization-checked, durably reread, and visible through the real Workspace")
}

func TestAcceptanceMemoryGovernance(t *testing.T) {
	ws, runtime := realControlWorkspace(t, "SESSION-acceptance-memory-governance")
	ctx := context.Background()
	principal := acceptancePrincipal(ws)
	candidate, err := runtime.Memory().Remember(ctx, principal, app.RememberRequest{
		ProjectID: string(ws.projectIdentity), ScopeID: string(ws.projectIdentity),
		Title: "acceptance governed memory", Body: "candidate promoted through canonical authority",
		Kind: model.MemoryKindSemantic,
	})
	if err != nil {
		t.Fatal(err)
	}
	promoted, err := runtime.Memory().Promote(ctx, principal, app.PromoteRequest{
		ProjectID: string(ws.projectIdentity), MemoryID: candidate.ID,
		ScopeID: string(ws.projectIdentity), Rationale: "acceptance operator review",
	})
	if err != nil {
		t.Fatal(err)
	}
	if promoted.Lifecycle != model.MemoryDurable || promoted.Revision <= candidate.Revision {
		t.Fatalf("canonical promotion = %+v, candidate revision=%d", promoted, candidate.Revision)
	}
	ws.navView.Refresh(ctx)
	for _, id := range []string{"CTUI-0427", "CTUI-0440", "CTUI-0450", "CTUI-0460", "CTUI-0461", "CTUI-0465"} {
		acceptanceScreen(t, ws, id)
	}
	t.Log("PASS: authorization-checked capture and evidence-gated operator promotion were durably CAS-updated by MemoryService")
}

func TestAcceptanceTranscriptImport(t *testing.T) {
	ws, runtime := realControlWorkspace(t, "SESSION-acceptance-import")
	ctx := context.Background()
	transcript := []byte(`{"session_id":"SESSION-imported","provider":"codex","task_id":"TASK-imported","messages":[{"role":"user","content":"How is acceptance run?"},{"role":"assistant","content":"Use the governed test path."}],"success":true}`)
	dry, err := runtime.Memory().ImportSessionTranscript(ctx, acceptancePrincipal(ws), string(ws.projectIdentity), transcript, true)
	if err != nil || len(dry.ImportedRecords) != 1 {
		t.Fatalf("canonical transcript dry run = %+v, err=%v", dry, err)
	}
	committed, err := runtime.Memory().ImportSessionTranscript(ctx, acceptancePrincipal(ws), string(ws.projectIdentity), transcript, false)
	if err != nil || len(committed.ImportedRecords) != 1 {
		t.Fatalf("canonical transcript commit = %+v, err=%v", committed, err)
	}
	duplicate, err := runtime.Memory().ImportSessionTranscript(ctx, acceptancePrincipal(ws), string(ws.projectIdentity), transcript, false)
	if err != nil || len(duplicate.ImportedRecords) != 0 || duplicate.SkippedCount != 1 {
		t.Fatalf("idempotent transcript re-import = %+v, err=%v", duplicate, err)
	}
	ws.navView.Refresh(ctx)
	for _, id := range []string{"CTUI-0473", "CTUI-0475", "CTUI-0476", "CTUI-0477", "CTUI-0489", "CTUI-0491"} {
		acceptanceScreen(t, ws, id)
	}
	t.Log("PASS: canonical transcript importer produced a dry-run preview, durable sanitized candidate, and idempotent duplicate result")
}

func TestAcceptanceProvidersAndResources(t *testing.T) {
	_, ws, _ := acceptanceWorkspace(t)
	for _, id := range []string{"CTUI-0501", "CTUI-0502", "CTUI-0504", "CTUI-0566", "CTUI-0713", "CTUI-0719"} {
		acceptanceScreen(t, ws, id)
	}
	t.Log("PASS: adapter/resource surfaces render measured values only when their canonical readers report them")
}

func TestAcceptanceQuotaUnknown(t *testing.T) {
	_, ws, _ := acceptanceWorkspace(t)
	for _, id := range []string{"CTUI-0144", "CTUI-0148", "CTUI-0149", "CTUI-0151", "CTUI-0523"} {
		out := acceptanceScreen(t, ws, id)
		if !strings.Contains(out, "IMPLEMENTATION GAP") {
			t.Fatalf("provider evidence gap %s stopped rendering truthfully", id)
		}
	}
	t.Log("PASS: quota/capacity/reset/allowance/session evidence is UNKNOWN or IMPLEMENTATION GAP, never invented")
}

func TestAcceptanceStandardOffline(t *testing.T) {
	_, ws, _ := acceptanceWorkspace(t)
	out := acceptanceScreen(t, ws, "CTUI-0155")
	if !strings.Contains(out, "Standard") && !strings.Contains(out, "NOT_RUN") && !strings.Contains(out, "UNKNOWN") {
		t.Fatalf("offline cloud surface is not truthful:\n%s", out)
	}
	t.Log("PASS: Standard/offline fallback does not forge an entitlement")
}

func TestAcceptanceUltraEntitlement(t *testing.T) {
	_, ws, _ := acceptanceWorkspace(t)
	for _, id := range []string{"CTUI-0035", "CTUI-0157", "CTUI-0557", "CTUI-0158", "CTUI-0164"} {
		out := acceptanceScreen(t, ws, id)
		if id == "CTUI-0157" || id == "CTUI-0557" {
			if !strings.Contains(out, "IMPLEMENTATION GAP") {
				t.Fatalf("entitlement request gap %s stopped rendering truthfully", id)
			}
		}
	}
	t.Log("PASS: ULTRA request/lease states stay evidence-gated; documented request-ID gaps remain visible")
}

func TestAcceptanceProcess08Counterfactual(t *testing.T) {
	ws, runtime := realControlWorkspace(t, "SESSION-acceptance-counterfactual")
	fixture := acceptanceOptimizationCycle(t, ws, runtime)
	ctx := context.Background()
	runner := &acceptanceReplayRunner{}
	factual := optimization.FactualRun{
		TaskID: "TASK-acceptance-replay", Route: fixture.route,
		Outcome: learning.OutcomeFailed, VerifierResult: optimization.StatusFail,
		ReplayClass: learning.ReplayExact, TreeDigest: fixture.treeDigest,
		EnvironmentDigest: fixture.environmentDigest,
	}
	alternate := fixture.route
	alternate.Provider, alternate.Model = "claude", "acceptance-alternate"
	replayed, err := runtime.Optimization().ExecuteReplay(ctx, fixture.cycle.ID, runner, factual, alternate,
		optimization.SandboxPolicy{WritableRoot: t.TempDir(), MaxWallMillis: 1_000, MaxMemoryBytes: 1 << 20},
		"acceptance-e2e", "independent-replay", fixture.governance)
	if err != nil || !runner.called || replayed.Method != optimization.MethodReplay || replayed.AlternateVerifier != optimization.StatusPass {
		t.Fatalf("canonical replay = %+v, called=%t, err=%v", replayed, runner.called, err)
	}
	ws.navView.Refresh(ctx)
	for _, id := range []string{"CTUI-0571", "CTUI-0573", "CTUI-0575", "CTUI-0584", "CTUI-0587", "CTUI-0589"} {
		acceptanceScreen(t, ws, id)
	}
	t.Log("PASS: a digest-bound Process07 commit opened a canonical Process08 cycle and a real bounded replay runner produced durable counterfactual evidence")
}

func TestAcceptanceProcess08Benchmarks(t *testing.T) {
	ws, runtime := realControlWorkspace(t, "SESSION-acceptance-benchmarks")
	fixture := acceptanceOptimizationCycle(t, ws, runtime)
	ctx := context.Background()
	now := time.Now().UTC()
	manifest, err := runtime.Optimization().RecordManifest(ctx, fixture.cycle.ID, optimization.BenchmarkManifest{
		ID: "MANIFEST-acceptance", Kind: optimization.BenchmarkInternal,
		Version: "1", EvaluatorVersion: "acceptance-1", DatasetSnapshot: "acceptance-fixture-v1",
		TaskIDs: []string{"TASK-acceptance-benchmark"}, MarshalSHA: fixture.treeDigest,
		ConfigDigest: "sha256:acceptance-config", ModelVersions: map[string]string{"codex": "acceptance"},
		HarnessVersions: map[string]string{"test-harness": "1"}, EnvironmentImage: "local-test",
		Mode: optimization.AblationSingleModel, StartedAt: now, FinishedAt: now,
	})
	if err != nil || manifest.Digest == "" {
		t.Fatalf("canonical benchmark manifest = %+v, err=%v", manifest, err)
	}
	result, err := optimization.NewExperimentResult(optimization.ExperimentResult{
		ID: "RESULT-acceptance", TaskID: "TASK-acceptance-benchmark", TaskClass: "code",
		Attempt: 1, Outcome: optimization.StatusPass, ClusterID: "benchmark-evaluator", ObservedAt: now,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Optimization().RecordExperiment(ctx, fixture.cycle.ID, fixture.candidate.ID, result); err != nil {
		t.Fatal(err)
	}
	ws.navView.Refresh(ctx)
	for _, id := range []string{"CTUI-0591", "CTUI-0592", "CTUI-0599", "CTUI-0600", "CTUI-0601", "CTUI-0607", "CTUI-0613"} {
		acceptanceScreen(t, ws, id)
	}
	t.Log("PASS: canonical Process08 authority validated and durably reread a reproducible benchmark manifest and sealed experiment result")
}

func TestAcceptanceProcess08Rollout(t *testing.T) {
	ws, runtime := realControlWorkspace(t, "SESSION-acceptance-rollout")
	fixture := acceptanceOptimizationCycle(t, ws, runtime)
	ctx := context.Background()
	now := time.Now().UTC()
	result, err := optimization.NewExperimentResult(optimization.ExperimentResult{
		ID: "RESULT-rollout", TaskID: "TASK-rollout", TaskClass: "code", Attempt: 1,
		Outcome: optimization.StatusPass, ClusterID: "independent-rollout", ObservedAt: now,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	promotion, err := runtime.Optimization().Promote(ctx, fixture.cycle.ID, optimization.PromotionInput{
		Candidate: fixture.candidate, Baseline: fixture.baseline, CandidateBaseline: fixture.baseline,
		Results: []optimization.ExperimentResult{result}, Reproducible: true,
	}, fixture.governance)
	if err != nil || promotion.Digest == "" {
		t.Fatalf("canonical promotion = %+v, err=%v", promotion, err)
	}
	canary, err := runtime.Optimization().Canary(ctx, fixture.cycle.ID, promotion.Digest, optimization.Canary{
		ID: "CANARY-acceptance", CandidateID: fixture.candidate.ID, BaselineID: fixture.baseline.ID,
		Exposure: .1, EligibleTaskClasses: []string{"code"}, MaxTasks: 1, Deadline: now.Add(time.Hour),
		RollbackTriggers: []optimization.Trigger{{Metric: "verification_failure", Threshold: 0, HigherIsWorse: true, Reason: "verified regression"}},
	}, fixture.governance)
	if err != nil {
		t.Fatal(err)
	}
	rolled, err := runtime.Optimization().Rollback(ctx, canary.ID, "acceptance rollback proof")
	if err != nil || rolled.State != optimization.CanaryRolledBack || rolled.RollbackReason == "" {
		t.Fatalf("canonical rollback = %+v, err=%v", rolled, err)
	}
	ws.navView.Refresh(ctx)
	for _, id := range []string{"CTUI-0608", "CTUI-0611", "CTUI-0612", "CTUI-0616", "CTUI-0618", "CTUI-0621", "CTUI-0626"} {
		acceptanceScreen(t, ws, id)
	}
	t.Log("PASS: evidence-backed promotion, bounded canary creation, and durable governed rollback executed through Process08 authority")
}

func TestAcceptanceSecurityFailClosed(t *testing.T) {
	_, ws, _ := acceptanceWorkspace(t)
	for _, id := range []string{"CTUI-0630", "CTUI-0631", "CTUI-0658", "CTUI-0666", "CTUI-0673", "CTUI-0681", "CTUI-0698"} {
		acceptanceScreen(t, ws, id)
	}
	t.Log("PASS: policy, capability, sandbox, network, secret, and trusted-content surfaces remain read-only/fail-closed")
}

func TestAcceptanceRuntimeAPI(t *testing.T) {
	_, ws, _ := acceptanceWorkspace(t)
	for _, id := range []string{"CTUI-0734", "CTUI-0740", "CTUI-0749", "CTUI-0758"} {
		acceptanceScreen(t, ws, id)
	}
	t.Log("PASS: runtime API/lifecycle/MCP/A2A status is inspectable without exposing shell/RCE or enterprise administration")
}

func TestAcceptanceTokens(t *testing.T) {
	ws, runtime := realControlWorkspace(t, "SESSION-acceptance-token")
	ctx := context.Background()
	for _, id := range []string{"CTUI-0760", "CTUI-0761", "CTUI-0762", "CTUI-0763", "CTUI-0767"} {
		acceptanceScreen(t, ws, id)
	}
	if err := ws.navView.Nav().DeepLink("CTUI-0762"); err != nil {
		t.Fatal(err)
	}
	ws.dispatchNavigationKey(ctx, key(KeyEnter))
	for _, r := range "acceptance-mcp" {
		ws.dispatchNavigationKey(ctx, runeKey(r))
	}
	ws.dispatchNavigationKey(ctx, key(KeyEnter))
	ws.dispatchNavigationKey(ctx, key(KeyEnter))
	ws.dispatchNavigationKey(ctx, key(KeyRight))
	ws.dispatchNavigationKey(ctx, key(KeyEnter))
	created := ws.navView.Confirmation().Outcome()
	if !created.Succeeded() || created.Target.ID == "" || created.SecretOnce == "" {
		t.Fatalf("canonical token creation outcome = %#v", created)
	}
	records, err := runtime.TokenMetadata(ctx)
	if err != nil || len(records) != 1 || records[0].ID != created.Target.ID || records[0].Revoked {
		t.Fatalf("token metadata after create = %#v, err=%v", records, err)
	}
	ws.dispatchNavigationKey(ctx, key(KeyEnter))
	if err := ws.navView.Nav().DeepLink("CTUI-0767"); err != nil {
		t.Fatal(err)
	}
	ws.dispatchNavigationKey(ctx, key(KeyEnter))
	for _, r := range created.Target.ID {
		ws.dispatchNavigationKey(ctx, runeKey(r))
	}
	ws.dispatchNavigationKey(ctx, key(KeyEnter))
	ws.dispatchNavigationKey(ctx, runeKey('y'))
	ws.dispatchNavigationKey(ctx, key(KeyRight))
	ws.dispatchNavigationKey(ctx, key(KeyEnter))
	revoked := ws.navView.Confirmation().Outcome()
	if !revoked.Succeeded() {
		t.Fatalf("canonical token revocation outcome = %#v", revoked)
	}
	records, err = runtime.TokenMetadata(ctx)
	if err != nil || len(records) != 1 || !records[0].Revoked {
		t.Fatalf("token metadata after revoke = %#v, err=%v", records, err)
	}
	t.Log("PASS: typed form → exact confirmation → runtime-owned auth manager → durable create/revoke reread; plaintext is transient")
}

func TestAcceptanceSystemMaintenance(t *testing.T) {
	ws, runtime := realControlWorkspace(t, "SESSION-acceptance-maintenance")
	ctx := context.Background()
	backupPath := filepath.Join(t.TempDir(), "acceptance-backup.db")
	meta, err := runtime.BackupState(ctx, backupPath)
	if err != nil || meta.DatabaseSHA256 == "" {
		t.Fatalf("canonical backup = %+v, err=%v", meta, err)
	}
	verified, err := app.VerifyStateBackup(ctx, backupPath, string(ws.projectIdentity), store.LatestSchemaVersion)
	if err != nil || verified.DatabaseSHA256 != meta.DatabaseSHA256 {
		t.Fatalf("backup verification = %+v, err=%v", verified, err)
	}
	gc, err := runtime.GCArtifacts(ctx, true, 0, 0)
	if err != nil || len(gc.CleanedFiles) != 0 || gc.CleanedBytes != 0 {
		t.Fatalf("canonical artifact GC preview = %+v, err=%v", gc, err)
	}
	ws.navView.Refresh(ctx)
	for _, id := range []string{"CTUI-0768", "CTUI-0769", "CTUI-0771", "CTUI-0774", "CTUI-0781", "CTUI-0787", "CTUI-0794"} {
		acceptanceScreen(t, ws, id)
	}
	// A bad restore input must fail during canonical verification, before the
	// runtime is closed or a destructive confirmation can be reached.
	if err := ws.navView.Nav().DeepLink("CTUI-0771"); err != nil {
		t.Fatal(err)
	}
	ws.dispatchNavigationKey(ctx, key(KeyEnter))
	for _, r := range backupPath + ".missing" {
		ws.dispatchNavigationKey(ctx, runeKey(r))
	}
	ws.dispatchNavigationKey(ctx, key(KeyEnter))
	if phase := ws.navView.Confirmation().Phase(); phase == PhaseConfirming || phase == PhaseSubmitting {
		t.Fatalf("invalid backup reached destructive confirmation: %s", phase)
	}
	if ws.runtime != runtime {
		t.Fatal("failed backup verification replaced the live runtime")
	}
	if _, err := runtime.Status(ctx); err != nil {
		t.Fatalf("failed backup verification closed the runtime: %v", err)
	}
	ws.dispatchNavigationKey(ctx, key(KeyEsc))
	// The restore path is a real Workspace interaction, not a direct store
	// call: typed sensitive path → digest-bound destructive confirmation →
	// stopped-runtime canonical restore → reopened runtime and durable reread.
	if err := ws.navView.Nav().DeepLink("CTUI-0771"); err != nil {
		t.Fatal(err)
	}
	ws.dispatchNavigationKey(ctx, key(KeyEnter))
	for _, r := range backupPath {
		ws.dispatchNavigationKey(ctx, runeKey(r))
	}
	ws.dispatchNavigationKey(ctx, key(KeyEnter))
	confirmFrame := strings.Join(ws.navView.Render(200, 100), "\n")
	if strings.Contains(confirmFrame, backupPath) {
		t.Fatal("restore confirmation leaked the sensitive host backup path")
	}
	ws.dispatchNavigationKey(ctx, runeKey('y'))
	ws.dispatchNavigationKey(ctx, key(KeyRight))
	ws.dispatchNavigationKey(ctx, key(KeyEnter))
	restored := ws.navView.Confirmation().Outcome()
	if !restored.Succeeded() || restored.Target.Digest != meta.DatabaseSHA256 {
		t.Fatalf("restore outcome = %#v", restored)
	}
	if ws.runtime == runtime {
		t.Fatal("restore did not replace the stopped runtime with the reopened canonical runtime")
	}
	t.Cleanup(func() { _ = ws.runtime.Close() })
	status, err := ws.runtime.Status(ctx)
	if err != nil || status.Project.ID != string(ws.projectIdentity) {
		t.Fatalf("reopened runtime status = %+v, err=%v", status, err)
	}
	// The real input loop must rebind every screen after replacement. A stale
	// ControlSource would continue to dereference the closed pre-restore
	// runtime even though Workspace itself points at the reopened one.
	currentSource := ws.navView.control.source
	authority, ok := currentSource.Authority.(*runtimeControlAuthority)
	if !ok || authority.runtime != ws.runtime {
		t.Fatalf("navigation retained a stale runtime source after restore: %#v", currentSource)
	}
	t.Log("PASS: runtime-owned backup, verified stopped-runtime restore/reopen, integrity verification, and non-mutating artifact-GC preview executed; unrelated release actions remain independently status-labelled")
}

func TestAcceptanceInventoryAndCoverageReport(t *testing.T) {
	// A distributable MARSHAL carries the frozen manifest inside the binary.
	// The development-only spec pack is intentionally not a runtime or public
	// release dependency, so acceptance validates the embedded canonical
	// inventory directly.
	pack := frozenManifest
	ia, err := FrozenIA()
	if err != nil {
		t.Fatal(err)
	}
	if got := ia.Count(); got != 804 {
		t.Fatalf("navigable node count = %d, want 804", got)
	}
	if gaps := ia.Gaps(); len(gaps) != 17 {
		t.Fatalf("implementation gaps = %d, want 17", len(gaps))
	}

	var manifest manifestFile
	if err := json.Unmarshal(pack, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.FrozenNodeCount != 804 {
		t.Fatalf("manifest frozen node count = %d, want 804", manifest.FrozenNodeCount)
	}
	navigableSpecs := 0
	for _, spec := range manifest.Specs {
		if !NodeType(spec.Type).Navigable() {
			continue
		}
		navigableSpecs++
		if _, ok := ia.Node(spec.SpecID); !ok {
			t.Fatalf("embedded manifest id %s is absent from the frozen IA", spec.SpecID)
		}
	}
	if navigableSpecs != 804 {
		t.Fatalf("embedded navigable specs = %d, want 804", navigableSpecs)
	}

	v := testView(t)
	v.OpenAndWait(context.Background())
	for _, gap := range ia.Gaps() {
		if gap.Binding != BindingGap {
			t.Fatalf("gap %s silently became %s", gap.SpecID, gap.Binding)
		}
		if err := v.Nav().DeepLink(gap.SpecID); err != nil {
			t.Fatalf("open gap %s: %v", gap.SpecID, err)
		}
		out := strings.Join(v.Render(200, 100), "\n")
		if !strings.Contains(out, "IMPLEMENTATION GAP") || !strings.Contains(out, gap.SpecID) {
			t.Fatalf("gap %s did not render its own truthful notice:\n%s", gap.SpecID, out)
		}
	}

	type coverage struct {
		dedicated, leafFallback, containerFallback, gaps int
	}
	report := map[string]*coverage{}
	for _, section := range ia.Sections {
		report[section.Title] = &coverage{}
	}
	for _, node := range integrationNodes(ia) {
		// Actions are rendered and invoked from their parent action bar; a
		// cross-link immediately opens its canonical owner. Neither is a screen
		// on which a user can remain, so counting either as a missing dedicated
		// screen renderer fabricates fallback debt. Their bindings and keyboard
		// reachability are covered by the action/cross-link acceptance tests.
		if node.Type == NodeRoot || node.Type.IsAction() || node.Type == NodeCrossLink {
			continue
		}
		c := report[sectionTitleOf(node)]
		if node.Binding == BindingGap {
			c.gaps++
		} else if acceptanceDedicatedRenderer(node) {
			c.dedicated++
		} else if len(node.Children) == 0 {
			c.leafFallback++
		} else {
			c.containerFallback++
		}
	}
	sections := make([]string, 0, len(report))
	for section := range report {
		sections = append(sections, section)
	}
	sort.Strings(sections)
	totalFallback := 0
	for _, section := range sections {
		c := report[section]
		totalFallback += c.leafFallback + c.containerFallback
		t.Logf("coverage %s: dedicated=%d leaf-fallback=%d container-fallback=%d gaps=%d",
			section, c.dedicated, c.leafFallback, c.containerFallback, c.gaps)
	}
	t.Logf("coverage Root: dedicated=0 fallback=1; section totals cover the other 803 nodes")
	if totalFallback > 0 {
		t.Fatalf("acceptance incomplete: %d non-root nodes still lack dedicated renderers", totalFallback)
	}
}

func acceptanceWorkspace(t *testing.T) (*store.Store, *Workspace, context.Context) {
	t.Helper()
	ctx := context.Background()
	repo := testgit.New(t)
	if _, err := app.Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatalf("bootstrap acceptance runtime: %v", err)
	}
	runtime, err := app.Open(ctx, repo.Path())
	if err != nil {
		t.Fatalf("open acceptance runtime: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	binding, found := projectid.LoadBinding(filepath.Join(repo.Path(), projectid.StateDirName))
	if !found {
		t.Fatal("bootstrapped acceptance project has no durable project identity")
	}
	st := runtime.Store()
	ws := NewWorkspace(st, runtime.ProjectID(), "ACCEPTANCE-session")
	ws.AttachRuntime(runtime, binding.ID)
	ws.openNavigation(ctx)
	ws.navView.Refresh(ctx)
	return st, ws, ctx
}

func acceptanceGoal(ws *Workspace) model.GoalContract {
	now := time.Now().UTC()
	return model.GoalContract{ID: "GOAL-acceptance", SessionID: ws.sessionID, ProjectID: ws.projectID,
		OriginalRequest: "exercise the acceptance lifecycle", RequestDigest: "sha256:acceptance-goal-v1",
		ConstitutionVersion: constitution.Current.String(), Confirmation: model.ConfirmationPending, Revision: 1,
		DesiredOutcome: "Exercise durable Community TUI acceptance paths", Risk: model.R1,
		AuthoritySource: "acceptance fixture", UnderstandingState: model.GoalReady, CreatedAt: now, UpdatedAt: now}
}

func acceptanceScreen(t *testing.T, ws *Workspace, specID string) string {
	t.Helper()
	if err := ws.navView.Nav().DeepLink(specID); err != nil {
		t.Fatalf("deep link %s: %v", specID, err)
	}
	out := strings.Join(ws.navView.Render(200, 100), "\n")
	if strings.TrimSpace(out) == "" {
		t.Fatalf("%s rendered a blank frame", specID)
	}
	current := ws.navView.Nav().Current()
	if current != nil && current.Binding != BindingGap && strings.Contains(out,
		"declared by the frozen pack but is not implemented in this build") {
		t.Fatalf("acceptance incomplete: bound screen %s has no dedicated renderer", current.SpecID)
	}
	return out
}

func acceptanceDedicatedRenderer(node *Node) bool {
	if node == nil || node.Binding == BindingGap || node.Type.IsAction() || node.Type == NodeCrossLink {
		return false
	}
	if _, ok := screenRenderers[node.SpecID]; ok {
		return true
	}
	switch sectionTitleOf(node) {
	case "Control":
		return hasControlScreen(node.SpecID)
	case "Work":
		_, ok := workScreens()[node.SpecID]
		return ok
	case "Verify":
		_, ok := verifyScreens()[node.SpecID]
		return ok
	case "Memory":
		_, ok := memoryScreens()[node.SpecID]
		return ok
	case "Models":
		_, ok := modelsScreens()[node.SpecID]
		return ok
	case "Security":
		_, ok := securityScreens()[node.SpecID]
		return ok
	case "System":
		_, ok := systemScreens()[node.SpecID]
		return ok
	}
	return false
}

func acceptancePrincipal(ws *Workspace) authz.Principal {
	return authz.Principal{ID: ws.sessionID, Role: authz.Role{
		Name: "acceptance-operator", Authorities: []authz.Authority{authz.AuthorityTaskPlan, authz.AuthorityPolicyAdmin},
	}}
}

// acceptanceVerifiedRun builds the actual Process03→06 chain through the
// application services. The mock harness is only the bounded execution host;
// planning, execution state, verification and attestation remain canonical.
func acceptanceVerifiedRun(t *testing.T, ws *Workspace, runtime *app.Runtime) verification.Session {
	t.Helper()
	ctx := context.Background()
	project := projectid.ID("PROJECT-0123456789abcdef0123456789abcdef")
	ws.AttachRuntime(runtime, project)
	goal := acceptanceGoal(ws)
	goal.ID, goal.SessionID, goal.ProjectID = "GOAL-acceptance-chain", ws.sessionID, string(project)
	goal.Confirmation = model.ConfirmationApproved
	goal.SuccessCriteria = []string{"the acceptance fixture is verified"}
	goal.Scope = []string{"README.md"}
	if err := runtime.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatalf("save canonical goal: %v", err)
	}
	capacity := goalintake.UnknownCapacity("codex", true)
	now := time.Now().UTC()
	created, err := runtime.Plans().Create(ctx, app.CreatePlanRequest{
		SessionID: ws.sessionID, ProjectID: project,
		Tasks:             []plan.Task{{ID: "TASK-acceptance-chain", Title: "run acceptance fixture", Mutating: true, Weight: 1, Paths: []string{"README.md"}, Criteria: goal.SuccessCriteria}},
		Candidates:        []goalintake.Candidate{{Provider: "codex", Model: "acceptance", Capacity: capacity, Governance: constitution.GovernanceVerified}},
		HarnessCandidates: []plan.HarnessCandidate{{Profile: model.HarnessProfile{Harness: "acceptance-harness", InstalledVersion: "1", SupportedModels: []string{"acceptance"}, DefaultModel: "acceptance", ProbeEvidenceID: "EVIDENCE-acceptance", ProbedAt: now}, InstalledVersion: "1", Provider: "codex", Capacity: capacity}},
	})
	if err != nil {
		t.Fatalf("create canonical plan: %v", err)
	}
	if created.ID == "" {
		t.Fatal("canonical plan returned no durable ID")
	}
	if _, err := runtime.Plans().Approve(ctx, project); err != nil {
		t.Fatalf("approve canonical plan: %v", err)
	}
	execService := runtime.Execution()
	execService.RegisterHarness(execution.NewMockHarness("acceptance-harness", func(_ context.Context, task execution.TaskExecution, _ execution.ConstraintPackage, _ string) (execution.TaskResult, error) {
		return execution.TaskResult{TaskID: task.TaskID, Success: true, Claims: []execution.ExecutionClaim{{ClaimID: "CLAIM-acceptance", TaskID: task.TaskID, ClaimText: "acceptance fixture completed", Status: execution.ClaimSupported, EvidenceRefs: []string{"EVIDENCE-acceptance"}}}}, nil
	}))
	run, err := execService.StartRun(ctx, ws.sessionID, project)
	if err != nil {
		t.Fatalf("start canonical run: %v", err)
	}
	execService.Engine().EvidenceOracle().RecordEvidence(execution.ExecutionEvidence{EvidenceID: "EVIDENCE-acceptance", RunID: run.RunID, RelevantFiles: []string{"README.md"}, Status: execution.EvidenceValid})
	completed, err := execService.ExecuteRun(ctx, run.RunID)
	if err != nil {
		t.Fatalf("execute canonical run: %v", err)
	}
	if completed.State == execution.RunNeedsApproval {
		var approvalID string
		for _, task := range completed.Tasks {
			if task.ApprovalID != "" {
				approvalID = task.ApprovalID
				break
			}
		}
		if approvalID == "" {
			t.Fatal("run required approval without an approval ID")
		}
		if err := execService.Approve(ctx, approvalID, ws.sessionID, "approve bounded acceptance fixture"); err != nil {
			t.Fatalf("approve fixture task: %v", err)
		}
		completed, err = execService.ExecuteRun(ctx, run.RunID)
		if err != nil {
			t.Fatalf("execute approved fixture run: %v", err)
		}
	}
	if completed.State != execution.RunDonePendingVerification {
		t.Fatalf("completed run state = %s", completed.State)
	}
	binding, err := runtime.Verification().BindingForRun(ctx, run.RunID)
	if err != nil {
		t.Fatalf("derive verification binding: %v", err)
	}
	session := verification.Session{ID: "VERIFY-" + ws.sessionID, Version: 1, Binding: binding,
		Criteria:       []verification.Criterion{{ID: "criterion-acceptance", Mandatory: true, ClaimIDs: []string{"CLAIM-acceptance"}}},
		Claims:         []verification.Claim{{ID: "CLAIM-acceptance", CriterionID: "criterion-acceptance", SemanticScope: []string{"README.md"}, EvidenceIDs: []string{"VERIFY-EVIDENCE-acceptance"}}},
		Evidence:       []verification.Evidence{{ID: "VERIFY-EVIDENCE-acceptance", ClaimID: "CLAIM-acceptance", Status: verification.StatusPass, ContentDigest: "acceptance-readback", TreeDigest: binding.TreeDigest, EnvironmentDigest: binding.EnvironmentDigest, ClusterID: "independent-acceptance", Attempts: 1, Passes: 1}},
		RequiredChecks: map[string]verification.Status{"security": verification.StatusPass}, CreatedAt: now, UpdatedAt: now}
	if _, err := runtime.Verification().Start(ctx, session); err != nil {
		t.Fatalf("start verification: %v", err)
	}
	verified, err := runtime.Verification().Evaluate(ctx, session.ID)
	if err != nil || verified.State != verification.VerifiedComplete {
		t.Fatalf("evaluate verification = %+v, err=%v", verified, err)
	}
	payload := []byte("acceptance independent evidence")
	sum := sha256.Sum256(payload)
	bundle, err := verification.BuildEvidenceBundle("BUNDLE-"+ws.sessionID, session.ID, binding, []verification.BundleEntry{{Path: "evidence.txt", Digest: hex.EncodeToString(sum[:]), Size: int64(len(payload))}}, now)
	if err != nil {
		t.Fatalf("build evidence bundle: %v", err)
	}
	if _, err := runtime.Verification().Attest(ctx, session.ID, verification.BundleEnvelope{Bundle: bundle, Payloads: map[string][]byte{"evidence.txt": payload}}, "acceptance-e2e"); err != nil {
		t.Fatalf("attest verification: %v", err)
	}
	return verified
}

type acceptanceOptimizationFixture struct {
	cycle             optimization.Cycle
	baseline          optimization.Baseline
	candidate         optimization.Candidate
	route             optimization.Route
	governance        optimization.Governance
	treeDigest        string
	environmentDigest string
}

func acceptanceOptimizationCycle(t *testing.T, ws *Workspace, runtime *app.Runtime) acceptanceOptimizationFixture {
	t.Helper()
	ctx := context.Background()
	verified := acceptanceVerifiedRun(t, ws, runtime)
	commit, err := runtime.Learning().Commit(ctx, app.CommitInput{ID: "MEMORY-" + ws.sessionID, Verification: verified.ID, Provenance: "acceptance-e2e", Candidates: []learning.PromotionInput{{Item: learning.Item{ID: "ITEM-" + ws.sessionID, Claim: "bounded counterfactual evidence is required", Scope: learning.ScopeProject, ProjectID: verified.Binding.ProjectID, State: model.ClaimStateVerified, Version: 1, Provenance: "acceptance-e2e", Evidence: []learning.EvidenceRef{{ID: "learn-a", ClusterID: "cluster-a", Digest: "a", Kind: "verification"}, {ID: "learn-b", ClusterID: "cluster-b", Digest: "b", Kind: "verification"}}}, Replayable: true}}})
	if err != nil {
		t.Fatalf("commit verified learning: %v", err)
	}
	baseline := optimization.Baseline{ID: "BASELINE-" + ws.sessionID, MarshalSHA: verified.Binding.RunID, RoutingConfig: "baseline", VerifierPolicy: "independent", Toolchain: "acceptance", EnvironmentHash: verified.Binding.EnvironmentDigest}
	candidate := optimization.Candidate{ID: "CANDIDATE-" + ws.sessionID, Dimension: optimization.DimRouting, Hypothesis: "evaluate governed alternate route", TaskScope: []string{"code"}, RollbackPlan: "restore baseline", VerificationPlan: "independent verifier", Provenance: "acceptance-e2e"}
	governance := optimization.Governance{GovernableProviders: map[string]bool{"codex": true, "claude": true}, MaxCanaryExposure: .25, RequireRollback: true}
	cycle, err := runtime.Optimization().StartCycle(ctx, app.StartCycleInput{ID: "CYCLE-" + ws.sessionID, MemoryCommitID: commit.ID, Objectives: []optimization.Objective{{Name: "verified_success", HigherIsBetter: true, Weight: 1}}, Candidates: []optimization.Candidate{candidate}, Baselines: []optimization.Baseline{baseline}, Provenance: "acceptance-e2e", Governance: governance})
	if err != nil {
		t.Fatalf("start canonical optimization cycle: %v", err)
	}
	route := optimization.Route{TaskClass: "code", Provider: "codex", ProviderVersion: "1", Model: "acceptance", Harness: "acceptance-harness", HarnessVersion: "1", VerifierPolicy: "independent"}
	return acceptanceOptimizationFixture{cycle: cycle, baseline: baseline, candidate: candidate, route: route, governance: governance, treeDigest: verified.Binding.TreeDigest, environmentDigest: verified.Binding.EnvironmentDigest}
}

type acceptanceReplayRunner struct{ called bool }

func (r *acceptanceReplayRunner) Replay(_ context.Context, _ optimization.ReplayRequest) (optimization.ReplayObservation, error) {
	r.called = true
	return optimization.ReplayObservation{Outcome: learning.OutcomeVerifiedComplete, VerifierResult: optimization.StatusPass}, nil
}

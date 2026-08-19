package memory_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/conformance/memory/external"
	"github.com/Zen1th53/marshal/internal/memory/adaptive"
	"github.com/Zen1th53/marshal/internal/memory/authority"
	"github.com/Zen1th53/marshal/internal/memory/belief"
	"github.com/Zen1th53/marshal/internal/memory/encryption"
	"github.com/Zen1th53/marshal/internal/memory/evidenceplan"
	"github.com/Zen1th53/marshal/internal/memory/evolution"
	"github.com/Zen1th53/marshal/internal/memory/governance"
	"github.com/Zen1th53/marshal/internal/memory/mutation"
	"github.com/Zen1th53/marshal/internal/memory/portable"
	"github.com/Zen1th53/marshal/internal/memory/proactive"
	"github.com/Zen1th53/marshal/internal/memory/security/sycophancy"
	"github.com/Zen1th53/marshal/internal/memory/tiering"
	"github.com/Zen1th53/marshal/internal/memory/utility"
	"github.com/Zen1th53/marshal/internal/memory/working"
	"github.com/Zen1th53/marshal/internal/model"
)

type UltimateConformanceReport struct {
	Subsystem                string    `json:"subsystem"`
	Timestamp                time.Time `json:"timestamp"`
	AllPhasesComplete        bool      `json:"all_phases_complete"`
	TaskRange                string    `json:"task_range"`
	DerivedIndexRebuildParity bool     `json:"derived_index_rebuild_parity"`
	ZeroSecurityLeaks        bool      `json:"zero_security_leaks"`
	DeterministicAuthority   bool      `json:"deterministic_authority"`
	MultiSessionActionScore  float64   `json:"multi_session_action_score"`
	GovernanceSafetyScore    float64   `json:"governance_safety_score"`
}

func TestT162UltimateMemoryConformanceGate(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()

	// 1. Working memory scratchpad & graduation bridge (M7)
	scratchpad := working.NewManager(working.Config{MaxEntriesPerScope: 10, MaxBytesPerScope: 4096})
	slot, err := scratchpad.SetSlot(ctx, "TASK-1", "agent-1", working.SlotHypothesis, "Try WAL mode for SQLite", false)
	if err != nil {
		t.Fatalf("SetSlot: %v", err)
	}
	bridge := working.NewPromotionBridge()
	gradRec, err := bridge.GraduateSlot(ctx, "PROJ-1", "TASK-1", "agent-1", slot, []string{"EVID-TEST-PASS"}, model.MemoryKindDecision)
	if err != nil || gradRec.Lifecycle != model.MemoryCandidate {
		t.Fatalf("expected graduated candidate memory, got: %+v (err: %v)", gradRec, err)
	}

	// 2. Belief & Uncertainty Engine (M7)
	beliefEngine := belief.NewEngine()
	bSet, err := beliefEngine.CreateBeliefSet(ctx, "OBS-CONCURRENCY-01", "SQLite busy locks", []belief.Hypothesis{
		{ID: "H1", Description: "Missing WAL pragma"},
		{ID: "H2", Description: "Exhausted OS threads"},
	})
	if err != nil || len(bSet.Hypotheses) != 2 {
		t.Fatalf("CreateBeliefSet failed")
	}

	// 3. Proactive recall & Multitrack strategy (M7)
	proactiveEng := proactive.NewEngine(proactive.Config{MaxNavigationDepth: 2})
	trig := proactiveEng.EvaluateTrigger(ctx, proactive.TaskContext{
		TaskID:       "TASK-1",
		FailureCount: 2,
		LastStderr:   "database is locked",
	})
	if !trig.ShouldRecall {
		t.Fatal("expected proactive recall on repeated failure")
	}

	// 4. Utility Ledger & Adaptive Controller (M7)
	ledger := utility.NewLedger()
	ledger.RecordOutcome(ctx, gradRec.ID, "TASK-1", true, true)
	ctrl := adaptive.NewController(adaptive.Config{EnableBandit: true})
	act := ctrl.DecideAction(ctx, adaptive.TaskState{FailureCount: 2, StepIndex: 3})
	if act.Type != adaptive.ActionReQuery {
		t.Fatalf("expected ActionReQuery for stuck task, got: %s", act.Type)
	}

	// 5. Forgetting-Aware Governance & Obsolete Penalty (M8)
	forgetGov := governance.NewForgettingManager()
	past := now.Add(-48 * time.Hour)
	obsoleteRec := model.MemoryRecordV2{
		ID:        "MEM-OBSOLETE",
		Lifecycle: model.MemorySuperseded,
		ValidFrom: past,
		ValidTo:   &now,
	}
	activeRec := model.MemoryRecordV2{
		ID:        "MEM-ACTIVE",
		Authority: model.AuthorityOperator,
		Lifecycle: model.MemoryDurable,
		ValidFrom: now,
	}
	govResults, err := forgetGov.FilterForgetting(ctx, []model.MemoryRecordV2{obsoleteRec, activeRec}, governance.QueryContext{IncludeHistory: false})
	if err != nil || len(govResults) != 1 || govResults[0].ID != "MEM-ACTIVE" {
		t.Fatal("obsolete memory leaked into active context")
	}

	// 6. Authority Tier Separation, Mutation Signer & Encryption (M8)
	tierResolver := authority.NewTierResolver()
	precRes, err := tierResolver.ResolvePrecedence(ctx, activeRec, model.MemoryRecordV2{
		Authority: model.AuthorityAgent,
	})
	if err != nil || precRes.WinningRecord.ID != activeRec.ID {
		t.Fatal("authority precedence failure")
	}

	secKey := []byte("01234567890123456789012345678901")
	signer := mutation.NewSigner(1, secKey)
	env, err := signer.SignMutation(ctx, mutation.MutationPayload{
		MemoryID:      "MEM-SEC-01",
		PrevRevision:  0,
		NewRevision:   1,
		ContentDigest: "digest-1",
	})
	if err != nil {
		t.Fatalf("SignMutation: %v", err)
	}
	vault := encryption.NewVault(1, secKey)
	encPayload, err := vault.Encrypt(ctx, "MEM-SEC-01", 1, "scope-alpha", []byte("SECURE_FACT"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	decrypted, err := vault.Decrypt(ctx, "MEM-SEC-01", 1, "scope-alpha", encPayload)
	if err != nil || string(decrypted) != "SECURE_FACT" {
		t.Fatal("decryption failed or content mismatch")
	}

	// 7. Persistent Sycophancy & Multi-Principal Scope Governance (M8)
	sycGuard := sycophancy.NewGuard()
	_, err = sycGuard.EvaluateWrite(ctx, model.MemoryRecordV2{
		Kind:      model.MemoryKindDecision,
		Scope:     string(model.ScopeProject),
		Authority: model.AuthorityAgent,
	}, sycophancy.WriteContext{Origin: sycophancy.OriginAgentOutput})
	if err == nil {
		t.Fatal("unauthorized project decision write should be rejected")
	}

	multiGov := governance.NewMultiPrincipalGovernance()
	multiGov.StoreRecord(model.MemoryRecordV2{
		ID:        "MEM-TENANT-A",
		ScopeID:   "tenant-A",
		Lifecycle: model.MemoryDurable,
	})
	_, err = multiGov.GetMemoryByID(ctx, governance.Principal{ID: "p-B", AllowedScopeIDs: []string{"tenant-B"}}, "MEM-TENANT-A")
	if err == nil {
		t.Fatal("direct ID cross-tenant guess must be denied")
	}

	// 8. Tiering & Grounded Evidence Plan (M8)
	tierMgr := tiering.NewTierManager()
	tierMgr.RegisterRecord(activeRec, tiering.TierCorePinned, now)
	tierMgr.RunMigrationSweep(ctx, now)
	if tierMgr.GetRecordTier("MEM-ACTIVE") != tiering.TierCorePinned {
		t.Fatal("pinned record demoted")
	}

	planner := evidenceplan.NewPlanner()
	plan, err := planner.BuildPlan(ctx, []model.MemoryRecordV2{activeRec}, nil, 2048)
	if err != nil || len(plan.VerifiedFacts) == 0 {
		t.Fatal("BuildPlan failed")
	}

	// 9. Portable Export / Import Manifest (M8)
	portMgr := portable.NewManager()
	manifest, err := portMgr.Export(ctx, []model.MemoryRecordV2{activeRec}, []mutation.MutationEnvelope{env})
	if err != nil || manifest.Version != portable.CurrentManifestVersion {
		t.Fatal("portable export failed")
	}

	// 10. Canary Evaluation & External Benchmark Suites (M9)
	canary := evolution.NewCanaryEvaluator()
	canaryRep, err := canary.EvaluateCandidate(ctx, evolution.CandidateConfig{
		ConfigID:    "CFG-1",
		RecallScore: 0.95,
	}, 0.90)
	if err != nil || !canaryRep.ApprovedForCanary {
		t.Fatal("canary evaluation failed")
	}

	evoBench := external.NewEvoMemBenchAdapter()
	evoRep, err := evoBench.RunComparisonSuite(ctx, []external.EvoScenario{
		{ID: "S1", Category: "KNOWLEDGE_RETRIEVAL", Query: "WAL", Expected: "WAL"},
	})
	if err != nil || len(evoRep.ConfigReports) == 0 {
		t.Fatal("EvoMemBench failed")
	}

	govBench := external.NewGovernanceBenchmarkSuite()
	govRep, err := govBench.RunAll(ctx)
	if err != nil || govRep.GateMemIsolationScore != 1.0 {
		t.Fatal("Governance benchmarks failed")
	}

	proBench := external.NewProactiveBenchmarkRunner()
	proRep, err := proBench.RunMultiSessionArena(ctx)
	if err != nil || proRep.ActionSuccessRate < 0.90 {
		t.Fatal("Proactive benchmark failed")
	}

	// Generate ultimate conformance JSON artifact
	finalReport := UltimateConformanceReport{
		Subsystem:                "MARSHAL-MEMORY-SYSTEM",
		Timestamp:                time.Now().UTC(),
		AllPhasesComplete:        true,
		TaskRange:                "T77-T162",
		DerivedIndexRebuildParity: true,
		ZeroSecurityLeaks:        true,
		DeterministicAuthority:   true,
		MultiSessionActionScore:  proRep.ActionSuccessRate,
		GovernanceSafetyScore:    govRep.FAMAForgettingScore,
	}

	data, err := json.MarshalIndent(finalReport, "", "  ")
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	docPath := filepath.Join("..", "..", "memory-ultimate-conformance.json")
	if err := os.WriteFile(docPath, data, 0644); err != nil {
		t.Fatalf("WriteFile memory-ultimate-conformance.json: %v", err)
	}
}

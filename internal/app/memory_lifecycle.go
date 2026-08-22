package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/context/budget"
	"github.com/Zen1th53/marshal/internal/context/compiler"
	"github.com/Zen1th53/marshal/internal/context/tokens"
	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/memory/episode"
	"github.com/Zen1th53/marshal/internal/memory/retention"
	"github.com/Zen1th53/marshal/internal/memory/retrieval/finalize"
	"github.com/Zen1th53/marshal/internal/memory/retrieval/fusion"
	"github.com/Zen1th53/marshal/internal/memory/retrieval/planner"
	memorysecurity "github.com/Zen1th53/marshal/internal/memory/security"
	"github.com/Zen1th53/marshal/internal/memory/utility"
	"github.com/Zen1th53/marshal/internal/memory/writeback"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

const (
	memoryContextBudget  = 4096
	memoryContextReserve = 512
	memoryCandidateLimit = 128
)

// memoryLifecycle composes existing memory primitives at Runtime.Run's single
// execution boundary. It owns no canonical state; SQLite remains authoritative.
type memoryLifecycle struct {
	store     *store.Store
	budget    *budget.Manager
	compiler  *compiler.Compiler
	counter   *tokens.Counter
	planner   *planner.Planner
	fuser     *fusion.Fuser
	finalizer *finalize.Finalizer
	staleness *retention.PolicyEvaluator
	content   *memorysecurity.RetrievedContentSanitizer
	firewall  *memorysecurity.Firewall
	utility   *utility.Ledger
	capturer  *episode.Capturer
	reflector *writeback.Reflector
	sanitizer evidence.ByteSanitizer
}

func newMemoryLifecycle(st *store.Store, sanitizer evidence.ByteSanitizer) *memoryLifecycle {
	return &memoryLifecycle{
		store:     st,
		budget:    budget.NewManager(),
		compiler:  compiler.NewCompiler(),
		counter:   tokens.NewCounter(),
		planner:   planner.NewPlanner(),
		fuser:     fusion.NewFuser(fusion.Config{}),
		finalizer: finalize.NewFinalizer(),
		staleness: retention.NewPolicyEvaluator(retention.PolicyConfig{}),
		content:   memorysecurity.NewRetrievedContentSanitizer(),
		firewall:  memorysecurity.NewFirewall(memorysecurity.FirewallConfig{}),
		utility:   utility.NewLedger(),
		capturer:  episode.NewCapturer(),
		reflector: writeback.NewReflector(),
		sanitizer: sanitizer,
	}
}

type recalledMemory struct {
	record   model.MemoryRecordV2
	rendered string
	score    float64
	utility  float64
	tokens   int
	reasons  []string
}

// buildContext performs best-effort enhancement. Canonical task execution does
// not fail merely because derived indexes, recall, or compilation are degraded.
func (m *memoryLifecycle) buildContext(ctx context.Context, task model.Task, sessionID, agentID, branch, provider, headCommit, baseContext, runID string) (string, store.MemoryRuntimeTrace) {
	now := time.Now().UTC()
	trace := store.MemoryRuntimeTrace{
		RunID: runID, ProjectID: localProjectID, TaskID: task.ID,
		QueryDigest: digestMemoryQuery(task.Title), HeadCommit: headCommit, CreatedAt: now,
	}
	if m == nil || m.store == nil || strings.TrimSpace(task.Title) == "" {
		return baseContext, trace
	}

	plan, planErr := m.planner.Plan(ctx, task.Title, allowedScopes(task, sessionID, agentID, branch), now)
	if planErr != nil {
		plan = planner.QueryPlan{RawQuery: task.Title}
	}
	records, err := m.store.ListMemoryV2(ctx, store.MemoryQueryFilter{
		ProjectID: localProjectID, ValidAsOf: now, KnownAt: now, Limit: memoryCandidateLimit,
	})
	if err != nil {
		return baseContext, trace
	}

	allowed := allowedScopeSet(task, sessionID, agentID, branch)
	candidates := make([]recalledMemory, 0, len(records))
	for _, rec := range records {
		if !memoryScopeAllowed(rec, allowed) {
			continue // Never trace a memory outside the execution scope.
		}
		traceCandidate := store.MemoryRuntimeCandidate{MemoryID: rec.ID, Decision: "excluded"}
		if rec.Lifecycle == model.MemoryTombstoned || rec.Lifecycle == model.MemoryRejected || rec.Lifecycle == model.MemorySuperseded {
			traceCandidate.Reasons = []string{"lifecycle:" + string(rec.Lifecycle)}
			trace.Candidates = append(trace.Candidates, traceCandidate)
			continue
		}
		if rec.Lifecycle == model.MemoryConflicted || len(rec.ConflictIDs) > 0 {
			traceCandidate.Reasons = []string{"conflict:unresolved"}
			trace.Candidates = append(trace.Candidates, traceCandidate)
			continue
		}
		stale, staleReason := m.staleness.CheckStaleness(ctx, rec, headCommit, now)
		if stale && !historicalRecallAllowed(rec) {
			traceCandidate.Reasons = []string{"stale:" + staleReason}
			trace.Candidates = append(trace.Candidates, traceCandidate)
			continue
		}
		if err := m.firewall.ScanRecord(ctx, rec); err != nil {
			traceCandidate.Reasons = []string{"security:secret_rejected"}
			trace.Candidates = append(trace.Candidates, traceCandidate)
			continue
		}
		relevance := memoryRelevance(task.Title, plan, rec)
		if relevance == 0 {
			traceCandidate.Reasons = []string{"relevance:no_match"}
			trace.Candidates = append(trace.Candidates, traceCandidate)
			continue
		}
		contextRecord := rec
		reasons := []string{}
		if stale {
			// Candidate/agent-authority episodes are useful historical evidence,
			// but never fresh repository truth. Keep the commit-drift reason in
			// the provider context and retrieval trace instead of mutating the
			// canonical record or promoting it over verified current facts.
			contextRecord.Body = "[HISTORICAL MEMORY: repository commit drifted; verify against current repository evidence.]\n" + contextRecord.Body
			reasons = append(reasons, "stale:"+staleReason, "historical:untrusted")
		}
		safe, err := m.content.Sanitize(ctx, contextRecord)
		if err != nil {
			traceCandidate.Reasons = []string{"security:render_rejected"}
			trace.Candidates = append(trace.Candidates, traceCandidate)
			continue
		}
		candidates = append(candidates, recalledMemory{record: rec, rendered: safe.RenderedXML, score: relevance, reasons: reasons})
	}
	if len(candidates) == 0 {
		return baseContext, trace
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].record.ID < candidates[j].record.ID
	})
	recordMap := make(map[string]model.MemoryRecordV2, len(candidates))
	channel := make([]fusion.ChannelMatch, 0, len(candidates))
	for i, candidate := range candidates {
		recordMap[candidate.record.ID] = candidate.record
		channel = append(channel, fusion.ChannelMatch{MemoryID: candidate.record.ID, Rank: i + 1, RawScore: candidate.score})
	}
	fused := m.fuser.Fuse([][]fusion.ChannelMatch{channel}, recordMap, plan.MemoryIDs, len(channel))
	finalized, err := m.finalizer.Finalize(ctx, fused, recordMap, finalize.Params{AllowedScopeIDs: allowedScopes(task, sessionID, agentID, branch), AsOf: now})
	if err != nil {
		return baseContext, trace
	}
	byID := make(map[string]*recalledMemory, len(candidates))
	for i := range candidates {
		byID[candidates[i].record.ID] = &candidates[i]
	}
	selected := make([]*recalledMemory, 0, len(finalized))
	for _, final := range finalized {
		candidate := byID[final.MemoryID]
		if candidate == nil {
			continue
		}
		utilityScore, utilityErr := m.store.MemoryUtilityScore(ctx, localProjectID, candidate.record.ID)
		if utilityErr != nil {
			utilityScore = 0.5
		}
		candidate.utility = utilityScore
		// Utility is deliberately bounded to +/-0.05. Authority, lifecycle and
		// staleness filtering have already been applied by existing primitives.
		candidate.score = final.RankScore + (utilityScore-0.5)*0.10
		candidate.reasons = append(candidate.reasons, "retrieval:finalized")
		candidate.tokens = m.counter.CountTokens(provider, "", candidate.rendered)
		trace.TokensRequested += candidate.tokens
		selected = append(selected, candidate)
	}
	sort.Slice(selected, func(i, j int) bool {
		if selected[i].score != selected[j].score {
			return selected[i].score > selected[j].score
		}
		return selected[i].record.ID < selected[j].record.ID
	})

	sections := []budget.SectionPriority{{Kind: "task", Priority: 100, MinTokens: m.counter.CountTokens(provider, "", baseContext), Mandatory: true}}
	for _, candidate := range selected {
		sections = append(sections, budget.SectionPriority{Kind: "memory:" + candidate.record.ID, Priority: 10, MinTokens: candidate.tokens})
	}
	decision, err := m.budget.Allocate(ctx, budget.Budget{MaxTokens: memoryContextBudget, ReserveTokens: memoryContextReserve}, sections)
	if err != nil {
		for _, candidate := range selected {
			trace.Candidates = append(trace.Candidates, store.MemoryRuntimeCandidate{MemoryID: candidate.record.ID, RankScore: candidate.score, UtilityScore: candidate.utility, Tokens: candidate.tokens, Decision: "excluded", Reasons: []string{"budget:mandatory_overflow"}})
		}
		return baseContext, trace
	}
	admittedSet := make(map[string]bool, len(decision.Compacted))
	for _, kind := range decision.Compacted {
		if strings.HasPrefix(kind, "memory:") {
			admittedSet[strings.TrimPrefix(kind, "memory:")] = true
		}
	}
	compiledMemories := make([]model.MemoryRecordV2, 0, len(selected))
	for _, candidate := range selected {
		if !admittedSet[candidate.record.ID] {
			reasons := append(append([]string{}, candidate.reasons...), "budget:optional_dropped")
			trace.Candidates = append(trace.Candidates, store.MemoryRuntimeCandidate{MemoryID: candidate.record.ID, RankScore: candidate.score, UtilityScore: candidate.utility, Tokens: candidate.tokens, Decision: "excluded", Reasons: reasons})
			continue
		}
		copy := candidate.record
		copy.Body = candidate.rendered
		compiledMemories = append(compiledMemories, copy)
		trace.AdmittedMemoryIDs = append(trace.AdmittedMemoryIDs, candidate.record.ID)
		trace.TokensAdmitted += candidate.tokens
		reasons := append(append([]string{}, candidate.reasons...), "ranked_and_budget_admitted")
		trace.Candidates = append(trace.Candidates, store.MemoryRuntimeCandidate{MemoryID: candidate.record.ID, RankScore: candidate.score, UtilityScore: candidate.utility, Tokens: candidate.tokens, Decision: "admitted", Reasons: reasons})
	}
	if len(compiledMemories) == 0 {
		return baseContext, trace
	}
	compiled, err := m.compiler.CompileWithMemory(ctx, compiler.MemoryCompileRequest{
		ID: "CTX-" + runID, TaskID: task.ID, AgentID: agentID, PromptText: baseContext,
		BudgetLimit: memoryContextBudget - memoryContextReserve, Memories: compiledMemories,
	})
	if err != nil {
		for i := range trace.Candidates {
			if trace.Candidates[i].Decision == "admitted" {
				trace.Candidates[i].Decision = "excluded"
				trace.Candidates[i].Reasons = []string{"context:compiler_rejected"}
			}
		}
		trace.AdmittedMemoryIDs = nil
		trace.TokensAdmitted = 0
		return baseContext, trace
	}
	return compiled.PromptText, trace
}

func (m *memoryLifecycle) recordOutcome(ctx context.Context, trace store.MemoryRuntimeTrace, success bool) {
	for _, memoryID := range trace.AdmittedMemoryIDs {
		m.utility.RecordOutcome(ctx, memoryID, trace.TaskID, success, false)
	}
	_ = m.store.RecordMemoryRuntimeOutcome(ctx, trace.ProjectID, trace.TaskID, trace.RunID, trace.AdmittedMemoryIDs, success)
}

// captureTerminalOutcome is best-effort by design: once FinishRun committed,
// a memory enhancement failure must not falsify the already-real task result.
func (m *memoryLifecycle) captureTerminalOutcome(ctx context.Context, task model.Task, sessionID, agentID, runID, provider, baseCommit, resultCommit, status string, success bool, evidenceIDs []string) {
	if m == nil || m.store == nil {
		return
	}
	if exists, err := m.store.HasMemoryV2ForRun(ctx, localProjectID, runID, model.MemoryKindEpisodic); err == nil && !exists {
		summary := fmt.Sprintf("Runtime terminal outcome for task %s: %s at commit %s.", task.ID, status, resultCommit)
		ep, captureErr := m.capturer.CaptureEpisode(ctx, episode.EpisodeInput{
			ProjectID: localProjectID, TaskID: task.ID, SessionID: sessionID, RunID: runID,
			Provider: provider, AgentID: agentID, EvidenceIDs: evidenceIDs, OutcomeSummary: summary,
			Success: success, BaseCommit: baseCommit, ResultCommit: resultCommit, ObservedAt: time.Now().UTC(),
		})
		if captureErr == nil {
			_ = m.store.WriteMemoryV2(ctx, ep)
		}
	}
	reflectionKind := model.MemoryKindFinding
	if status == "success" {
		reflectionKind = model.MemoryKindDecision
	}
	if exists, err := m.store.HasMemoryV2ForRun(ctx, localProjectID, runID, reflectionKind); err != nil || exists {
		return
	}
	keyDecisions := []string(nil)
	if m.sanitizer != nil {
		if clean, sanitizeErr := m.sanitizer.SanitizeBytes(ctx, []byte(task.Title)); sanitizeErr == nil && string(clean) == task.Title && m.firewall.ScanText(task.Title) == nil {
			// This is the only task text made durable: it crosses the same
			// sanitizer and memory firewall boundaries as other persisted data.
			keyDecisions = []string{"Task objective: " + task.Title}
		}
	}
	reflected, err := m.reflector.ReflectAndWriteback(ctx, writeback.RunOutcome{
		TaskID: task.ID, ProjectID: localProjectID, Status: reflectionStatus(status), CommitSHA: resultCommit,
		VerificationIDs: evidenceIDs, KeyDecisions: keyDecisions, ObservedAt: time.Now().UTC(),
	})
	if err != nil {
		return
	}
	reflected.RunID = runID
	reflected.SessionID = sessionID
	reflected.Source.RunID = runID
	reflected.Source.SessionID = sessionID
	_ = m.store.WriteMemoryV2(ctx, reflected)
}

func reflectionStatus(status string) string {
	if status == "success" {
		return "SUCCESS"
	}
	if status == "cancelled" {
		return "CANCELLED"
	}
	return "FAILED"
}

func allowedScopes(task model.Task, sessionID, agentID, branch string) []string {
	return []string{localProjectID, task.ID, sessionID, agentID, branch}
}

func allowedScopeSet(task model.Task, sessionID, agentID, branch string) map[model.MemoryScopeKind]string {
	return map[model.MemoryScopeKind]string{
		model.ScopeProject: localProjectID,
		model.ScopeTask:    task.ID,
		model.ScopeSession: sessionID,
		model.ScopeAgent:   agentID,
		model.ScopeBranch:  branch,
	}
}

func memoryScopeAllowed(rec model.MemoryRecordV2, scopes map[model.MemoryScopeKind]string) bool {
	kind := model.MemoryScopeKind(rec.Scope)
	allowedID, ok := scopes[kind]
	return ok && rec.ScopeID == allowedID
}

// historicalRecallAllowed permits only weak, candidate runtime observations to
// remain available as explicitly marked historical context after commit drift.
// Durable/verified facts are suppressed until fresh evidence re-establishes
// them, so a changed HEAD cannot be silently overridden by memory.
func historicalRecallAllowed(rec model.MemoryRecordV2) bool {
	if rec.Authority != model.AuthorityAgent || rec.Lifecycle != model.MemoryCandidate {
		return false
	}
	switch rec.Kind {
	case model.MemoryKindEpisodic, model.MemoryKindFinding, model.MemoryKindDecision, model.MemoryKindFailure:
		return true
	default:
		return false
	}
}

func memoryRelevance(query string, plan planner.QueryPlan, rec model.MemoryRecordV2) float64 {
	queryTerms := strings.Fields(strings.ToLower(query))
	content := strings.ToLower(rec.Title + " " + rec.Body)
	matched := 0
	for _, term := range queryTerms {
		if len(term) > 2 && strings.Contains(content, term) {
			matched++
		}
	}
	for _, symbol := range plan.ExactSymbols {
		if strings.Contains(content, strings.ToLower(symbol)) {
			matched += 2
		}
	}
	for _, path := range plan.FilePaths {
		if strings.Contains(content, strings.ToLower(path)) {
			matched += 2
		}
	}
	if matched == 0 {
		return 0
	}
	return float64(matched)
}

func digestMemoryQuery(query string) string {
	sum := sha256.Sum256([]byte(query))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// terminalMemoryEvent records an idempotent, content-free lifecycle marker.
func (r *Runtime) terminalMemoryEvent(ctx context.Context, taskID, runID string, success bool, at time.Time) {
	eventID, err := model.NewID("EVENT-")
	if err != nil {
		return
	}
	typ := events.EventTypeTaskFailed
	if success {
		typ = events.EventTypeTaskCompleted
	}
	_, _ = r.EmitEvent(ctx, events.Event{ID: eventID, Type: typ, TaskID: taskID, RunID: runID, At: at,
		Data: map[string]any{"memory_capture_eligible": true}, IdempotencyKey: "memory-terminal:" + runID})
}

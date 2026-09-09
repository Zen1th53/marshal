package app

import (
	"context"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/optimization"
)

// OptimizationService is the sole application boundary for Process 08 state.
//
// Surfaces render what it returns; they cannot manufacture an optimization
// cycle out of thin air. The Process 07 binding is always derived from the
// canonical stored memory commit, never accepted from the caller, so a
// surface cannot attach an optimization to knowledge that was never actually
// promoted through the evidence gates.
type OptimizationService struct {
	runtime *Runtime
	now     func() time.Time
}

func (r *Runtime) Optimization() *OptimizationService {
	if r == nil {
		return nil
	}
	return &OptimizationService{runtime: r, now: func() time.Time { return time.Now().UTC() }}
}

// EntryFor builds the exact Process 07 entry binding for one memory commit by
// reading canonical state.
//
// A caller cannot supply the memory digest, the source SHA, the tree digest or
// the outcome: all come from the stored, digest-verified commit. If the commit
// does not exist, optimization that depends on it is blocked rather than run
// against a guess.
func (s *OptimizationService) EntryFor(ctx context.Context, memoryCommitID string) (optimization.Entry, error) {
	if s == nil || s.runtime == nil {
		return optimization.Entry{}, fmt.Errorf("%w: optimization service unavailable", model.ErrUnavailable)
	}
	commit, err := s.runtime.store.GetMemoryCommit(ctx, memoryCommitID)
	if err != nil {
		return optimization.Entry{}, err
	}
	if commit.Digest == "" {
		return optimization.Entry{}, fmt.Errorf("%w: memory commit is not digest-bound", optimization.ErrInvalid)
	}

	return optimization.Entry{
		ProjectID:       commit.Binding.ProjectID,
		MemoryCommitID:  commit.ID,
		MemoryVersion:   commit.Version,
		MemoryDigest:    commit.Digest,
		SourceSHA:       commit.Binding.RunID,
		TreeDigest:      commit.Binding.TreeDigest,
		EnvironmentHash: commit.Binding.EnvironmentDigest,
		Outcome:         commit.Binding.Outcome,
		Limitations:     append([]string(nil), commit.BlockedLearning...),
	}, nil
}

// StartCycleInput is one proposed optimization cycle. The caller supplies the
// objectives, constraints and candidates; the service supplies the binding.
type StartCycleInput struct {
	ID             string
	MemoryCommitID string
	Objectives     []optimization.Objective
	Constraints    []optimization.Constraint
	Candidates     []optimization.Candidate
	Baselines      []optimization.Baseline
	Provenance     string
	Governance     optimization.Governance
}

// StartCycle opens a new optimization cycle bound to one canonical memory
// commit and evaluates governance against every candidate up front.
//
// A vetoed candidate is not silently dropped: it is recorded as a VetoRecord
// and a BLOCKED promotion decision, so the refusal is auditable and a
// candidate cannot quietly disappear from the cycle it was proposed in.
func (s *OptimizationService) StartCycle(ctx context.Context, in StartCycleInput) (optimization.Cycle, error) {
	if s == nil || s.runtime == nil {
		return optimization.Cycle{}, fmt.Errorf("%w: optimization service unavailable", model.ErrUnavailable)
	}
	entry, err := s.EntryFor(ctx, in.MemoryCommitID)
	if err != nil {
		return optimization.Cycle{}, err
	}
	now := s.now()

	c := optimization.Cycle{
		ID:          in.ID,
		Binding:     entry,
		Objectives:  in.Objectives,
		Constraints: in.Constraints,
		Baselines:   in.Baselines,
		Provenance:  in.Provenance,
	}

	for _, cand := range in.Candidates {
		if err := optimization.ValidateCandidate(cand); err != nil {
			c.BlockedOptimization = append(c.BlockedOptimization, fmt.Sprintf("%s: %v", cand.ID, err))
			continue
		}
		if reasons := optimization.Veto(cand, in.Governance); len(reasons) > 0 {
			c.Vetoes = append(c.Vetoes, optimization.VetoRecord{
				CandidateID: cand.ID, Reasons: reasons, DecidedAt: now,
			})
			record, err := optimization.Promote(optimization.PromotionInput{Candidate: cand}, in.Governance, now)
			if err == nil {
				// Promote only returns a nil error on a non-BLOCKED decision;
				// a vetoed candidate always yields ErrVeto, so this branch is
				// unreachable in practice but never silently drops the record.
				c.Decisions = append(c.Decisions, record)
			} else {
				c.Decisions = append(c.Decisions, record)
			}
			continue
		}
		c.Candidates = append(c.Candidates, cand)
	}

	built, err := optimization.NewCycle(c, now)
	if err != nil {
		return optimization.Cycle{}, err
	}
	if err := s.runtime.store.AppendOptimizationCycle(ctx, built); err != nil {
		return optimization.Cycle{}, err
	}
	return s.runtime.store.GetOptimizationCycle(ctx, built.ID)
}

// Get returns one durable optimization cycle with its digest verified.
func (s *OptimizationService) Get(ctx context.Context, id string) (optimization.Cycle, error) {
	if s == nil || s.runtime == nil {
		return optimization.Cycle{}, model.ErrUnavailable
	}
	return s.runtime.store.GetOptimizationCycle(ctx, id)
}

// Candidates returns every candidate recorded against one cycle.
func (s *OptimizationService) Candidates(ctx context.Context, cycleID string) ([]optimization.Candidate, error) {
	if s == nil || s.runtime == nil {
		return nil, model.ErrUnavailable
	}
	return s.runtime.store.OptimizationCandidates(ctx, cycleID)
}

// EvaluateCounterfactual runs the safety and validity checks on one
// counterfactual comparison and records it against a cycle.
//
// The cycle must exist so a counterfactual can never be filed against
// optimization state that was never opened.
func (s *OptimizationService) EvaluateCounterfactual(ctx context.Context, cycleID string, c optimization.Counterfactual, g optimization.Governance) (optimization.Counterfactual, error) {
	if s == nil || s.runtime == nil {
		return optimization.Counterfactual{}, model.ErrUnavailable
	}
	// A replay result must be produced by ExecuteReplay below. Accepting a
	// caller-populated alternate outcome here would turn the critical Process
	// 07 carry-forward requirement back into mere substrate.
	if c.Method == optimization.MethodReplay {
		return optimization.Counterfactual{}, fmt.Errorf("%w: replay counterfactuals must execute through ExecuteReplay", optimization.ErrInvalid)
	}
	if _, err := s.runtime.store.GetOptimizationCycle(ctx, cycleID); err != nil {
		return optimization.Counterfactual{}, err
	}
	built, err := optimization.NewCounterfactual(c, g, s.now())
	if err != nil {
		return optimization.Counterfactual{}, err
	}
	if err := s.runtime.store.AppendCounterfactual(ctx, cycleID, built); err != nil {
		return optimization.Counterfactual{}, err
	}
	return s.runtime.store.GetCounterfactual(ctx, built.ID)
}

// ExecuteReplay runs an alternate route through the offline replay boundary
// and durably records the verifier-backed comparison. It is the application
// path for closing the Process 07 counterfactual-routing gap: unlike a record
// submitted after the fact, this method actually invokes a bounded runner.
func (s *OptimizationService) ExecuteReplay(ctx context.Context, cycleID string, runner optimization.ReplayRunner, factual optimization.FactualRun, alternate optimization.Route, sandbox optimization.SandboxPolicy, provenance, clusterID string, g optimization.Governance) (optimization.Counterfactual, error) {
	if s == nil || s.runtime == nil {
		return optimization.Counterfactual{}, model.ErrUnavailable
	}
	if _, err := s.runtime.store.GetOptimizationCycle(ctx, cycleID); err != nil {
		return optimization.Counterfactual{}, err
	}
	built, err := optimization.ExecuteReplay(ctx, runner, factual, alternate, sandbox, provenance, clusterID, g, s.now())
	if err != nil {
		return optimization.Counterfactual{}, err
	}
	if err := s.runtime.store.AppendCounterfactual(ctx, cycleID, built); err != nil {
		return optimization.Counterfactual{}, err
	}
	return s.runtime.store.GetCounterfactual(ctx, built.ID)
}

// Counterfactuals returns every counterfactual recorded against one cycle.
func (s *OptimizationService) Counterfactuals(ctx context.Context, cycleID string) ([]optimization.Counterfactual, error) {
	if s == nil || s.runtime == nil {
		return nil, model.ErrUnavailable
	}
	return s.runtime.store.Counterfactuals(ctx, cycleID)
}

// RecordExperiment stores one experiment result exactly as reported. Every
// attempt is preserved, including a failure, a quarantine or an UNKNOWN
// outcome: the promotion gates decide what counts, not the write path.
func (s *OptimizationService) RecordExperiment(ctx context.Context, cycleID, candidateID string, r optimization.ExperimentResult) error {
	if s == nil || s.runtime == nil {
		return model.ErrUnavailable
	}
	if _, err := s.runtime.store.GetOptimizationCycle(ctx, cycleID); err != nil {
		return err
	}
	if r.ObservedAt.IsZero() {
		r.ObservedAt = s.now()
	}
	return s.runtime.store.AppendExperimentResult(ctx, cycleID, candidateID, r)
}

// ExperimentResults returns every attempt recorded for one candidate.
func (s *OptimizationService) ExperimentResults(ctx context.Context, cycleID, candidateID string) ([]optimization.ExperimentResult, error) {
	if s == nil || s.runtime == nil {
		return nil, model.ErrUnavailable
	}
	return s.runtime.store.ExperimentResults(ctx, cycleID, candidateID)
}

// RecordManifest validates, digests and stores one benchmark manifest.
func (s *OptimizationService) RecordManifest(ctx context.Context, cycleID string, m optimization.BenchmarkManifest) (optimization.BenchmarkManifest, error) {
	if s == nil || s.runtime == nil {
		return optimization.BenchmarkManifest{}, model.ErrUnavailable
	}
	if _, err := s.runtime.store.GetOptimizationCycle(ctx, cycleID); err != nil {
		return optimization.BenchmarkManifest{}, err
	}
	built, err := optimization.NewManifest(m)
	if err != nil {
		return optimization.BenchmarkManifest{}, err
	}
	if err := s.runtime.store.AppendBenchmarkManifest(ctx, cycleID, built); err != nil {
		return optimization.BenchmarkManifest{}, err
	}
	return s.runtime.store.GetBenchmarkManifest(ctx, built.ID)
}

// Manifests returns every benchmark manifest recorded against one cycle.
func (s *OptimizationService) Manifests(ctx context.Context, cycleID string) ([]optimization.BenchmarkManifest, error) {
	if s == nil || s.runtime == nil {
		return nil, model.ErrUnavailable
	}
	return s.runtime.store.BenchmarkManifests(ctx, cycleID)
}

// Promote evaluates a candidate for promotion and records the decision,
// whether it approves, bounds, defers or refuses. A refusal is written just
// like an approval: nothing here drops a BLOCKED, REJECT, STALE or
// NEEDS_MORE_EVIDENCE verdict, because the refusal is the evidentiary record
// that the candidate was actually considered.
func (s *OptimizationService) Promote(ctx context.Context, cycleID string, in optimization.PromotionInput, g optimization.Governance) (optimization.PromotionRecord, error) {
	if s == nil || s.runtime == nil {
		return optimization.PromotionRecord{}, model.ErrUnavailable
	}
	if _, err := s.runtime.store.GetOptimizationCycle(ctx, cycleID); err != nil {
		return optimization.PromotionRecord{}, err
	}
	record, promoteErr := optimization.Promote(in, g, s.now())
	// Promote returns a non-nil record together with ErrVeto for a BLOCKED
	// decision; every other path returns a nil error. Either way the record
	// itself is what gets stored: a veto is a decision, not a failure to
	// decide.
	if record.Digest == "" {
		return optimization.PromotionRecord{}, promoteErr
	}
	if err := s.runtime.store.AppendPromotionRecord(ctx, cycleID, record); err != nil {
		return optimization.PromotionRecord{}, err
	}
	return record, nil
}

// PromotionRecords returns every promotion decision made against one
// candidate, including refusals, in the order they were made.
func (s *OptimizationService) PromotionRecords(ctx context.Context, cycleID, candidateID string) ([]optimization.PromotionRecord, error) {
	if s == nil || s.runtime == nil {
		return nil, model.ErrUnavailable
	}
	return s.runtime.store.PromotionRecords(ctx, cycleID, candidateID)
}

// Canary opens or updates a bounded rollout of a promoted candidate. The
// candidate must have an actual promotion decision on record and governance
// must accept the requested exposure; neither check can be bypassed by a
// caller assembling the Canary record directly.
func (s *OptimizationService) Canary(ctx context.Context, cycleID, promotionID string, c optimization.Canary, g optimization.Governance) (optimization.Canary, error) {
	if s == nil || s.runtime == nil {
		return optimization.Canary{}, model.ErrUnavailable
	}
	if _, err := s.runtime.store.GetOptimizationCycle(ctx, cycleID); err != nil {
		return optimization.Canary{}, err
	}
	if err := optimization.ValidateCanary(c, g); err != nil {
		return optimization.Canary{}, err
	}
	if c.StartedAt.IsZero() {
		c.StartedAt = s.now()
	}
	if c.State == "" {
		c.State = optimization.CanaryPending
	}
	if err := s.runtime.store.AppendCanary(ctx, cycleID, promotionID, c); err != nil {
		return optimization.Canary{}, err
	}
	return s.runtime.store.GetCanary(ctx, c.ID)
}

// CanaryStatus returns one canary rollout's current record.
func (s *OptimizationService) CanaryStatus(ctx context.Context, canaryID string) (optimization.Canary, error) {
	if s == nil || s.runtime == nil {
		return optimization.Canary{}, model.ErrUnavailable
	}
	return s.runtime.store.GetCanary(ctx, canaryID)
}

// Canaries returns every canary recorded against one cycle.
func (s *OptimizationService) Canaries(ctx context.Context, cycleID string) ([]optimization.Canary, error) {
	if s == nil || s.runtime == nil {
		return nil, model.ErrUnavailable
	}
	return s.runtime.store.Canaries(ctx, cycleID)
}

// Rollback reverses a running canary, preserving every result it had already
// accumulated. The evidence a rollback was built on is exactly what Process 07
// needs to learn from the failure, so it is never discarded here.
func (s *OptimizationService) Rollback(ctx context.Context, canaryID, reason string) (optimization.Canary, error) {
	if s == nil || s.runtime == nil {
		return optimization.Canary{}, model.ErrUnavailable
	}
	current, err := s.runtime.store.GetCanary(ctx, canaryID)
	if err != nil {
		return optimization.Canary{}, err
	}
	rolledBack, err := optimization.Rollback(current, reason, s.now())
	if err != nil {
		return optimization.Canary{}, err
	}
	promotionID := ""
	if err := s.runtime.store.AppendCanary(ctx, "", promotionID, rolledBack); err != nil {
		return optimization.Canary{}, err
	}
	return s.runtime.store.GetCanary(ctx, canaryID)
}

// Recover reconciles an optimization cycle after an interrupted run.
//
// It reloads the cycle from durable state, revalidates that the Process 07
// memory commit it binds to still verifies, and returns a deterministic plan
// for the in-flight work. Nothing is applied here: the plan is returned so it
// can be reviewed, because an interruption is exactly the situation where
// automatically resuming is how a half-finished experiment becomes a result.
//
// An interrupted experiment never resolves as a pass, and a canary that was
// live across the restart is halted rather than resumed, since nothing was
// evaluating its rollback triggers while the process was down.
func (s *OptimizationService) Recover(ctx context.Context, cycleID string, inFlight []string) ([]optimization.ResumePlan, error) {
	if s == nil || s.runtime == nil {
		return nil, model.ErrUnavailable
	}
	cycle, err := s.runtime.store.GetOptimizationCycle(ctx, cycleID)
	if err != nil {
		return nil, err
	}

	// The source memory commit is reloaded rather than trusted: a cycle whose
	// learning has since been superseded cannot resume against it.
	sourceFresh := true
	if _, err := s.runtime.store.GetMemoryCommit(ctx, cycle.Binding.MemoryCommitID); err != nil {
		sourceFresh = false
	}

	// Results are gathered across every candidate in the cycle: recovery
	// reconciles the whole cycle, not one candidate's slice of it.
	var results []optimization.ExperimentResult
	for _, candidate := range cycle.Candidates {
		got, err := s.runtime.store.ExperimentResults(ctx, cycleID, candidate.ID)
		if err != nil {
			return nil, err
		}
		results = append(results, got...)
	}
	canaries, err := s.runtime.store.Canaries(ctx, cycleID)
	if err != nil {
		return nil, err
	}

	plans, err := optimization.Recover(optimization.RecoveryInput{
		Cycle: cycle, SourceFresh: sourceFresh,
		Results: results, InFlight: inFlight, Canaries: canaries,
	}, s.now())
	if err != nil {
		return nil, err
	}
	// Guard the recovery logic itself, so a future change that would let an
	// interruption resolve as success fails here rather than in production.
	if err := optimization.InterruptedNeverPasses(plans); err != nil {
		return nil, err
	}
	return plans, nil
}

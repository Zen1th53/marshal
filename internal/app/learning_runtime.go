package app

import (
	"context"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/learning"
	"github.com/Zen1th53/marshal/internal/model"
	marshalstore "github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/verification"
)

// LearningService is the sole application boundary for Process 07 state.
//
// Surfaces render what it returns; they cannot manufacture memory. The entry
// binding is always derived from canonical Process 06 records, never accepted
// from the caller, so a surface cannot attach durable knowledge to an outcome
// that was not verified.
type LearningService struct {
	runtime *Runtime
	now     func() time.Time
}

func (r *Runtime) Learning() *LearningService {
	if r == nil {
		return nil
	}
	return &LearningService{runtime: r, now: func() time.Time { return time.Now().UTC() }}
}

// EntryFor builds the exact Process 06 entry binding for one verification
// session by reading canonical state.
//
// A caller cannot supply the attestation digest, the evidence digest or the
// outcome: all three come from the stored attestation. If no attestation
// exists, learning that depends on it is blocked rather than guessed.
func (s *LearningService) EntryFor(ctx context.Context, verificationID string) (learning.Entry, error) {
	if s == nil || s.runtime == nil {
		return learning.Entry{}, fmt.Errorf("%w: learning service unavailable", model.ErrUnavailable)
	}
	session, err := s.runtime.store.GetVerificationSession(ctx, verificationID)
	if err != nil {
		return learning.Entry{}, err
	}
	attestation, err := s.runtime.store.LatestCompletionAttestation(ctx, verificationID)
	if err != nil {
		return learning.Entry{}, fmt.Errorf("%w: no completion attestation for %s", learning.ErrInvalid, verificationID)
	}
	// The attestation must describe the session version being learned from.
	// An attestation for an older version does not describe this state.
	if attestation.VerificationVersion != session.Version || attestation.Binding != session.Binding {
		return learning.Entry{}, learning.ErrBindingMismatch
	}
	if attestation.Digest == "" || attestation.EvidenceBundleDigest == "" {
		return learning.Entry{}, fmt.Errorf("%w: attestation is not digest-bound", learning.ErrInvalid)
	}

	outcome, err := entryOutcome(attestation.Decision)
	if err != nil {
		return learning.Entry{}, err
	}
	waivers := make([]string, 0, len(attestation.WaiverIDs))
	waivers = append(waivers, attestation.WaiverIDs...)
	return learning.Entry{
		ProjectID:           session.Binding.ProjectID,
		GoalID:              session.Binding.GoalID,
		GoalRevision:        session.Binding.GoalRevision,
		PlanID:              session.Binding.PlanID,
		PlanVersion:         session.Binding.PlanVersion,
		RunID:               session.Binding.RunID,
		RunVersion:          session.Binding.RunVersion,
		VerificationID:      session.ID,
		VerificationVersion: session.Version,
		AttestationDigest:   attestation.Digest,
		EvidenceDigest:      attestation.EvidenceBundleDigest,
		TreeDigest:          session.Binding.TreeDigest,
		EnvironmentDigest:   session.Binding.EnvironmentDigest,
		Outcome:             outcome,
		Limitations:         append([]string(nil), attestation.Limitations...),
		Waivers:             waivers,
	}, nil
}

// entryOutcome maps a Process 06 decision onto the Process 07 outcome
// vocabulary. A cancelled or replan decision is not a learnable outcome: those
// return an error rather than being flattened into FAILED, because inventing an
// outcome is how an unverified run becomes a fact.
func entryOutcome(d verification.Decision) (learning.Outcome, error) {
	switch d {
	case verification.VerifiedComplete:
		return learning.OutcomeVerifiedComplete, nil
	case verification.PartiallySatisfied:
		return learning.OutcomePartial, nil
	case verification.VerificationFailed, verification.NeedsReexecution:
		return learning.OutcomeFailed, nil
	case verification.Blocked:
		return learning.OutcomeBlocked, nil
	default:
		return "", fmt.Errorf("%w: decision %s is not a learnable outcome", learning.ErrInvalid, d)
	}
}

// CommitInput is one proposed learning commit. The caller supplies candidate
// knowledge; the service supplies the binding and every promotion gate.
type CommitInput struct {
	ID           string
	Verification string
	Provenance   string
	Candidates   []learning.PromotionInput
	Revisions    []learning.Item
	Invalidate   []string
	Observations []learning.RoutingObservation
	Fingerprints []learning.Fingerprint
	Playbooks    []learning.PlaybookCandidate
	Retention    learning.RetentionDecision
}

// Commit promotes candidate knowledge into durable memory.
//
// Every candidate passes the promotion gates individually. A candidate that
// fails is not silently dropped and does not fail the whole commit: it is
// recorded in BlockedLearning with the reason, so the refusal is auditable and
// the honest outcome is stored rather than a weakened fact.
func (s *LearningService) Commit(ctx context.Context, in CommitInput) (learning.Commit, error) {
	if s == nil || s.runtime == nil {
		return learning.Commit{}, fmt.Errorf("%w: learning service unavailable", model.ErrUnavailable)
	}
	entry, err := s.EntryFor(ctx, in.Verification)
	if err != nil {
		return learning.Commit{}, err
	}
	now := s.now()

	c := learning.Commit{
		ID:           in.ID,
		Binding:      entry,
		Provenance:   in.Provenance,
		Observations: in.Observations,
		Fingerprints: in.Fingerprints,
		Playbooks:    in.Playbooks,
		Retention:    in.Retention,
	}
	if in.Retention.Class != "" {
		if err := learning.ValidateRetention(in.Retention); err != nil {
			return learning.Commit{}, err
		}
	}

	for _, candidate := range in.Candidates {
		// The candidate is bound to the canonical entry, not to whatever
		// binding the caller attached to it.
		candidate.Item.Binding = entry
		promoted, err := learning.Promote(candidate, now)
		if err != nil {
			c.BlockedLearning = append(c.BlockedLearning, fmt.Sprintf("%s: %v", candidate.Item.ID, err))
			continue
		}
		c.Additions = append(c.Additions, promoted)
	}

	for _, next := range in.Revisions {
		prev, err := s.runtime.store.GetMemoryItem(ctx, next.ID)
		if err != nil {
			c.BlockedLearning = append(c.BlockedLearning, fmt.Sprintf("%s: %v", next.ID, err))
			continue
		}
		revised, err := learning.Revise(prev, next, now)
		if err != nil {
			c.BlockedLearning = append(c.BlockedLearning, fmt.Sprintf("%s: %v", next.ID, err))
			continue
		}
		c.Revisions = append(c.Revisions, revised)
	}

	c.Invalidations = append(c.Invalidations, in.Invalidate...)

	built, err := learning.NewCommit(c, now)
	if err != nil {
		return learning.Commit{}, err
	}
	if err := s.runtime.store.AppendMemoryCommit(ctx, built); err != nil {
		return learning.Commit{}, err
	}
	return s.runtime.store.GetMemoryCommit(ctx, built.ID)
}

// Get returns one durable memory commit with its digest verified.
func (s *LearningService) Get(ctx context.Context, id string) (learning.Commit, error) {
	if s == nil || s.runtime == nil {
		return learning.Commit{}, model.ErrUnavailable
	}
	return s.runtime.store.GetMemoryCommit(ctx, id)
}

// Item returns one stored memory item with its digest verified.
func (s *LearningService) Item(ctx context.Context, id string) (learning.Item, error) {
	if s == nil || s.runtime == nil {
		return learning.Item{}, model.ErrUnavailable
	}
	return s.runtime.store.GetMemoryItem(ctx, id)
}

// History returns the full version history of one item, oldest first.
func (s *LearningService) History(ctx context.Context, id string) ([]learning.Item, error) {
	if s == nil || s.runtime == nil {
		return nil, model.ErrUnavailable
	}
	return s.runtime.store.ItemRevisions(ctx, id)
}

// Search returns bounded, scope-aware, freshness-aware memory.
func (s *LearningService) Search(ctx context.Context, q learning.Query) ([]learning.Result, error) {
	if s == nil || s.runtime == nil {
		return nil, model.ErrUnavailable
	}
	if err := learning.ValidateQuery(q); err != nil {
		return nil, err
	}
	items, err := s.runtime.store.ListMemoryItems(ctx, q.ProjectID, q.IncludeGeneral)
	if err != nil {
		return nil, err
	}
	return learning.Retrieve(items, q, s.now()), nil
}

// Context builds the advisory memory context for a later Process 03, 04 or 05
// decision. Constraints stay binding, advisory memory stays advisory, and
// unresolved contradictions stay visible.
func (s *LearningService) Context(ctx context.Context, constraints []string, q learning.Query) ([]learning.Injection, error) {
	results, err := s.Search(ctx, q)
	if err != nil {
		return nil, err
	}
	return learning.BuildContext(constraints, results)
}

// Invalidate marks every item resting on a changed dependency as stale, and
// records the change as a memory commit so the invalidation is auditable.
//
// The fan-out is targeted: only items whose stored dependency version differs
// are touched, so upgrading one tool does not wipe unrelated knowledge.
func (s *LearningService) Invalidate(ctx context.Context, commitID, verificationID, provenance string, changed []learning.Dependency) (learning.Commit, error) {
	if s == nil || s.runtime == nil {
		return learning.Commit{}, model.ErrUnavailable
	}
	affected := map[string]learning.Item{}
	for _, dep := range changed {
		items, err := s.runtime.store.ItemsDependingOn(ctx, dep)
		if err != nil {
			return learning.Commit{}, err
		}
		for _, it := range items {
			affected[it.ID] = it
		}
	}
	now := s.now()
	stale := make([]learning.Item, 0, len(affected))
	for _, it := range affected {
		stale = append(stale, it)
	}
	revised := learning.Invalidate(stale, changed, now)

	entry, err := s.EntryFor(ctx, verificationID)
	if err != nil {
		return learning.Commit{}, err
	}
	built, err := learning.NewCommit(learning.Commit{
		ID:         commitID,
		Binding:    entry,
		Revisions:  revised,
		Provenance: provenance,
	}, now)
	if err != nil {
		return learning.Commit{}, err
	}
	if err := s.runtime.store.AppendMemoryCommit(ctx, built); err != nil {
		return learning.Commit{}, err
	}
	return s.runtime.store.GetMemoryCommit(ctx, built.ID)
}

// Trust aggregates routing observations for one task class.
//
// Aggregation reads every stored observation, including failures, blocked runs
// and routes that were never selected, so a provider handed only easy work
// cannot appear universally strong.
func (s *LearningService) Trust(ctx context.Context, taskClass string) (map[learning.TrustKey]learning.Trust, error) {
	if s == nil || s.runtime == nil {
		return nil, model.ErrUnavailable
	}
	obs, err := s.runtime.store.RoutingObservations(ctx, taskClass)
	if err != nil {
		return nil, err
	}
	return learning.AggregateTrust(obs), nil
}

// ProposeRouting evaluates a routing proposal against governance.
//
// The proposal is returned with its veto reasons whether or not it passes.
// Learning may inform routing; it can never relax a governance rule, so a
// vetoed proposal is refused here rather than applied with a warning.
func (s *LearningService) ProposeRouting(ctx context.Context, p learning.RoutingProposal, g learning.Governance) (learning.RoutingProposal, error) {
	if s == nil || s.runtime == nil {
		return learning.RoutingProposal{}, model.ErrUnavailable
	}
	return learning.ApplyProposal(p, g, s.now())
}

// Fingerprints returns bounded failure fingerprints for one project.
func (s *LearningService) Fingerprints(ctx context.Context, projectID string) ([]learning.Fingerprint, error) {
	if s == nil || s.runtime == nil {
		return nil, model.ErrUnavailable
	}
	return s.runtime.store.FailureFingerprints(ctx, projectID)
}

// Playbooks returns candidate procedures. They are always inactive.
func (s *LearningService) Playbooks(ctx context.Context, projectID string) ([]learning.PlaybookCandidate, error) {
	if s == nil || s.runtime == nil {
		return nil, model.ErrUnavailable
	}
	return s.runtime.store.PlaybookCandidates(ctx, projectID)
}

// Replays returns the replay index for one run.
func (s *LearningService) Replays(ctx context.Context, runID string) ([]learning.ReplayRecord, error) {
	if s == nil || s.runtime == nil {
		return nil, model.ErrUnavailable
	}
	return s.runtime.store.ReplayRecords(ctx, runID)
}

// RecordReplay indexes one reproducible run against a stored memory commit.
func (s *LearningService) RecordReplay(ctx context.Context, commitID string, r learning.ReplayRecord) error {
	if s == nil || s.runtime == nil {
		return model.ErrUnavailable
	}
	if _, err := s.runtime.store.GetMemoryCommit(ctx, commitID); err != nil {
		return err
	}
	if r.RecordedAt.IsZero() {
		r.RecordedAt = s.now()
	}
	return s.runtime.store.AppendReplayRecord(ctx, commitID, r)
}

// Benchmarks returns stored evaluation records.
func (s *LearningService) Benchmarks(ctx context.Context, benchmark string) ([]learning.BenchmarkRecord, error) {
	if s == nil || s.runtime == nil {
		return nil, model.ErrUnavailable
	}
	return s.runtime.store.BenchmarkRecords(ctx, benchmark)
}

// RecordBenchmark stores one reproducible evaluation result. A benchmark score
// is telemetry: it is recorded here and never promotes a provider by itself.
func (s *LearningService) RecordBenchmark(ctx context.Context, b learning.BenchmarkRecord) error {
	if s == nil || s.runtime == nil {
		return model.ErrUnavailable
	}
	if b.RecordedAt.IsZero() {
		b.RecordedAt = s.now()
	}
	return s.runtime.store.AppendBenchmarkRecord(ctx, b)
}

// Export produces a secret-safe, integrity-checked backup of project memory.
func (s *LearningService) Export(ctx context.Context, projectID string, includeGeneral bool) (learning.ExportBundle, error) {
	if s == nil || s.runtime == nil {
		return learning.ExportBundle{}, model.ErrUnavailable
	}
	items, err := s.runtime.store.ListMemoryItems(ctx, projectID, includeGeneral)
	if err != nil {
		return learning.ExportBundle{}, err
	}
	return learning.Export(items, marshalstore.LatestSchemaVersion, projectID, s.now())
}

// PlanRestore reconciles a backup against current memory without applying it.
// Restore is a reviewed operation: the plan says what would change and why, and
// a backup never overwrites newer canonical memory.
func (s *LearningService) PlanRestore(ctx context.Context, b learning.ExportBundle, projectID string, includeGeneral bool) ([]learning.RestorePlan, error) {
	if s == nil || s.runtime == nil {
		return nil, model.ErrUnavailable
	}
	items, err := s.runtime.store.ListMemoryItems(ctx, projectID, includeGeneral)
	if err != nil {
		return nil, err
	}
	return learning.Restore(b, items, marshalstore.LatestSchemaVersion)
}

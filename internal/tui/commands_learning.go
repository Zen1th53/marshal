package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/learning"
)

// handleMemoryCommit renders one canonical Process 07 memory commit.
//
// Refusals are shown alongside promotions. A commit that promoted nothing and
// blocked four candidates is the interesting case, and hiding it would make
// learning look more successful than it was.
func (h *CommandHandler) handleMemoryCommit(ctx context.Context, id string) (string, error) {
	if h.ws.store == nil {
		return "Canonical learning store unavailable.", nil
	}
	record, err := h.ws.store.GetMemoryCommit(ctx, id)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "MEMORY COMMIT %s\n", record.ID)
	fmt.Fprintf(&b, "  Outcome: %s\n", record.Binding.Outcome)
	fmt.Fprintf(&b, "  Verification: %s v%d\n", record.Binding.VerificationID, record.Binding.VerificationVersion)
	fmt.Fprintf(&b, "  Attestation: %s\n", record.Binding.AttestationDigest)
	fmt.Fprintf(&b, "  Promoted: %d | Revised: %d | Invalidated: %d\n",
		len(record.Additions), len(record.Revisions), len(record.Invalidations))
	fmt.Fprintf(&b, "  Observations: %d | Fingerprints: %d | Playbook candidates: %d\n",
		len(record.Observations), len(record.Fingerprints), len(record.Playbooks))
	if len(record.BlockedLearning) > 0 {
		fmt.Fprintf(&b, "  Blocked learning:\n")
		for _, blocked := range record.BlockedLearning {
			fmt.Fprintf(&b, "    %s\n", blocked)
		}
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// handleMemorySearch renders bounded Process 07 memory for one project.
//
// Every line carries the claim's state and freshness, and a contradicted claim
// is labelled, so nothing here reads as a settled fact when it is not.
func (h *CommandHandler) handleMemorySearch(ctx context.Context, projectID string, terms []string, includeStale bool) (string, error) {
	if h.ws.store == nil {
		return "Canonical learning store unavailable.", nil
	}
	items, err := h.ws.store.ListMemoryItems(ctx, projectID, true)
	if err != nil {
		return "", err
	}
	results := learning.Retrieve(items, learning.Query{
		ProjectID:      projectID,
		IncludeGeneral: true,
		Terms:          terms,
		IncludeStale:   includeStale,
	}, time.Now().UTC())
	if len(results) == 0 {
		return "No memory matched.", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "MEMORY (%d)\n", len(results))
	for _, r := range results {
		marker := " "
		if !r.Usable {
			marker = "!"
		}
		fmt.Fprintf(&b, " %s %s [%s %s clusters=%d] %s\n",
			marker, r.Item.ID, r.Item.State, r.Item.Scope, r.Clusters, r.Item.Claim)
		if r.Contradicted {
			fmt.Fprintf(&b, "     contradicted by: %s\n", strings.Join(r.Item.Contradicts, ", "))
		}
		if !r.Fresh {
			fmt.Fprintf(&b, "     stale: not usable as current knowledge\n")
		}
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// handleMemoryProvenance renders one item's full version history, so what was
// believed before a revision stays visible.
func (h *CommandHandler) handleMemoryProvenance(ctx context.Context, itemID string) (string, error) {
	if h.ws.store == nil {
		return "Canonical learning store unavailable.", nil
	}
	history, err := h.ws.store.ItemRevisions(ctx, itemID)
	if err != nil {
		return "", err
	}
	if len(history) == 0 {
		return fmt.Sprintf("No memory item %s.", itemID), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "MEMORY PROVENANCE %s\n", itemID)
	for _, it := range history {
		fmt.Fprintf(&b, "  v%d %s  %s\n", it.Version, it.State, it.Provenance)
		fmt.Fprintf(&b, "     claim: %s\n", it.Claim)
		if len(it.Evidence) > 0 {
			clusters := map[string]bool{}
			for _, e := range it.Evidence {
				clusters[e.ClusterID] = true
			}
			fmt.Fprintf(&b, "     evidence: %d refs across %d independent clusters\n", len(it.Evidence), len(clusters))
		}
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// handleRoutingTrust renders measured routing outcomes.
//
// Failures, blocked runs and the selection-bias flag are shown with the
// successes, and an unmeasured cost prints as "unmeasured" rather than zero.
func (h *CommandHandler) handleRoutingTrust(ctx context.Context, taskClass string) (string, error) {
	if h.ws.store == nil {
		return "Canonical learning store unavailable.", nil
	}
	observations, err := h.ws.store.RoutingObservations(ctx, taskClass)
	if err != nil {
		return "", err
	}
	trust := learning.AggregateTrust(observations)
	if len(trust) == 0 {
		return "No routing observations.", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "ROUTING TRUST (%d)\n", len(trust))
	for key, record := range trust {
		fmt.Fprintf(&b, "  %s  %s/%s %s\n", key.TaskClass, key.Provider, key.ProviderVersion, key.Model)
		fmt.Fprintf(&b, "    verified=%d partial=%d failed=%d blocked=%d clusters=%d\n",
			record.Verified, record.Partial, record.Failed, record.Blocked, record.Clusters)
		fmt.Fprintf(&b, "    cost=%s latency=%s\n",
			renderMeasured(record.MeasuredCostMicros), renderMeasured(record.MeasuredLatencyMillis))
		if record.SelectionBiased {
			fmt.Fprintf(&b, "    selection bias: every observation came from a deliberately selected route\n")
		}
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// renderMeasured keeps an unmeasured value distinguishable from a measured zero.
func renderMeasured(v *int64) string {
	if v == nil {
		return "unmeasured"
	}
	return fmt.Sprintf("%d", *v)
}

// handleFingerprints renders bounded failure fingerprints.
func (h *CommandHandler) handleFingerprints(ctx context.Context, projectID string) (string, error) {
	if h.ws.store == nil {
		return "Canonical learning store unavailable.", nil
	}
	prints, err := h.ws.store.FailureFingerprints(ctx, projectID)
	if err != nil {
		return "", err
	}
	if len(prints) == 0 {
		return "No failure fingerprints.", nil
	}
	now := time.Now().UTC()
	var b strings.Builder
	fmt.Fprintf(&b, "FAILURE FINGERPRINTS (%d)\n", len(prints))
	for _, f := range prints {
		stale := ""
		if f.ExpiresAt != nil && !now.Before(*f.ExpiresAt) {
			// A historical fingerprint is advisory and never outranks fresh
			// evidence, so an expired one says so on its own line.
			stale = "  (stale: revalidate before relying on it)"
		}
		fmt.Fprintf(&b, "  %s  %s  occurrences=%d scope=%s%s\n", f.ID, f.Signature, f.Occurrences, f.Scope, stale)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// handlePlaybookCandidates renders candidate procedures awaiting review.
func (h *CommandHandler) handlePlaybookCandidates(ctx context.Context, projectID string) (string, error) {
	if h.ws.store == nil {
		return "Canonical learning store unavailable.", nil
	}
	candidates, err := h.ws.store.PlaybookCandidates(ctx, projectID)
	if err != nil {
		return "", err
	}
	if len(candidates) == 0 {
		return "No playbook candidates.", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "PLAYBOOK CANDIDATES (%d awaiting review)\n", len(candidates))
	for _, p := range candidates {
		// Activation is a governed decision made elsewhere. The TUI shows the
		// candidate and says plainly that it is not active.
		fmt.Fprintf(&b, "  %s  %q  scope=%s  steps=%d  active=false\n", p.ID, p.Title, p.Scope, len(p.Steps))
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// handleReplayIndex renders the reproducibility index for one run.
func (h *CommandHandler) handleReplayIndex(ctx context.Context, runID string) (string, error) {
	if h.ws.store == nil {
		return "Canonical learning store unavailable.", nil
	}
	records, err := h.ws.store.ReplayRecords(ctx, runID)
	if err != nil {
		return "", err
	}
	if len(records) == 0 {
		return "No replay records.", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "REPLAY INDEX (%d)\n", len(records))
	for _, r := range records {
		fmt.Fprintf(&b, "  %s  class=%s  run=%s\n", r.ID, r.Class, r.Binding.RunID)
		if !r.Replayable() {
			// Naming the side effects is the point: an operator should see why
			// a replay will not run itself.
			fmt.Fprintf(&b, "     not automatically replayable: %s\n", strings.Join(r.SideEffects, ", "))
		}
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

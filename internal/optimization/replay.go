package optimization

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/learning"
)

// ReplayRequest is the complete, side-effect-free hand-off to an execution
// adapter.  The adapter receives no production credentials and must execute
// only inside the supplied bounded sandbox.  Keeping these constraints in the
// request makes them testable at the boundary where an alternate route is
// actually invoked, rather than merely describing them in a record afterward.
type ReplayRequest struct {
	TaskID            string
	TreeDigest        string
	EnvironmentDigest string
	Alternate         Route
	Sandbox           SandboxPolicy
}

// ReplayObservation is the verifier-backed result of executing an alternate
// route.  An adapter must report UNKNOWN when it cannot obtain a verifier
// result; callers never infer a PASS from process exit or agent self-report.
type ReplayObservation struct {
	Outcome        learning.Outcome
	VerifierResult Status
	Metrics        []Metric
	Limitations    []string
}

// ReplayRunner executes a recorded task on an isolated copy. Implementations
// are responsible for materializing the pinned tree and enforcing the supplied
// filesystem, network, memory and wall-time bounds. It deliberately has no
// credentials or live-action capability in its input.
type ReplayRunner interface {
	Replay(context.Context, ReplayRequest) (ReplayObservation, error)
}

// ExecuteReplay performs a real alternate-route replay and returns an
// independently digested counterfactual record. Unsafe or unbound work is
// refused before the runner is called, which prevents a destructive task from
// being duplicated even by a faulty adapter.
func ExecuteReplay(ctx context.Context, runner ReplayRunner, factual FactualRun, alternate Route, sandbox SandboxPolicy, provenance, clusterID string, g Governance, now time.Time) (Counterfactual, error) {
	if runner == nil {
		return Counterfactual{}, fmt.Errorf("%w: replay runner", ErrInvalid)
	}
	if err := SafeToEvaluate(factual, MethodReplay); err != nil {
		return Counterfactual{}, err
	}
	if err := ValidateSandbox(sandbox); err != nil {
		return Counterfactual{}, err
	}
	if sandbox.NetworkEnabled {
		return Counterfactual{}, fmt.Errorf("%w: replay network must remain disabled", ErrUnsafeCounterfactual)
	}
	if !alternate.Valid() {
		return Counterfactual{}, fmt.Errorf("%w: alternate route is not exact", ErrInvalid)
	}
	if g.GovernableProviders != nil && !g.GovernableProviders[alternate.Provider] {
		return Counterfactual{}, fmt.Errorf("%w: alternate provider %s is not governable", ErrVeto, alternate.Provider)
	}

	// Context deadline is a second enforcement layer. The runner must also
	// enforce its local bound, but a cooperative adapter cannot exceed this
	// caller-owned limit.
	replayCtx, cancel := context.WithTimeout(ctx, time.Duration(sandbox.MaxWallMillis)*time.Millisecond)
	defer cancel()
	observation, err := runner.Replay(replayCtx, ReplayRequest{
		TaskID: factual.TaskID, TreeDigest: factual.TreeDigest,
		EnvironmentDigest: factual.EnvironmentDigest, Alternate: alternate, Sandbox: sandbox,
	})
	if err != nil {
		return Counterfactual{}, fmt.Errorf("replay alternate route: %w", err)
	}
	if !ValidStatus(observation.VerifierResult) {
		return Counterfactual{}, fmt.Errorf("%w: replay runner returned invalid verifier result", ErrInvalid)
	}
	limitations := append([]string(nil), observation.Limitations...)
	limitations = append(limitations, "alternate route executed in offline replay sandbox")
	return NewCounterfactual(Counterfactual{
		ID:                replayID(factual, alternate, now),
		Method:            MethodReplay,
		Factual:           factual,
		Alternate:         alternate,
		AlternateOutcome:  observation.Outcome,
		AlternateVerifier: observation.VerifierResult,
		AlternateMetrics:  observation.Metrics,
		Sandbox:           sandbox,
		Limitations:       limitations,
		ClusterID:         clusterID,
		Provenance:        provenance,
	}, g, now)
}

func replayID(factual FactualRun, alternate Route, now time.Time) string {
	parts := []string{factual.TaskID, alternate.Provider, alternate.Model, now.UTC().Format(time.RFC3339Nano)}
	return "replay-" + strings.ReplaceAll(strings.Join(parts, "-"), " ", "_")
}

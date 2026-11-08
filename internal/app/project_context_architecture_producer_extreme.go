//go:build !governed

package app

import (
	"context"
	"reflect"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/verification"
)

// Never exported or reconstructed from native JSON. Only the authenticated
// runtime's canonical completion/source fence prepares this exact CLI target.
type requiredArchitectureTurnTarget struct {
	runtime                         *Runtime
	cli                             *MarshalCLI
	principal                       authz.Principal
	binding                         projectcontext.Binding
	admission                       store.ProjectContextAdmission
	input                           store.ProjectArchitectureReviewInput
	session                         verification.Session
	envelope                        verification.BundleEnvelope
	versions                        map[string]int64
	provider, workspace, head, tree string
	prepared, consumed              bool
	verification                    bool
	proof                           requiredProjectWorkspaceProof
	receipt                         projectcontext.Receipt
}

func (m *MarshalCLI) prepareRequiredArchitectureMarshalTurn(ctx context.Context, prompt string, target *requiredArchitectureTurnTarget) (context.Context, string, func() error, error) {
	if m == nil || target == nil || target.cli != m || target.runtime != m.runtime || target.provider != m.Provider || target.prepared || target.consumed || m.ConversationID != "" {
		return nil, "", nil, projectcontext.ErrReviewPending
	}
	r := target.runtime
	principal, err := requiredArchitectureOwner(ctx, r)
	if err != nil || principal.ID != target.principal.ID {
		return nil, "", nil, projectcontext.Changed("project_context_architecture_producer_extreme.go:42")
	}
	target.prepared = true
	var token *requiredProjectInvocation
	var text string
	var receipt projectcontext.Receipt
	var input store.ProjectArchitectureReviewInput
	release, err := admitRunControlLifecycle(r)
	if err != nil {
		return nil, "", nil, err
	}
	err = r.Execution().Engine().WithRequiredContextFence(ctx, target.versions, func(snapshots map[string]execution.ExecutionRun) error {
		if _, err := r.requiredCompletionSourceFenced(ctx, snapshots, target.binding.RunID); err != nil {
			return err
		}
		target.proof = target.architectureProof(snapshots)
		sealed := context.WithValue(ctx, requiredProjectWorkspaceKey{}, target.proof)
		prepared, err := r.prepareRequiredProjectContext(sealed, principal, target.binding, target.admission.Selection, prompt, requiredProviderEnvelopeBytes)
		if err != nil {
			return err
		}
		prepared.Admission.Stage = "review"
		if !target.verification {
			input, err = r.store.ReadProjectArchitectureReview(ctx, prepared.Admission, target.session.ID, target.session.Version, target.envelope)
			if err != nil {
				return err
			}
			before, after := target.input, input
			before.Digest, after.Digest = "", ""
			before.Packet, after.Packet = projectcontext.Packet{}, projectcontext.Packet{}
			if !reflect.DeepEqual(before, after) {
				return projectcontext.Changed("project_context_architecture_producer_extreme.go:73")
			}
		}
		token, err = r.admitRequiredProjectInvocation(sealed, principal, target.binding, target.admission.Selection, prompt, requiredProviderEnvelopeBytes, "review", "")
		if err != nil {
			return err
		}
		text, receipt, err = r.consumeRequiredProjectInvocation(sealed, token)
		return err
	})
	release()
	if err != nil {
		target.consumed = true
		return nil, "", nil, err
	}
	target.input, target.binding, target.admission, target.receipt = input, receipt.Binding, token.dispatchAdmission, receipt
	owned, watch, err := r.startRequiredProjectContextWatch(ctx, receipt, target.admission)
	if err != nil {
		target.consumed = true
		return nil, "", nil, err
	}
	m.requiredTurnPreparation = &requiredMarshalTurnPreparation{runtime: r, cli: m, receipt: receipt, admission: target.admission, workspace: target.workspace, head: target.head, tree: target.tree}
	finish := func() error {
		if err := watch.Finish(); err != nil {
			return err
		}
		return target.recheck(ctx)
	}
	return owned, text, finish, nil
}

// This predicate permits the narrow private resolver's task-less canonical
// completion target; no serialized metadata can mint the target pointer.
func (t *requiredArchitectureTurnTarget) matches(r *Runtime, b projectcontext.Binding) bool {
	return t != nil && r != nil && t.runtime == r && t.cli != nil && t.cli.runtime == r && !t.consumed && requiredWatchBinding(t.binding) == requiredWatchBinding(b) && b.TaskID == "" && b.CanonicalTaskID == "" && b.Provider == "" && b.PlanID != "" && b.PlanVersion > 0 && b.RunID != "" && b.RunVersion > 0
}

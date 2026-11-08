//go:build !governed

package app

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/verification"
)

// An attestation in SQLite is data. This exact-runtime capability exists only
// after the actual review and atomic all-task completion transaction succeed.
type requiredContextCloseProof struct {
	mu                           sync.Mutex
	runtime                      *Runtime
	sourceObservation, principal string
	session                      verification.Session
	attestation                  verification.CompletionAttestation
	admissions                   []store.ProjectContextAdmission
	consumed                     bool
}

func (r *Runtime) retainRequiredContextCloseProof(source *requiredIntegrationSource, proof *requiredArchitectureCompletionProof, session verification.Session, attestation verification.CompletionAttestation, admissions []store.ProjectContextAdmission) error {
	r.requiredIntegrationSources.mu.Lock()
	defer r.requiredIntegrationSources.mu.Unlock()
	observed := r.requiredIntegrationSources.byRun[session.Binding.RunID]
	if observed == nil || observed.runtime != r || observed.observationID != source.observationID || source.head != proof.capture.preparation.head || source.tree != session.Binding.TreeDigest {
		return fmt.Errorf("required close retained completion source: %w", projectcontext.Changed("project_context_close_extreme.go:37"))
	}
	all := append([]store.ProjectContextAdmission{proof.capture.preparation.admission}, admissions...)
	observed.completion = &requiredContextCloseProof{runtime: r, sourceObservation: source.observationID, principal: proof.receipt.Binding.Principal, session: session, attestation: attestation, admissions: all}
	return nil
}

// The trusted common caller supplies only the final target Git effect. The
// journal's two exact writes are the sole accepted Marshal revision change.
// No model invocation or SQL transaction spans that effect.
func (r *Runtime) withRequiredContextCloseEffect(ctx context.Context, service *MarshalService, id string, baseline marshal.Run, op *marshal.LifecycleOperation, effect func() error) error {
	principal, err := requiredArchitectureOwner(ctx, r)
	if err != nil {
		return err
	}
	if service == nil || service.Store != r.store || service.Repository != r.ProjectRoot() || service.Worktrees != r.layout.Worktrees || effect == nil || op == nil || op.ID == "" || op.Kind != "close" || !op.Authorized || op.Next == nil || op.TaskID != "" || op.Before != baseline.BaseCommit {
		return fmt.Errorf("required close journal input: %w", projectcontext.Changed("project_context_close_extreme.go:53"))
	}
	r.requiredIntegrationSources.mu.Lock()
	var source *requiredIntegrationSource
	for _, candidate := range r.requiredIntegrationSources.byRun {
		if candidate.marshalRunID == id {
			if source != nil {
				r.requiredIntegrationSources.mu.Unlock()
				return fmt.Errorf("required close unique observed source: %w", projectcontext.Changed("project_context_close_extreme.go:61"))
			}
			source = candidate
		}
	}
	var proof *requiredContextCloseProof
	if source != nil {
		proof = source.completion
	}
	r.requiredIntegrationSources.mu.Unlock()
	if source == nil || source.runtime != r || proof == nil {
		return projectcontext.ErrReviewPending
	}
	if proof.runtime != r || proof.principal != principal.ID || proof.sourceObservation != source.observationID {
		return fmt.Errorf("required close live proof owner: %w", projectcontext.Changed("project_context_close_extreme.go:75"))
	}
	proof.mu.Lock()
	defer proof.mu.Unlock()
	if proof.consumed {
		return fmt.Errorf("required close one-shot capability: %w", projectcontext.Changed("project_context_close_extreme.go:80"))
	}
	proof.consumed = true
	if !reflect.DeepEqual(baseline, source.marshalRun) || op.After != source.head || op.Target != baseline.TargetRef {
		return fmt.Errorf("required close approved baseline: %w", projectcontext.Changed("project_context_close_extreme.go:84"))
	}
	next := baseline
	next.State = marshal.Closed
	if !reflect.DeepEqual(*op.Next, next) {
		return fmt.Errorf("required close authorized next state: %w", projectcontext.Changed("project_context_close_extreme.go:89"))
	}
	project, err := r.store.Project(ctx)
	if err != nil || op.Target != "refs/heads/"+project.DefaultBranch {
		return fmt.Errorf("required close approved target branch: %w", projectcontext.Changed("project_context_close_extreme.go:93"))
	}
	expected := baseline
	expected.Operation = op
	stored, err := r.store.GetMarshalRun(ctx, r.ProjectID(), id)
	if err != nil || stored.Revision != source.marshalRevision+2 || !reflect.DeepEqual(stored.Value, expected) {
		return fmt.Errorf("required close exact journal revision: %w", projectcontext.Changed("project_context_close_extreme.go:99"))
	}
	release, err := admitRunControlLifecycle(r)
	if err != nil {
		return err
	}
	defer release()
	return r.Execution().Engine().WithRequiredContextFence(ctx, source.versions, func(snapshots map[string]execution.ExecutionRun) error {
		root, tasks, err := r.Execution().Engine().ValidateRequiredContextFenceCompletion(ctx, snapshots, source.rootRun.RunID)
		if err != nil || !reflect.DeepEqual(root, source.rootRun) {
			return fmt.Errorf("required close owned terminal run: %w", projectcontext.Changed("project_context_close_extreme.go:109"))
		}
		current := *source
		current.marshalRun, current.marshalRevision = stored.Value, stored.Revision
		if err = r.validateRequiredIntegrationSource(ctx, &current, root, tasks); err != nil {
			return err
		}
		if current.tree != source.tree || current.commonDir != source.commonDir {
			return fmt.Errorf("required close current source tree: %w", projectcontext.Changed("project_context_close_extreme.go:117"))
		}
		session, err := r.store.GetVerificationSession(ctx, proof.session.ID)
		if err != nil || !reflect.DeepEqual(session, proof.session) || verification.Evaluate(session, session.Binding, time.Now().UTC()) != verification.VerifiedComplete {
			return fmt.Errorf("required close current verification session: %w", projectcontext.Changed("project_context_close_extreme.go:121"))
		}
		attestation, err := r.store.LatestCompletionAttestation(ctx, session.ID)
		if err != nil || attestation.Digest != proof.attestation.Digest || attestation.Verify(session.Binding) != nil {
			return fmt.Errorf("required close canonical attestation digest: %w", projectcontext.Changed("project_context_close_extreme.go:125"))
		}
		goal, err := r.store.GetGoalContract(ctx, session.Binding.GoalID, session.Binding.GoalRevision)
		if err != nil {
			return err
		}
		grounding := &requiredProjectGroundingBudget{Hashes: map[string]string{}}
		for _, admission := range proof.admissions {
			now := time.Now().UTC()
			snapshot, err := r.store.ReadProjectContextSnapshotBound(ctx, admission.Binding.Project, admission.Audience, admission.Selection, now, admission.Binding.Contract)
			if err != nil || snapshot.StateDigest != admission.ExpectedStateDigest {
				return fmt.Errorf("required close current mandatory memory: %w", projectcontext.Changed("project_context_close_extreme.go:136"))
			}
			for _, record := range snapshot.Records {
				inputs, err := requiredProjectFreshnessInputsBounded(source.workspace, source.head, record, grounding)
				if err != nil || r.memoryService.EvaluateFreshness(record, inputs).Classification != FreshnessFresh {
					return fmt.Errorf("required close fresh mandatory memory: %w", projectcontext.Changed("project_context_close_extreme.go:141"))
				}
			}
			packet, err := projectcontext.RenderWithPrefix(projectcontext.Manifest{Contract: snapshot.Contract, Goal: projectcontext.GoalFromCanonical(goal), Binding: admission.Binding, Selection: admission.Selection, Records: snapshot.Records}, admission.Prefix, admission.ByteCap)
			if err != nil || packet.Digest != admission.ExpectedPacketDigest || admission.ExpectedAuthority == nil {
				return fmt.Errorf("required close full context packet: %w", projectcontext.Changed("project_context_close_extreme.go:146"))
			}
			authority, err := r.store.ReadProjectContextAuthority(ctx, admission.ExpectedAuthority.Request, now)
			if err != nil || authority.Digest != admission.ExpectedAuthority.Digest {
				return fmt.Errorf("required close current SQL authority: %w", projectcontext.Changed("project_context_close_extreme.go:150"))
			}
		}
		if err = r.validateRequiredIntegrationSource(ctx, &current, root, tasks); err != nil {
			return err
		}
		before, err := gitMarshal(ctx, service.Repository, "rev-parse", op.Target)
		if err != nil || strings.TrimSpace(before) != op.Before {
			return fmt.Errorf("required close target base: %w", projectcontext.Changed("project_context_close_extreme.go:158"))
		}
		return effect()
	})
}

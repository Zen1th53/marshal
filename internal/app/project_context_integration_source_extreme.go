//go:build !governed

package app

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/hostgit"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/projectid"
)

// Durable source observations are data only. This same-runtime registry binds
// revalidation to a trusted observed source; caller JSON cannot restore it.
type requiredIntegrationSourceRegistry struct {
	mu    sync.Mutex
	byRun map[string]*requiredIntegrationSource
}
type requiredIntegrationSource struct {
	runtime                                         *Runtime
	marshalRunID                                    string
	marshalRevision                                 int64
	marshalRun                                      marshal.Run
	rootRun                                         execution.ExecutionRun
	versions                                        map[string]int64
	workspace, head, tree, commonDir, observationID string
	contract                                        projectcontext.ContractRef
	completion                                      *requiredContextCloseProof
	verification                                    *requiredCanonicalVerifierProof
}

func requiredArchitectureOwner(ctx context.Context, r *Runtime) (authz.Principal, error) {
	if r == nil || r.store == nil {
		return authz.Principal{}, projectcontext.ErrMissing
	}
	owner, ok := auth.LocalFromContext(ctx)
	if !ok || ctx.Value(localControlKey{}) != r || owner.ProjectID() != r.ProjectIdentity() {
		return authz.Principal{}, authz.ErrDenied
	}
	principal := authz.Principal{ID: owner.ID(), Role: authz.Role{Name: "marshal", Authorities: []authz.Authority{authz.AuthorityPolicyAdmin}}}
	if err := r.authorizeProjectContractInspection(ctx, principal); err != nil {
		return authz.Principal{}, err
	}
	return principal, nil
}

func (r *Runtime) observeRequiredMarshalIntegrationSource(ctx context.Context, s *MarshalService, marshalRunID string, expected marshal.Run, head string) error {
	if _, err := requiredArchitectureOwner(ctx, r); err != nil {
		return err
	}
	if s == nil || s.Store != r.store || s.ProjectID != r.ProjectID() || s.Repository != r.ProjectRoot() || s.Worktrees != r.layout.Worktrees || marshalRunID == "" || !expected.Process05Bound || expected.State != marshal.Verifying || !fullProofObject(head) {
		return projectcontext.Changed("project_context_integration_source_extreme.go:62")
	}
	stored, err := r.store.GetMarshalRun(ctx, r.ProjectID(), marshalRunID)
	if err != nil || !reflect.DeepEqual(stored.Value, expected) {
		return projectcontext.Changed("project_context_integration_source_extreme.go:66")
	}
	p, err := r.store.GetActivePlan(ctx, projectid.ID(r.ProjectIdentity()))
	if err != nil || p.ID != expected.PlanID || p.Version != expected.PlanVersion || p.State != plan.StateApproved {
		return projectcontext.Changed("project_context_integration_source_extreme.go:70")
	}
	accepted, err := r.store.ReadAcceptedProjectContract(ctx, r.ProjectContractBinding())
	if err != nil {
		return err
	}
	ref := projectcontext.ContractRef{ID: accepted.Contract.ID, Revision: accepted.Contract.Revision, Digest: accepted.Digest}
	if pinned, err := requiredApprovedPlanProjectContract(p); err != nil || pinned != ref {
		return projectcontext.Changed("project_context_integration_source_extreme.go:78")
	}
	goal, err := r.store.GetActiveGoalContract(ctx, pGoalSession(ctx, r, p))
	if err != nil || goal.ID != p.Goal.GoalID || goal.Revision != p.Goal.Revision {
		return projectcontext.Changed("project_context_integration_source_extreme.go:82")
	}
	engine := r.Execution().Engine()
	runs, err := engine.ListRuns(ctx)
	if err != nil {
		return err
	}
	var root execution.ExecutionRun
	for _, candidate := range runs {
		if candidate.ParentRunID == "" && string(candidate.ProjectID) == r.ProjectIdentity() && candidate.SessionID == goal.SessionID && candidate.GoalID == goal.ID && candidate.GoalRevision == goal.Revision && candidate.PlanID == p.ID && candidate.PlanVersion == p.Version {
			if root.RunID != "" {
				return projectcontext.Changed("project_context_integration_source_extreme.go:93")
			}
			root = candidate
		}
	}
	if root.RunID == "" {
		return fmt.Errorf("%w: no completed canonical Process05 root matches this goal/plan; re-review the current goal and plan and re-execute before /marshal resume", projectcontext.ErrReviewPending)
	}
	versions, err := requiredCompletionVersions(ctx, engine, root)
	if err != nil {
		return err
	}
	release, err := admitRunControlLifecycle(r)
	if err != nil {
		return err
	}
	defer release()
	return engine.WithRequiredContextFence(ctx, versions, func(snapshots map[string]execution.ExecutionRun) error {
		current, tasks, err := engine.ValidateRequiredContextFenceCompletion(ctx, snapshots, root.RunID)
		if err != nil {
			return err
		}
		source := &requiredIntegrationSource{runtime: r, marshalRunID: marshalRunID, marshalRevision: stored.Revision, marshalRun: stored.Value, rootRun: current, versions: versions, workspace: filepath.Join(r.layout.Worktrees, integrationTaskID(marshalRunID, expected)), head: head, contract: ref}
		if err = r.validateRequiredIntegrationSource(ctx, source, current, tasks); err != nil {
			return err
		}
		source.observationID, err = model.NewID("CTX-SOURCE-")
		if err != nil {
			return err
		}
		data := map[string]any{"marshal_run_id": marshalRunID, "marshal_revision": source.marshalRevision, "root_run_id": current.RunID, "root_run_version": current.Version, "all_run_versions": versions, "source_head": source.head, "workspace_tree": source.tree, "contract": ref, "marshal_digest": projectcontext.Digest("marshal.required.integration.marshal.v1", source.marshalRun)}
		if err = r.store.AppendEvent(ctx, nil, model.Event{ID: source.observationID, ProjectID: r.ProjectID(), Type: "PROJECT_INTEGRATION_SOURCE_OBSERVED", Timestamp: time.Now().UTC(), Data: data}); err != nil {
			return err
		}
		r.requiredIntegrationSources.mu.Lock()
		defer r.requiredIntegrationSources.mu.Unlock()
		if r.requiredIntegrationSources.byRun == nil {
			r.requiredIntegrationSources.byRun = map[string]*requiredIntegrationSource{}
		}
		r.requiredIntegrationSources.byRun[current.RunID] = source
		return nil
	})
}

// The current goal session comes only from its canonical Goal row.
func pGoalSession(ctx context.Context, r *Runtime, p plan.ExecutionPlan) string {
	goal, err := r.store.GetGoalContract(ctx, p.Goal.GoalID, p.Goal.Revision)
	if err != nil {
		return ""
	}
	return goal.SessionID
}

func requiredCompletionVersions(ctx context.Context, engine *execution.Engine, root execution.ExecutionRun) (map[string]int64, error) {
	versions := map[string]int64{root.RunID: root.Version}
	for _, id := range root.TaskLanes {
		if id == root.RunID || versions[id] != 0 || len(versions) >= 256 {
			return nil, projectcontext.Changed("project_context_integration_source_extreme.go:150")
		}
		child, err := engine.GetRun(ctx, id)
		if err != nil {
			return nil, err
		}
		versions[id] = child.Version
	}
	return versions, nil
}

func (r *Runtime) resolveRequiredCompletionSource(ctx context.Context, root execution.ExecutionRun) (string, string, string, error) {
	if _, err := requiredArchitectureOwner(ctx, r); err != nil {
		return "", "", "", err
	}
	engine := r.Execution().Engine()
	versions, err := requiredCompletionVersions(ctx, engine, root)
	if err != nil {
		return "", "", "", err
	}
	release, err := admitRunControlLifecycle(r)
	if err != nil {
		return "", "", "", err
	}
	defer release()
	var source *requiredIntegrationSource
	err = engine.WithRequiredContextFence(ctx, versions, func(snapshots map[string]execution.ExecutionRun) error {
		var err error
		source, err = r.requiredCompletionSourceFenced(ctx, snapshots, root.RunID)
		return err
	})
	if err != nil {
		return "", "", "", err
	}
	return source.workspace, source.head, source.tree, nil
}

func (r *Runtime) requiredCompletionSourceFenced(ctx context.Context, snapshots map[string]execution.ExecutionRun, rootRunID string) (*requiredIntegrationSource, error) {
	if _, err := requiredArchitectureOwner(ctx, r); err != nil {
		return nil, err
	}
	root, tasks, err := r.Execution().Engine().ValidateRequiredContextFenceCompletion(ctx, snapshots, rootRunID)
	if err != nil {
		return nil, err
	}
	r.requiredIntegrationSources.mu.Lock()
	observed := r.requiredIntegrationSources.byRun[rootRunID]
	r.requiredIntegrationSources.mu.Unlock()
	if observed == nil || observed.runtime != r || observed.observationID == "" || !reflect.DeepEqual(observed.rootRun, root) || len(observed.versions) != len(snapshots) {
		return nil, fmt.Errorf("%w: integration source has no fresh observation in this runtime; /marshal resume to re-observe and rerun verification against the approved goal and plan", projectcontext.ErrReviewPending)
	}
	for id, row := range snapshots {
		if observed.versions[id] != row.Version {
			return nil, projectcontext.Changed("project_context_integration_source_extreme.go:203")
		}
	}
	copy := *observed
	if err = r.validateRequiredIntegrationSource(ctx, &copy, root, tasks); err != nil {
		return nil, err
	}
	if copy.head != observed.head || copy.tree != observed.tree || copy.commonDir != observed.commonDir {
		return nil, projectcontext.Changed("project_context_integration_source_extreme.go:211")
	}
	return &copy, nil
}

func (r *Runtime) validateRequiredIntegrationSource(ctx context.Context, source *requiredIntegrationSource, root execution.ExecutionRun, tasks map[string]execution.TaskExecution) error {
	stored, err := r.store.GetMarshalRun(ctx, r.ProjectID(), source.marshalRunID)
	if err != nil || stored.Revision != source.marshalRevision || !reflect.DeepEqual(stored.Value, source.marshalRun) || len(stored.Value.Tasks) != len(tasks) || !stored.Value.Process05Bound || stored.Value.State != marshal.Verifying || stored.Value.PlanID != root.PlanID || stored.Value.PlanVersion != root.PlanVersion {
		return projectcontext.Changed("project_context_integration_source_extreme.go:219")
	}
	p, err := r.store.GetActivePlan(ctx, root.ProjectID)
	if err != nil || p.ID != root.PlanID || p.Version != root.PlanVersion || p.State != plan.StateApproved {
		return projectcontext.Changed("project_context_integration_source_extreme.go:223")
	}
	if pinned, err := requiredApprovedPlanProjectContract(p); err != nil || pinned != source.contract {
		return projectcontext.Changed("project_context_integration_source_extreme.go:226")
	}
	accepted, err := r.store.ReadAcceptedProjectContract(ctx, r.ProjectContractBinding())
	if err != nil || source.contract != (projectcontext.ContractRef{ID: accepted.Contract.ID, Revision: accepted.Contract.Revision, Digest: accepted.Digest}) {
		return projectcontext.Changed("project_context_integration_source_extreme.go:230")
	}
	expectedPath := filepath.Join(r.layout.Worktrees, integrationTaskID(source.marshalRunID, source.marshalRun))
	resolved, err := filepath.EvalSymlinks(source.workspace)
	if err != nil || resolved != source.workspace || source.workspace != expectedPath {
		return projectcontext.Changed("project_context_integration_source_extreme.go:235")
	}
	head, err := runtimeProofGit(ctx, source.workspace, "rev-parse", "HEAD")
	if err != nil || strings.TrimSpace(head) != source.head {
		return projectcontext.Changed("project_context_integration_source_extreme.go:239")
	}
	// Ignored files are left out: an approved check that imports Python (or
	// builds anything) writes ignored artifacts such as __pycache__ while this
	// source is watched, and they are never part of the delivered tree.
	// Untracked interpreter or tool caches are skipped the same way (the
	// workspace digest skips them too). Tracked changes and other untracked
	// files still refuse the source.
	status, err := runtimeProofGit(ctx, source.workspace, "status", "--porcelain", "--untracked-files=all")
	if err != nil || len(hostgit.SourceStatusChanges(status)) != 0 {
		return projectcontext.Changed("project_context_integration_source_extreme.go:247")
	}
	common, err := gitMarshal(ctx, source.workspace, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	rootCommon, err := gitMarshal(ctx, r.ProjectRoot(), "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil || strings.TrimSpace(common) != strings.TrimSpace(rootCommon) {
		return projectcontext.Changed("project_context_integration_source_extreme.go:255")
	}
	seen := map[string]bool{}
	for _, task := range source.marshalRun.Tasks {
		actual, exists := tasks[task.PlanTaskID]
		if !exists || seen[task.PlanTaskID] || task.State != marshal.Merged || task.Mode != marshal.Governed || task.Worker != actual.AssignedHarness || !fullProofObject(task.ResultCommit) || !fullProofObject(actual.ResultCommit) {
			return projectcontext.Changed("project_context_integration_source_extreme.go:261")
		}
		seen[task.PlanTaskID] = true
		attempt := 1 + task.EvidenceAttemptBase
		attempt += taskReturns(task)
		handin, err := r.store.GetMarshalHandIn(ctx, source.marshalRunID, task.PlanTaskID, attempt)
		if err != nil || handin.Value.ResultCommit == "" || handin.Value.Provider != task.Worker || handin.Value.Mode != marshal.Governed {
			return projectcontext.Changed("project_context_integration_source_extreme.go:270")
		}
		anchors := 0
		for _, record := range handin.Value.RuntimeObserved {
			if strings.HasPrefix(record.Command, providerObservationRecordPrefix) {
				continue
			}
			if strings.HasPrefix(record.Command, "process05 ") && strings.HasPrefix(record.Output, "imported ") {
				if record.Command != "process05 "+actual.RunID || record.Output != "imported "+actual.ResultCommit || record.ExitCode != 0 {
					return projectcontext.Changed("project_context_integration_source_extreme.go:279")
				}
				anchors++
			}
		}
		if anchors != 1 {
			return projectcontext.Changed("project_context_integration_source_extreme.go:285")
		}
		for _, pair := range [][2]string{{actual.ResultCommit, handin.Value.ResultCommit}, {handin.Value.ResultCommit, task.ResultCommit}, {task.ResultCommit, source.head}} {
			if _, err := runtimeProofGit(ctx, source.workspace, "merge-base", "--is-ancestor", pair[0], pair[1]); err != nil {
				return projectcontext.Changed("project_context_integration_source_extreme.go:289")
			}
		}
	}
	tree, err := execution.WorkspaceTreeDigest(source.workspace)
	if err != nil {
		return err
	}
	source.tree, source.commonDir = tree, strings.TrimSpace(common)
	// Existing verifier provenance validation checks typed provider observations.
	// This source bridge conveys no model PASS or completion grant.
	return ctx.Err()
}

//go:build !governed

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/memory/security"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/store"
)

// Only trusted application composition can install this private observer.
// It must reread current execution provenance, task lane and authorization
// generation. Its output is observation data, never an execution grant.
type requiredProjectWorkspaceKey struct{}
type requiredProjectWorkspaceProof struct {
	Runtime            *Runtime
	Read               func(context.Context, projectcontext.Binding) (requiredProjectWorkspaceState, error)
	SQLAuthority       func(context.Context, projectcontext.Binding) (store.ProjectContextAuthorityObservation, error)
	OwnerPlanning      bool
	ArchitectureReview *requiredArchitectureTurnTarget
}
type requiredProjectWorkspaceState struct{ Workspace, Base, Head, Tree, AuthorityDigest, SQLAuthorityDigest string }
type preparedRequiredProjectContext struct {
	Snapshot              store.ProjectContextSnapshot
	Packet                projectcontext.Packet
	Admission             store.ProjectContextAdmission
	Workspace, Head, Tree string
	TaskRevision          int64
	AuthorityDigest       string
}

// prepareRequiredProjectContext observes and renders only. The caller MUST
// perform the durable transactional admission and current authority/run fence
// immediately before dispatch; this method cannot linearize external engine
// state, mint a one-shot token, qualify completion or invoke a provider.
func (r *Runtime) prepareRequiredProjectContext(ctx context.Context, principal authz.Principal, b projectcontext.Binding, selection projectcontext.Selection, prefix string, maxBytes int) (preparedRequiredProjectContext, error) {
	var zero preparedRequiredProjectContext
	proof, ok := ctx.Value(requiredProjectWorkspaceKey{}).(requiredProjectWorkspaceProof)
	if r == nil || r.store == nil || r.memoryService == nil || !ok || proof.Runtime != r || proof.Read == nil || principal.ID == "" || principal.ID != b.Principal || b.Project != r.ProjectContractBinding() || b.FormatVersion != projectcontext.FormatVersion || b.Session == "" {
		return zero, authz.ErrDenied
	}
	if maxBytes < 1 || maxBytes > projectcontext.MaximumPacketBytes {
		return zero, projectcontext.ErrBudget
	}
	before, err := proof.Read(ctx, b)
	if err != nil {
		return zero, err
	}
	if err = requiredProjectWorkspaceCurrent(ctx, b, before); err != nil {
		return zero, err
	}
	goal, task, canonical, err := r.requiredProjectCanonicalInputs(ctx, b)
	if err != nil {
		return zero, err
	}
	audience := store.ProjectContextAudience{Actor: principal.ID, Worker: true, ScopeIDs: []string{b.Project.StorageID, principal.ID}}
	if b.TaskID != "" {
		audience.ScopeIDs = append(audience.ScopeIDs, b.TaskID, b.CanonicalTaskID)
	}
	now := time.Now().UTC()
	var snapshot store.ProjectContextSnapshot
	if proof.OwnerPlanning {
		if b.TaskID != "" || b.Provider != "" || proof.SQLAuthority == nil {
			return zero, authz.ErrDenied
		}
		if err = r.authorizeProjectContractInspection(ctx, principal); err != nil {
			return zero, err
		}
		audience, err = r.projectContractOwnerAudience(ctx, principal.ID)
		if err != nil {
			return zero, err
		}
		planning, readErr := r.store.ReadProjectPlanningContext(ctx, store.ProjectPlanningContextRequest{Binding: b, Audience: audience, Prefix: prefix, ByteCap: maxBytes, Now: now})
		if readErr != nil {
			return zero, readErr
		}
		if !reflect.DeepEqual(selection, planning.Selection) {
			return zero, projectcontext.Changed("project_context_resolver_extreme.go:96")
		}
		snapshot = planning.Snapshot
	} else {
		snapshot, err = r.store.ReadProjectContextSnapshotBound(ctx, b.Project, audience, selection, now, b.Contract)
	}
	if err != nil {
		return zero, err
	}
	if b.TaskID == "" && !proof.OwnerPlanning {
		if !reflect.DeepEqual(requiredProjectNormalizeSelection(selection), projectcontext.Selection{Components: []string{}, Paths: []string{}}) {
			return zero, projectcontext.Changed("project_context_resolver_extreme.go:107")
		}
		for _, ref := range snapshot.Contract.Required {
			if ref.Mandatory() && len(ref.Applies.TaskIDs) > 0 {
				return zero, projectcontext.ErrReviewPending
			}
		}
	} else if b.TaskID != "" {
		p, err := r.store.GetActivePlan(ctx, projectid.ID(b.Project.CanonicalID))
		if err != nil {
			return zero, err
		}
		expected, err := requiredProjectPlanSelection(p, b, snapshot.Contract)
		if err != nil || !reflect.DeepEqual(expected, requiredProjectNormalizeSelection(selection)) {
			return zero, projectcontext.Changed("project_context_resolver_extreme.go:121")
		}
	}
	if b.RequiredStateDigest != "" && b.RequiredStateDigest != snapshot.StateDigest {
		return zero, projectcontext.Changed("project_context_resolver_extreme.go:125")
	}
	b.RequiredStateDigest = snapshot.StateDigest
	b.InventoryDigest = snapshot.InventoryDigest
	grounding := &requiredProjectGroundingBudget{Hashes: map[string]string{}}
	for _, rec := range snapshot.Records {
		inputs, err := requiredProjectFreshnessInputsBounded(before.Workspace, before.Head, rec, grounding)
		if err != nil {
			return zero, err
		}
		if got := r.memoryService.EvaluateFreshness(rec, inputs); got.Classification != FreshnessFresh {
			return zero, fmt.Errorf("%w: required memory freshness %s", projectcontext.Changed("project_context_resolver_extreme.go:136"), got.Classification)
		}
	}
	packet, err := projectcontext.RenderWithPrefix(projectcontext.Manifest{Contract: snapshot.Contract, Goal: projectcontext.GoalFromCanonical(goal), Binding: b, Selection: selection, Records: snapshot.Records}, prefix, maxBytes)
	if err != nil {
		return zero, err
	}
	if err = security.NewFirewall(security.FirewallConfig{}).ScanText(packet.Text); err != nil {
		return zero, err
	}
	after, err := proof.Read(ctx, b)
	if err != nil {
		return zero, err
	}
	if before != after {
		return zero, projectcontext.Changed("project_context_resolver_extreme.go:151")
	}
	_, currentTask, currentCanonical, err := r.requiredProjectCanonicalInputs(ctx, b)
	if err != nil {
		return zero, err
	}
	if canonical != currentCanonical || task.Revision != currentTask.Revision {
		return zero, projectcontext.Changed("project_context_resolver_extreme.go:158")
	}
	if err = requiredProjectWorkspaceCurrent(ctx, b, after); err != nil {
		return zero, err
	}
	b.ManifestDigest = packet.ManifestDigest
	var authority *store.ProjectContextAuthorityObservation
	if proof.SQLAuthority != nil {
		observed, observeErr := proof.SQLAuthority(ctx, b)
		if observeErr != nil {
			return zero, observeErr
		}
		if observed.Digest == "" || observed.Digest != after.SQLAuthorityDigest {
			return zero, projectcontext.Changed("project_context_resolver_extreme.go:171")
		}
		authority = &observed
	} else if after.SQLAuthorityDigest != "" {
		return zero, projectcontext.Changed("project_context_resolver_extreme.go:175")
	}
	stage := "admission"
	if b.TaskID == "" {
		stage = "planning"
	}
	return preparedRequiredProjectContext{Snapshot: snapshot, Packet: packet, Admission: store.ProjectContextAdmission{Stage: stage, Audience: audience, Selection: selection, Binding: b, ExpectedStateDigest: snapshot.StateDigest, ExpectedPacketDigest: packet.Digest, ByteCap: maxBytes, Prefix: prefix, Now: time.Now().UTC(), ExpectedAuthority: authority}, Workspace: after.Workspace, Head: after.Head, Tree: after.Tree, TaskRevision: task.Revision, AuthorityDigest: after.AuthorityDigest}, nil
}

func requiredProjectWorkspaceCurrent(ctx context.Context, b projectcontext.Binding, s requiredProjectWorkspaceState) error {
	if !filepath.IsAbs(s.Workspace) || filepath.Clean(s.Workspace) != s.Workspace || s.AuthorityDigest == "" || !fullProofObject(s.Base) || !fullProofObject(s.Head) || s.Tree == "" || s.Base != b.Base || s.Tree != b.Tree {
		return projectcontext.Changed("project_context_resolver_extreme.go:186")
	}
	resolved, err := filepath.EvalSymlinks(s.Workspace)
	if err != nil || resolved != s.Workspace {
		return projectcontext.Changed("project_context_resolver_extreme.go:190")
	}
	head, err := runtimeProofGit(ctx, s.Workspace, "rev-parse", "HEAD")
	if err != nil || strings.TrimSpace(head) != s.Head {
		return projectcontext.Changed("project_context_resolver_extreme.go:194")
	}
	tree, err := execution.WorkspaceTreeDigest(s.Workspace)
	if err != nil || tree != s.Tree {
		return projectcontext.Changed("project_context_resolver_extreme.go:198")
	}
	return nil
}

func (r *Runtime) requiredProjectCanonicalInputs(ctx context.Context, b projectcontext.Binding) (model.GoalContract, model.Task, string, error) {
	var task model.Task
	goal, err := r.store.GetActiveGoalContract(ctx, b.Session)
	if err != nil {
		return goal, task, "", err
	}
	if goal.ID != b.GoalID || goal.Revision != b.GoalRevision || (goal.ProjectID != b.Project.StorageID && goal.ProjectID != b.Project.CanonicalID) || goal.SessionID != b.Session {
		return goal, task, "", projectcontext.Changed("project_context_resolver_extreme.go:210")
	}
	if b.TaskID == "" {
		if b.CanonicalTaskID != "" || b.PlanID != "" || b.PlanVersion != 0 {
			proof, ok := ctx.Value(requiredProjectWorkspaceKey{}).(requiredProjectWorkspaceProof)
			if !ok || proof.Runtime != r || !proof.OwnerPlanning || proof.SQLAuthority == nil || !proof.ArchitectureReview.matches(r, b) {
				return goal, task, "", projectcontext.Changed("project_context_resolver_extreme.go:216")
			}
			p, err := r.store.GetActivePlan(ctx, projectid.ID(b.Project.CanonicalID))
			if err != nil || p.ID != b.PlanID || p.Version != b.PlanVersion || p.State != plan.StateApproved || p.Goal.GoalID != b.GoalID || p.Goal.Revision != b.GoalRevision || p.ApprovedScope == nil || p.ApprovalScopeDigest != b.ApprovalScopeDigest || p.ApprovedScope.Digest() != b.ApprovalScopeDigest {
				return goal, task, "", projectcontext.Changed("project_context_resolver_extreme.go:220")
			}
			return goal, task, projectcontext.Digest("marshal.required.canonical.v1", struct {
				Goal model.GoalContract
				Plan plan.ExecutionPlan
			}{goal, p}), nil
		}
		return goal, task, projectcontext.Digest("marshal.required.canonical.v1", goal), nil
	}
	if b.CanonicalTaskID != canonicalPlanTaskID(b.PlanID, b.PlanVersion, b.TaskID) {
		return goal, task, "", projectcontext.Changed("project_context_resolver_extreme.go:230")
	}
	task, err = r.store.ReadProjectContextTask(ctx, b.Project, b.CanonicalTaskID)
	if err != nil {
		return goal, task, "", err
	}
	binding, err := r.store.TaskExecutionBinding(ctx, b.CanonicalTaskID)
	if err != nil {
		return goal, task, "", err
	}
	if binding != (store.TaskBinding{SessionID: b.Session, GoalID: b.GoalID, GoalRevision: b.GoalRevision, PlanID: b.PlanID, PlanVersion: b.PlanVersion}) {
		return goal, task, "", projectcontext.Changed("project_context_resolver_extreme.go:241")
	}
	agent, err := r.store.GetAgent(ctx, b.Principal)
	if err != nil {
		return goal, task, "", err
	}
	if agent.ProjectID != b.Project.StorageID || agent.Status == model.AgentDisabled || agent.ModelProvider != b.Provider {
		return goal, task, "", projectcontext.Changed("project_context_resolver_extreme.go:248")
	}
	p, err := r.store.GetActivePlan(ctx, projectid.ID(b.Project.CanonicalID))
	if err != nil {
		return goal, task, "", err
	}
	if p.ID != b.PlanID || p.Version != b.PlanVersion || p.State != plan.StateApproved || p.Goal.GoalID != b.GoalID || p.Goal.Revision != b.GoalRevision || p.ApprovalScopeDigest != b.ApprovalScopeDigest || p.ApprovedScope == nil || p.ApprovedScope.Digest() != b.ApprovalScopeDigest {
		return goal, task, "", projectcontext.Changed("project_context_resolver_extreme.go:255")
	}
	return goal, task, projectcontext.Digest("marshal.required.canonical.v1", struct {
		Goal  model.GoalContract
		Task  model.Task
		Agent model.Agent
		Plan  plan.ExecutionPlan
	}{goal, task, agent, p}), nil
}

func requiredProjectNormalizeSelection(s projectcontext.Selection) projectcontext.Selection {
	normalize := func(values []string) []string {
		set := map[string]bool{}
		for _, v := range values {
			set[v] = true
		}
		out := []string{}
		for v := range set {
			out = append(out, v)
		}
		sort.Strings(out)
		return out
	}
	s.Paths = normalize(s.Paths)
	s.Components = normalize(s.Components)
	return s
}
func requiredProjectPlanSelection(p plan.ExecutionPlan, b projectcontext.Binding, c projectcontext.Contract) (projectcontext.Selection, error) {
	for _, task := range p.Tasks {
		if task.ID != b.TaskID {
			continue
		}
		s := projectcontext.Selection{TaskID: b.TaskID, CanonicalTaskID: b.CanonicalTaskID, Paths: append([]string(nil), task.Paths...), Components: []string{}}
		if policy, ok := p.Policy.PolicyFor(task.ID); ok {
			s.Paths = append(s.Paths, policy.AllowedScope...)
		}
		for _, component := range c.Components {
			if len(s.Paths) == 0 || len(component.Paths) == 0 || (projectcontext.Applicability{Paths: component.Paths}).Matches(s) {
				s.Components = append(s.Components, component.ID)
			}
		}
		return requiredProjectNormalizeSelection(s), nil
	}
	return projectcontext.Selection{}, projectcontext.Changed("project_context_resolver_extreme.go:298")
}

// Resolve grounding solely in the observed authoritative worktree. Unknown
// symbol/test support cannot qualify required context; a later canonical
// evidence observer can add it explicitly. Reads are bounded and never follow
// escaped symlinks. Git/provider/network commands are not used to infer facts.
func requiredProjectFreshnessInputs(root, head string, rec model.MemoryRecordV2) (MemoryReconcileRequest, error) {
	return requiredProjectFreshnessInputsBounded(root, head, rec, &requiredProjectGroundingBudget{Hashes: map[string]string{}})
}

type requiredProjectGroundingBudget struct {
	Bytes  int64
	Hashes map[string]string
}

func requiredProjectFreshnessInputsBounded(root, head string, rec model.MemoryRecordV2, budget *requiredProjectGroundingBudget) (MemoryReconcileRequest, error) {
	req := MemoryReconcileRequest{ProjectID: rec.ProjectID, CurrentHead: head, CanonicalHead: head, CurrentFileHashes: map[string]string{}}
	if rec.WorktreeID != "" || rec.BranchName != "" && rec.HeadCommit != head {
		return req, projectcontext.Changed("project_context_resolver_extreme.go:317")
	}
	rootHandle, err := os.OpenRoot(root)
	if err != nil {
		return req, projectcontext.Changed("project_context_resolver_extreme.go:321")
	}
	defer rootHandle.Close()
	paths := map[string]bool{}
	if rec.ExtMeta != nil {
		raw, err := json.Marshal(rec.ExtMeta)
		if err != nil || len(raw) > 64<<10 {
			return req, projectcontext.ErrBudget
		}
		var metadata struct {
			FilePath        string            `json:"file_path"`
			ReferencedFiles []string          `json:"referenced_files"`
			FileHashes      map[string]string `json:"file_hashes"`
			Symbols         []string          `json:"symbols"`
			VerifiedTests   []string          `json:"verified_tests"`
		}
		if json.Unmarshal(raw, &metadata) != nil || len(metadata.Symbols) > 0 || len(metadata.VerifiedTests) > 0 {
			return req, projectcontext.Changed("project_context_resolver_extreme.go:338")
		}
		if metadata.FilePath != "" {
			paths[metadata.FilePath] = true
		}
		for _, p := range metadata.ReferencedFiles {
			paths[p] = true
		}
		for p := range metadata.FileHashes {
			paths[p] = true
		}
		// A commit ID alone does not ground changed working-tree file bytes.
		// Unknown file support must not pass the evaluator's skipped checks.
		for path := range paths {
			if metadata.FileHashes[path] == "" {
				return req, projectcontext.Changed("project_context_resolver_extreme.go:353")
			}
		}
	}
	if len(paths) > 256 {
		return req, projectcontext.ErrBudget
	}
	for relative := range paths {
		if relative == "" || filepath.IsAbs(relative) || filepath.Clean(relative) != relative || relative == ".." || strings.HasPrefix(relative, "../") || strings.Contains(relative, "\\") {
			return req, projectcontext.Changed("project_context_resolver_extreme.go:362")
		}
		if hash, ok := budget.Hashes[relative]; ok {
			req.CurrentFileHashes[relative] = hash
			continue
		}
		if len(budget.Hashes) >= 256 {
			return req, projectcontext.ErrBudget
		}
		file := filepath.Join(root, relative)
		resolved, err := filepath.EvalSymlinks(file)
		if err != nil || resolved != file {
			return req, projectcontext.Changed("project_context_resolver_extreme.go:374")
		}
		// OpenRoot enforces confinement even if a symlink changes after the
		// inspection above; read-then-check alone would leak an escaped file.
		f, err := rootHandle.Open(relative)
		if err != nil {
			return req, projectcontext.Changed("project_context_resolver_extreme.go:380")
		}
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 8<<20 {
			f.Close()
			return req, projectcontext.ErrBudget
		}
		hash := sha256.New()
		n, err := io.Copy(hash, io.LimitReader(f, (8<<20)+1))
		closeErr := f.Close()
		budget.Bytes += n
		if err != nil || closeErr != nil || n > 8<<20 || budget.Bytes > 32<<20 {
			return req, projectcontext.ErrBudget
		}
		req.CurrentFileHashes[relative] = hex.EncodeToString(hash.Sum(nil))
		budget.Hashes[relative] = req.CurrentFileHashes[relative]
	}
	return req, nil
}

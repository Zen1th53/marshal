//go:build !governed

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/store"
)

func TestRequiredContextRefusesUnsealedWorkspaceAuthority(t *testing.T) {
	r := openNetpolRuntime(t)
	b := projectcontext.Binding{Project: r.ProjectContractBinding(), Principal: "owner", Session: "session"}
	p := authz.Principal{ID: "owner"}
	for _, proof := range []any{nil, requiredProjectWorkspaceProof{Runtime: r}, requiredProjectWorkspaceProof{Runtime: &Runtime{}, Read: func(context.Context, projectcontext.Binding) (requiredProjectWorkspaceState, error) {
		t.Fatal("foreign proof invoked")
		return requiredProjectWorkspaceState{}, nil
	}}} {
		ctx := context.WithValue(t.Context(), requiredProjectWorkspaceKey{}, proof)
		if _, err := r.prepareRequiredProjectContext(ctx, p, b, projectcontext.Selection{}, "", 16384); err == nil {
			t.Fatal("unsealed workspace accepted")
		}
	}
}

// Journal fixture uses the real separate proposal/Decision transaction. The
// private observer below is a host test observer, not a native authority proof.
func requiredContextFixture(t *testing.T, taskOnly ...bool) (*Runtime, projectcontext.Binding, model.MemoryRecordV2, requiredProjectWorkspaceState) {
	t.Helper()
	r := openNetpolRuntime(t)
	return requiredContextFixtureForRuntime(t, r, taskOnly...)
}

func requiredContextFixtureForRuntime(t *testing.T, r *Runtime, taskOnly ...bool) (*Runtime, projectcontext.Binding, model.MemoryRecordV2, requiredProjectWorkspaceState) {
	t.Helper()
	ctx := t.Context()
	goal := planGoal()
	goal.ProjectID = r.ProjectIdentity()
	goal.SessionID = "SESSION-required"
	if err := r.store.SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	c, rec := requiredAcceptedContextFixture(t, r, taskOnly...)
	head, err := runtimeProofGit(ctx, r.ProjectRoot(), "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	tree, err := execution.WorkspaceTreeDigest(r.ProjectRoot())
	if err != nil {
		t.Fatal(err)
	}
	state := requiredProjectWorkspaceState{Workspace: r.ProjectRoot(), Base: strings.TrimSpace(head), Head: strings.TrimSpace(head), Tree: tree, AuthorityDigest: "checked-root-owner-generation-1"}
	digest, _ := c.CanonicalDigest()
	b := projectcontext.Binding{FormatVersion: 1, Project: c.Project, Contract: projectcontext.ContractRef{ID: c.ID, Revision: c.Revision, Digest: digest}, GoalID: goal.ID, GoalRevision: goal.Revision, Session: goal.SessionID, Principal: "owner", Base: state.Base, Tree: state.Tree}
	return r, b, rec, state
}

// Separate protected proposal/Decision acceptance for an existing owned test
// Runtime. This invokes the same durable acceptance APIs as the original
// resolver fixture; it does not fabricate a context token or approved pin.
func requiredAcceptedContextFixture(t *testing.T, r *Runtime, taskOnly ...bool) (projectcontext.Contract, model.MemoryRecordV2) {
	t.Helper()
	ctx := t.Context()
	now := time.Now().UTC()
	if err := r.store.InitMCPProposals(ctx); err != nil {
		t.Fatal(err)
	}
	rec := model.MemoryRecordV2{ID: "MEM-required-resolver", ProjectID: r.ProjectID(), Revision: 1, Kind: model.MemoryKindDecision, Lifecycle: model.MemoryDurable, Authority: model.AuthorityOperator, Confidence: model.ConfidenceVerified, Title: "Preserve accepted history", Body: "Preserve original accepted history and evidence.", Scope: "project", ScopeID: r.ProjectID(), ObservedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now, Source: model.MemorySource{Kind: "runtime_service", Reference: "owner"}}
	rec.ContentDigest = rec.CanonicalDigest()
	if err := r.store.WriteMemoryV2(ctx, rec); err != nil {
		t.Fatal(err)
	}
	audience := store.ProjectContextAudience{Actor: "owner", ScopeIDs: []string{r.ProjectID()}}
	_, inventory, err := r.store.ProjectContractInventory(ctx, r.ProjectContractBinding(), audience)
	if err != nil {
		t.Fatal(err)
	}
	c := projectcontext.Contract{FormatVersion: 1, Project: r.ProjectContractBinding(), ID: "CONTRACT-required", Revision: 1, Purpose: "Preserve reliable project execution", Components: []projectcontext.Component{{ID: "core", Responsibility: "Canonical application", Paths: []string{"internal"}}}, Invariants: []string{"Preserve original audit history"}, ReviewedInventoryDigest: inventory, Required: []projectcontext.MemoryRef{{ID: rec.ID, ProjectID: rec.ProjectID, Scope: rec.Scope, ScopeID: rec.ScopeID, Revision: rec.Revision, ContentDigest: rec.ContentDigest, StateFingerprint: projectcontext.MemoryFingerprint(rec), Class: projectcontext.Policy, Applies: projectcontext.Applicability{ProjectWide: true}, Reason: "Explicit reviewed preservation policy", AcceptanceOccurrence: "owner-policy"}}}
	if len(taskOnly) > 0 && taskOnly[0] {
		c.Required[0].Applies = projectcontext.Applicability{TaskIDs: []string{"fix"}}
	}
	source, _, err := r.store.AdmitMCPProjectContract(ctx, store.MCPProposal{ID: "mcp:resolver-contract", Launch: "resolver-launch", Owner: "owner", Provider: "codex", Submission: "resolver-contract"}, store.ProjectContractSubmission{Action: "project-contract", Contract: c})
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := c.CanonicalDigest()
	binding, _ := json.Marshal(store.ProjectContractApprovalBinding{ProposalDigest: source.Digest, ContractDigest: digest, InventoryDigest: inventory})
	req := permission.Request{Kind: "project-contract", Object: c.ID, Scope: r.ProjectID(), Who: "Marshal", Reason: "Review exact required context", ProposalID: source.ID, ApprovalBinding: string(binding), SourceLaunch: source.Launch, SourceOccurrence: "mcp:resolver-decision"}
	raw, _ := json.Marshal(map[string]any{"action": "decide", "id": "QUESTION-resolver", "allow": true, "reason": "Reviewed original exact contract"})
	_, _, err = r.store.AdmitMCPProposal(ctx, r.ProjectID(), store.MCPProposal{ID: req.SourceOccurrence, Launch: source.Launch, Owner: source.Owner, Provider: source.Provider, Submission: "resolver-decision", Payload: string(raw)})
	if err != nil {
		t.Fatal(err)
	}
	request, _ := json.Marshal(req)
	if err = r.store.AcceptMCPDecision(ctx, r.ProjectID(), req.SourceOccurrence, string(request), req.Key()); err != nil {
		t.Fatal(err)
	}
	if err = r.store.SetMCPProposalState(ctx, r.ProjectID(), req.SourceOccurrence, "applying", "protected fixture effect"); err != nil {
		t.Fatal(err)
	}
	c, err = r.store.CommitMCPProjectContractDecision(ctx, req, true, audience, now, model.Event{ID: "resolver-accepted", ProjectID: r.ProjectID(), Type: "PERMISSION_DECIDED", Timestamp: now, Data: map[string]any{"source_occurrence": req.SourceOccurrence}})
	if err != nil {
		t.Fatal(err)
	}
	return c, rec
}

func TestRequiredContextPlanningNeverOmitsTaskOnlyRequiredPolicy(t *testing.T) {
	r, b, _, state := requiredContextFixture(t, true)
	ctx := context.WithValue(t.Context(), requiredProjectWorkspaceKey{}, requiredProjectWorkspaceProof{Runtime: r, Read: func(context.Context, projectcontext.Binding) (requiredProjectWorkspaceState, error) {
		return state, nil
	}})
	if _, err := r.prepareRequiredProjectContext(ctx, authz.Principal{ID: b.Principal}, b, projectcontext.Selection{}, "", 16384); err != projectcontext.ErrReviewPending {
		t.Fatal("planning silently omitted task-only accepted policy", err)
	}
}

func TestRequiredContextSealedObserverMustMatchActualRepositoryAndBinding(t *testing.T) {
	r, b, _, state := requiredContextFixture(t)
	for _, scenario := range []string{"missing authority", "wrong base", "wrong head", "wrong tree", "relative workspace", "wrong goal", "wrong principal", "oversized cap", "planning task scope"} {
		t.Run(scenario, func(t *testing.T) {
			candidate := state
			binding := b
			selection := projectcontext.Selection{}
			capBytes := 16384
			switch scenario {
			case "missing authority":
				candidate.AuthorityDigest = ""
			case "wrong base":
				candidate.Base = strings.Repeat("1", 40)
			case "wrong head":
				candidate.Head = strings.Repeat("2", 40)
			case "wrong tree":
				candidate.Tree = "invented-tree"
			case "relative workspace":
				candidate.Workspace = "relative"
			case "wrong goal":
				binding.GoalID = "foreign-goal"
			case "wrong principal":
				binding.Principal = "other"
			case "oversized cap":
				capBytes = projectcontext.MaximumPacketBytes + 1
			case "planning task scope":
				selection.Paths = []string{"internal"}
			}
			ctx := context.WithValue(t.Context(), requiredProjectWorkspaceKey{}, requiredProjectWorkspaceProof{Runtime: r, Read: func(context.Context, projectcontext.Binding) (requiredProjectWorkspaceState, error) {
				return candidate, nil
			}})
			if _, err := r.prepareRequiredProjectContext(ctx, authz.Principal{ID: "owner"}, binding, selection, "", capBytes); err == nil {
				t.Fatalf("%s observer qualified", scenario)
			}
		})
	}
}

func TestRequiredContextActualMaterializedProcess05TaskNeedsNoCanonicalClaim(t *testing.T) {
	r, b, _, state := requiredContextFixture(t)
	ctx := t.Context()
	goal, err := r.store.GetActiveGoalContract(ctx, b.Session)
	if err != nil {
		t.Fatal(err)
	}
	request := planCreateRequest()
	request.SessionID = b.Session
	request.ProjectID = projectid.ID(r.ProjectIdentity())
	if _, err = r.Plans().Create(ctx, request); err != nil {
		t.Fatal(err)
	}
	p, err := r.Plans().Approve(ctx, request.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	handoff, err := plan.PrepareHandoff(p, goal, p.ProjectID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Execution().materializeCanonicalTasks(ctx, &handoff, goal.Risk, b.Session); err != nil {
		t.Fatal(err)
	}
	id := "AGENT-required-codex"
	if err = r.store.RegisterAgent(ctx, model.Agent{ID: id, ProjectID: r.ProjectID(), DisplayName: "Required Codex", Role: model.RoleDeveloper, ModelProvider: "codex", ModelName: "configured-selector", Status: model.AgentRegistered, Revision: 1}); err != nil {
		t.Fatal(err)
	}
	actual, err := r.process05CodexAgent(ctx, execution.TaskExecution{AssignedAgent: id, AssignedRole: "developer", AssignedModel: "resolved-model"})
	if err != nil || actual != id {
		t.Fatal(actual, err)
	}
	b.Principal = id
	b.Provider = "codex"
	b.Model = "resolved-model"
	b.TaskID = p.Tasks[0].ID
	b.CanonicalTaskID = handoff.CanonicalTaskIDs[b.TaskID]
	b.PlanID = p.ID
	b.PlanVersion = p.Version
	b.ApprovalScopeDigest = p.ApprovalScopeDigest
	task, err := r.store.ReadProjectContextTask(ctx, b.Project, b.CanonicalTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.OwnerAgentID != nil || task.Worktree != nil {
		t.Fatal("fixture accidentally fabricated a Runtime claim", task)
	}
	contract, err := r.store.ReadAcceptedProjectContract(ctx, b.Project)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := requiredProjectPlanSelection(p, b, contract.Contract)
	if err != nil {
		t.Fatal(err)
	}
	sealed := context.WithValue(ctx, requiredProjectWorkspaceKey{}, requiredProjectWorkspaceProof{Runtime: r, Read: func(context.Context, projectcontext.Binding) (requiredProjectWorkspaceState, error) {
		return state, nil
	}})
	prepared, err := r.prepareRequiredProjectContext(sealed, authz.Principal{ID: id}, b, selection, "full-provider-prefix\n", 16384)
	if err != nil {
		t.Fatal(err)
	}
	prepared.Admission.ID = "actual-materialized-task-context"
	if _, err = r.store.CommitProjectContextAdmission(ctx, prepared.Admission); err != nil {
		t.Fatal(err)
	}
	selection.Paths = []string{}
	if _, err = r.prepareRequiredProjectContext(sealed, authz.Principal{ID: id}, b, selection, "", 16384); err == nil {
		t.Fatal("caller removed canonical task scope")
	}
	selection, err = requiredProjectPlanSelection(p, b, contract.Contract)
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"wrong canonical task", "wrong provider", "unknown agent", "stale plan", "wrong approval scope"} {
		t.Run(scenario, func(t *testing.T) {
			binding := b
			switch scenario {
			case "wrong canonical task":
				binding.CanonicalTaskID = "TASK-foreign"
			case "wrong provider":
				binding.Provider = "claude"
			case "unknown agent":
				binding.Principal = "AGENT-foreign"
			case "stale plan":
				binding.PlanVersion++
			case "wrong approval scope":
				binding.ApprovalScopeDigest = "foreign-scope"
			}
			if _, err := r.prepareRequiredProjectContext(sealed, authz.Principal{ID: binding.Principal}, binding, selection, "", 16384); err == nil {
				t.Fatalf("%s canonical task qualified", scenario)
			}
		})
	}
}

func TestRequiredContextPlanningCanonicalPacketAndMutationBeforeAdmission(t *testing.T) {
	r, b, rec, state := requiredContextFixture(t)
	calls := 0
	proof := requiredProjectWorkspaceProof{Runtime: r, Read: func(context.Context, projectcontext.Binding) (requiredProjectWorkspaceState, error) {
		calls++
		return state, nil
	}}
	ctx := context.WithValue(t.Context(), requiredProjectWorkspaceKey{}, proof)
	prepared, err := r.prepareRequiredProjectContext(ctx, authz.Principal{ID: b.Principal}, b, projectcontext.Selection{}, "provider-prefix\n", 16384)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || prepared.Packet.Bytes != len(prepared.Packet.Text) || len(prepared.Packet.Loaded) != 1 || prepared.Admission.Stage != "planning" || prepared.Admission.ExpectedStateDigest == "" || !strings.Contains(prepared.Packet.Text, "The documented typo is fixed") || !strings.Contains(prepared.Packet.Text, rec.Body) {
		t.Fatalf("incomplete canonical packet %+v calls=%d", prepared, calls)
	}
	prepared.Admission.ID = "resolver-planning"
	if _, err = r.store.CommitProjectContextAdmission(ctx, prepared.Admission); err != nil {
		t.Fatal(err)
	}
	if _, err = r.prepareRequiredProjectContext(ctx, authz.Principal{ID: b.Principal}, b, projectcontext.Selection{}, "provider-prefix\n", prepared.Packet.Bytes-1); err == nil {
		t.Fatal("overflow omitted mandatory content")
	}
	if _, err = r.store.UpdateMemory(ctx, rec.ProjectID, rec.ID, rec.Revision, func(current *model.MemoryRecordV2) error {
		current.ACLScope = "other"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	prepared.Admission.ID = "resolver-stale-before-admission"
	if _, err = r.store.CommitProjectContextAdmission(ctx, prepared.Admission); err == nil {
		t.Fatal("prepared packet admitted after applicable ACL revoke")
	}
}

func TestRequiredContextRefusesAuthorityOrGoalMutationDuringPreparation(t *testing.T) {
	for _, mutation := range []string{"authority", "goal", "worktree"} {
		t.Run(mutation, func(t *testing.T) {
			r, b, _, state := requiredContextFixture(t)
			calls := 0
			ctx := context.WithValue(t.Context(), requiredProjectWorkspaceKey{}, requiredProjectWorkspaceProof{Runtime: r, Read: func(context.Context, projectcontext.Binding) (requiredProjectWorkspaceState, error) {
				calls++
				if calls == 2 {
					switch mutation {
					case "authority":
						state.AuthorityDigest = "revoked-generation-2"
					case "goal":
						goal, err := r.store.GetActiveGoalContract(t.Context(), b.Session)
						if err != nil {
							t.Fatal(err)
						}
						goal.DesiredOutcome = "A changed goal"
						if err = r.store.SaveGoalContract(t.Context(), goal, goal.Revision); err != nil {
							t.Fatal(err)
						}
					case "worktree":
						if err := os.WriteFile(filepath.Join(state.Workspace, "result.txt"), []byte("changed"), 0600); err != nil {
							t.Fatal(err)
						}
					}
				}
				return state, nil
			}})
			if _, err := r.prepareRequiredProjectContext(ctx, authz.Principal{ID: b.Principal}, b, projectcontext.Selection{}, "", 16384); err == nil {
				t.Fatalf("%s mutation qualified", mutation)
			}
		})
	}
}

func TestRequiredContextFreshnessReadsExactWorkspaceHashesAndRefusesUnknownAnchors(t *testing.T) {
	root := t.TempDir()
	content := []byte("canonical result")
	if err := os.WriteFile(filepath.Join(root, "result.txt"), content, 0600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(content)
	rec := model.MemoryRecordV2{ID: "required", Lifecycle: model.MemoryDurable, ExtMeta: map[string]any{"file_hashes": map[string]string{"result.txt": hex.EncodeToString(hash[:])}}}
	req, err := requiredProjectFreshnessInputs(root, "head", rec)
	if err != nil || req.CurrentFileHashes["result.txt"] != hex.EncodeToString(hash[:]) {
		t.Fatal(req, err)
	}
	s := &MemoryService{}
	if got := s.EvaluateFreshness(rec, req); got.Classification != FreshnessFresh {
		t.Fatal(got)
	}
	if err := os.WriteFile(filepath.Join(root, "result.txt"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	req, err = requiredProjectFreshnessInputs(root, "head", rec)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.EvaluateFreshness(rec, req); got.Classification != FreshnessStale {
		t.Fatal("changed required content qualified", got)
	}
	for _, meta := range []map[string]any{
		{"file_hashes": map[string]string{"../outside": "hash"}},
		{"file_path": "missing.txt"},
		{"symbols": []string{"unknown.Symbol"}},
		{"verified_tests": []string{"unknown_test"}},
		{"file_hashes": "malformed"},
	} {
		rec.ExtMeta = meta
		if _, err := requiredProjectFreshnessInputs(root, "head", rec); err == nil {
			t.Fatalf("unverifiable anchor skipped: %+v", meta)
		}
	}
	outside := filepath.Join(t.TempDir(), "private.txt")
	if err := os.WriteFile(outside, content, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked.txt")); err != nil {
		t.Fatal(err)
	}
	rec.ExtMeta = map[string]any{"file_hashes": map[string]string{"linked.txt": hex.EncodeToString(hash[:])}}
	if _, err := requiredProjectFreshnessInputs(root, "head", rec); err == nil {
		t.Fatal("required memory read through escaped symlink")
	}
	rec.WorktreeID = "unmerged-worker"
	rec.ExtMeta = nil
	if _, err := requiredProjectFreshnessInputs(root, "head", rec); err == nil {
		t.Fatal("worktree-local fact qualified without canonical evidence")
	}
	rec.WorktreeID = ""
	rec.BranchName = "unverified-branch"
	rec.HeadCommit = "old-head"
	if _, err := requiredProjectFreshnessInputs(root, "head", rec); err == nil {
		t.Fatal("unknown branch currentness skipped")
	}
	rec.BranchName = ""
	rec.ExtMeta = map[string]any{"file_path": "result.txt"}
	if _, err := requiredProjectFreshnessInputs(root, "head", rec); err == nil {
		t.Fatal("working-tree anchor without hash qualified")
	}
	tooMany := map[string]string{}
	for i := 0; i < 257; i++ {
		tooMany[fmt.Sprintf("file-%d", i)] = "hash"
	}
	rec.ExtMeta = map[string]any{"file_hashes": tooMany}
	if _, err := requiredProjectFreshnessInputs(root, "head", rec); err != projectcontext.ErrBudget {
		t.Fatal("grounding path cap silently omitted excess", err)
	}
	large := filepath.Join(root, "large.txt")
	if err := os.WriteFile(large, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(large, (8<<20)+1); err != nil {
		t.Fatal(err)
	}
	rec.ExtMeta = map[string]any{"file_hashes": map[string]string{"large.txt": "hash"}}
	if _, err := requiredProjectFreshnessInputs(root, "head", rec); err != projectcontext.ErrBudget {
		t.Fatal("oversized grounding file read", err)
	}
}

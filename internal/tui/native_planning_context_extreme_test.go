//go:build !governed

package tui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/proposalmcp"
	"github.com/Zen1th53/marshal/internal/statepath"
	"github.com/Zen1th53/marshal/internal/store"
)

func nativePlanningFixture(t *testing.T, body ...string) (*Workspace, *proposalMCPLease, proposalmcp.Config, model.MemoryRecordV2, model.GoalContract) {
	t.Helper()
	w, lease, cfg := liveMCPFixture(t)
	now := time.Now().UTC().Add(-time.Second)
	record := model.MemoryRecordV2{ID: "MEM-native-planning", ProjectID: w.projectID, Revision: 1, Kind: model.MemoryKindDecision, Lifecycle: model.MemoryDurable, Authority: model.AuthorityOperator, Confidence: model.ConfidenceVerified, Title: "Mandatory native planning policy", Body: "Never discard accepted architecture and task policy.", Scope: "project", ScopeID: w.projectID, ObservedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now, Source: model.MemorySource{Kind: "runtime_service", Reference: "owner"}}
	if len(body) > 0 {
		record.Body = body[0]
	}
	record.ContentDigest = record.CanonicalDigest()
	if err := w.store.WriteMemoryV2(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	goal := model.GoalContract{ID: "GOAL-native-planning", SessionID: w.sessionID, ProjectID: w.runtime.ProjectIdentity(), OriginalRequest: "Preserve mandatory project context", ConstitutionVersion: constitution.Current.String(), DesiredOutcome: "Preserve mandatory project context", ExpectedArtifact: "src/policy.txt", SuccessCriteria: []string{"Every required policy is loaded"}, Risk: model.R0, AuthoritySource: "operator", Confirmation: model.ConfirmationApproved, Assessment: map[string]string{"complexity": "LOW", "ambiguity": "LOW", "blast_radius": "LOW", "privilege": "LOW", "reversibility": "LOW", "external_effects": "LOW", "data_sensitivity": "LOW", "dependency_depth": "LOW", "verification_difficulty": "LOW", "operational_criticality": "LOW"}}
	if err := w.store.SaveGoalContract(t.Context(), goal, 0); err != nil {
		t.Fatal(err)
	}
	goal.Revision = 1
	child := memoryChild(t, w.runtime.ProjectRoot(), &cfg)
	var inventory store.ProjectContractInventoryPage
	if err := json.Unmarshal([]byte(child.call(t, "marshal_project_contract_inventory", map[string]any{"limit": 1}, true)), &inventory); err != nil || len(inventory.Entries) != 1 {
		t.Fatal("inventory", err)
	}
	e := inventory.Entries[0]
	draft := store.ProjectContractSubmission{Action: "project-contract", Contract: projectcontext.Contract{FormatVersion: 1, Project: w.runtime.ProjectContractBinding(), ID: "ARCH-native-planning", Revision: 1, Purpose: "Keep authoritative planning context complete", Components: []projectcontext.Component{{ID: "core", Responsibility: "Govern work", Paths: []string{"src"}}}, Invariants: []string{"Never omit required policy"}, ReviewedInventoryDigest: inventory.CompletenessDigest, Required: []projectcontext.MemoryRef{{ID: e.ID, ProjectID: e.ProjectID, Revision: e.Revision, Scope: e.Scope, ScopeID: e.ScopeID, ContentDigest: e.ContentDigest, StateFingerprint: e.StateFingerprint, Class: projectcontext.Policy, Applies: projectcontext.Applicability{TaskIDs: []string{"future-task"}}, AcceptanceOccurrence: "owner-policy", Reason: "Required before native planning"}}}}
	child.call(t, "marshal_project_contract_submit", map[string]any{"submission_id": "native-planning-contract", "proposal": draft}, true)
	ownerAcceptsArchitecture(t, w)
	authorizeTestMemory(t, w, cfg, true)
	return w, lease, cfg, record, goal
}

func nativePlanningVerifiedCodex(t *testing.T, w *Workspace) {
	t.Helper()
	profile := model.HarnessProfile{Harness: "codex", InstalledVersion: "codex-cli 0.159.2", SupportedModels: []string{"gpt-test"}, DefaultModel: "gpt-test", ProbeEvidenceID: "EV-native-planning-codex", ProbedAt: time.Now().UTC()}
	if err := w.store.SaveHarnessProfile(t.Context(), profile); err != nil {
		t.Fatal(err)
	}
}

func TestNativePlanningLaunchAndResumeCarryFullMandatoryPacket(t *testing.T) {
	for _, provider := range []string{"codex", "claude", "opencode", "antigravity"} {
		t.Run(provider, func(t *testing.T) {
			w, _, _, record, goal := nativePlanningFixture(t)
			root := w.runtime.ProjectRoot()
			for _, base := range [][]string{nil, marshalResumeArgs(provider, "fixture-session")} {
				brief, err := marshalRoleBriefing(app.MarshalWorkers(provider), marshal.DefaultSettings(), marshal.Standard)
				if err != nil {
					t.Fatal(err)
				}
				if len(brief) < 19000 {
					t.Fatal("fixture did not exercise actual compiled private protocol")
				}
				args, env, dir, err := prepareMarshalLaunch(provider, root, base, brief, false, "")
				if err == nil {
					args, _, dir, err = w.prepareProposalMCP(provider, root, args, env, dir)
				}
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(dir.remove)
				delivered := strings.Join(args, "\n")
				if hiddenChannel(provider) == injectMarshalDir {
					data, e := os.ReadFile(dir.file())
					if e != nil {
						t.Fatal(e)
					}
					delivered = string(data)
				}
				if !strings.Contains(delivered, record.Body) || !strings.Contains(delivered, goal.DesiredOutcome) || !strings.Contains(delivered, "Keep authoritative planning context complete") {
					t.Fatal("actual launch/resume constructor omitted mandatory packet")
				}
			}
		})
	}
}

func TestNativePlanningLaunchPacketCannotReplaceFreshMCPRead(t *testing.T) {
	w, _, _, _, goal := nativePlanningFixture(t)
	root := w.runtime.ProjectRoot()
	_, env, dir, err := w.prepareProposalMCP("codex", root, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(dir.remove)
	var configPath string
	for _, entry := range env {
		if strings.HasPrefix(entry, "MARSHAL_PROPOSAL_MCP_CONFIG=") {
			configPath = strings.TrimPrefix(entry, "MARSHAL_PROPOSAL_MCP_CONFIG=")
		}
	}
	cfg, err := proposalmcp.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	a := &activeTmuxAgent{id: "marshal-chat", provider: "codex", paneID: "%42", sessionID: "second-native-session", role: "marshal-chat", state: "working", briefingDir: dir}
	w.tmuxMu.Lock()
	w.tmuxActiveWins["marshal-chat"] = a
	w.tmuxMu.Unlock()
	w.activateProposalMCP(a)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\necho 'codex-cli 0.159.2'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	nativePlanningVerifiedCodex(t, w)
	service := w.runtime.Marshal()
	proposal := []byte(`{"tasks":[{"id":"future-task","title":"Preserve policy","criteria":["Every required policy is loaded"],"paths":["src/policy.txt"],"worker":"codex","mode":"governed","checks":[{"command":"true","criteria":["Every required policy is loaded"]}],"instructions":"Preserve the accepted policy","expected_output":"one reviewed policy change"}]}`)
	draft, err := service.DraftFromProposal(proposal, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.persistNativePlanningDraft(t.Context(), "codex", service, "RUN-launch-only", draft, marshal.Budget{}); err == nil {
		t.Fatal("launch hidden packet authorized draft without fresh MCP context read")
	}
	if _, err := w.store.GetMarshalRun(t.Context(), w.projectID, "RUN-launch-only"); err == nil {
		t.Fatal("launch-only draft persisted")
	}
	memoryChild(t, root, &cfg).call(t, "marshal_project_contract_context", map[string]any{}, true)
	run, err := w.persistNativePlanningDraft(t.Context(), "codex", service, "RUN-after-MCP-read", draft, marshal.Budget{})
	if err != nil || run.State != marshal.Drafting {
		t.Fatalf("fresh MCP context read did not authorize canonical draft: state=%s err=%v", run.State, err)
	}
	saved, err := w.store.GetMarshalRun(t.Context(), w.projectID, "RUN-after-MCP-read")
	if err != nil {
		t.Fatal(err)
	}
	storedPlan, err := w.store.GetPlan(t.Context(), saved.Value.PlanID, saved.Value.PlanVersion)
	if err != nil || !storedPlan.Goal.Matches(goal) {
		t.Fatalf("canonical draft missing current Goal: plan=%+v err=%v", storedPlan, err)
	}
	if storedPlan.State != plan.StateReady || storedPlan.RequiredProjectContract == nil || len(storedPlan.Routes) != 1 || storedPlan.Routes["future-task"].Provider != "codex" || storedPlan.Routes["future-task"].Model != "gpt-test" || !storedPlan.Assignments.Complete() || len(storedPlan.Team.Members) == 0 || !storedPlan.Verification.Complete() || len(storedPlan.Checks["future-task"]) != 1 {
		t.Fatalf("chat draft did not become an executable canonical Process 04 plan: %+v", storedPlan)
	}
	m := w.marshalSession()
	m.mu.Lock()
	m.runID, m.service, m.provider = "RUN-after-MCP-read", service, "codex"
	m.mu.Unlock()
	if err := w.queueMarshalRunProposal(marshalProposal{action: "approve"}); err != nil {
		t.Fatalf("canonical chat plan offered no reviewable approval: %v", err)
	}
	var reviewed permission.Request
	for _, request := range w.permissions.queue.Take(false) {
		if request.Object == "/marshal approve" {
			reviewed = request
		}
	}
	if reviewed.ApprovalBinding == "" || !strings.Contains(reviewed.Detail, storedPlan.ID) || !strings.Contains(reviewed.Detail, goal.ID) || !strings.Contains(reviewed.Detail, "gpt-test") || !strings.Contains(reviewed.Detail, "true") {
		t.Fatalf("canonical approval popup omitted reviewed route, check, or binding: %+v", reviewed)
	}
	var snapshot app.PlanApprovalSnapshot
	if err := json.Unmarshal([]byte(reviewed.ApprovalBinding), &snapshot); err != nil || snapshot.CanonicalScopeDigest == "" || snapshot.AssignmentSummary == "" || snapshot.PlanningMode != "standard" || snapshot.MarshalBudgetSummary == "" || !strings.Contains(reviewed.Detail, snapshot.CanonicalScopeDigest) || !strings.Contains(reviewed.Detail, snapshot.AssignmentSummary) || !strings.Contains(reviewed.Detail, snapshot.MarshalBudgetSummary) {
		t.Fatalf("canonical approval popup omitted immutable scope or exact harness assignment: %+v %v", snapshot, err)
	}
	if _, err := permission.Render([]permission.Request{reviewed}); err != nil {
		t.Fatalf("canonical approval popup did not render: %v", err)
	}
}

func TestNativePlanningDraftWithoutVerifiedWorkerCannotOfferApproval(t *testing.T) {
	w, _, cfg, _, _ := nativePlanningFixture(t)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\necho 'codex-cli 0.159.2'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	memoryChild(t, w.runtime.ProjectRoot(), &cfg).call(t, "marshal_project_contract_context", map[string]any{}, true)
	service := w.runtime.Marshal()
	draft, err := service.DraftFromProposal([]byte(`{"tasks":[{"id":"future-task","title":"Preserve policy","criteria":["Every required policy is loaded"],"paths":["src/policy.txt"],"worker":"codex","mode":"governed","checks":[{"command":"true","criteria":["Every required policy is loaded"]}]}]}`), "codex")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.persistNativePlanningDraft(t.Context(), "codex", service, "RUN-unverified-worker", draft, marshal.Budget{}); err == nil || !strings.Contains(err.Error(), "/harness verify codex") {
		t.Fatalf("unverified worker did not receive actionable refusal: %v", err)
	}
	if _, err := w.store.GetMarshalRun(t.Context(), w.projectID, "RUN-unverified-worker"); err == nil {
		t.Fatal("unverified worker persisted an approvable Marshal run")
	}
	if batch := w.permissions.queue.Take(false); len(batch) != 0 {
		t.Fatalf("unverified worker offered an approval popup: %+v", batch)
	}
}

func TestNativePlanningChatPopupApprovesExactCanonicalPlan(t *testing.T) {
	w, _, cfg, _, goal := nativePlanningFixture(t)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\necho 'codex-cli 0.159.2'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	nativePlanningVerifiedCodex(t, w)
	memoryChild(t, w.runtime.ProjectRoot(), &cfg).call(t, "marshal_project_contract_context", map[string]any{}, true)
	m := w.marshalSession()
	m.mu.Lock()
	m.provider = "codex"
	m.mu.Unlock()
	service, _, _, err := w.marshalService(t.Context(), "RUN-canonical-chat-popup")
	if err != nil {
		t.Fatal(err)
	}
	service.GovernedDrivers = map[string]driver.Driver{"codex": driver.Governed{Provider: "codex", Run: func(context.Context, driver.Request) ([]marshal.CommandRecord, error) {
		return nil, errors.New("fixture blocks worker execution")
	}}}
	draft, err := service.DraftFromProposal([]byte(`{"tasks":[{"id":"future-task","title":"Preserve policy","criteria":["Every required policy is loaded"],"paths":["src/policy.txt"],"worker":"codex","mode":"governed","checks":[{"command":"true","criteria":["Every required policy is loaded"]}],"instructions":"Preserve the accepted policy"}]}`), "codex")
	if err != nil {
		t.Fatal(err)
	}
	const runID = "RUN-canonical-chat-popup"
	run, err := w.persistNativePlanningDraft(t.Context(), "codex", service, runID, draft, marshal.Budget{})
	if err != nil || run.State != marshal.Drafting {
		t.Fatalf("chat draft did not create canonical review state: %v %+v", err, run)
	}
	m.mu.Lock()
	m.runID, m.service, m.provider = runID, service, "codex"
	m.mu.Unlock()
	if err := w.queueMarshalRunProposal(marshalProposal{action: "approve"}); err != nil {
		t.Fatal(err)
	}
	var request permission.Request
	for _, candidate := range w.permissions.queue.Take(false) {
		if candidate.Object == "/marshal approve" {
			request = candidate
		}
	}
	if request.ApprovalBinding == "" || !strings.Contains(request.Detail, goal.ID) || !strings.Contains(request.Detail, "gpt-test") || !strings.Contains(request.Detail, "true") {
		t.Fatalf("canonical popup omitted exact review material: %+v", request)
	}
	var snapshot app.PlanApprovalSnapshot
	if err := json.Unmarshal([]byte(request.ApprovalBinding), &snapshot); err != nil || snapshot.CanonicalScopeDigest == "" || snapshot.AssignmentSummary == "" || snapshot.PlanningMode != "standard" || snapshot.MarshalBudgetSummary == "" || !strings.Contains(request.Detail, snapshot.CanonicalScopeDigest) || !strings.Contains(request.Detail, snapshot.AssignmentSummary) || !strings.Contains(request.Detail, snapshot.MarshalBudgetSummary) {
		t.Fatalf("canonical popup omitted immutable scope or exact harness assignment: %+v %v", snapshot, err)
	}
	if _, err := permission.Render([]permission.Request{request}); err != nil {
		t.Fatalf("canonical popup failed to render: %v", err)
	}
	if err := w.applyMarshalProposal(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		approvedRun, e := service.Snapshot(t.Context(), runID)
		if e == nil && approvedRun.Process05Bound && approvedRun.ApprovalScopeDigest != "" {
			approvedPlan, e := w.store.GetPlan(t.Context(), approvedRun.PlanID, approvedRun.PlanVersion)
			if e != nil || approvedPlan.State != plan.StateApproved || !approvedPlan.Goal.Matches(goal) || approvedPlan.ApprovalScopeDigest == "" {
				t.Fatalf("popup consent did not bind approved Process 04 plan: %+v %v", approvedPlan, e)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("canonical popup did not approve and bind a Process 05 run: %+v", w.marshalPanel())
}

func TestNativePlanningLeaseReadRevalidatedOneShotBeforeDraftEffect(t *testing.T) {
	for _, scenario := range []string{"current", "missing-read", "changed-memory", "foreign-session", "revoked-lease"} {
		t.Run(scenario, func(t *testing.T) {
			w, lease, cfg, record, goal := nativePlanningFixture(t)
			if scenario != "missing-read" {
				memoryChild(t, w.runtime.ProjectRoot(), &cfg).call(t, "marshal_project_contract_context", map[string]any{}, true)
			}
			switch scenario {
			case "changed-memory":
				_, err := w.store.UpdateMemory(t.Context(), record.ProjectID, record.ID, record.Revision, func(m *model.MemoryRecordV2) error { m.ACLScope = "foreign"; return nil })
				if err != nil {
					t.Fatal(err)
				}
			case "foreign-session":
				w.tmuxMu.Lock()
				w.tmuxActiveWins["marshal-chat"].sessionID = "foreign-session"
				w.tmuxMu.Unlock()
			case "revoked-lease":
				w.revokeProposalLease(lease)
			}
			effects := 0
			err := w.withNativePlanningDraftRead(t.Context(), "codex", goal.ID, goal.Revision, func(context.Context) error { effects++; return nil })
			if scenario == "current" {
				if err != nil || effects != 1 {
					t.Fatal("current full read refused", err)
				}
				if err := w.withNativePlanningDraftRead(t.Context(), "codex", goal.ID, goal.Revision, func(context.Context) error { effects++; return nil }); err == nil || effects != 1 {
					t.Fatal("same read reused")
				}
			} else if err == nil || effects != 0 {
				t.Fatal("unqualified native context reached draft effect", err)
			}
		})
	}
}

func TestNativePlanningActualDraftWriteUsesCanonicalGoalAndRefusesMissingRead(t *testing.T) {
	for _, scenario := range []string{"current", "missing-read", "foreign-pin", "changed-memory", "native-forbidden"} {
		t.Run(scenario, func(t *testing.T) {
			w, _, cfg, record, goal := nativePlanningFixture(t)
			bin := t.TempDir()
			if err := os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\necho 'codex-cli 0.159.2'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			nativePlanningVerifiedCodex(t, w)
			if scenario == "native-forbidden" {
				goal.DesiredOutcome = "Use governed sandboxed workers for this change"
				goal.Revision = 2
				if err := w.store.SaveGoalContract(t.Context(), goal, 1); err != nil {
					t.Fatal(err)
				}
			}
			mode := "governed"
			if scenario == "native-forbidden" {
				mode = "native"
			}
			proposal := []byte(`{"tasks":[{"id":"future-task","title":"Preserve policy","criteria":["Every required policy is loaded"],"paths":["src/policy.txt"],"worker":"codex","mode":"` + mode + `","checks":[{"command":"true","criteria":["Every required policy is loaded"]}],"instructions":"Preserve the accepted policy","expected_output":"one reviewed policy change"}]}`)
			service := w.runtime.Marshal()
			draft, err := service.DraftFromProposal(proposal, "codex")
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "foreign-pin" {
				draft.Plan.Goal = plan.GoalBinding{GoalID: "GOAL-foreign", Revision: 1}
			}
			if scenario != "missing-read" {
				memoryChild(t, w.runtime.ProjectRoot(), &cfg).call(t, "marshal_project_contract_context", map[string]any{}, true)
			}
			if scenario == "changed-memory" {
				if _, err := w.store.UpdateMemory(t.Context(), record.ProjectID, record.ID, record.Revision, func(m *model.MemoryRecordV2) error { m.ACLScope = "foreign"; return nil }); err != nil {
					t.Fatal(err)
				}
			}
			run, err := w.persistNativePlanningDraft(t.Context(), "codex", service, "RUN-native-current-draft", draft, marshal.Budget{})
			saved, readErr := w.store.GetMarshalRun(t.Context(), w.projectID, "RUN-native-current-draft")
			if scenario != "current" {
				if err == nil || readErr == nil {
					t.Fatal("unqualified draft persisted", err)
				}
				return
			}
			if err != nil || readErr != nil || run.State != marshal.Drafting {
				t.Fatal("actual current canonical draft write refused", err, readErr)
			}
			p, err := w.store.GetPlan(t.Context(), saved.Value.PlanID, saved.Value.PlanVersion)
			if err != nil {
				t.Fatal(err)
			}
			if !p.Goal.Matches(goal) || p.RequiredProjectContract == nil || len(run.Tasks) != 1 || run.Tasks[0].Instructions != "Preserve the accepted policy" {
				t.Fatal("derived Goal/contract pin or authored task body changed")
			}
		})
	}
}

func TestNativePlanningActualHostProcessReceivesCompiledHiddenPacket(t *testing.T) {
	w, _, _, record, goal := nativePlanningFixture(t)
	brief, err := marshalRoleBriefing(app.MarshalWorkers("codex"), marshal.DefaultSettings(), marshal.Standard)
	if err != nil {
		t.Fatal(err)
	}
	for _, base := range [][]string{nil, marshalResumeArgs("codex", "owned-fixture-session")} {
		args, env, dir, err := prepareMarshalLaunch("codex", w.runtime.ProjectRoot(), base, brief, false, "")
		if err == nil {
			args, env, dir, err = w.prepareProposalMCP("codex", w.runtime.ProjectRoot(), args, env, dir)
		}
		if err != nil {
			t.Fatal(err)
		}
		defer dir.remove()
		// An owned host executable receives the actual production launch envelope.
		// No native account/provider/model, model decision or execution grant.
		command := exec.CommandContext(t.Context(), "/bin/sh", append([]string{"-c", `printf '%s\n' "$@"`, "fixture"}, args...)...)
		command.Dir = w.runtime.ProjectRoot()
		command.Env = append(os.Environ(), env...)
		output, err := command.Output()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(output), record.Body) || !strings.Contains(string(output), goal.DesiredOutcome) || !strings.Contains(string(output), strings.ReplaceAll(buildRoleAuthority, ".marshal/", statepath.DirName+"/")) {
			t.Fatal("actual host child omitted full hidden protocol/required context")
		}
	}
}

func TestNativeCtxEscapes(t *testing.T) {
	w, lease, _, _, _ := nativePlanningFixture(t, strings.Repeat(`\"`, 1600))
	w.proposalMCP.mu.Lock()
	raw, err := w.readNativePlanningMCP(t.Context(), lease)
	minted := lease.planning != nil
	w.proposalMCP.mu.Unlock()
	if err != nil || !minted || len(raw) <= 4096 || !projectcontext.NativePlanningRPCResponseFits(raw) {
		t.Fatalf("large escaped complete context refused: raw=%d proof=%v err=%v", len(raw), minted, err)
	}
	// A raw projection can fit 16KiB while the second JSON-RPC escaping
	// exceeds the exact wire bound; the parent rejects this before minting proof.
	escaped := json.RawMessage(`{"context":"` + strings.Repeat(`\\`, 8150) + `"}`)
	if !json.Valid(escaped) || len(escaped) > app.NativePlanningMaximumBytes || projectcontext.NativePlanningRPCResponseFits(escaped) {
		t.Fatalf("raw-fits/escaped-overflow boundary not exercised: raw=%d", len(escaped))
	}
}

func TestNativePlanningCompleteLargeContextReadAndOversizeRefusal(t *testing.T) {
	w, lease, _, _, goal := nativePlanningFixture(t)
	ownerCtx, err := w.nativePlanningOwnerContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	principal := authz.Principal{ID: lease.owner, Role: authz.Role{Name: "marshal", Authorities: []authz.Authority{authz.AuthorityPolicyAdmin}}}
	base, err := w.runtime.InspectProjectPlanningContext(ownerCtx, principal, w.sessionID, app.NativePlanningMaximumBytes)
	if err != nil {
		t.Fatal(err)
	}
	padding := 11717 - len(base.Context)
	if padding <= 0 {
		t.Fatalf("fixture baseline unexpectedly large: %d", len(base.Context))
	}
	goal.OriginalRequest = strings.Repeat("x", padding)
	if err := w.store.SaveGoalContract(t.Context(), goal, goal.Revision); err != nil {
		t.Fatal(err)
	}
	w.proposalMCP.mu.Lock()
	raw, err := w.readNativePlanningMCP(t.Context(), lease)
	minted := lease.planning != nil
	w.proposalMCP.mu.Unlock()
	if err != nil || !minted || len(raw) <= 3072 || len(raw) > app.NativePlanningMaximumBytes {
		t.Fatalf("complete ~11.7KiB context refused: raw=%d proof=%v err=%v", len(raw), minted, err)
	}
	var delivered app.NativePlanningInspection
	if err := json.Unmarshal(raw, &delivered); err != nil || len(delivered.Context) < 11000 || !strings.Contains(delivered.Context, goal.OriginalRequest) {
		t.Fatalf("large native read lost complete Goal: context=%d err=%v", len(delivered.Context), err)
	}
	goal.Revision++
	goal.OriginalRequest = strings.Repeat("y", app.NativePlanningMaximumBytes)
	if err := w.store.SaveGoalContract(t.Context(), goal, goal.Revision); err != nil {
		t.Fatal(err)
	}
	w.proposalMCP.mu.Lock()
	_, err = w.readNativePlanningMCP(t.Context(), lease)
	minted = lease.planning != nil
	w.proposalMCP.mu.Unlock()
	if !errors.Is(err, projectcontext.ErrBudget) || minted {
		t.Fatalf("oversized complete context minted proof: proof=%v err=%v", minted, err)
	}
}

func TestNativePlanningAbortClearsExactReadBeforeActualDraftWrite(t *testing.T) {
	w, lease, cfg, _, _ := nativePlanningFixture(t)
	child := memoryChild(t, w.runtime.ProjectRoot(), &cfg)
	child.call(t, "marshal_project_contract_context", map[string]any{}, true)
	w.proposalMCP.mu.Lock()
	old := lease.planning.readID
	w.proposalMCP.mu.Unlock()
	child.call(t, "marshal_project_contract_context", map[string]any{}, true)
	w.proposalMCP.mu.Lock()
	current := lease.planning.readID
	w.proposalMCP.mu.Unlock()
	if current == old || current == "" {
		t.Fatal("read selector reused")
	}
	// A late failure of the old response cannot invalidate a newer read.
	raw, _ := json.Marshal(map[string]string{"read_id": old})
	if _, err := w.handleProposalMCP(t.Context(), lease, "project_contract_context_abort", raw); err != nil {
		t.Fatal(err)
	}
	w.proposalMCP.mu.Lock()
	stillCurrent := lease.planning != nil && lease.planning.readID == current
	w.proposalMCP.mu.Unlock()
	if !stillCurrent {
		t.Fatal("late old response erased current read")
	}
	raw, _ = json.Marshal(map[string]string{"read_id": current})
	if _, err := w.handleProposalMCP(t.Context(), lease, "project_contract_context_abort", raw); err != nil {
		t.Fatal(err)
	}
	// Actual canonical persistence, not merely a callback counter, is refused.
	draft := app.MarshalDraft{Plan: plan.ExecutionPlan{ID: "PLAN-aborted-native-read", ProjectID: projectid.ID(w.runtime.ProjectIdentity()), Version: 1, State: plan.StateReady, Goal: plan.GoalBinding{GoalID: "GOAL-PLAN-aborted-native-read", Revision: 1}}, Tasks: []marshal.Task{{PlanTaskID: "future-task", Worker: "codex", Mode: marshal.Governed}}}
	if _, err := w.persistNativePlanningDraft(t.Context(), "codex", w.runtime.Marshal(), "RUN-aborted-native-read", draft, marshal.Budget{}); err == nil {
		t.Fatal("aborted read reached canonical plan persistence")
	}
	if _, err := w.store.GetMarshalRun(t.Context(), w.projectID, "RUN-aborted-native-read"); err == nil {
		t.Fatal("aborted response persisted run")
	}
	if _, err := w.handleProposalMCP(t.Context(), lease, "project_contract_context_abort", raw); err != nil {
		t.Fatal("clear-only abort replay failed", err)
	}
	w.proposalMCP.mu.Lock()
	restored := lease.planning != nil
	w.proposalMCP.mu.Unlock()
	if restored {
		t.Fatal("clear-only JSON restored read authority")
	}
}

// A draft published without a current marshal_project_contract_context read
// is refused with a reason that names the missing read. Before, the refusal
// was the bare REQUIRED_CONTEXT_REVIEW_PENDING code under a note telling the
// Marshal to verify workers, so a real Marshal re-verified a healthy worker
// and published the same unread draft again.
func TestNativePlanningDraftWithoutReadNamesTheMissingRead(t *testing.T) {
	w, _, _, _, _ := nativePlanningFixture(t)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\necho 'codex-cli 0.159.2'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	nativePlanningVerifiedCodex(t, w)
	service := w.runtime.Marshal()
	draft, err := service.DraftFromProposal([]byte(`{"tasks":[{"id":"future-task","title":"Preserve policy","criteria":["Every required policy is loaded"],"paths":["src/policy.txt"],"worker":"codex","mode":"governed","checks":[{"command":"true","criteria":["Every required policy is loaded"]}]}]}`), "codex")
	if err != nil {
		t.Fatal(err)
	}
	_, err = w.persistNativePlanningDraft(t.Context(), "codex", service, "RUN-unread-draft", draft, marshal.Budget{})
	if !errors.Is(err, projectcontext.ErrReviewPending) || !strings.Contains(err.Error(), "marshal_project_contract_context") {
		t.Fatalf("unread draft refusal does not name the missing read: %v", err)
	}
	if _, err := w.store.GetMarshalRun(t.Context(), w.projectID, "RUN-unread-draft"); err == nil {
		t.Fatal("unread draft persisted")
	}
	m := w.marshalSession()
	m.mu.Lock()
	m.runID = "RUN-watched"
	m.mu.Unlock()
	w.marshalDraftRejected(m, "RUN-watched", "codex", "canonical Process 04 evidence unavailable", err)
	inbox, readErr := os.ReadFile(inboxPath(w.workDir, "marshal"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	text := string(inbox)
	if !strings.Contains(text, "call marshal_project_contract_context") {
		t.Fatalf("Marshal inbox does not say which evidence is missing:\n%s", text)
	}
	if strings.Contains(text, "verify the missing workers") {
		t.Fatalf("Marshal inbox still sends the Marshal to verify workers for a missing read:\n%s", text)
	}
}

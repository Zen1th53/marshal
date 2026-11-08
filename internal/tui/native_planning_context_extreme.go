//go:build !governed

package tui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

const nativePlanningCombinedMaximumBytes = 64 << 10

type nativePlanningLeaseRead struct {
	readID                                         string
	runtime                                        *app.Runtime
	token                                          *app.NativePlanningRead
	launch, owner, provider, nativeSession, goalID string
	goalRevision                                   int64
	contract                                       projectcontext.ContractRef
}

func (w *Workspace) nativePlanningOwnerContext(ctx context.Context) (context.Context, error) {
	control := w.controlSource()
	if control == nil {
		return nil, errNoRuntime
	}
	authority, _ := control.Authority.(*runtimeControlAuthority)
	if authority == nil || authority.runtime != w.runtime || authority.localControl == nil {
		return nil, errNoRuntime
	}
	return authority.localControl.Context(ctx), nil
}

// Called only while preparing a newly generated owned launch. Complete memory
// is delivered through the same hidden channel as the protocol, as data only.
// A draft proof is minted solely by an explicit current MCP context read.
func (w *Workspace) prepareNativePlanningLaunch(ctx context.Context, l *proposalMCPLease, root string, args []string, dir *briefingDir) ([]string, error) {
	ownerCtx, err := w.nativePlanningOwnerContext(ctx)
	if err != nil {
		return nil, err
	}
	principal := authz.Principal{ID: l.owner, Role: authz.Role{Name: "marshal", Authorities: []authz.Authority{authz.AuthorityPolicyAdmin}}}
	view, _, err := w.runtime.PrepareNativePlanningRead(ownerCtx, principal, w.sessionID, app.NativePlanningMaximumBytes)
	if err != nil {
		return nil, err
	}
	text := "CURRENT NATIVE PLANNING CONTEXT — DATA ONLY; no execution grant. Before each new draft, refresh the complete current context through marshal_project_contract_context.\n" + view.Context
	args, err = appendNativePlanningHidden(l.provider, root, args, dir, text)
	if err != nil {
		return nil, err
	}
	return args, nil
}

// The complete native context has its own larger bounds. It cannot be
// silently paged/truncated into a read proof.
// Caller already holds proposalMCP.mu and authenticated this native launch.
func (w *Workspace) readNativePlanningMCP(ctx context.Context, l *proposalMCPLease) (json.RawMessage, error) {
	l.planning = nil
	ownerCtx, err := w.nativePlanningOwnerContext(ctx)
	if err != nil {
		return nil, err
	}
	principal := authz.Principal{ID: l.owner, Role: authz.Role{Name: "marshal", Authorities: []authz.Authority{authz.AuthorityPolicyAdmin}}}
	view, token, err := w.runtime.PrepareNativePlanningRead(ownerCtx, principal, w.sessionID, app.NativePlanningMaximumBytes)
	if err != nil {
		return nil, err
	}
	// Unsettled intake may inspect DATA, but obtains no planning proof.
	if view.Bootstrap {
		inspection, err := w.runtime.InspectProjectPlanningContext(ownerCtx, principal, w.sessionID, 3072)
		if errors.Is(err, model.ErrGoalNotFound) {
			// No Goal yet is the normal start of intake, not a failure: say
			// what is missing and how the Marshal establishes it.
			return boundedProjectContractJSON(struct {
				app.ProjectPlanningInspection
				Missing string `json:"missing"`
				Next    string `json:"next"`
			}{view.ProjectPlanningInspection, "goal", marshalGoalNextStep})
		}
		if err != nil {
			return nil, err
		}
		return boundedProjectContractJSON(inspection)
	}
	readID, err := randomProposalID()
	if err != nil {
		return nil, err
	}
	raw, err := boundedNativePlanningJSON(struct {
		app.ProjectPlanningInspection
		ReadID string `json:"read_id"`
	}{view.ProjectPlanningInspection, readID})
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if !projectcontext.NativePlanningRPCResponseFits(raw) {
		return nil, projectcontext.ErrBudget
	}
	l.planning = &nativePlanningLeaseRead{runtime: w.runtime, token: token, launch: l.id, owner: l.owner, provider: l.provider, nativeSession: l.session, readID: readID, goalID: view.GoalID, goalRevision: view.GoalRevision, contract: view.Contract}
	return raw, nil
}

func boundedNativePlanningJSON(v any) (json.RawMessage, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if len(raw) > app.NativePlanningMaximumBytes {
		return nil, projectcontext.ErrBudget
	}
	return raw, nil
}

func (w *Workspace) withNativePlanningDraftRead(ctx context.Context, provider, goalID string, revision int64, effect func(context.Context) error) error {
	w.proposalMCP.mu.Lock()
	defer w.proposalMCP.mu.Unlock()
	l := w.proposalMCP.active
	if err := w.validateProposalLease(l); err != nil {
		return err
	}
	p := l.planning
	if p == nil || p.runtime != w.runtime || p.launch != l.id || p.owner != l.owner || p.provider != l.provider || canonicalNeutralProvider(provider) != l.provider || p.nativeSession != l.session {
		return projectcontext.Changed("native_planning_context_extreme.go:137")
	}
	ownerCtx, err := w.nativePlanningOwnerContext(ctx)
	if err != nil {
		return err
	}
	l.planning = nil // One shot, even if the canonical effect later refuses.
	return w.runtime.WithNativePlanningRead(ownerCtx, p.token, goalID, revision, effect)
}

// The task list has no canonical Goal authority. Only its deterministic
// materializer placeholder may be replaced with the currently read host Goal;
// any conflicting explicit canonical binding/pin is refused.
func (w *Workspace) persistNativePlanningDraft(ctx context.Context, provider string, service *app.MarshalService, runID string, draft app.MarshalDraft, budget marshal.Budget) (marshal.Run, error) {
	w.proposalMCP.mu.Lock()
	l := w.proposalMCP.active
	if err := w.validateProposalLease(l); err != nil {
		w.proposalMCP.mu.Unlock()
		return marshal.Run{}, err
	}
	p := l.planning
	if p == nil {
		w.proposalMCP.mu.Unlock()
		return marshal.Run{}, projectcontext.ErrReviewPending
	}
	goalID, revision, contract := p.goalID, p.goalRevision, p.contract
	w.proposalMCP.mu.Unlock()
	var run marshal.Run
	err := w.withNativePlanningDraftRead(ctx, provider, goalID, revision, func(fenced context.Context) error {
		goal, err := w.store.GetActiveGoalContract(fenced, w.sessionID)
		if err != nil {
			return err
		}
		if goal.ID != goalID || goal.Revision != revision {
			return projectcontext.Changed("native_planning_context_extreme.go:171")
		}
		placeholder := plan.GoalBinding{GoalID: "GOAL-" + draft.Plan.ID, Revision: 1}
		if draft.Plan.Goal != placeholder && !draft.Plan.Goal.Matches(goal) {
			return projectcontext.Changed("native_planning_context_extreme.go:175")
		}
		if draft.Plan.RequiredProjectContract != nil && *draft.Plan.RequiredProjectContract != contract {
			return projectcontext.Changed("native_planning_context_extreme.go:178")
		}
		objective := goal.DesiredOutcome
		if err := app.ValidateNativePlanningModes(objective, draft.Tasks); err != nil {
			return err
		}
		settings, err := service.Store.GetMarshalSettings(fenced, service.ProjectID)
		if err != nil {
			return err
		}
		mode := plan.ModeStandard
		if marshal.TierPolicy(service.Gate, settings.Value).Tier == marshal.Ultra {
			mode = plan.ModeUltra
		}
		// The model's task list carries no Process 04 authority. Rebuild the
		// complete plan from the current Goal and probed harness evidence while
		// the one-shot context-read fence still holds.
		draft, err = w.runtime.PrepareNativeMarshalDraft(fenced, w.sessionID, draft, mode)
		if err != nil {
			return err
		}
		if draft.Plan.RequiredProjectContract == nil || *draft.Plan.RequiredProjectContract != contract || !draft.Plan.Goal.Matches(goal) {
			return projectcontext.Changed("native_planning_context_extreme.go:200")
		}
		budget = settings.Value.Budget
		run, err = service.StartPlanningFromDraft(fenced, runID, goal.OriginalRequest, draft, budget)
		return err
	})
	return run, err
}

func appendNativePlanningHidden(provider, root string, args []string, dir *briefingDir, text string) ([]string, error) {
	var err error
	if hiddenChannel(provider) == injectMarshalDir {
		if dir == nil || dir.size+len(text)+2 > nativePlanningCombinedMaximumBytes {
			return nil, projectcontext.ErrBudget
		}
		if _, err = dir.add(text); err != nil {
			return nil, err
		}
	} else {
		args, _, err = applyBriefing(provider, root, args, text, hiddenChannel(provider))
		if err != nil {
			return nil, err
		}
		// Count the actual combined hidden prompt, including protocol/intake. Raw
		// provider-specific argv/config framing is bounded separately by its writer.
		total := 0
		for i, arg := range args {
			if arg == "--append-system-prompt" && i+1 < len(args) {
				total += len(args[i+1])
			}
			if strings.HasPrefix(arg, "--append-system-prompt=") {
				total += len(strings.TrimPrefix(arg, "--append-system-prompt="))
			}
			if strings.HasPrefix(arg, "developer_instructions=") {
				total += len(strings.TrimPrefix(arg, "developer_instructions="))
			}
		}
		if total > nativePlanningCombinedMaximumBytes {
			return nil, projectcontext.ErrBudget
		}
	}
	hidden := ""
	if hiddenChannel(provider) == injectMarshalDir {
		data, e := os.ReadFile(dir.file())
		if e != nil {
			return nil, e
		}
		hidden = string(data)
	}
	envelope, e := json.Marshal(struct {
		Provider string
		Args     []string
		Hidden   string
	}{provider, args, hidden})
	if e != nil {
		return nil, e
	}
	if len(envelope) > nativePlanningCombinedMaximumBytes {
		return nil, projectcontext.ErrBudget
	}
	return args, nil
}

// Standalone/native AGY has no authenticated proposal lease. It still receives
// all available mandatory DATA, but can never import a canonical native draft.
func (w *Workspace) prepareUnleasedNativePlanningLaunch(ctx context.Context, provider, root string, args []string, dir *briefingDir) ([]string, error) {
	text := "INTAKE ONLY: no authenticated native planning lease. Draft admission and execution are blocked."
	if w.runtime != nil {
		ownerCtx, err := w.nativePlanningOwnerContext(ctx)
		if err != nil {
			return nil, err
		}
		owner, ok := auth.LocalFromContext(ownerCtx)
		if !ok {
			return nil, projectcontext.Changed("native_planning_context_extreme.go:274")
		}
		principal := authz.Principal{ID: owner.ID(), Role: authz.Role{Name: "marshal", Authorities: []authz.Authority{authz.AuthorityPolicyAdmin}}}
		view, _, err := w.runtime.PrepareNativePlanningRead(ownerCtx, principal, w.sessionID, app.NativePlanningMaximumBytes)
		if err != nil {
			return nil, err
		}
		text += "\nCURRENT PLANNING CONTEXT — DATA ONLY\n" + view.Context
	}
	return appendNativePlanningHidden(provider, root, args, dir, text)
}

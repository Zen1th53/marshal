//go:build !governed

package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// nativePlanningCombinedMaximumBytes is a plain guard on a launch's whole
// hidden prompt (protocol, intake, launch scan, cross-agent memory and the
// short architecture pointer). Codex takes it as one argv element and Linux
// refuses any single argument over 128 KiB (MAX_ARG_STRLEN). The project
// architecture and planning context are never inlined: the Marshal reads them
// from the store through marshal_project_contract_context.
const nativePlanningCombinedMaximumBytes = 96 << 10

type nativePlanningLeaseRead struct {
	readID                                         string
	runtime                                        *app.Runtime
	token                                          *app.NativePlanningRead
	launch, owner, provider, nativeSession, goalID string
	goalRevision                                   int64
	contract                                       projectcontext.ContractRef
	// pages holds a context too large for one MCP reply; next is the page
	// the Marshal reads next. The read admits a draft only once every page
	// has been delivered (next == len(pages)).
	pages         []string
	next          int
	packetDigest  string
	requiredCount int
	qualified     bool
}

// complete reports whether the Marshal has received the whole context.
func (p *nativePlanningLeaseRead) complete() bool { return p != nil && p.next >= len(p.pages) }

// nativePlanningPageBytes bounds one page's reply so it fits the proposal
// transport's 16 KiB frame with room for its envelope.
const nativePlanningPageBytes = 12 << 10

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
	return appendNativePlanningHiddenExact(l.provider, root, args, dir, w.nativePlanningPointer(ownerCtx, view, true))
}

// nativePlanningPointer is all a launch carries about the project
// architecture and planning context: what is current and where to read it.
// The contract body, Goal and mandatory memories stay in the store and are
// read through marshal_project_contract_context, which also mints the one
// read that admits a draft.
func (w *Workspace) nativePlanningPointer(ctx context.Context, view app.NativePlanningInspection, tools bool) string {
	var b strings.Builder
	b.WriteString("PROJECT ARCHITECTURE AND PLANNING CONTEXT — NOT INCLUDED HERE (DATA ONLY; no execution grant).\n")
	if w.store == nil || w.runtime == nil {
		b.WriteString("- Architecture state unavailable at launch.\n")
	} else if accepted, err := w.store.ReadAcceptedProjectContract(ctx, w.runtime.ProjectContractBinding()); err == nil {
		active := 0
		for _, c := range accepted.Contract.Components {
			if !c.Removed {
				active++
			}
		}
		fmt.Fprintf(&b, "- Accepted project contract %s revision %d: %d components (%d active), %d connections.\n", accepted.Contract.ID, accepted.Contract.Revision, len(accepted.Contract.Components), active, len(accepted.Contract.Connections))
	} else {
		b.WriteString("- No accepted project architecture yet.\n")
	}
	if view.GoalID != "" {
		fmt.Fprintf(&b, "- Active Goal %s revision %d.\n", view.GoalID, view.GoalRevision)
	}
	if view.Bootstrap {
		b.WriteString("- " + view.Context + "\n")
	} else if view.RequiredCount > 0 {
		fmt.Fprintf(&b, "- %d mandatory memories apply.\n", view.RequiredCount)
	}
	if tools {
		b.WriteString("Read the complete current context with marshal_project_contract_context before drafting or planning, and again before each new draft; marshal_project_contract_read, marshal_architecture_history and marshal_architecture_at give the contract pages, its revisions and changes. Never plan from memory of an earlier revision.\n")
	} else {
		b.WriteString("This launch has no MARSHAL proposal tools, so the context cannot be read here: do not draft or plan, and do not invent or recall the architecture.\n")
	}
	return b.String()
}

// nativePlanningPage is one page of a context too large for a single reply.
type nativePlanningPage struct {
	app.ProjectPlanningInspection
	ReadID string `json:"read_id"`
	Page   int    `json:"page"`
	Pages  int    `json:"pages"`
	Next   string `json:"next,omitempty"`
}

func nativePlanningPageJSON(view app.ProjectPlanningInspection, readID string, page int, pages []string) (json.RawMessage, error) {
	view.Context = pages[page]
	reply := nativePlanningPage{ProjectPlanningInspection: view, ReadID: readID, Page: page + 1, Pages: len(pages)}
	if page+1 < len(pages) {
		reply.Next = fmt.Sprintf("This is page %d of %d. Call marshal_project_contract_context with {\"read_id\":\"%s\",\"page\":%d} for the next page; a draft is admitted only after the last page.", page+1, len(pages), readID, page+2)
	}
	raw, err := json.Marshal(reply)
	if err != nil {
		return nil, err
	}
	if len(raw) > nativePlanningPageBytes || !projectcontext.NativePlanningRPCResponseFits(raw) {
		return nil, projectcontext.ErrBudget
	}
	return raw, nil
}

// splitNativePlanningContext cuts text into pages whose replies all fit,
// on UTF-8 boundaries, shrinking the page size for heavily escaped text.
func splitNativePlanningContext(view app.ProjectPlanningInspection, readID string) ([]string, error) {
	for size := nativePlanningPageBytes - 2048; size >= 512; size /= 2 {
		var pages []string
		for rest := view.Context; rest != ""; {
			cut := min(size, len(rest))
			for cut < len(rest) && cut > 0 && !utf8.RuneStart(rest[cut]) {
				cut--
			}
			pages = append(pages, rest[:cut])
			rest = rest[cut:]
		}
		fits := true
		for i := range pages {
			if _, err := nativePlanningPageJSON(view, readID, i, pages); err != nil {
				fits = false
				break
			}
		}
		if fits {
			return pages, nil
		}
	}
	return nil, projectcontext.ErrBudget
}

// nativePlanningContinue serves the next page of the read in progress.
func (w *Workspace) nativePlanningContinue(l *proposalMCPLease, readID string, page int) (json.RawMessage, error) {
	p := l.planning
	if p == nil || p.readID != readID || page != p.next+1 || p.complete() {
		l.planning = nil
		return nil, fmt.Errorf("%w: page %d is not the next page of a current context read; call marshal_project_contract_context again without arguments", model.ErrInvalid, page)
	}
	view := app.ProjectPlanningInspection{Contract: p.contract, GoalID: p.goalID, GoalRevision: p.goalRevision, PacketDigest: p.packetDigest, RequiredCount: p.requiredCount, Qualified: p.qualified}
	raw, err := nativePlanningPageJSON(view, readID, p.next, p.pages)
	if err != nil {
		l.planning = nil
		return nil, err
	}
	p.next++
	return raw, nil
}

// readNativePlanningMCP returns the complete current context. One that does
// not fit a single reply is delivered in pages; it is never truncated, and
// the read admits a draft only after its last page.
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
		if errors.Is(err, projectcontext.ErrReviewPending) && w.architectureAwaitsAccept(ownerCtx) {
			// A confirmed Goal whose architecture the owner has not
			// accepted in the browser yet is the normal middle of intake,
			// not a failure: say what is missing and what to do. It is
			// data only and mints no planning proof.
			return boundedProjectContractJSON(struct {
				app.ProjectPlanningInspection
				Missing string `json:"missing"`
				Next    string `json:"next"`
			}{view.ProjectPlanningInspection, "architecture", marshalArchitectureNextStep})
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
	read := &nativePlanningLeaseRead{runtime: w.runtime, token: token, launch: l.id, owner: l.owner, provider: l.provider, nativeSession: l.session, readID: readID, goalID: view.GoalID, goalRevision: view.GoalRevision, contract: view.Contract, packetDigest: view.PacketDigest, requiredCount: view.RequiredCount, qualified: view.Qualified}
	if len(raw) <= nativePlanningPageBytes && projectcontext.NativePlanningRPCResponseFits(raw) {
		l.planning = read
		return raw, nil
	}
	pages, err := splitNativePlanningContext(view.ProjectPlanningInspection, readID)
	if err != nil {
		return nil, err
	}
	read.pages = pages
	raw, err = nativePlanningPageJSON(view.ProjectPlanningInspection, readID, 0, pages)
	if err != nil {
		return nil, err
	}
	read.next = 1
	l.planning = read
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
	if !p.complete() || p.runtime != w.runtime || p.launch != l.id || p.owner != l.owner || p.provider != l.provider || canonicalNeutralProvider(provider) != l.provider || p.nativeSession != l.session {
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
// errNoNativePlanningRead is the refusal of a draft published without a
// current marshal_project_contract_context read on the live launch. It keeps
// the REQUIRED_CONTEXT_REVIEW_PENDING class and names the missing evidence,
// so the Marshal reads the context instead of re-verifying workers.
var errNoNativePlanningRead = fmt.Errorf("%w: this draft has no current project context read; call marshal_project_contract_context (its reply carries a read_id), then publish the draft again; one read admits one draft", projectcontext.ErrReviewPending)

func (w *Workspace) persistNativePlanningDraft(ctx context.Context, provider string, service *app.MarshalService, runID string, draft app.MarshalDraft, budget marshal.Budget) (marshal.Run, error) {
	w.proposalMCP.mu.Lock()
	l := w.proposalMCP.active
	if err := w.validateProposalLease(l); err != nil {
		w.proposalMCP.mu.Unlock()
		return marshal.Run{}, err
	}
	p := l.planning
	if !p.complete() {
		w.proposalMCP.mu.Unlock()
		return marshal.Run{}, errNoNativePlanningRead
	}
	goalID, revision, contract := p.goalID, p.goalRevision, p.contract
	w.proposalMCP.mu.Unlock()
	var run marshal.Run
	var warnings []string
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
		warnings = plan.DecompositionWarnings(goal, draft.Plan.Tasks)
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
	if adviceErr := writeMarshalPlanningAdvice(service.Repository, runID, warnings); adviceErr != nil {
		w.logTUIError("planning advice not delivered", adviceErr)
	}
	return run, err
}

// appendNativePlanningHiddenExact adds text as it is, or refuses with
// ErrBudget and changes nothing (neither args nor dir).
func appendNativePlanningHiddenExact(provider, root string, args []string, dir *briefingDir, text string) ([]string, error) {
	var err error
	hidden := ""
	if hiddenChannel(provider) == injectMarshalDir {
		if dir == nil || dir.size+len(text)+2 > nativePlanningCombinedMaximumBytes {
			return nil, projectcontext.ErrBudget
		}
		data, e := os.ReadFile(dir.file())
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			return nil, e
		}
		// What dir.add will leave in the file, so the envelope is checked
		// before anything is written.
		hidden = string(data)
		if trimmed := strings.TrimSpace(text); trimmed != "" {
			if dir.size > 0 {
				hidden += "\n"
			}
			hidden += trimmed + "\n"
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
	if hiddenChannel(provider) == injectMarshalDir {
		if _, err = dir.add(text); err != nil {
			return nil, err
		}
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
		text += "\n" + w.nativePlanningPointer(ownerCtx, view, false)
	}
	return appendNativePlanningHiddenExact(provider, root, args, dir, text)
}

// marshalArchitectureNextStep tells the Marshal what to do while the
// architecture waits for the owner's browser Accept.
const marshalArchitectureNextStep = `The architecture is not accepted yet, so there is no project context to read. Carry on with the intake: submit the architecture with marshal_project_contract_submit if it is not submitted yet (every component needs a non-empty responsibility, every dependency needs a contract.connections entry whose carries says what flows over it, and every goal success criterion and confirmed requirement must be covered in at least one component's covers list; copy the full text of goal criteria, confirmed goal scope/constraints/do-not-do, and filled confirmed requirements card lines without their captions). It opens in the browser when the person presses y on the Board, and they accept it there. Call marshal_project_contract_context again after the runtime tells you the architecture was accepted.`

// architectureAwaitsAccept reports whether the only reason the planning
// context is unavailable is the architecture: the active Goal is confirmed
// and no project contract is accepted yet. A pending required memory or an
// unconfirmed Goal still reads as the refusal it is.
func (w *Workspace) architectureAwaitsAccept(ctx context.Context) bool {
	if w.store == nil || w.runtime == nil {
		return false
	}
	goal, err := w.store.GetActiveGoalContract(ctx, w.sessionID)
	if err != nil || !goal.Confirmation.Settled() {
		return false
	}
	_, err = w.store.ReadAcceptedProjectContract(ctx, w.runtime.ProjectContractBinding())
	return errors.Is(err, projectcontext.ErrReviewPending)
}

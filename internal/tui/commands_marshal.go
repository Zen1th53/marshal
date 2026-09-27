package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
)

const marshalUsage = `Marshal mode — one model plans with you, then marshals the work to other agents.
  /marshal                         Open a conversation with the Marshal
  /marshal chat                    Open a conversation with the Marshal
  /marshal <goal>                  Draft a plan for the goal with the Marshal model
  /marshal use-plan                Run the current approved Process 04 plan through Process 05
  /marshal approve-task <approval-id>  Approve a Process 05 task paused for your decision
  /marshal approve                 Approve the drafted plan and start running it
  /marshal status                  Show the run, its tasks and what it is waiting for
  /marshal close                   Close a verified run (moves the target branch)
  /marshal amend <reason>          Ask the Marshal to amend the plan; major changes need re-approval
  /marshal amend approve|deny      Approve or deny a major amended plan
  /marshal accept <task>           Approve one task for user acceptance mode
  /marshal resume                  Continue a run that stopped or was interrupted
  /marshal stop                    Stop the running run; its state is kept
  /marshal model <codex|claude|agy>  Choose the Marshal model for the next run
  /marshal settings [key value]    Show or change execution-rights, acceptance-mode, rework-limit, ultra-concurrency`

// marshalSession is the workspace's Marshal state: the service, the active
// run, and the approvals the person has given in this session.
type marshalSession struct {
	mu        sync.Mutex
	service   *app.MarshalService
	runID     string
	provider  string
	cancel    context.CancelFunc
	busy      bool
	amended   bool
	pending   *marshalAmendment
	approvals map[string]bool
}

type marshalAmendment struct {
	draft       app.MarshalDraft
	reason      string
	planVersion int64
}

// grant records that the person just approved purpose for runID. An approval
// is spent by the one decision it was given for.
func (m *marshalSession) grant(runID, purpose string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.approvals == nil {
		m.approvals = map[string]bool{}
	}
	m.approvals[runID+"/"+purpose] = true
}

// approver answers the service's approval questions only from approvals the
// person gave with an explicit command. Nothing a model or worker produces
// can reach this map.
func (m *marshalSession) approver(_ context.Context, runID, purpose string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := runID + "/" + purpose
	if !m.approvals[key] {
		return "", fmt.Errorf("no approval from you for %s of run %s", purpose, runID)
	}
	delete(m.approvals, key)
	return marshalOperator(), nil
}

// marshalOperator names the person at this terminal.
func marshalOperator() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return "operator:" + u.Username
	}
	return "operator"
}

func (w *Workspace) marshalSession() *marshalSession {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.marshal == nil {
		w.marshal = &marshalSession{}
	}
	return w.marshal
}

// setMarshalPanel publishes a snapshot and repaints. It is safe to call from
// the goroutine that runs the plan.
func (w *Workspace) setMarshalPanel(p *MarshalPanel) {
	w.mu.Lock()
	w.state.Marshal = p
	w.mu.Unlock()
	w.renderFullView()
}

func (w *Workspace) marshalPanel() *MarshalPanel {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.state.Marshal
}

func (h *CommandHandler) handleMarshal(ctx context.Context, args []string) (string, error) {
	w := h.ws
	if len(args) == 0 {
		return w.marshalChat(ctx)
	}
	switch strings.ToLower(args[0]) {
	case "chat":
		if len(args) != 1 {
			return "", errors.New("usage: /marshal chat")
		}
		return w.marshalChat(ctx)
	case "help":
		return marshalUsage, nil
	case "status":
		return marshalStatusText(w.marshalPanel()), nil
	case "model":
		return w.marshalSetModel(args[1:])
	case "approve":
		return w.marshalApprove(ctx)
	case "use-plan":
		if len(args) != 1 {
			return "", errors.New("usage: /marshal use-plan")
		}
		return w.marshalUsePlan(ctx)
	case "approve-task":
		return w.marshalApproveProcess05Task(ctx, args[1:])
	case "accept":
		return w.marshalAccept(args[1:])
	case "close":
		return w.marshalClose(ctx)
	case "stop":
		return w.marshalStop(), nil
	case "resume":
		return w.marshalResume(ctx)
	case "amend":
		if len(args) == 2 && strings.EqualFold(args[1], "approve") {
			return w.marshalAmendApprove(ctx)
		}
		if len(args) == 2 && strings.EqualFold(args[1], "deny") {
			return w.marshalAmendDeny()
		}
		return w.marshalAmend(ctx, strings.Join(args[1:], " "))
	case "settings":
		return w.marshalSettings(ctx, args[1:])
	default:
		return w.marshalStart(ctx, strings.Join(args, " "))
	}
}

const marshalDraftRelativePath = ".marshal/marshal/plan-draft.json"

func marshalChatProvider(provider string) string {
	if provider == "agy" {
		return "antigravity"
	}
	return provider
}

func marshalRoleBriefing(projectID string) string {
	return "You are the Marshal. Plan the work with the person in this conversation. " +
		"Do not edit project files. When the person agrees, write the plan as JSON matching the MarshalDraft schema " +
		"(plan: ExecutionPlan; tasks: Marshal tasks) to " + marshalDraftRelativePath + ". " +
		"Set plan.project_id to " + projectID + ". Tell the person when the draft is written. " +
		"Only the person can approve the plan, in the MARSHAL window, with /marshal approve. " +
		"You cannot approve it from this CLI."
}

func consumeMarshalDraft(root string) (app.MarshalDraft, bool, error) {
	path := filepath.Join(root, marshalDraftRelativePath)
	var draft app.MarshalDraft
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return draft, false, nil
	} else if err != nil {
		return draft, false, err
	}
	consumed := path + ".consumed"
	if err := os.Rename(path, consumed); err != nil {
		return draft, false, err
	}
	data, err := os.ReadFile(consumed)
	if err != nil {
		return draft, true, err
	}
	if err := json.Unmarshal(data, &draft); err != nil {
		return draft, true, fmt.Errorf("invalid Marshal draft JSON: %w", err)
	}
	return draft, true, nil
}

func (w *Workspace) marshalChat(ctx context.Context) (string, error) {
	m := w.marshalSession()
	m.mu.Lock()
	if m.busy {
		m.mu.Unlock()
		return "", errors.New("a Marshal operation is already running")
	}
	provider := m.provider
	m.mu.Unlock()
	runID := fmt.Sprintf("RUN-%d", time.Now().UTC().UnixNano())
	service, selected, note, err := w.marshalService(ctx, runID)
	if err != nil {
		return "", err
	}
	if provider == "" {
		provider = selected
	}
	root := service.Repository
	draftPath := filepath.Join(root, marshalDraftRelativePath)
	if _, err := os.Stat(draftPath); err == nil {
		return "", fmt.Errorf("existing Marshal draft at %s must be handled first", draftPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	result, sessionErr := w.runNativeAgent(ctx, marshalChatProvider(provider), nil, marshalRoleBriefing(service.ProjectID))
	if sessionErr != nil {
		return result, sessionErr
	}
	draft, exists, err := consumeMarshalDraft(root)
	if err != nil {
		return result, err
	}
	if !exists {
		return result, nil
	}
	goal := draft.Plan.Goal.GoalID
	if goal == "" {
		goal = draft.Plan.ID
	}
	run, err := service.StartPlanningFromDraft(ctx, runID, goal, draft, marshal.Budget{})
	if err != nil {
		return result, fmt.Errorf("Marshal draft rejected: %w", err)
	}
	m.mu.Lock()
	m.runID, m.service, m.provider, m.amended, m.pending = runID, service, provider, false, nil
	m.approvals = nil
	m.mu.Unlock()
	w.marshalPublish(m, runID, newMarshalPanel(runID, provider, run, fmt.Sprintf("plan drafted: %d tasks · /marshal approve to run it", len(run.Tasks))))
	return result + "\nMarshal plan drafted. " + note + " Use /marshal approve in MARSHAL to run it.", nil
}

func (w *Workspace) marshalSetModel(args []string) (string, error) {
	if len(args) != 1 {
		return "", errors.New("usage: /marshal model <codex|claude|agy>")
	}
	switch args[0] {
	case "codex", "claude", "agy":
	default:
		return "", fmt.Errorf("unknown Marshal model provider %q; use codex, claude or agy", args[0])
	}
	m := w.marshalSession()
	m.mu.Lock()
	m.provider = args[0]
	m.mu.Unlock()
	return "The next Marshal run will use " + args[0] + " as the Marshal model.", nil
}

// marshalRecommend recommends a Marshal provider from the static model
// inventory. It never probes a provider: probing Claude opens a billed
// session.
func marshalRecommend(ctx context.Context, service *app.MarshalService, runID string) (string, string) {
	rec, err := service.Recommend(ctx, runID, marshal.GoalAssessment{}, app.ModelInventory(nil, nil))
	if err != nil {
		return "codex", "no recommendation available; using codex"
	}
	for _, c := range rec.Candidates {
		provider := c.Provider
		if provider == "gemini" {
			provider = "agy"
		}
		switch provider {
		case "codex", "claude", "agy":
			return provider, fmt.Sprintf("recommended Marshal model: %s (%s)", provider, c.Model)
		}
	}
	return "codex", "no supported candidate in the inventory; using codex"
}

func (w *Workspace) marshalService(ctx context.Context, runID string) (*app.MarshalService, string, string, error) {
	if w.runtime == nil {
		return nil, "", "", errors.New("the MARSHAL runtime is not attached to this workspace")
	}
	m := w.marshalSession()
	m.mu.Lock()
	provider := m.provider
	m.mu.Unlock()
	note := ""
	if provider == "" {
		bare := w.runtime.Marshal()
		if bare == nil {
			return nil, "", "", errors.New("no project is open")
		}
		provider, note = marshalRecommend(ctx, bare, runID)
	}
	var gate marshal.CapabilityGate
	if w.ultra != nil {
		gate = w.ultra
	}
	service, err := w.runtime.MarshalWired(app.MarshalWiring{Provider: provider, Gate: gate, Approver: m.approver})
	return service, provider, note, err
}

// marshalReserve owns the session until the background operation finishes.
func (m *marshalSession) reserve() (context.Context, context.CancelFunc, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.busy {
		return nil, nil, errors.New("a Marshal operation is already running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.busy, m.cancel = true, cancel
	return ctx, cancel, nil
}

func (m *marshalSession) finish(cancel context.CancelFunc) {
	m.finishWithNativeTurn(cancel, false)
}

func (m *marshalSession) finishWithNativeTurn(cancel context.CancelFunc, nativeTurnWaiting bool) {
	m.mu.Lock()
	m.busy, m.cancel = false, nil
	m.mu.Unlock()
	if !nativeTurnWaiting {
		cancel()
	}
}

func (w *Workspace) marshalPublish(m *marshalSession, runID string, p *MarshalPanel) {
	m.mu.Lock()
	service := m.service
	current := m.runID == runID
	m.mu.Unlock()
	if !current {
		return
	}
	if service != nil {
		if events, err := service.Store.MarshalDecisions(context.Background(), runID); err == nil {
			p.Usage.Tokens.Known = true
			p.Usage.Money.Known = true
			for _, event := range events {
				d := event.Data
				if d["charge_phase"] == nil {
					continue
				}
				if known, _ := d["usage_known"].(bool); known {
					p.Usage.Tokens.Value += marshalNumber(d["usage_units"])
				} else {
					p.Usage.Tokens.Known = false
				}
				if known, _ := d["money_known"].(bool); known {
					p.Usage.Money.Value += marshalNumber(d["money"])
				} else {
					p.Usage.Money.Known = false
				}
				p.Usage.WallTime += time.Duration(marshalNumber(d["wall_ns"]))
			}
			if len(events) == 0 {
				p.Usage.Tokens.Known = false
				p.Usage.Money.Known = false
			}
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runID == runID {
		w.setMarshalPanel(p)
	}
}

func marshalNumber(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	default:
		return 0
	}
}

func (w *Workspace) marshalStart(ctx context.Context, goal string) (string, error) {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return marshalUsage, nil
	}
	m := w.marshalSession()
	runCtx, cancel, err := m.reserve()
	if err != nil {
		return "", err
	}
	runID := fmt.Sprintf("RUN-%d", time.Now().UTC().UnixNano())
	m.mu.Lock()
	m.runID, m.service, m.amended, m.pending = runID, nil, false, nil
	provider := m.provider
	m.approvals = nil
	m.mu.Unlock()
	w.marshalPublish(m, runID, &MarshalPanel{RunID: runID, Provider: provider, State: marshal.Drafting, Note: "preparing and drafting a plan…"})
	go func() {
		defer m.finish(cancel)
		service, selected, note, err := w.marshalService(runCtx, runID)
		if err != nil {
			w.marshalPublish(m, runID, &MarshalPanel{RunID: runID, State: marshal.Drafting, Note: "planning failed: " + err.Error()})
			return
		}
		m.mu.Lock()
		if m.runID != runID || runCtx.Err() != nil {
			m.mu.Unlock()
			return
		}
		m.service, m.provider = service, selected
		m.mu.Unlock()
		w.marshalPublish(m, runID, &MarshalPanel{RunID: runID, Provider: selected, State: marshal.Drafting, Note: "drafting a plan… " + note})
		run, err := service.StartPlanning(runCtx, runID, goal, marshal.Budget{})
		if runCtx.Err() != nil {
			return
		}
		if err != nil {
			w.marshalPublish(m, runID, &MarshalPanel{RunID: runID, Provider: selected, State: marshal.Drafting, Note: "planning failed: " + err.Error()})
			return
		}
		w.marshalPublish(m, runID, newMarshalPanel(runID, selected, run, fmt.Sprintf("plan drafted: %d tasks · /marshal approve to run it", len(run.Tasks))))
	}()
	return "Marshal is preparing and drafting a plan for: " + goal, nil
}

func (w *Workspace) marshalActive() (*marshalSession, *app.MarshalService, string, string, error) {
	m := w.marshalSession()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.service == nil || m.runID == "" {
		return m, nil, "", "", errors.New("no Marshal run; start one with /marshal <goal>")
	}
	return m, m.service, m.runID, m.provider, nil
}

func (w *Workspace) marshalUsePlan(ctx context.Context) (string, error) {
	m := w.marshalSession()
	runCtx, cancel, err := m.reserve()
	if err != nil {
		return "", err
	}
	started := false
	defer func() {
		if !started {
			m.finish(cancel)
		}
	}()
	runID := fmt.Sprintf("RUN-%d", time.Now().UTC().UnixNano())
	service, provider, _, err := w.marshalService(ctx, runID)
	if err != nil {
		return "", err
	}
	run, err := service.BindApprovedPlan(ctx, runID)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	m.runID, m.service, m.provider, m.amended, m.pending = runID, service, provider, false, nil
	m.approvals = nil
	m.mu.Unlock()
	w.marshalPublish(m, runID, newMarshalPanel(runID, provider, run, "approved Process 04 plan · running through Process 05"))
	started = true
	go func() {
		keepNativeTurn := false
		defer func() { m.finishWithNativeTurn(cancel, keepNativeTurn) }()
		keepNativeTurn = w.marshalExecute(runCtx, m, service, runID, provider)
	}()
	return "Marshal is executing the approved Process 04 plan through Process 05.", nil
}

func (w *Workspace) marshalApproveProcess05Task(ctx context.Context, args []string) (string, error) {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return "", errors.New("usage: /marshal approve-task <approval-id>")
	}
	_, service, runID, _, err := w.marshalActive()
	if err != nil {
		return "", err
	}
	run, err := service.Snapshot(ctx, runID)
	if err != nil {
		return "", err
	}
	if w.runtime == nil {
		return "", errors.New("Process 05 runtime is unavailable")
	}
	execService := w.runtime.Execution()
	runs, err := execService.ListRuns(ctx)
	if err != nil {
		return "", err
	}
	bound := false
	for _, p05 := range runs {
		bound = bound || marshalProcess05ApprovalBound(run, p05, string(service.CanonicalPlanProjectID()), args[0])
	}
	if !bound {
		return "", errors.New("approval does not belong to the active Marshal plan")
	}
	if err := execService.Approve(ctx, args[0], marshalOperator(), "approved in Marshal TUI"); err != nil {
		return "", err
	}
	return "Process 05 task approved. Use /marshal resume to continue.", nil
}

func marshalProcess05ApprovalBound(run marshal.Run, p05 execution.ExecutionRun, projectID, approvalID string) bool {
	if p05.PlanID != run.PlanID || p05.PlanVersion != run.PlanVersion || string(p05.ProjectID) != projectID || p05.Delivery != execution.DeliveryPreserveBranch || p05.BaseCommit != run.BaseCommit || p05.State != execution.RunNeedsApproval || len(p05.Tasks) != len(run.Tasks) {
		return false
	}
	found := false
	for _, task := range run.Tasks {
		p05task, ok := p05.Tasks[task.PlanTaskID]
		if !ok || task.Mode != marshal.Governed || task.Worker != p05task.AssignedHarness {
			return false
		}
		if p05task.State == execution.TaskNeedsApproval && p05task.ApprovalID == approvalID {
			found = true
		}
	}
	return found
}

func (w *Workspace) marshalApprove(ctx context.Context) (string, error) {
	m, service, runID, provider, err := w.marshalActive()
	if err != nil {
		return "", err
	}
	runCtx, cancel, err := m.reserve()
	if err != nil {
		return "", err
	}
	w.marshalPublish(m, runID, &MarshalPanel{RunID: runID, Provider: provider, State: marshal.Drafting, Note: "approving plan…"})
	go func() {
		defer m.finish(cancel)
		m.mu.Lock()
		pending := m.pending
		m.mu.Unlock()
		if pending != nil {
			if _, err := service.ApplyAmendDraftBound(runCtx, runID, pending.reason, pending.draft, pending.planVersion); err != nil {
				w.marshalPublish(m, runID, &MarshalPanel{RunID: runID, Provider: provider, State: marshal.Drafting, Note: "amendment failed: " + err.Error()})
				return
			}
			m.mu.Lock()
			m.pending = nil
			m.mu.Unlock()
		}
		m.grant(runID, "plan")
		run, err := service.Approve(runCtx, runID)
		if err != nil {
			w.marshalPublish(m, runID, newMarshalPanel(runID, provider, run, "approval failed: "+err.Error()))
			return
		}
		m.mu.Lock()
		m.amended = false
		m.mu.Unlock()
		w.marshalPublish(m, runID, newMarshalPanel(runID, provider, run, "approved · running"))
		_ = w.marshalExecute(runCtx, m, service, runID, provider)
	}()
	return "Plan approval started; the panel will show the result.", nil
}

// marshalExecute runs while the caller owns the reserved operation.
func (w *Workspace) marshalExecute(ctx context.Context, m *marshalSession, service *app.MarshalService, runID, provider string) bool {
	run, err := service.Execute(ctx, runID, marshalTaskBrief, func(r marshal.Run) {
		if ctx.Err() == nil {
			w.marshalPublish(m, runID, newMarshalPanel(runID, provider, r, "running"))
		}
	})
	w.marshalPublish(m, runID, newMarshalPanel(runID, provider, run, marshalOutcomeNote(run, err)))
	return w.marshalNativeApprovalWaiting(service, run)
}

func (w *Workspace) marshalNativeApprovalWaiting(service *app.MarshalService, run marshal.Run) bool {
	if w.runtime == nil || len(run.Tasks) == 0 {
		return false
	}
	runs, err := w.runtime.Execution().ListRuns(context.Background())
	if err != nil {
		return false
	}
	for _, p05 := range runs {
		if p05.PlanID != run.PlanID || p05.PlanVersion != run.PlanVersion || string(p05.ProjectID) != string(service.CanonicalPlanProjectID()) || p05.State != execution.RunNeedsApproval {
			continue
		}
		for _, task := range p05.Tasks {
			if task.State == execution.TaskNeedsApproval && task.NativeTurn != nil {
				return true
			}
		}
	}
	return false
}

// marshalOutcomeNote says what the run is waiting for once Execute returns.
func marshalOutcomeNote(run marshal.Run, err error) string {
	if err != nil {
		return "stopped: " + err.Error()
	}
	switch run.State {
	case marshal.Verifying:
		return "verified · /marshal close to move the target branch"
	case marshal.AwaitingUser:
		return "waiting for you · see the escalated task, then /marshal amend or /marshal resume"
	case marshal.Closed:
		return "closed"
	default:
		return string(run.State)
	}
}

// marshalTaskBrief is the instruction a worker receives: its task, the files
// it may change and the checks its work will be judged by.
func marshalTaskBrief(t marshal.Task) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are a worker on MARSHAL task %s.\n", t.PlanTaskID)
	if t.Title != "" {
		fmt.Fprintf(&b, "Task: %s\n", t.Title)
	}
	if len(t.Criteria) > 0 {
		b.WriteString("Acceptance criteria:\n")
		for _, c := range t.Criteria {
			b.WriteString("- " + c + "\n")
		}
	}
	if len(t.Files) > 0 {
		b.WriteString("You may change only these files: " + strings.Join(t.Files, ", ") + "\n")
	}
	if len(t.Checks) > 0 {
		b.WriteString("Your work will be judged by these commands, run by MARSHAL:\n")
		for _, c := range t.Checks {
			b.WriteString("- " + c.Command + "\n")
		}
	}
	b.WriteString("Make the change in this directory. Do not push and do not commit; MARSHAL records your work.")
	return b.String()
}

// marshalClose is the person's approval to move the target branch.
func (w *Workspace) marshalClose(ctx context.Context) (string, error) {
	m, service, runID, provider, err := w.marshalActive()
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	pending := m.pending != nil
	m.mu.Unlock()
	if pending {
		return "", errors.New("decide the proposed amendment before closing")
	}
	runCtx, cancel, err := m.reserve()
	if err != nil {
		return "", err
	}
	go func() {
		defer m.finish(cancel)
		m.grant(runID, "close")
		if err := service.Close(runCtx, runID); err != nil {
			w.marshalPublish(m, runID, &MarshalPanel{RunID: runID, Provider: provider, Note: "close failed: " + err.Error()})
			return
		}
		if run, err := service.Resume(runCtx, runID); err == nil {
			w.marshalPublish(m, runID, newMarshalPanel(runID, provider, run, "closed"))
		}
	}()
	return "Closing Marshal run; the panel will show the result.", nil
}

func (w *Workspace) marshalStop() string {
	m := w.marshalSession()
	m.mu.Lock()
	cancel := m.cancel
	m.mu.Unlock()
	if cancel == nil {
		return "No Marshal operation is running."
	}
	cancel()
	return "Stopping the Marshal operation. Its state is kept."
}

func (w *Workspace) marshalResume(ctx context.Context) (string, error) {
	m, service, runID, provider, err := w.marshalActive()
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	pending := m.pending != nil
	m.mu.Unlock()
	if pending {
		return "", errors.New("decide the proposed amendment before resuming")
	}
	runCtx, cancel, err := m.reserve()
	if err != nil {
		return "", err
	}
	go func() {
		keepNativeTurn := false
		defer func() { m.finishWithNativeTurn(cancel, keepNativeTurn) }()
		run, err := service.Resume(runCtx, runID)
		if err != nil {
			w.marshalPublish(m, runID, newMarshalPanel(runID, provider, run, "resume failed: "+err.Error()))
			return
		}
		w.marshalPublish(m, runID, newMarshalPanel(runID, provider, run, "resuming"))
		keepNativeTurn = w.marshalExecute(runCtx, m, service, runID, provider)
	}()
	return "Resuming Marshal run " + runID + ".", nil
}

func (w *Workspace) marshalAmend(ctx context.Context, reason string) (string, error) {
	if strings.TrimSpace(reason) == "" {
		return "", errors.New("usage: /marshal amend <reason>")
	}
	m, service, runID, provider, err := w.marshalActive()
	if err != nil {
		return "", err
	}
	runCtx, cancel, err := m.reserve()
	if err != nil {
		return "", err
	}
	go func() {
		defer m.finish(cancel)
		draft, major, err := service.ProposeAmend(runCtx, runID, reason)
		if err != nil {
			w.marshalPublish(m, runID, &MarshalPanel{RunID: runID, Provider: provider, Note: "amendment failed: " + err.Error()})
			return
		}
		note := "amended in scope"
		if major {
			run, err := service.Resume(runCtx, runID)
			if err != nil {
				w.marshalPublish(m, runID, &MarshalPanel{RunID: runID, Provider: provider, Note: "amendment failed: " + err.Error()})
				return
			}
			m.mu.Lock()
			m.amended = true
			m.pending = &marshalAmendment{draft: draft, reason: reason, planVersion: run.PlanVersion}
			m.mu.Unlock()
			note = "major amendment proposed · /marshal amend approve or deny"
			preview := run
			preview.Tasks = draft.Tasks
			preview.State = marshal.Drafting
			w.marshalPublish(m, runID, newMarshalPanel(runID, provider, preview, note))
			return
		}
		run, err := service.ApplyAmendDraft(runCtx, runID, reason, draft)
		if err != nil {
			w.marshalPublish(m, runID, newMarshalPanel(runID, provider, run, "amendment failed: "+err.Error()))
			return
		}
		w.marshalPublish(m, runID, newMarshalPanel(runID, provider, run, note))
	}()
	return "Amendment started; the panel will show the result.", nil
}

func (w *Workspace) marshalAmendApprove(ctx context.Context) (string, error) {
	m := w.marshalSession()
	m.mu.Lock()
	amended := m.amended
	m.mu.Unlock()
	if !amended {
		return "", errors.New("no major amendment awaits approval")
	}
	return w.marshalApprove(ctx)
}

func (w *Workspace) marshalAmendDeny() (string, error) {
	m := w.marshalSession()
	m.mu.Lock()
	if !m.amended || m.busy {
		m.mu.Unlock()
		return "", errors.New("no major amendment awaits a decision")
	}
	m.amended = false
	m.pending = nil
	runID := m.runID
	service, provider := m.service, m.provider
	m.mu.Unlock()
	if service != nil {
		if run, err := service.Snapshot(context.Background(), runID); err == nil {
			w.marshalPublish(m, runID, newMarshalPanel(runID, provider, run, "amendment denied · original plan remains active"))
			return "Amendment denied. The original plan remains active.", nil
		}
	}
	p := w.marshalPanel()
	if p != nil && p.RunID == runID {
		copy := *p
		copy.Note = "amendment denied"
		w.marshalPublish(m, runID, &copy)
	}
	return "Amendment denied.", nil
}

func (w *Workspace) marshalAccept(args []string) (string, error) {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return "", errors.New("usage: /marshal accept <task>")
	}
	m := w.marshalSession()
	m.mu.Lock()
	runID := m.runID
	m.mu.Unlock()
	p := w.marshalPanel()
	if runID == "" || p == nil || p.RunID != runID {
		return "", errors.New("no Marshal run")
	}
	for _, task := range p.Tasks {
		if task.ID == args[0] {
			m.grant(runID, args[0])
			return "Approved task " + args[0] + " once; /marshal resume to continue.", nil
		}
	}
	return "", fmt.Errorf("task %q is not in the active run", args[0])
}

// marshalSettings shows or changes the project's Marshal settings.
func (w *Workspace) marshalSettings(ctx context.Context, args []string) (string, error) {
	if w.runtime == nil {
		return "", errors.New("the MARSHAL runtime is not attached to this workspace")
	}
	service := w.runtime.Marshal()
	if service == nil {
		return "", errors.New("no project is open")
	}
	record, err := service.Store.GetMarshalSettings(ctx, service.ProjectID)
	if err != nil {
		return "", err
	}
	s := record.Value
	if len(args) == 0 {
		return fmt.Sprintf("execution-rights %s\nacceptance-mode %s\nrework-limit %d\nultra-concurrency %d",
			s.ExecutionRights, s.AcceptanceMode, s.ReworkLimit, s.UltraConcurrency), nil
	}
	if len(args) != 2 {
		return "", errors.New("usage: /marshal settings <key> <value>")
	}
	switch args[0] {
	case "execution-rights":
		s.ExecutionRights = marshal.ExecutionRights(args[1])
	case "acceptance-mode":
		s.AcceptanceMode = marshal.AcceptanceMode(args[1])
	case "rework-limit", "ultra-concurrency":
		n, err := strconv.Atoi(args[1])
		if err != nil {
			return "", fmt.Errorf("%s needs a number", args[0])
		}
		if args[0] == "rework-limit" {
			s.ReworkLimit = n
		} else {
			s.UltraConcurrency = n
		}
	default:
		return "", fmt.Errorf("unknown setting %q", args[0])
	}
	if _, err := service.Store.SetMarshalSettings(ctx, service.ProjectID, s, record.Revision); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s set to %s. It applies to the next Marshal run.", args[0], args[1]), nil
}

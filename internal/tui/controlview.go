package tui

// The interactive Control surface: how a key press becomes a canonical
// mutation, and how it is prevented from becoming one accidentally.
//
// The rules enforced here are the keyboard half of the governance contract:
//
//   - Enter on a list row never mutates. It moves focus to the action bar.
//   - Enter on the action bar opens a confirmation. It does not submit.
//   - Only Enter on Proceed, inside a confirmation, submits — once.
//   - Every confirmation starts on Cancel; destructive ones also need 'y'.
//   - Esc dismisses a confirmation and mutates nothing.
//   - A submission already in flight accepts no further keys.
//
// That is four deliberate keystrokes between a list row and a destructive
// mutation, and none of them can be reached by holding a key down.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/execution"
)

// controlState is the Control half of the navigation view.
type controlState struct {
	source  *ControlSource
	confirm *Confirmation
	form    *actionForm
	snap    ControlSnapshot
	// snapValid distinguishes "never read" from "read and found nothing".
	snapValid bool
	// listIndex is the selected row within an approval or checkpoint list.
	listIndex int

	// work is the Work section's read state, refreshed alongside Control so
	// the two sections are observed at the same instant.
	work      *WorkSource
	workSnap  WorkSnapshot
	workValid bool

	// verify is the Process 06 read state, refreshed alongside the rest so
	// every section is observed at the same instant.
	verify      *VerifySource
	verifySnap  VerifySnapshot
	verifyValid bool

	// memory is the Process 07 read state.
	memory      *MemoryFeed
	memorySnap  MemorySnapshot
	memoryValid bool

	// models is the Process 08 and provider read state.
	models      *ModelsFeed
	modelsSnap  ModelsSnapshot
	modelsValid bool

	// security is the constitution, authorization and isolation read state.
	security      *SecurityFeed
	securitySnap  SecuritySnapshot
	securityValid bool

	// system is the runtime/store/resource read state.
	system      *SystemFeed
	systemSnap  SystemSnapshot
	systemValid bool
}

// AttachSystem supplies the canonical System readers.
func (v *NavView) AttachSystem(feed *SystemFeed) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.control.system = feed
}

// refreshSystem re-reads System state, outside the view lock.
func (v *NavView) refreshSystem(ctx context.Context) (SystemSnapshot, bool) {
	v.mu.Lock()
	feed := v.control.system
	v.mu.Unlock()
	if feed == nil {
		return SystemSnapshot{}, false
	}
	return feed.ReadSystem(ctx), true
}

// AttachSecurity supplies the canonical security readers.
func (v *NavView) AttachSecurity(feed *SecurityFeed) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.control.security = feed
}

// refreshSecurity re-reads security state, outside the view lock.
func (v *NavView) refreshSecurity(ctx context.Context) (SecuritySnapshot, bool) {
	v.mu.Lock()
	feed := v.control.security
	v.mu.Unlock()
	if feed == nil {
		return SecuritySnapshot{}, false
	}
	return feed.ReadSecurity(ctx), true
}

// AttachModels supplies the canonical Models readers.
func (v *NavView) AttachModels(feed *ModelsFeed) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.control.models = feed
}

// refreshModels re-reads Models state, outside the view lock.
func (v *NavView) refreshModels(ctx context.Context) (ModelsSnapshot, bool) {
	v.mu.Lock()
	feed := v.control.models
	v.mu.Unlock()
	if feed == nil {
		return ModelsSnapshot{}, false
	}
	return feed.ReadModels(ctx), true
}

// AttachMemory supplies the canonical Process 07 readers.
func (v *NavView) AttachMemory(feed *MemoryFeed) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.control.memory = feed
}

// refreshMemory re-reads Process 07 state, outside the view lock.
func (v *NavView) refreshMemory(ctx context.Context) (MemorySnapshot, bool) {
	v.mu.Lock()
	feed := v.control.memory
	v.mu.Unlock()
	if feed == nil {
		return MemorySnapshot{}, false
	}
	return feed.ReadMemory(ctx), true
}

// AttachVerify supplies the canonical Process 06 readers.
func (v *NavView) AttachVerify(source *VerifySource) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.control.verify = source
}

// refreshVerify re-reads Process 06 state, outside the view lock.
func (v *NavView) refreshVerify(ctx context.Context) (VerifySnapshot, bool) {
	v.mu.Lock()
	source := v.control.verify
	v.mu.Unlock()
	if source == nil {
		return VerifySnapshot{}, false
	}
	return source.ReadVerify(ctx), true
}

// AttachWork supplies the canonical Work readers.
func (v *NavView) AttachWork(source *WorkSource) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.control.work = source
}

// refreshWork re-reads Work state. Called from Refresh, outside the lock so a
// slow canonical read cannot freeze the paint path.
func (v *NavView) refreshWork(ctx context.Context) (WorkSnapshot, bool) {
	v.mu.Lock()
	source := v.control.work
	v.mu.Unlock()
	if source == nil {
		return WorkSnapshot{}, false
	}
	return source.ReadWork(ctx), true
}

// AttachControl supplies the canonical mutation authority.
func (v *NavView) AttachControl(source *ControlSource) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.control.source = source
	if v.control.confirm == nil {
		v.control.confirm = &Confirmation{}
	}
}

// Confirmation exposes the pending mutation, for tests and rendering.
func (v *NavView) Confirmation() *Confirmation {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.control.confirm == nil {
		v.control.confirm = &Confirmation{}
	}
	return v.control.confirm
}

// ControlSnapshotOf returns the last Control read.
func (v *NavView) ControlSnapshotOf() ControlSnapshot {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.control.snap
}

// refreshControl re-reads Control state. Called from Refresh, outside the lock.
func (v *NavView) refreshControl(ctx context.Context) (ControlSnapshot, bool) {
	v.mu.Lock()
	source := v.control.source
	v.mu.Unlock()
	if source == nil {
		return ControlSnapshot{}, false
	}
	return source.ReadControl(ctx), true
}

// confirmOpen reports whether a confirmation owns the keyboard.
func (v *NavView) confirmOpen() bool {
	c := v.control.confirm
	if c == nil {
		return false
	}
	switch c.Phase() {
	case PhaseConfirming, PhaseSubmitting, PhaseDone, PhasePreparing:
		return true
	}
	return false
}

func (v *NavView) formOpen() bool {
	return v.control.form != nil && v.control.form.open
}

func (v *NavView) handleActionFormKey(ctx context.Context, event KeyEvent) bool {
	f := v.control.form
	if f == nil || !f.open {
		return false
	}
	switch event.Type {
	case KeyEsc:
		f.cancel()
		v.status, v.statusSeen = "form cancelled; nothing was submitted", false
	case KeyUp, KeyShiftTab:
		f.move(-1)
	case KeyDown, KeyTab:
		f.move(1)
	case KeyBackspace:
		f.backspace()
	case KeyCtrlU:
		if field, ok := f.current(); ok {
			f.values[field.Key] = ""
		}
	case KeyPaste:
		f.append(event.Paste)
	case KeyRune:
		f.append(string(event.Rune))
	case KeyEnter:
		if f.index < len(f.binding.Inputs)-1 {
			f.move(1)
			return true
		}
		req, err := f.build()
		if err != nil {
			v.status, v.statusSeen = err.Error(), false
			return true
		}
		f.open = false
		if err := v.control.confirm.Begin(ctx, f.binding, req); err != nil {
			v.status, v.statusSeen = err.Error(), false
		}
	}
	return true
}

// handleConfirmKey processes a key while a confirmation is open.
//
// It is called with the view lock held. The confirmation has its own lock, so
// the only shared state touched here is the view's status line.
func (v *NavView) handleConfirmKey(ctx context.Context, event KeyEvent) bool {
	c := v.control.confirm
	phase := c.Phase()

	// A submission in flight accepts nothing. This is what makes a repeated
	// Enter, or a key repeat, incapable of running a mutation twice.
	if phase == PhaseSubmitting {
		v.status, v.statusSeen = "a submission is in flight; further keys are ignored", false
		return true
	}

	// A finished confirmation is dismissed by any of Esc, Enter or q. Nothing
	// here re-submits.
	if phase == PhaseDone {
		switch event.Type {
		case KeyEsc, KeyEnter:
			c.Dismiss()
			return true
		case KeyRune:
			if event.Rune == 'q' {
				c.Dismiss()
				return true
			}
		}
		return true
	}

	if phase == PhasePreparing {
		// Preparation is a read; Esc abandons it.
		if event.Type == KeyEsc {
			c.Cancel()
		}
		return true
	}

	switch event.Type {
	case KeyEsc:
		// Dismissal mutates nothing, which is the property Esc must always have.
		c.Cancel()
		v.status, v.statusSeen = "cancelled; nothing was submitted", false
		return true

	case KeyLeft:
		c.MoveSelection(-1)
		return true
	case KeyRight:
		c.MoveSelection(1)
		return true
	case KeyTab:
		// Tab toggles between Cancel and Proceed.
		if c.Selection() == 1 {
			c.MoveSelection(-1)
		} else {
			c.MoveSelection(1)
		}
		return true

	case KeyEnter:
		if c.Selection() != 1 {
			// Enter while the selection sits on Cancel is a held key, not a
			// decision: a destructive confirmation opens on Cancel precisely
			// so the next repeat lands here. Dismissing on it would make a
			// held Enter toggle the dialog open and shut, so instead it says
			// what is needed and leaves the confirmation standing.
			v.status, v.statusSeen =
				"on Cancel — press → or Tab to reach Proceed, or Esc to dismiss", false
			return true
		}
		v.submitConfirmation(ctx, c)
		return true
	}

	if event.Type == KeyRune {
		switch event.Rune {
		case 'y', 'Y':
			// The explicit acknowledgement a destructive action requires. It
			// is deliberately not Enter: a key repeat must not supply it.
			c.Acknowledge()
			v.status, v.statusSeen = "acknowledged; choose Proceed to submit", false
			return true
		case 'n', 'N', 'q':
			c.Cancel()
			v.status, v.statusSeen = "cancelled; nothing was submitted", false
			return true
		}
	}
	return true
}

// submitConfirmation performs the mutation and records what happened.
//
// It runs with the view lock held, which serialises it against the paint path.
// The mutation itself may be slow; that is acceptable here because the
// alternative — releasing the lock mid-submission — would let a second key
// press observe PhaseConfirming and submit again.
func (v *NavView) submitConfirmation(ctx context.Context, c *Confirmation) {
	outcome, err := c.Submit(ctx)
	switch {
	case err != nil && outcome.Verdict == "":
		v.status, v.statusSeen = fmt.Sprintf("refused: %s", err), false
	case outcome.Verdict == VerdictPass && outcome.Proof.Status.IsSuccess():
		v.status, v.statusSeen = outcome.Detail, false
	case outcome.Detail != "":
		v.status, v.statusSeen = outcome.Detail, false
	default:
		v.status, v.statusSeen = "the authority returned no result", false
	}
}

// beginAction starts a confirmation for the selected action.
//
// This is the only path from a key press to a mutation, and it always goes
// through a confirmation: there is deliberately no way to submit an action
// without one appearing first.
func (v *NavView) beginAction(ctx context.Context, node *Node) bool {
	if v.control.source == nil {
		v.status, v.statusSeen = "no execution authority is attached to this workspace", false
		return true
	}
	bindings := v.control.source.Bindings()
	binding, known := bindings[ActionID(node.SpecID)]
	if !known {
		v.status, v.statusSeen = fmt.Sprintf(
			"%s has no binding in this build", node.SpecID), false
		return true
	}

	// A gap never reaches a confirmation. The reason is shown instead, so the
	// user learns what is missing rather than watching a dialog refuse them.
	if !binding.Bound() {
		reason := binding.Gap
		if reason == "" {
			reason = binding.Requires
		}
		if reason == "" {
			reason = ErrNoBinding.Error()
		}
		v.status, v.statusSeen = reason, false
		return true
	}

	req := ActionRequest{
		Action:    ActionID(node.SpecID),
		Title:     binding.Title,
		Safety:    binding.Safety,
		SessionID: v.control.source.SessionID,
		ProjectID: v.control.source.ProjectID,
	}
	if len(binding.Inputs) > 0 {
		v.control.form = newActionForm(binding, req)
		v.status, v.statusSeen = "complete the typed action form; Enter advances", false
		return true
	}
	// A governed action carries the approval it consumes. The id comes from
	// the selected approval, so the decision is bound to one exact record
	// rather than to whatever the queue happens to hold.
	if binding.RequiresApproval {
		req.ApprovalID = v.control.source.selectedApproval
	}

	if err := v.control.confirm.Begin(ctx, binding, req); err != nil {
		v.status, v.statusSeen = err.Error(), false
	}
	return true
}

// selectControlRecord binds the list selection to the record actions act on.
//
// Without this, "Approve" would mean "approve whatever is first", which is the
// generic behaviour the contract forbids.
func (v *NavView) selectControlRecord(node *Node) {
	if v.control.source == nil || !v.control.snapValid {
		return
	}
	path := node.MenuPath
	switch {
	case strings.Contains(path, "/ Approvals"):
		if len(v.control.snap.Approvals) == 0 {
			return
		}
		i := clampIndex(v.control.listIndex, len(v.control.snap.Approvals))
		v.control.source.SelectApproval(v.control.snap.Approvals[i].ApprovalID)
	case strings.Contains(path, "/ Checkpoints"), strings.Contains(path, "/ Rollback & Recovery"):
		if len(v.control.snap.Checkpoints) == 0 {
			return
		}
		i := clampIndex(v.control.listIndex, len(v.control.snap.Checkpoints))
		v.control.source.SelectCheckpoint(v.control.snap.Checkpoints[i].CheckpointID)
	}
}

func clampIndex(i, n int) int {
	if n == 0 {
		return 0
	}
	if i < 0 {
		return 0
	}
	if i >= n {
		return n - 1
	}
	return i
}

// renderConfirmation draws the pending mutation.
func (v *NavView) renderConfirmation(c *Confirmation, width, rows int) []string {
	phase := c.Phase()
	binding := c.Binding()

	out := []string{
		truncate(fmt.Sprintf("── Confirm: %s ──", communityLabel(binding.Title)), width),
		"",
	}

	switch phase {
	case PhasePreparing:
		out = append(out, wrap("Reading the canonical target…", width)...)
		return out

	case PhaseDone:
		out = append(out, truncate("Result", width), "")
		for _, line := range RenderFields(OutcomeFields(c.Outcome()), width) {
			out = append(out, truncate(line, width))
		}
		out = append(out, "", truncate("Esc or Enter to dismiss", width))
		return out

	case PhaseSubmitting:
		out = append(out, wrap(
			"Submitting to the canonical authority. Further keys are ignored "+
				"until it answers, so this cannot run twice.", width)...)
		return out
	}

	// Confirming: show the exact request that will be submitted.
	for _, line := range RenderFields(ConfirmationFields(c), width) {
		out = append(out, truncate(line, width))
	}

	if warning, ok := ConfirmationWarning(c); ok {
		out = append(out, "")
		out = append(out, wrap(warning.Display(), width)...)
	}

	if binding.Safety == SafetyDestructive {
		out = append(out, "")
		if c.Acknowledged() {
			out = append(out, truncate("[✓] acknowledged", width))
		} else {
			out = append(out, truncate("[ ] press 'y' to acknowledge", width))
		}
	}

	out = append(out, "")
	cancel, proceed := "  Cancel  ", "  Proceed  "
	if c.Selection() == 0 {
		cancel = " ▸Cancel  "
	} else {
		proceed = " ▸Proceed  "
	}
	out = append(out, truncate(cancel+proceed, width))
	out = append(out, "")
	out = append(out, wrap(
		"← → or Tab to choose · Enter to confirm · Esc to cancel", width)...)

	if len(out) > rows {
		out = out[:rows]
	}
	return out
}

// renderControlDetail draws a Control screen's own content.
//
// It returns false when the node is not a Control screen with special
// rendering, so the ordinary screen renderer handles it.
func (v *NavView) renderControlDetail(node *Node, width, rows int) ([]string, bool) {
	if !v.control.snapValid {
		return nil, false
	}
	// The registry is also used by acceptance coverage. Do not infer a
	// renderer from a CTUI id range: a Control node renders here only when this
	// dispatcher has an explicit implementation for it.
	if sectionTitleOf(node) == "Control" && !hasControlScreen(node.SpecID) {
		return nil, false
	}
	snap := v.control.snap
	path := node.MenuPath
	fields := func(title string, values []Field) ([]string, bool) {
		out := []string{truncate(title, width), ""}
		for _, line := range RenderFields(values, width) {
			out = append(out, truncate(line, width))
		}
		if len(out) > rows {
			out = out[:rows]
		}
		return out, true
	}

	switch {
	case node.SpecID == "CTUI-0029":
		return fields("Control", controlOverviewFields(snap))
	// CTUI-0036 is deliberately absent: it is a CROSS_LINK to the authority
	// summary that Security owns. Rendering Mode & Autonomy fields under it
	// would make a cross-link display content of its own instead of opening
	// its one canonical owner.
	case node.SpecID == "CTUI-0030":
		return fields("Mode & Autonomy", []Field{
			{Label: "Entitlement mode", Value: snap.Mode},
			{Label: "Autonomy", Value: snap.Autonomy},
			{Label: "ULTRA execution preference", Value: snap.ULTRAPreference},
		})
	case node.SpecID == "CTUI-0037" || node.SpecID == "CTUI-0049":
		return fields("Execution", []Field{
			{Label: "Plan", Value: snap.Plan},
			{Label: "Run", Value: snap.Run},
			{Label: "Tasks", Value: snap.Tasks},
		})
	case strings.HasSuffix(path, "/ Approvals / Pending queue"),
		strings.HasSuffix(path, "/ Control / Approvals"):
		return v.renderApprovalQueue(snap, width, rows), true
	case node.SpecID == "CTUI-0055" || node.SpecID == "CTUI-0056" || node.SpecID == "CTUI-0057":
		return v.renderApprovalKind(snap, node.SpecID, width, rows), true

	case strings.Contains(path, "/ Approvals / Exact action"),
		strings.Contains(path, "/ Approvals / Bound diff"),
		strings.Contains(path, "/ Approvals / Expiry"):
		return v.renderSelectedApproval(snap, width, rows), true
	case node.SpecID == "CTUI-0063":
		return v.renderApprovalHistory(snap, width, rows), true

	case strings.HasSuffix(path, "/ Checkpoints / List and inspect checkpoints"),
		strings.HasSuffix(path, "/ Control / Checkpoints"):
		return v.renderCheckpointList(snap, width, rows), true

	case strings.Contains(path, "/ Checkpoints / Checkpoint diff and integrity"):
		return v.renderSelectedCheckpoint(snap, width, rows), true

	case node.SpecID == "CTUI-0068":
		return v.renderRollbackHistory(snap, width, rows), true

	case node.SpecID == "CTUI-0069":
		return fields("Rollback & Recovery", []Field{
			{Label: "Checkpoints", Value: snap.CheckpointsStatus},
			{Label: "Active canaries", Value: snap.Canaries},
			{Label: "Run", Value: snap.Run},
		})

	case node.SpecID == "CTUI-0076":
		return fields("Budgets", budgetFields(snap))

	case node.SpecID == "CTUI-0084" || node.SpecID == "CTUI-0085" || node.SpecID == "CTUI-0086":
		return fields("Termination", terminationFields(snap))
	}

	// Work screens render from their own snapshot, which is refreshed
	// alongside Control so the two are read at the same instant.
	if v.control.workValid {
		if content, ok := RenderWorkNode(node, v.control.workSnap); ok {
			return v.renderScreenContent(content, width, rows), true
		}
	}
	// Verify and Memory likewise. Each section owns its own snapshot, all
	// refreshed together so no screen shows two different instants.
	if v.control.verifyValid {
		if content, ok := RenderVerifyNode(node, v.control.verifySnap); ok {
			return v.renderScreenContent(content, width, rows), true
		}
	}
	if v.control.memoryValid {
		if content, ok := RenderMemoryNode(node, v.control.memorySnap); ok {
			return v.renderScreenContent(content, width, rows), true
		}
	}
	if v.control.modelsValid {
		if content, ok := RenderModelsNode(node, v.control.modelsSnap); ok {
			return v.renderScreenContent(content, width, rows), true
		}
	}
	if v.control.securityValid {
		if content, ok := RenderSecurityNode(node, v.control.securitySnap); ok {
			return v.renderScreenContent(content, width, rows), true
		}
	}
	if v.control.systemValid {
		if content, ok := RenderSystemNode(node, v.control.systemSnap); ok {
			return v.renderScreenContent(content, width, rows), true
		}
	}
	return nil, false
}

// controlScreens is the explicit registry of Control detail renderers.
// Keeping it beside the dispatcher makes coverage mechanically falsifiable:
// deleting a renderer requires deleting its registry entry or its test fails.
func controlScreens() map[string]struct{} {
	return map[string]struct{}{
		"CTUI-0029": {},
		"CTUI-0030": {},
		"CTUI-0037": {}, "CTUI-0049": {},
		"CTUI-0053": {}, "CTUI-0054": {}, "CTUI-0055": {}, "CTUI-0056": {}, "CTUI-0057": {},
		"CTUI-0058": {}, "CTUI-0059": {}, "CTUI-0062": {}, "CTUI-0063": {},
		"CTUI-0064": {}, "CTUI-0066": {}, "CTUI-0067": {}, "CTUI-0068": {},
		"CTUI-0069": {},
		"CTUI-0076": {},
		"CTUI-0084": {}, "CTUI-0085": {}, "CTUI-0086": {},
	}
}

func hasControlScreen(specID string) bool {
	_, ok := controlScreens()[specID]
	return ok
}

// renderScreenContent lays out a ScreenContent.
//
// It is shared by the Work and Status paths so the two cannot drift apart in
// how they present a value or its truth status.
func (v *NavView) renderScreenContent(content ScreenContent, width, rows int) []string {
	out := make([]string, 0, rows)
	if content.HasNotice {
		out = append(out, wrap(content.Notice.Display(), width)...)
		out = append(out, "")
	}
	for _, line := range RenderFields(content.Fields, width) {
		out = append(out, truncate(line, width))
	}
	if len(content.CrossLinks) > 0 {
		out = append(out, "", truncate("Corrective screens:", width))
		for _, link := range content.CrossLinks {
			out = append(out, truncate("  \u2192 "+link.Label, width))
		}
	}
	if content.ReadOnly {
		out = append(out, "", truncate("[read-only screen]", width))
	}
	if len(out) > rows {
		out = out[:rows]
	}
	return out
}

func (v *NavView) renderApprovalHistory(snap ControlSnapshot, width, rows int) []string {
	out := []string{truncate("Decision history", width), ""}
	if len(snap.ResolvedApprovals) == 0 {
		return append(out, wrap(snap.ResolvedApprovalsStatus.Display(), width)...)
	}
	for _, approval := range snap.ResolvedApprovals {
		when := approval.CreatedAt
		if approval.ResolvedAt != nil {
			when = *approval.ResolvedAt
		}
		out = append(out, truncate(fmt.Sprintf("%s  %s  %s  %s",
			when.UTC().Format(time.RFC3339), approval.ApprovalID,
			approval.OperationType, approval.Status), width))
	}
	if len(out) > rows {
		out = out[:rows]
	}
	return out
}

func controlOverviewFields(snap ControlSnapshot) []Field {
	return []Field{
		{Label: "Mode", Value: snap.Mode},
		{Label: "Autonomy", Value: snap.Autonomy},
		{Label: "Goal", Value: snap.Goal},
		{Label: "Plan", Value: snap.Plan},
		{Label: "Run", Value: snap.Run},
		{Label: "Approvals", Value: snap.ApprovalsStatus},
		{Label: "Checkpoints", Value: snap.CheckpointsStatus},
		{Label: "Budget", Value: snap.BudgetStatus},
		{Label: "Termination", Value: snap.TerminationStatus},
	}
}

func budgetFields(snap ControlSnapshot) []Field {
	fields := []Field{{Label: "Budget record", Value: snap.BudgetStatus}, {Label: "Goal", Value: snap.Goal}}
	if snap.Budget == nil {
		return fields
	}
	b := snap.Budget
	tokens := Unknown("the provider has not reported token consumption", "internal/store.GetBudgetTracker")
	if b.TotalTokens != nil && b.HasReportedTokens {
		tokens = Known(fmt.Sprintf("%d", *b.TotalTokens), "internal/store.GetBudgetTracker")
	}
	cost := Unknown("the provider has not reported cost consumption", "internal/store.GetBudgetTracker")
	if b.CostUSD != nil && b.HasReportedCost {
		cost = Known(fmt.Sprintf("$%.4f", *b.CostUSD), "internal/store.GetBudgetTracker")
	}
	return append(fields,
		Field{Label: "Tokens consumed", Value: tokens},
		Field{Label: "Cost consumed", Value: cost},
		Field{Label: "Duration", Value: Known(b.Duration.String(), "internal/store.GetBudgetTracker")},
		Field{Label: "Model calls", Value: Known(fmt.Sprintf("%d", b.ModelCalls), "internal/store.GetBudgetTracker")},
		Field{Label: "Handoffs", Value: Known(fmt.Sprintf("%d", b.Handoffs), "internal/store.GetBudgetTracker")},
		Field{Label: "Retries", Value: Known(fmt.Sprintf("%d", b.Retries), "internal/store.GetBudgetTracker")},
	)
}

func terminationFields(snap ControlSnapshot) []Field {
	fields := []Field{{Label: "Termination record", Value: snap.TerminationStatus}, {Label: "Goal", Value: snap.Goal}}
	if snap.Termination == nil {
		return fields
	}
	t := snap.Termination
	return append(fields,
		Field{Label: "State", Value: Known(string(t.State), "internal/store.GetGoalTermination")},
		Field{Label: "Reason", Value: Known(string(t.ReasonCode), "internal/store.GetGoalTermination")},
		Field{Label: "Detail", Value: Known(t.ReasonDetail, "internal/store.GetGoalTermination")},
		Field{Label: "Checkpoint", Value: Known(t.CheckpointID, "internal/store.GetGoalTermination")},
		Field{Label: "Completed", Value: Known(t.CompletedAt.UTC().Format(time.RFC3339), "internal/store.GetGoalTermination")},
	)
}

func (v *NavView) renderApprovalKind(snap ControlSnapshot, specID string, width, rows int) []string {
	want := map[string]string{"CTUI-0055": "GOAL_CONFIRM", "CTUI-0056": "PLAN_APPROVE"}[specID]
	var matches []*execution.RuntimeApproval
	for _, approval := range snap.Approvals {
		if want == "" || approval.OperationType == want {
			matches = append(matches, approval)
		}
	}
	if len(matches) == 0 {
		return append([]string{truncate("Canonical approval", width), ""},
			wrap(NotRun("no matching approval is pending", controlSource).Display(), width)...)
	}
	out := []string{truncate("Canonical approval", width), ""}
	for _, line := range RenderFields(ApprovalFields(matches[0], snap.ObservedAt), width) {
		out = append(out, truncate(line, width))
	}
	if len(out) > rows {
		out = out[:rows]
	}
	return out
}

func (v *NavView) renderRollbackHistory(snap ControlSnapshot, width, rows int) []string {
	out := []string{truncate("Rollback history", width), ""}
	count := 0
	for _, record := range snap.Checkpoints {
		if record.RestoredAt == nil {
			continue
		}
		count++
		out = append(out, truncate(fmt.Sprintf("%s restored %s", record.CheckpointID,
			record.RestoredAt.UTC().Format(time.RFC3339)), width))
	}
	if count == 0 {
		out = append(out, wrap(Empty("internal/execution/checkpoint.go").Display(), width)...)
	}
	if len(out) > rows {
		out = out[:rows]
	}
	return out
}

func (v *NavView) renderApprovalQueue(snap ControlSnapshot, width, rows int) []string {
	out := []string{truncate("Pending approvals", width), ""}
	if len(snap.Approvals) == 0 {
		out = append(out, wrap(snap.ApprovalsStatus.Display(), width)...)
		return out
	}
	selected := clampIndex(v.control.listIndex, len(snap.Approvals))
	for i, a := range snap.Approvals {
		marker := "  "
		if i == selected {
			marker = "▸ "
		}
		out = append(out, truncate(fmt.Sprintf("%s%s  %s on %s",
			marker, a.ApprovalID, a.OperationType, a.TargetResource), width))
	}
	out = append(out, "")
	// The selected record's full binding, because a decision is about one
	// exact action and the user must see which.
	for _, line := range RenderFields(
		ApprovalFields(snap.Approvals[selected], snap.ObservedAt), width) {
		out = append(out, truncate(line, width))
	}
	if len(out) > rows {
		out = out[:rows]
	}
	return out
}

func (v *NavView) renderSelectedApproval(snap ControlSnapshot, width, rows int) []string {
	if len(snap.Approvals) == 0 {
		return append([]string{truncate("Selected approval", width), ""},
			wrap(snap.ApprovalsStatus.Display(), width)...)
	}
	selected := clampIndex(v.control.listIndex, len(snap.Approvals))
	out := []string{truncate("Selected approval", width), ""}
	for _, line := range RenderFields(
		ApprovalFields(snap.Approvals[selected], snap.ObservedAt), width) {
		out = append(out, truncate(line, width))
	}
	if len(out) > rows {
		out = out[:rows]
	}
	return out
}

func (v *NavView) renderCheckpointList(snap ControlSnapshot, width, rows int) []string {
	out := []string{truncate("Durable checkpoints", width), ""}
	if len(snap.Checkpoints) == 0 {
		out = append(out, wrap(snap.CheckpointsStatus.Display(), width)...)
		return out
	}
	selected := clampIndex(v.control.listIndex, len(snap.Checkpoints))
	for i, record := range snap.Checkpoints {
		marker := "  "
		if i == selected {
			marker = "▸ "
		}
		out = append(out, truncate(fmt.Sprintf("%s%s  %s  %s",
			marker, record.CheckpointID,
			record.CreatedAt.UTC().Format(time.RFC3339), record.Reason), width))
	}
	out = append(out, "")
	for _, line := range RenderFields(CheckpointFields(snap.Checkpoints[selected]), width) {
		out = append(out, truncate(line, width))
	}
	if len(out) > rows {
		out = out[:rows]
	}
	return out
}

func (v *NavView) renderSelectedCheckpoint(snap ControlSnapshot, width, rows int) []string {
	if len(snap.Checkpoints) == 0 {
		return append([]string{truncate("Selected checkpoint", width), ""},
			wrap(snap.CheckpointsStatus.Display(), width)...)
	}
	selected := clampIndex(v.control.listIndex, len(snap.Checkpoints))
	out := []string{truncate("Selected checkpoint", width), ""}
	for _, line := range RenderFields(CheckpointFields(snap.Checkpoints[selected]), width) {
		out = append(out, truncate(line, width))
	}
	if len(out) > rows {
		out = out[:rows]
	}
	return out
}

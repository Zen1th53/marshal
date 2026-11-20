package tui

import (
	"path/filepath"
	"strings"

	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	boardmodel "github.com/Zen1th53/marshal/internal/tui/board/model"
	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

// collectV2Flow fills the top bar, the NOW line, Plan, Lanes, Tasks, Inbox, Activity and the Marshal steps of the Board v2 view model. A field left at its zero
// value renders as "not known yet".
//
// It runs per frame: everything it reads is in memory (the published run
// panel, the inbox queue, the cards, the notes, the broker's steps, the
// milestone and architecture snapshots the followers keep). It never reads
// the store or a provider, and it must be called without w.mu held.
func (w *Workspace) collectV2Flow(b *vm.Board) {
	in := w.flowInputs()
	b.Now = in.now
	b.Top = flowTop(in)
	b.NowLine = flowNowLine(in)
	b.Plan = flowPlan(in)
	b.Lanes = flowLanes(in)
	b.Tasks = flowTasks(in)
	b.Inbox = flowInbox(in)
	b.Activity = flowActivity(in)
	b.Steps = stepsView(in.steps, in.lastCall, in.stepsModel, in.now)
}

// flowInputs gathers what the flow collector reads, once per frame.
func (w *Workspace) flowInputs() flowIn {
	state := w.GetUIState()
	in := flowIn{now: boardNow(), version: w.buildVersion(), project: filepath.Base(w.providerRoot())}
	p := state.Marshal
	in.hasRun = p != nil
	if p == nil {
		p = &MarshalPanel{}
	}
	in.panel = p
	in.run = p.run
	if in.run.State == "" {
		in.run.State = p.State
	}
	v := w.stopVisual(p)
	v.now = in.now
	v.board.Inbox = w.boardInbox()
	v.gates = w.ownerGates(state.Marshal)
	v.ask, v.req, v.change, v.next = w.currentAsk(), w.currentReq(), w.currentChange(), w.currentNext()
	v.guard, _, _, _ = w.guardPaint()
	in.v = v
	in.planning = w.planningNow(!in.hasRun, state.Goal)
	if goal, ok := w.activeGoalNow(!in.hasRun, state.Goal); ok {
		in.goalGiven, in.goalSince, in.goalText = true, goalStartedAt(goal), goalSummaryText(goal)
	}
	if in.planning || in.goalGiven {
		in.milestone = w.milestone.Load()
	}
	if snap := w.archSnapshotNow(); snap.Loaded {
		in.archRev, in.archAccepted = snap.Status.Revision, snap.Status.Accepted
	}
	in.notes = w.foldedNotes()
	in.steps, in.lastCall, in.stepsProvider = w.marshalSteps.snapshot()
	w.chat.mu.Lock()
	in.chatProvider, in.chatModel = w.chat.provider, w.chat.model
	w.chat.mu.Unlock()
	in.stepsModel = marshalModelLabel(firstSet(in.chatProvider, in.stepsProvider), in.chatModel)
	in.marshalProvider = firstSet(in.chatProvider, p.Provider, in.stepsProvider)
	// The NOW line names the Marshal as the top bar does. In a tmux chat
	// w.chat (the in-process chat) is empty, so read the same source as
	// the top bar's Marshal segment: the live chat window and its probe.
	mi := w.v2MarshalModelIn(in.now)
	if prov := mi.marshalProvider(); prov != "" {
		in.marshalProvider, in.marshalModel = prov, mi.marshalModel(prov)
		in.stepsModel = marshalModelLabel(prov, in.marshalModel)
	}
	in.chatBlocked, in.chatWaiting = w.chatDecisions()
	in.limits, in.globalCap = laneLimits(in.run.Settings)
	ui := &w.boardUI
	_, stopped := v.stopNotice()
	if ui.pane(len(v.board.Inbox) > 0 || stopped) == layout.Lanes {
		in.selectedLane = ui.lane
	}
	return in
}

// marshalModelLabel is "claude opus-5.5", or whichever half is known.
func marshalModelLabel(provider, model string) string {
	label := ""
	if strings.TrimSpace(provider) != "" {
		label = providerLabel(provider)
	}
	return strings.TrimSpace(label + " " + strings.TrimSpace(model))
}

// firstSet is the first non-blank string, trimmed.
func firstSet(s ...string) string {
	for _, x := range s {
		if x = strings.TrimSpace(x); x != "" {
			return x
		}
	}
	return ""
}

// liveLane reports whether a lane has a worker running its task.
func liveLane(l boardmodel.Lane) bool {
	return l.State != boardmodel.LaneIdle && l.State != boardmodel.LaneParked && l.State != boardmodel.LaneLost
}

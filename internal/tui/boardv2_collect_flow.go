package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	boardmodel "github.com/Zen1th53/marshal/internal/tui/board/model"
	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

// collectV2Flow fills the top bar, the NOW line, Plan, Lanes, Tasks, Inbox, Activity and the Marshal steps of the Board v2 view model. A field left at its zero
// value renders as "not known yet".
//
// It runs per frame: everything it reads is in memory (the published run
// panel, the inbox queue, the cards, the notes, the Marshal's steps (the
// step log is read once, then on the chat monitor's tick), the
// milestone and architecture snapshots the followers keep). It never reads
// the store or a provider, and it must be called without w.mu held.
func (w *Workspace) collectV2Flow(b *vm.Board) {
	in := w.flowInputs()
	b.Now = in.now
	b.LastRun = in.lastRun
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
		if !in.hasRun && (in.goalGiven || in.planning) {
			switch {
			case snap.Status.Answering:
				in.milestone = &planningMark{PlanningMilestone: app.PlanningMilestone{Step: stageArch, Now: "the Marshal is answering your request"}}
			case in.planning && (in.milestone == nil || in.milestone.Step < stageTasks):
				in.milestone = &planningMark{PlanningMilestone: app.PlanningMilestone{Step: stageTasks, Now: "planning the tasks"}}
			case snap.Status.OwnerAccepted:
				in.milestone = &planningMark{PlanningMilestone: app.PlanningMilestone{Step: stageArch, Now: "applying the architecture you accepted", Applying: true}}
			case snap.Status.Exists && !snap.Status.Accepted:
				in.milestone = &planningMark{PlanningMilestone: app.PlanningMilestone{Step: stageArch, Now: "the architecture is ready for you to accept in the browser", OwnerTurn: true}}
			}
		}
	}
	in.notes = w.foldedNotes()
	in.steps, in.lastCall, in.stepsProvider = w.stepsLog().snapshot()
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
	in.chatAsked = w.boardChatAsked()
	in.milestone = w.timedMilestone(in)
	in.limits, in.globalCap = laneLimits(in.run.Settings)
	in.draftRejected = w.draftRejectedNow()
	stallKey, stallReason := stallCondition(in)
	in.stall = w.stallSince(stallKey, stallReason, in.now)
	ui := &w.boardUI
	_, stopped := v.stopNotice()
	if ui.pane(len(v.board.Inbox) > 0 || stopped) == layout.Lanes {
		in.selectedLane = ui.lane
	}
	// A stored result is display data, not state.Marshal: chat startup must
	// remain free to accept a new goal and create a fresh run.
	if !in.hasRun && !in.planning && !in.goalGiven {
		if last := w.lastRun.Load(); last != nil {
			in.hasRun, in.lastRun = true, true
			in.panel, in.run = last.panel, last.panel.run
			inbox := in.v.board.Inbox
			in.v.board = last.panel.Board
			in.v.board.Inbox = inbox
		}
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

// timedMilestone times the state actually shown, including new proposals at
// the same planning step. Repeated paints and follower reads keep the timer.
func (w *Workspace) timedMilestone(in flowIn) *planningMark {
	s := w.v2CardState()
	s.mu.Lock()
	defer s.mu.Unlock()
	if in.milestone == nil {
		s.milestoneKey = ""
		return nil
	}
	m := *in.milestone
	key := fmt.Sprintf("%s/%s/%d/%+v", in.goalText, in.goalSince.Format(time.RFC3339Nano), in.archRev, m.PlanningMilestone)
	if key != s.milestoneKey {
		since := in.now
		if s.milestoneKey == "" && !m.Since.IsZero() {
			since = m.Since
		}
		s.milestoneKey, s.milestoneSince = key, since
	}
	m.Since = s.milestoneSince
	return &m
}

// boardChatAsked is shared by the status collector and card/key selection.
func (w *Workspace) boardChatAsked() bool {
	w.tmuxMu.Lock()
	asked := false
	if a := w.tmuxActiveWins["marshal-chat"]; a != nil && a.paneID != "" {
		asked = chatAskedNow(a.paneID)
	}
	w.tmuxMu.Unlock()
	return asked || w.nativeChatAsked()
}

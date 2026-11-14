package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/statepath"
)

// Change cards: the owner types a new goal or a change while a run executes.
// The Marshal writes a "hold" proposal naming the affected plan tasks; the
// TUI pauses the ones that have not started (a real per-task hold that only
// delays dispatch, it grants nothing), lets running ones finish, and shows a
// one-key card: [Enter] apply this change, or [k] keep the current goal.
//
// The hold lives on the marshalSession, never in the run state: persisting
// it there would race the executing operation's revision writes. A change
// hold is memory only: after a restart it is gone, and the Marshal holds
// again if the change is still open. Only the owner's pressing "apply this
// change" (and the amendment that follows) or "keep the current goal"
// releases it; an amendment the Marshal sends on its own leaves it in place.
//
// An owner's hold ("until":"release": hold these tasks until I say ready, no
// goal change) is pinned: no change answer releases it, it is saved beside
// the run state (marshal-holds.json) so a restart keeps it, and the Board
// shows it plainly as held with [x] release the hold. Only a release
// proposal or that key ends it. dispatchReady skips a held task that has not
// started (marshal_execute.go, wired through MarshalService.HeldTask in
// commands_marshal.go, which is asked about the run being dispatched).
// Every hold, refusal and release is reported to the Marshal in its inbox.

// changeCard is the open mid-run change as the inbox shows it.
type changeCard struct {
	runID string
	// notStarted is how many held tasks had not been dispatched when the hold
	// applied: the ones the hold actually pauses. running is the rest of the
	// held set (already dispatched or beyond): they finish first.
	notStarted int
	running    int
	// applying is set once the owner presses Enter: the card shows
	// "Applying your change…" while the change is applied. timedOut replaces
	// it when the run could not be paused within the budget.
	applying bool
	timedOut bool
	// ownerApplied is set when the owner pressed "apply this change": only
	// then does the amendment that follows release the hold.
	ownerApplied bool
	// held is non-empty for the owner's own hold card: the pinned task IDs,
	// shown plainly as held with a release key instead of the change keys.
	held []string
	at   time.Time
}

// ownerHold reports whether the card is the owner's own hold, not a change.
func (c changeCard) ownerHold() bool { return len(c.held) > 0 }

// errHoldIDs is the one error parseHoldIDs returns: the proposal is refused
// as invalid, like any other malformed Marshal proposal.
var errHoldIDs = errors.New("invalid hold ids")

// parseHoldIDs reads a hold/release proposal's ids field: a JSON array of 1 to
// 50 plan task IDs, each matching the task-id rule ([A-Za-z0-9._-], at most
// 200 bytes). It rejects trailing data.
func parseHoldIDs(s string) ([]string, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	var raw []string
	if err := dec.Decode(&raw); err != nil {
		return nil, errHoldIDs
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errHoldIDs
	}
	if len(raw) < 1 || len(raw) > 50 {
		return nil, errHoldIDs
	}
	for _, id := range raw {
		if id == "" || len(id) > 200 || strings.ContainsFunc(id, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' && r != '.'
		}) {
			return nil, errHoldIDs
		}
	}
	return raw, nil
}

// isHeldFor reports whether runID's plan task is held. The held set is keyed by
// run so a hold never touches another run's tasks.
func (m *marshalSession) isHeldFor(runID, planTaskID string) bool {
	m.holdMu.Lock()
	defer m.holdMu.Unlock()
	return m.isHeldLocked(runID, planTaskID)
}

func (m *marshalSession) isHeldLocked(runID, planTaskID string) bool {
	return runID != "" && (runID == m.pausedRunID || runID == m.heldRunID && m.held[planTaskID])
}

// holdDispatch holds every not-yet-started task of runID: the person paused
// the run, so the Marshal launches nothing new until releaseDispatch. Tasks
// already running are paused by run control, not by this hold.
func (m *marshalSession) holdDispatch(runID string) {
	m.holdMu.Lock()
	defer m.holdMu.Unlock()
	m.pausedRunID = runID
	m.notifyHoldLocked()
}

// dispatchHeld reports whether the person's pause holds a Marshal run.
func (m *marshalSession) dispatchHeld() bool {
	m.holdMu.Lock()
	defer m.holdMu.Unlock()
	return m.pausedRunID != ""
}

func (m *marshalSession) pausedFor(runID string) bool {
	m.holdMu.Lock()
	defer m.holdMu.Unlock()
	return runID != "" && m.pausedRunID == runID
}

// releaseDispatch ends holdDispatch's hold, waking a run that waits on it.
func (m *marshalSession) releaseDispatch() {
	m.holdMu.Lock()
	defer m.holdMu.Unlock()
	if m.pausedRunID == "" {
		return
	}
	m.pausedRunID = ""
	m.notifyHoldLocked()
}

// claimDispatch is dispatch's last hold check, under the lock a hold takes:
// a held task is refused; otherwise the launch is counted until end is
// called, so a hold that lands meanwhile counts the task as running rather
// than paused. The hold and the dispatch decision are atomic.
func (m *marshalSession) claimDispatch(runID, planTaskID string) (func(), bool) {
	m.holdMu.Lock()
	defer m.holdMu.Unlock()
	if m.isHeldLocked(runID, planTaskID) {
		return nil, false
	}
	key := runID + "\x00" + planTaskID
	if m.launching == nil {
		m.launching = map[string]int{}
	}
	m.launching[key]++
	var once sync.Once
	return func() {
		once.Do(func() {
			m.holdMu.Lock()
			defer m.holdMu.Unlock()
			if m.launching[key]--; m.launching[key] <= 0 {
				delete(m.launching, key)
			}
		})
	}, true
}

// holdChanged returns a channel closed the next time the held set changes.
func (m *marshalSession) holdChanged() <-chan struct{} {
	m.holdMu.Lock()
	defer m.holdMu.Unlock()
	if m.holdCh == nil {
		m.holdCh = make(chan struct{})
	}
	return m.holdCh
}

// notifyHoldLocked wakes whoever waits on the held set. holdMu is held.
func (m *marshalSession) notifyHoldLocked() {
	if m.holdCh != nil {
		close(m.holdCh)
		m.holdCh = nil
	}
}

// holdTasks validates ids against the run as stored and adds them to the
// held set for runID, replacing any set left over from another run, all under
// holdMu so no dispatch claim can slip between the count and the hold. It
// returns how many held tasks had not started (and are now paused) and how
// many were running or past it, or the first ID that is not in the run.
func (m *marshalSession) holdTasks(runID string, ids []string, pin bool, load func() (marshal.Run, bool)) (notStarted, running int, unknown string, ok bool) {
	m.holdMu.Lock()
	defer m.holdMu.Unlock()
	run, found := load()
	if !found {
		return 0, 0, "", false
	}
	state := map[string]marshal.TaskState{}
	for _, t := range run.Tasks {
		state[t.PlanTaskID] = t.State
	}
	held := map[string]bool{}
	for _, id := range ids {
		if _, exists := state[id]; !exists {
			return 0, 0, id, false
		}
		held[id] = true
	}
	if m.heldRunID != runID || m.held == nil {
		m.heldRunID = runID
		m.held = map[string]bool{}
		m.pinned = nil
	}
	for id := range held {
		if taskNotStarted(state[id]) && m.launching[runID+"\x00"+id] == 0 {
			notStarted++
		} else {
			running++
		}
		m.held[id] = true
		if pin {
			if m.pinned == nil {
				m.pinned = map[string]bool{}
			}
			m.pinned[id] = true
		}
	}
	m.saveHoldsLocked()
	m.notifyHoldLocked()
	return notStarted, running, "", true
}

// releaseTasks drops ids from the held set, pinned or not.
func (m *marshalSession) releaseTasks(ids []string) {
	m.holdMu.Lock()
	defer m.holdMu.Unlock()
	for _, id := range ids {
		delete(m.held, id)
		delete(m.pinned, id)
	}
	m.saveHoldsLocked()
	m.notifyHoldLocked()
}

// releaseChangeHeld ends the change's hold: every held task the owner did not
// pin is released. It returns the pinned IDs still held, sorted.
func (m *marshalSession) releaseChangeHeld() []string {
	m.holdMu.Lock()
	defer m.holdMu.Unlock()
	for id := range m.held {
		if !m.pinned[id] {
			delete(m.held, id)
		}
	}
	if len(m.held) == 0 {
		m.held, m.pinned, m.heldRunID = nil, nil, ""
	}
	m.notifyHoldLocked()
	return sortedHoldIDs(m.pinned)
}

// releaseAllHeld clears the whole held set, pinned holds included.
func (m *marshalSession) releaseAllHeld() {
	m.holdMu.Lock()
	defer m.holdMu.Unlock()
	m.held, m.pinned = nil, nil
	m.heldRunID = ""
	m.saveHoldsLocked()
	m.notifyHoldLocked()
}

// pinnedIDs is the owner's held task IDs, sorted.
func (m *marshalSession) pinnedIDs() []string {
	m.holdMu.Lock()
	defer m.holdMu.Unlock()
	return sortedHoldIDs(m.pinned)
}

func sortedHoldIDs(set map[string]bool) []string {
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// ownerHolds is the saved form of the owner's pinned holds.
type ownerHolds struct {
	RunID string   `json:"run_id"`
	IDs   []string `json:"ids"`
}

// saveHoldsLocked writes the pinned holds to holdsFile (removing it when none
// remain), so the owner's hold survives a restart. holdMu is held. A failed
// write leaves the in-memory hold in force.
func (m *marshalSession) saveHoldsLocked() {
	if m.holdsFile == "" {
		return
	}
	if len(m.pinned) == 0 || m.heldRunID == "" {
		_ = os.Remove(m.holdsFile)
		return
	}
	data, err := json.Marshal(ownerHolds{RunID: m.heldRunID, IDs: sortedHoldIDs(m.pinned)})
	if err != nil {
		return
	}
	tmp := m.holdsFile + ".tmp"
	if os.WriteFile(tmp, data, 0o600) == nil {
		_ = os.Rename(tmp, m.holdsFile)
	}
}

// restoreOwnerHolds binds m to the project's saved owner holds and restores
// them, with their held card, after a restart. Called once, when the session
// is created.
func (w *Workspace) restoreOwnerHolds(m *marshalSession) {
	if w.workDir == "" {
		return
	}
	dir := filepath.Join(w.workDir, statepath.DirName)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return
	}
	m.holdsFile = filepath.Join(dir, "marshal-holds.json")
	data, err := os.ReadFile(m.holdsFile)
	if err != nil {
		return
	}
	var saved ownerHolds
	if json.Unmarshal(data, &saved) != nil || saved.RunID == "" || len(saved.IDs) == 0 {
		return
	}
	encoded, _ := json.Marshal(saved.IDs)
	ids, err := parseHoldIDs(string(encoded))
	if err != nil {
		return
	}
	m.heldRunID, m.held, m.pinned = saved.RunID, map[string]bool{}, map[string]bool{}
	for _, id := range ids {
		m.held[id], m.pinned[id] = true, true
	}
	w.change.mu.Lock()
	if w.change.card == nil {
		w.change.card = &changeCard{runID: saved.RunID, held: sortedHoldIDs(m.pinned), at: time.Now()}
	}
	w.change.mu.Unlock()
}

// heldCount is how many tasks the session currently holds.
func (m *marshalSession) heldCount() int {
	m.holdMu.Lock()
	defer m.holdMu.Unlock()
	return len(m.held)
}

// currentMarshalRun loads the run the change is about, the same run lookup the
// run proposals use. ok is false with no active run.
func (w *Workspace) currentMarshalRun() (*marshalSession, string, marshal.Run, bool) {
	m := w.marshalSession()
	m.mu.Lock()
	service, runID := m.service, m.runID
	m.mu.Unlock()
	if service == nil || runID == "" {
		return m, runID, marshal.Run{}, false
	}
	record, err := service.Store.GetMarshalRun(context.Background(), service.ProjectID, runID)
	if err != nil {
		return m, runID, marshal.Run{}, false
	}
	return m, runID, record.Value, true
}

// taskNotStarted reports whether a task has not launched yet: exactly the
// states dispatchReady would launch. Everything else is running or past it.
func taskNotStarted(s marshal.TaskState) bool {
	return s == marshal.Queued || s == marshal.Returned || s == marshal.Reassigned
}

// applyHold holds the affected tasks of the current run and opens the change
// card. An unknown task ID is refused, like any other invalid proposal.
func (w *Workspace) applyHold(p marshalProposal) {
	ids, err := parseHoldIDs(p.ids)
	if err != nil {
		w.rejectHold(p, "malformed hold ids")
		return
	}
	m := w.marshalSession()
	m.mu.Lock()
	runID := m.runID
	m.mu.Unlock()
	// The run is read under the hold's lock, so the count matches what
	// dispatch will do: a task whose launch is already claimed counts as
	// running, and every task counted as paused is refused by dispatch.
	pin := p.until == "release"
	notStarted, running, unknown, ok := m.holdTasks(runID, ids, pin, func() (marshal.Run, bool) {
		_, id, run, ok := w.currentMarshalRun()
		return run, ok && id == runID
	})
	if unknown != "" {
		w.rejectHold(p, "task "+unknown+" is not in the active run")
		return
	}
	if !ok {
		w.rejectHold(p, "no active run to change")
		return
	}
	w.change.mu.Lock()
	c := w.change.card
	switch {
	case !pin:
		// A change: its card replaces an owner-hold card, whose tasks stay
		// held and reappear once the change is answered.
		w.change.card = &changeCard{runID: runID, notStarted: notStarted, running: running, at: time.Now()}
	case c == nil || c.ownerHold():
		w.change.card = &changeCard{runID: runID, held: m.pinnedIDs(), at: time.Now()}
	}
	// An owner's hold arriving while a change card is open leaves that card:
	// the hold is in force and shows once the change is answered.
	w.change.mu.Unlock()
	headline := changeHoldHeadline(notStarted, running)
	if pin {
		headline = ownerHoldHeadline(ids)
	}
	w.RecordActivity(headline)
	w.resolveChangeProposal(p, "applied")
	w.holdNotice("hold applied", holdAppliedNotice(ids, notStarted, running, pin), runID)
	w.requestRepaint()
}

// holdAppliedNotice is the Marshal's inbox receipt for an applied hold.
func holdAppliedNotice(ids []string, notStarted, running int, pin bool) string {
	text := "Held: " + strings.Join(ids, ", ") + ". "
	if notStarted > 0 {
		text += fmt.Sprintf("%d not started and will not be dispatched while held. ", notStarted)
	}
	if running > 0 {
		text += fmt.Sprintf("%d already running or past dispatch; a hold does not stop it. ", running)
	}
	if pin {
		return text + "This is the owner's hold: it stays until a release proposal or the owner's [x] release on the Board; no change answer and no amendment releases it."
	}
	return text + "The Board asks the owner: [Enter] apply this change or [k] keep the current goal. For a hold with no goal change, send the hold with \"until\":\"release\"."
}

// holdNotice writes one runtime line to the Marshal's inbox.
func (w *Workspace) holdNotice(event, text, runID string) {
	root := w.workDir
	if w.runtime != nil {
		root = w.runtime.ProjectRoot()
	}
	if root == "" {
		return
	}
	view, err := openInboxView(root, "marshal", false)
	if err != nil {
		return
	}
	if runID != "" {
		text += "\nRun: " + runID
	}
	if view.write(fmt.Sprintf("## runtime · %s · %s\n\n%s\n", time.Now().UTC().Format(time.RFC3339), event, text)) == nil {
		_ = view.trim()
	}
}

// rejectHold refuses a hold or release and tells the Marshal why.
func (w *Workspace) rejectHold(p marshalProposal, reason string) {
	w.rejectChange(p, reason)
	m := w.marshalSession()
	m.mu.Lock()
	runID := m.runID
	m.mu.Unlock()
	w.holdNotice(p.action+" refused", "The "+p.action+" was not applied: "+reason+".", runID)
}

// applyRelease drops the named tasks from the held set and closes the card
// when nothing remains held.
func (w *Workspace) applyRelease(p marshalProposal) {
	ids, err := parseHoldIDs(p.ids)
	if err != nil {
		w.rejectHold(p, "malformed release ids")
		return
	}
	m := w.marshalSession()
	m.mu.Lock()
	runID := m.runID
	m.mu.Unlock()
	m.releaseTasks(ids)
	w.refreshHoldCard()
	w.resolveChangeProposal(p, "applied")
	w.RecordActivity("Released: " + strings.Join(ids, ", ") + ".")
	w.holdNotice("hold released", "Released: "+strings.Join(ids, ", ")+". A released task starts once its dependencies are merged.", runID)
	w.requestRepaint()
}

// refreshHoldCard closes the card when nothing is held any more, and keeps an
// owner-hold card's task list current.
func (w *Workspace) refreshHoldCard() {
	m := w.marshalSession()
	if m.heldCount() == 0 {
		w.clearChange()
		return
	}
	pinned := m.pinnedIDs()
	w.change.mu.Lock()
	if c := w.change.card; c != nil && c.ownerHold() {
		if len(pinned) == 0 {
			w.change.card = nil
		} else {
			c.held = pinned
		}
	}
	w.change.mu.Unlock()
}

// rejectChange records an ignored proposal and resolves it as rejected, the
// same way other invalid proposals are reported.
func (w *Workspace) rejectChange(p marshalProposal, reason string) {
	w.resolveChangeProposal(p, "rejected")
	w.RecordActivity("Ignored Marshal proposal: " + reason + ".")
}

func (w *Workspace) resolveChangeProposal(p marshalProposal, state string) {
	if w.store != nil && p.occurrence != "" {
		_ = w.store.ResolveMarshalProposal(context.Background(), w.projectID, p.occurrence, state)
	}
}

// currentChange is the open change card, nil for none.
func (w *Workspace) currentChange() *changeCard {
	w.change.mu.Lock()
	defer w.change.mu.Unlock()
	return w.change.card
}

// clearChange closes the open change card.
func (w *Workspace) clearChange() {
	w.change.mu.Lock()
	w.change.card = nil
	w.change.mu.Unlock()
	w.requestRepaint()
}

// releaseChange ends the change: its held tasks are released and its card
// closes. The owner's pinned holds stay, and their held card takes its place.
func (w *Workspace) releaseChange() {
	pinned := w.marshalSession().releaseChangeHeld()
	if len(pinned) == 0 {
		w.clearChange()
		return
	}
	w.change.mu.Lock()
	runID := ""
	if w.change.card != nil {
		runID = w.change.card.runID
	}
	w.change.card = &changeCard{runID: runID, held: pinned, at: time.Now()}
	w.change.mu.Unlock()
	w.requestRepaint()
}

// releaseOwnerHold is the owner's [x] on the held card: the pinned tasks are
// released and the Marshal is told in its inbox.
func (w *Workspace) releaseOwnerHold() {
	m := w.marshalSession()
	ids := m.pinnedIDs()
	if len(ids) == 0 {
		w.refreshHoldCard()
		return
	}
	m.mu.Lock()
	runID := m.runID
	m.mu.Unlock()
	m.releaseTasks(ids)
	w.refreshHoldCard()
	w.RecordActivity("You released the hold on " + strings.Join(ids, ", ") + ".")
	w.holdNotice("hold released", "The owner released the hold on "+strings.Join(ids, ", ")+" from the Board. A released task starts once its dependencies are merged.", runID)
	w.requestRepaint()
}

// keepHoldAfterAmend ends the busy-apply sequence of an amendment the owner
// did not ask for through the card: the hold stays and the card returns to
// its keys.
func (w *Workspace) keepHoldAfterAmend() {
	w.change.mu.Lock()
	c := w.change.card
	runID := ""
	if c != nil {
		c.applying, c.timedOut = false, false
		runID = c.runID
	}
	w.change.mu.Unlock()
	if ids := sortedHoldIDs(w.marshalSession().heldSet()); len(ids) > 0 {
		w.holdNotice("hold kept", "The amendment ran; the hold on "+strings.Join(ids, ", ")+" stays. Only the owner's answer on the Board or a release proposal ends it.", runID)
	}
	w.requestRepaint()
}

// heldSet is a copy of the held set.
func (m *marshalSession) heldSet() map[string]bool {
	m.holdMu.Lock()
	defer m.holdMu.Unlock()
	out := make(map[string]bool, len(m.held))
	for id := range m.held {
		out[id] = true
	}
	return out
}

// handleChangeKey is the change card's keys on the Board, taken after the ask
// card and before the gate keys while a card is open and no body view or
// inspector covers the Board: Enter applies the change, k keeps the current
// goal. Other keys fall through to keep their meaning. While the change is
// being applied the keys are swallowed except Enter, which retries after a
// timeout.
func (w *Workspace) handleChangeKey(event KeyEvent) bool {
	card := w.currentChange()
	if card == nil || w.anyBodyView() || w.boardUI.inspect != "" {
		return false
	}
	w.change.mu.Lock()
	applying, timedOut, ownerHold := card.applying, card.timedOut, card.ownerHold()
	w.change.mu.Unlock()
	if ownerHold {
		// The owner's own hold: x releases it; Enter and k keep their
		// ordinary meaning, so no stray key ends the hold.
		if event.Type == KeyRune && event.Rune == 'x' && !w.inboxView.Confirming() {
			w.releaseOwnerHold()
			return true
		}
		return false
	}
	if event.Type == KeyEnter {
		if applying && !timedOut {
			return true
		}
		if w.boardStopWaiting() {
			// A stopped run leads the inbox: Enter is its key.
			return false
		}
		w.changeApply()
		return true
	}
	if event.Type == KeyRune && event.Rune == 'k' {
		if !applying || timedOut {
			w.changeKeep()
		}
		return true
	}
	return false
}

// clickChange is the same two actions by mouse.
// While the change is being applied a click does nothing, as for the keys:
// it must not send "apply this change" again or release the hold.
func (w *Workspace) clickChange(id string) {
	if w.changeApplying() {
		return
	}
	switch id {
	case "apply":
		w.changeApply()
	case "keep":
		w.changeKeep()
	case "release":
		w.releaseOwnerHold()
	}
}

// changeApply tells the Marshal to apply the change and shows "Applying your
// change…". The Marshal answers with an amend proposal; if the run is busy the
// TUI pauses it to apply the amendment (beginChangeApply). With no chat open
// nothing is sent and the card stays.
func (w *Workspace) changeApply() {
	if w.changeApplying() {
		return
	}
	if w.sayPane() == "" && !w.nativeChatShown() {
		w.setOutput("No Marshal chat is open; the change was not applied.", true)
		return
	}
	w.change.mu.Lock()
	if w.change.card != nil {
		w.change.card.applying = true
		w.change.card.timedOut = false
		w.change.card.ownerApplied = true
	}
	w.change.mu.Unlock()
	w.sayToMarshal(w.workers.context(), "apply this change")
	w.requestRepaint()
}

// changeApplying reports whether the open card is applying the change and
// has not timed out: the card then takes no action until it settles.
func (w *Workspace) changeApplying() bool {
	w.change.mu.Lock()
	defer w.change.mu.Unlock()
	c := w.change.card
	return c != nil && c.applying && !c.timedOut
}

// finishAppliedChange releases the hold and closes the card after an
// amendment the owner asked for ran through the ordinary amend path (the run
// was not busy). The busy-apply sequence does this itself, so it is skipped
// while that sequence runs.
func (w *Workspace) finishAppliedChange() {
	w.change.mu.Lock()
	c := w.change.card
	applying := c != nil && c.applying && !w.change.inFlight
	w.change.mu.Unlock()
	if applying {
		w.releaseChange()
	}
}

// changeKeep tells the Marshal to keep the current goal, releases the held set
// itself (no release proposal is needed) and closes the card.
func (w *Workspace) changeKeep() {
	if w.sayPane() == "" && !w.nativeChatShown() {
		w.setOutput("No Marshal chat is open; nothing was sent.", true)
		return
	}
	w.releaseChange()
	w.sayToMarshal(w.workers.context(), "keep the current goal")
	w.RecordActivity("You kept the current goal.")
}

// beginChangeApply runs the busy-apply sequence after the owner pressed apply
// and the Marshal answered with an amend proposal while the run operation is
// busy. The proposal is resolved as applied here; the sequence runs in the
// background so it can wait for the run to pause.
func (w *Workspace) beginChangeApply(p marshalProposal) {
	w.change.mu.Lock()
	if w.change.inFlight {
		w.change.mu.Unlock()
		// One change, one amendment: a repeated or separate amend proposal
		// while the sequence runs is refused, not applied a second time.
		w.rejectChange(p, "the change is already being applied")
		return
	}
	w.change.inFlight = true
	w.change.mu.Unlock()
	w.resolveChangeProposal(p, "applied")
	w.change.mu.Lock()
	if w.change.card != nil {
		// A card already applying is the owner's "apply this change".
		if w.change.card.applying {
			w.change.card.ownerApplied = true
		}
		w.change.card.applying = true
		w.change.card.timedOut = false
	}
	w.change.mu.Unlock()
	w.requestRepaint()
	ctx := w.workers.context()
	reason := p.reason
	go w.applyChangeSequence(ctx, reason)
}

// applyChangeSequence pauses the run, applies the amendment and resumes, in
// that order. A major amendment is not resumed: it waits for approval as
// today. On success the held set is released and the card closes. If the run
// could not be paused within the budget the card stays and asks the owner to
// try again. The stop/busy/amend/resume steps go through seams so tests can
// drive them.
func (w *Workspace) applyChangeSequence(ctx context.Context, reason string) {
	defer func() {
		w.change.mu.Lock()
		w.change.inFlight = false
		w.change.mu.Unlock()
	}()
	stopFn, busyFn, amendFn, resumeFn, poll, timeout := w.changeSeams()
	w.RecordActivity("Applying your change: pausing the run briefly.")
	stopFn()
	if !waitUntilFalse(busyFn, poll, timeout) {
		w.change.mu.Lock()
		if w.change.card != nil {
			w.change.card.timedOut = true
			w.change.card.applying = false
		}
		w.change.mu.Unlock()
		w.setOutput("I couldn't pause the run to apply the change. Press Enter to try again.", true)
		w.requestRepaint()
		return
	}
	major, err := amendFn(ctx, reason)
	if err == nil && !major {
		_, _ = resumeFn(ctx)
	}
	// Only the owner's "apply this change" ends the hold with the
	// amendment (on failure too, so the run is not left held; the panel
	// shows why). An amendment the Marshal sent on its own while the card
	// was open leaves the owner's hold in place.
	w.change.mu.Lock()
	owner := w.change.card != nil && w.change.card.ownerApplied
	w.change.mu.Unlock()
	if owner {
		w.releaseChange()
		return
	}
	w.keepHoldAfterAmend()
}

// ownerHoldHeadline is the owner-hold card's line, from the verified IDs.
func ownerHoldHeadline(ids []string) string {
	if len(ids) == 1 {
		return ids[0] + " is held until you release it; it will not start."
	}
	return fmt.Sprintf("%d tasks are held until you release them; they will not start: %s.", len(ids), strings.Join(ids, ", "))
}

// changeSeams returns the busy-apply steps, falling back to the real
// implementations when a test did not replace them.
func (w *Workspace) changeSeams() (func() string, func() bool, func(context.Context, string) (bool, error), func(context.Context) (string, error), time.Duration, time.Duration) {
	w.change.mu.Lock()
	stopFn, busyFn, amendFn, resumeFn := w.change.stopFn, w.change.busyFn, w.change.amendFn, w.change.resumeFn
	poll, timeout := w.change.poll, w.change.timeout
	w.change.mu.Unlock()
	if stopFn == nil {
		stopFn = w.marshalStop
	}
	if busyFn == nil {
		busyFn = w.marshalBusy
	}
	if amendFn == nil {
		amendFn = w.defaultChangeAmend
	}
	if resumeFn == nil {
		resumeFn = w.marshalResume
	}
	if poll <= 0 {
		poll = 200 * time.Millisecond
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return stopFn, busyFn, amendFn, resumeFn, poll, timeout
}

// marshalBusy reports whether a Marshal operation is running.
func (w *Workspace) marshalBusy() bool {
	m := w.marshalSession()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.busy
}

// defaultChangeAmend runs the amendment through the same code marshalAmend
// uses, once the run operation is free. It reports whether the amendment was
// major.
func (w *Workspace) defaultChangeAmend(ctx context.Context, reason string) (bool, error) {
	m, service, runID, provider, err := w.marshalActive(ctx)
	if err != nil {
		return false, err
	}
	runCtx, cancel, err := w.reserveMarshalOperation(ctx)
	if err != nil {
		return false, err
	}
	defer m.finish(cancel)
	return w.runAmend(runCtx, m, service, runID, provider, reason)
}

// waitUntilFalse polls cond until it is false or the timeout elapses. It
// reports whether cond went false in time.
func waitUntilFalse(cond func() bool, poll, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for cond() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(poll)
	}
	return true
}

// changeHoldHeadline is the card's first line: how many tasks the hold paused,
// and whether any affected task is already running and will finish first. It
// is written by the runtime from the counts it verified, never by the model.
func changeHoldHeadline(notStarted, running int) string {
	if notStarted == 0 {
		return "The affected tasks are already running; they will finish first."
	}
	msg := fmt.Sprintf("I paused %d affected %s.", notStarted, changePlural(notStarted, "task", "tasks"))
	if running > 0 {
		msg += fmt.Sprintf(" %d %s already running and will finish first.", running, changeIsAre(running))
	}
	return msg
}

func changePlural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func changeIsAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

// changeButton is one action on the change card.
type changeButton struct {
	key, label, id string
}

// changeEntry is the open change card as the inbox shows it: the headline on
// one or more rows, then a row of one-key actions. x is the inbox's first
// cell, row its frame row.
func (v boardVisual) changeEntry(c changeCard, x, row, w int) []string {
	flag := "⚑ "
	if !v.unicode() {
		flag = "! "
	}
	label := "change"
	if c.ownerHold() {
		label = "held"
	}
	head := v.fg("waiting", flag+label) + v.fg("dim", " · ")
	prefix := VisibleLen(head)
	var headline string
	switch {
	case c.ownerHold():
		headline = ownerHoldHeadline(c.held)
	case c.timedOut:
		headline = "I couldn't pause the run to apply the change. Press Enter to try again."
	case c.applying:
		headline = "Applying your change…"
	default:
		headline = changeHoldHeadline(c.notStarted, c.running)
	}
	lines := wrapWords(cleanBoard(headline), max(8, w-prefix))
	if len(lines) == 0 {
		lines = []string{""}
	}
	out := []string{PadCell(head+v.bold(v.fg("white", lines[0])), w)}
	for _, extra := range lines[1:] {
		out = append(out, PadCell(strings.Repeat(" ", prefix)+v.bold(v.fg("white", extra)), w))
	}
	var buttons []changeButton
	switch {
	case c.ownerHold():
		buttons = []changeButton{{"x", "release the hold", "release"}}
	case c.timedOut:
		buttons = []changeButton{{"Enter", "try again", "apply"}}
	case c.applying:
		return out
	default:
		buttons = []changeButton{{"Enter", "apply this change", "apply"}, {"k", "keep the current goal", "keep"}}
	}
	crow := row + len(out)
	line, at := "    ", 4
	for i, b := range buttons {
		if i > 0 {
			line += "  "
			at += 2
		}
		key := b.key
		if key == "Enter" {
			key = v.cardEnterKey()
		}
		button := v.bg("rule", "["+key+"]") + " " + b.label
		v.hit(boardHit{row: crow, x0: x + at, x1: x + min(w, at+VisibleLen(button)), kind: hitChange, id: b.id})
		line += button
		at += VisibleLen(button)
	}
	out = append(out, PadCell(line, w))
	return out
}

// changeCount is 1 while a change card is open, for the inbox's counts.
func (v boardVisual) changeCount() int {
	if v.change != nil {
		return 1
	}
	return 0
}

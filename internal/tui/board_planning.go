package tui

// planningCard is the first-use card's one line between the owner's
// architecture Accept and the run it plans.
const planningCard = "The Marshal is planning the tasks…"

// planningNow reports whether the Board shows the planning state: the owner
// accepted the architecture and no run exists yet. Once a run is on screen
// the state ends for good, so a later empty Board says NO RUN YET again.
func (w *Workspace) planningNow(empty bool) bool {
	if !empty {
		w.planning.Store(false)
		return false
	}
	return w.planning.Load()
}

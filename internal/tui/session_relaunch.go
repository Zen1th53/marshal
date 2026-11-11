package tui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

// CTUI-0219 Open by session ID.
//
// A workspace's session binding is fixed when it is launched: every Control,
// Board and goal authority is keyed by it. Opening another session therefore
// never rebinds this process. The workspace exits as /quit does, and the
// launcher (internal/cli) replaces the process with `marshal tui SESSION-ID`
// in the same terminal, so inside tmux the pane and its window stay where
// they are; the Marshal chat leaves the dock as on any exit and the new
// workspace docks it again, as at any start. The hand-over is refused while anything
// bound to this session would be cut off: a native worker window, a governed
// dispatch, or a Marshal run that is moving.

// SessionRelaunch is what Workspace.Run returns when the operator chose to
// open another session. The launcher execs `marshal tui SessionID`.
type SessionRelaunch struct {
	SessionID string
}

func (r *SessionRelaunch) Error() string {
	return "relaunch the workspace in session " + r.SessionID
}

// sessionIDPattern is what a session id typed here may be: it becomes one
// argument of `marshal tui`, so it can never read as a flag.
var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func validateSessionID(v string) error {
	if !sessionIDPattern.MatchString(strings.TrimSpace(v)) {
		return errors.New("a session id is letters, digits, '.', '_' or '-', starting with a letter or digit, at most 128")
	}
	return nil
}

// EnableSessionRelaunch is called by the launcher that execs the relaunch;
// without it the workspace refuses to open another session, since nothing
// would start it.
func (w *Workspace) EnableSessionRelaunch() {
	w.mu.Lock()
	w.relaunchable = true
	w.mu.Unlock()
}

// relaunchBlockers names everything that keeps this workspace from handing
// its terminal to another session now. Empty means it may.
func (w *Workspace) relaunchBlockers() []string {
	var out []string
	w.mu.RLock()
	relaunchable := w.relaunchable
	interactive := w.uiEvents != nil
	dispatching := len(w.governedDispatches)
	w.mu.RUnlock()
	if !relaunchable || !interactive {
		out = append(out, "this workspace was not started by `marshal tui` on a terminal, so nothing would open the other session; run `marshal tui SESSION-ID` instead")
	}
	var native []string
	w.tmuxMu.Lock()
	for _, a := range w.tmuxActiveWins {
		if a.role != "marshal-chat" {
			native = append(native, cmp.Or(a.label, a.id, a.provider))
		}
	}
	w.tmuxMu.Unlock()
	sort.Strings(native)
	if len(native) > 0 {
		out = append(out, fmt.Sprintf("native window(s) %s are bound to session %s; stop them first (/stop all)", strings.Join(native, ", "), w.sessionID))
	}
	if dispatching > 0 {
		out = append(out, fmt.Sprintf("%d governed dispatch(es) are running in session %s", dispatching, w.sessionID))
	}
	if p := w.marshalPanel(); p != nil {
		switch p.State {
		case marshal.Approved, marshal.Dispatching, marshal.Reviewing, marshal.Merging, marshal.Verifying:
			out = append(out, fmt.Sprintf("Marshal run %s is %s in session %s; pause it or let it finish first", p.RunID, strings.ToLower(string(p.State)), w.sessionID))
		}
	}
	return out
}

// sessionSummary is what the store knows of a session, for the operator to
// review before the hand-over. A session nothing was recorded in yet is
// still a session: `marshal tui SESSION-ID` starts it.
func (w *Workspace) sessionSummary(ctx context.Context, id string) (string, error) {
	if id == w.sessionID {
		return "", fmt.Errorf("session %s is the one this workspace already has open", id)
	}
	st := w.store
	if st == nil {
		return "", errNoRuntime
	}
	var known []string
	goal, err := st.GetActiveGoalContract(ctx, id)
	switch {
	case err == nil:
		known = append(known, "active goal "+goal.ID)
	case !errors.Is(err, model.ErrGoalNotFound):
		return "", fmt.Errorf("read the session's goal: %w", err)
	}
	if sess, err := st.GetTeamSession(ctx, id); err == nil && sess != nil {
		known = append(known, fmt.Sprintf("team session %s, %d participant(s)", sess.Status, len(sess.Participants)))
	}
	if len(known) == 0 {
		return "new session: nothing recorded under it yet", nil
	}
	return strings.Join(known, "; "), nil
}

// requestRelaunch records the hand-over and asks the workspace to exit,
// checking again what the review checked: the state may have moved.
func (w *Workspace) requestRelaunch(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if err := validateSessionID(id); err != nil {
		return err
	}
	if id == w.sessionID {
		return fmt.Errorf("session %s is already open", id)
	}
	if blockers := w.relaunchBlockers(); len(blockers) > 0 {
		return errors.New(strings.Join(blockers, "; "))
	}
	w.mu.Lock()
	w.relaunchSession = id
	w.exitRequested = true
	events := w.uiEvents
	w.mu.Unlock()
	// The action runs off the input loop; wake the loop so it leaves now
	// rather than on the next key. A full queue is the loop already busy,
	// and it checks the exit after each event it runs.
	if events != nil {
		select {
		case events <- func() {}:
		default:
		}
	}
	return nil
}

func (w *Workspace) relaunchTarget() string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.relaunchSession
}

// sessionRelauncher is the workspace seen from the Control authority.
type sessionRelauncher interface {
	relaunchBlockers() []string
	sessionSummary(ctx context.Context, id string) (string, error)
	requestRelaunch(ctx context.Context, id string) error
	relaunchTarget() string
}

// SessionRelaunchAuthority is optional: an authority opens another session
// only when the workspace behind it can hand its terminal over.
type SessionRelaunchAuthority interface {
	RelaunchBlockers() []string
	SessionSummary(ctx context.Context, id string) (string, error)
	RequestSessionRelaunch(ctx context.Context, id string) error
	SessionRelaunchRequested() string
}

func (a *runtimeControlAuthority) RelaunchBlockers() []string {
	if a == nil || a.relauncher == nil {
		return []string{"workspace session hand-over is unavailable"}
	}
	return a.relauncher.relaunchBlockers()
}

func (a *runtimeControlAuthority) SessionSummary(ctx context.Context, id string) (string, error) {
	if a == nil || a.relauncher == nil {
		return "", errors.New("workspace session hand-over is unavailable")
	}
	return a.relauncher.sessionSummary(ctx, id)
}

func (a *runtimeControlAuthority) RequestSessionRelaunch(ctx context.Context, id string) error {
	if a == nil || a.relauncher == nil {
		return errors.New("workspace session hand-over is unavailable")
	}
	return a.relauncher.requestRelaunch(ctx, id)
}

func (a *runtimeControlAuthority) SessionRelaunchRequested() string {
	if a == nil || a.relauncher == nil {
		return ""
	}
	return a.relauncher.relaunchTarget()
}

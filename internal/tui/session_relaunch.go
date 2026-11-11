package tui

// SessionRelaunch is what Workspace.Run returns when a workspace hands its
// terminal to another session. The launcher (internal/cli) catches it and
// execs `marshal tui SessionID` on the same terminal.
type SessionRelaunch struct {
	SessionID string
}

func (r *SessionRelaunch) Error() string {
	return "relaunch the workspace in session " + r.SessionID
}

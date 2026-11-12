package tui

import "context"

// MCP is machinery: while it works the owner never sees it. mcpFault is the
// one place a failure of it is kept, a plain sentence the Memory view shows
// as its MCP line and F6 opens. Nothing reads it to decide anything.

// noteMCPFailure records why MCP failed, replacing an older reason.
func (w *Workspace) noteMCPFailure(why string) {
	if why == "" {
		return
	}
	w.mcpFault.Store(&why)
}

// mcpFailure is the last MCP failure, or "" while MCP works.
func (w *Workspace) mcpFailure() string {
	if p := w.mcpFault.Load(); p != nil {
		return *p
	}
	return ""
}

// mcpFailed reports whether MCP has failed in this session.
func (w *Workspace) mcpFailed() bool { return w.mcpFault.Load() != nil }

// mcpNoFailure is what F6 and its palette row say while MCP works.
const mcpNoFailure = "MCP is working; there is no failure to show."

// showMCPFailure is F6: the MCP failure's detail, or that there is none.
func (w *Workspace) showMCPFailure(context.Context) {
	if why := w.mcpFailure(); why != "" {
		w.setOutput(why, true)
		return
	}
	w.setOutput(mcpNoFailure, false)
}

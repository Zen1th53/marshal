//go:build !governed

package tui

import (
	"os/exec"
	"strings"
	"testing"
)

// The Marshal chat started from inside a Claude Code session must not inherit
// that session's markers: with CLAUDE_CODE_CHILD_SESSION Claude Code saves no
// transcript, the chat's conversation ID is never captured, and the browser
// Accept is refused for a live chat.
func TestMarshalChatCommandDropsLaunchingClaudeSession(t *testing.T) {
	for _, k := range []string{"CLAUDECODE", "CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_MESSAGING_SOCKET", "CLAUDE_CODE_MESSAGING_TOKEN", "TMUX", "TMUX_PANE"} {
		t.Setenv(k, "inherited")
	}
	t.Setenv("MARSHAL_KEEP_ME", "kept")
	envBin, err := exec.LookPath("env")
	if err != nil {
		t.Skip("no env binary")
	}
	cmd := marshalChatCommand(envBin, nil)
	out, err := exec.Command(cmd[0], cmd[1:]...).Output()
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, line := range strings.Split(got, "\n") {
		if strings.HasSuffix(line, "=inherited") {
			t.Fatalf("Marshal chat inherits %s", line)
		}
	}
	if !strings.Contains(got, "MARSHAL_KEEP_ME=kept") {
		t.Fatalf("unrelated environment dropped: %s", got)
	}
}

// boundChatFixture is a live tmux Marshal chat whose conversation ID was never
// captured, holding the live MCP launch "L1" when leased.
func boundChatFixture(t *testing.T, leased bool) (*Workspace, *activeTmuxAgent, *proposalMCPLease) {
	t.Helper()
	dir := &briefingDir{}
	a := &activeTmuxAgent{id: "marshal-chat", role: "marshal-chat", provider: "claude", paneID: "%1", state: "working", briefingDir: dir, mcpLaunchID: "L1"}
	w := &Workspace{tmuxActiveWins: map[string]*activeTmuxAgent{"marshal-chat": a}}
	l := &proposalMCPLease{id: "L1", provider: "claude", dir: dir, active: leased}
	w.proposalMCP.leases = map[*briefingDir]*proposalMCPLease{dir: l}
	w.proposalMCP.active = l
	return w, a, l
}

func TestBoundMarshalArchitectureSourceUsesLiveLaunch(t *testing.T) {
	w, a, _ := boundChatFixture(t, true)
	if got := w.boundMarshalArchitectureSource(); got != "launch:L1" {
		t.Fatalf("live leased chat without a captured session: %q", got)
	}
	// File-dropped proposals keep needing a captured conversation.
	if got := w.boundMarshalChatSession(); got != "" {
		t.Fatalf("file proposals trusted on the launch alone: %q", got)
	}
	w.tmuxMu.Lock()
	a.sessionID = "conv-1"
	w.tmuxMu.Unlock()
	if got := w.boundMarshalArchitectureSource(); got != "conv-1" {
		t.Fatalf("captured session not preferred: %q", got)
	}
}

func TestBoundMarshalArchitectureRefusalsSayWhatIsMissing(t *testing.T) {
	cases := []struct {
		name string
		set  func(w *Workspace, a *activeTmuxAgent, l *proposalMCPLease)
		want string
	}{
		{"no chat", func(w *Workspace, _ *activeTmuxAgent, _ *proposalMCPLease) { w.tmuxActiveWins = nil }, "The Marshal chat is not running."},
		{"ended", func(_ *Workspace, a *activeTmuxAgent, _ *proposalMCPLease) { a.state = "done" }, "The Marshal chat has ended."},
		{"no launch", func(_ *Workspace, _ *activeTmuxAgent, l *proposalMCPLease) { l.active = false }, "neither its conversation ID nor a live proposal connection"},
		{"foreign launch", func(_ *Workspace, a *activeTmuxAgent, _ *proposalMCPLease) { a.mcpLaunchID = "L0" }, "neither its conversation ID nor a live proposal connection"},
		{"replaced pane", func(_ *Workspace, a *activeTmuxAgent, _ *proposalMCPLease) { a.briefingDir = &briefingDir{} }, "neither its conversation ID nor a live proposal connection"},
		{"mcp unavailable", func(_ *Workspace, a *activeTmuxAgent, _ *proposalMCPLease) { a.state = "mcp-unavailable" }, "MARSHAL connection is unavailable"},
	}
	for _, c := range cases {
		w, a, l := boundChatFixture(t, true)
		c.set(w, a, l)
		source, err := w.boundMarshalArchitectureBinding()
		if source != "" || err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: %q %v", c.name, source, err)
		}
		if strings.Contains(err.Error(), "Open the Marshal chat in your terminal, then") {
			t.Fatalf("%s: stale refusal %v", c.name, err)
		}
	}
}

package tui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/store"
)

// relaunchFake is the Control authority over a workspace's hand-over, as
// runtimeControlAuthority wires it.
type relaunchFake struct {
	*fakeAuthority
	w *Workspace
}

func (f relaunchFake) RelaunchBlockers() []string { return f.w.relaunchBlockers() }
func (f relaunchFake) SessionSummary(ctx context.Context, id string) (string, error) {
	return f.w.sessionSummary(ctx, id)
}
func (f relaunchFake) RequestSessionRelaunch(ctx context.Context, id string) error {
	return f.w.requestRelaunch(ctx, id)
}
func (f relaunchFake) SessionRelaunchRequested() string { return f.w.relaunchTarget() }

func relaunchStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "relaunch.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return st
}

func relaunchWorkspace(t *testing.T) (*Workspace, Binding) {
	t.Helper()
	w := NewWorkspace(relaunchStore(t), "project", "S-first")
	w.uiEvents = make(chan func(), 1) // an interactive terminal
	w.EnableSessionRelaunch()
	s := &ControlSource{Authority: relaunchFake{newFakeAuthority(), w}, SessionID: "S-first", ProjectID: "project"}
	return w, s.Bindings()["CTUI-0219"]
}

func prepareRelaunch(b Binding, id string) (Target, error) {
	return b.PrepareRequest(context.Background(), ActionRequest{Action: "CTUI-0219", Inputs: map[string]string{"session_id": id}})
}

func TestOpenBySessionIDRelaunchesAfterConfirmation(t *testing.T) {
	w, b := relaunchWorkspace(t)
	if !b.Bound() || len(b.Inputs) != 1 || b.Inputs[0].Key != "session_id" {
		t.Fatalf("CTUI-0219 is not a form over the session id: %+v", b)
	}
	target, err := prepareRelaunch(b, "S-second")
	if err != nil {
		t.Fatal(err)
	}
	if target.ID != "S-second" || !strings.Contains(target.Summary, "new session") || !strings.Contains(target.Scope, "leave session S-first") {
		t.Fatalf("review target %+v", target)
	}
	// Reviewing changes nothing: only the confirmation hands over.
	if w.relaunchTarget() != "" || w.commandExitRequested() {
		t.Fatal("preparing the action already asked the workspace to leave")
	}
	out, err := b.Execute(context.Background(), ActionRequest{Action: "CTUI-0219", Target: target})
	if err != nil || out.Verdict != VerdictPass {
		t.Fatalf("execute: %+v %v", out, err)
	}
	if w.relaunchTarget() != "S-second" || !w.commandExitRequested() {
		t.Fatalf("hand-over not recorded: %q exit %v", w.relaunchTarget(), w.commandExitRequested())
	}
	var r *SessionRelaunch
	if err := error(&SessionRelaunch{SessionID: w.relaunchTarget()}); !errors.As(err, &r) || r.SessionID != "S-second" {
		t.Fatal("SessionRelaunch does not carry the session")
	}
}

func TestOpenBySessionIDRefusals(t *testing.T) {
	for _, bad := range []string{"", "--codex", "-x", "two words", "a;b", "S-first", strings.Repeat("a", 129)} {
		_, b := relaunchWorkspace(t)
		if _, err := prepareRelaunch(b, bad); err == nil {
			t.Errorf("session id %q was accepted", bad)
		}
	}
	// A native worker window is bound to this session.
	w, b := relaunchWorkspace(t)
	w.tmuxMu.Lock()
	w.tmuxActiveWins["task-T-1"] = &activeTmuxAgent{id: "task-T-1", role: "task", label: "codex T-1"}
	w.tmuxActiveWins["marshal-chat"] = &activeTmuxAgent{id: "marshal-chat", role: "marshal-chat"}
	w.tmuxMu.Unlock()
	if _, err := prepareRelaunch(b, "S-second"); err == nil || !strings.Contains(err.Error(), "codex T-1") || strings.Contains(err.Error(), "marshal-chat") {
		t.Fatalf("a worker window did not refuse (the chat must not): %v", err)
	}
	// The state moved between review and confirmation: Execute checks again.
	w, b = relaunchWorkspace(t)
	target, err := prepareRelaunch(b, "S-second")
	if err != nil {
		t.Fatal(err)
	}
	p := newMarshalPanel("R-1", "codex", marshal.Run{State: marshal.Dispatching}, "running")
	p.State = marshal.Dispatching
	w.setMarshalPanel(p)
	out, _ := b.Execute(context.Background(), ActionRequest{Action: "CTUI-0219", Target: target})
	if out.Verdict == VerdictPass || w.relaunchTarget() != "" || w.commandExitRequested() {
		t.Fatalf("a moving run did not refuse the hand-over: %+v", out)
	}
	// A workspace no launcher will relaunch refuses.
	w = NewWorkspace(relaunchStore(t), "project", "S-first")
	s := &ControlSource{Authority: relaunchFake{newFakeAuthority(), w}, SessionID: "S-first"}
	if _, err := prepareRelaunch(s.Bindings()["CTUI-0219"], "S-second"); err == nil || !strings.Contains(err.Error(), "marshal tui SESSION-ID") {
		t.Fatalf("an unlaunched workspace offered the hand-over: %v", err)
	}
	// An authority with no hand-over keeps the entry unbound, saying why.
	plain := (&ControlSource{Authority: newFakeAuthority()}).Bindings()["CTUI-0219"]
	if plain.Bound() || plain.Requires == "" {
		t.Fatalf("CTUI-0219 without a hand-over authority: %+v", plain)
	}
}

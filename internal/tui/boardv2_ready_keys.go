package tui

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/cloud"
	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/tmux"
	boardv2 "github.com/Zen1th53/marshal/internal/tui/board/v2"
	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

// The keys of the Board v2 readiness screen's own cards, and the key bar of
// the v2 cards. Every key the v2 Board draws acts as labelled in the state
// it is drawn in:
//
//   - the Usage counts card: y keeps telemetry on and records that the
//     owner was told (the one-time notice, cloud.Store.TakeTelemetryNotice);
//     t turns it off through cloud.SetTelemetry, the call `marshal
//     telemetry disable` makes; ? says what is sent. After y or t the card
//     goes.
//   - g, offered only when agy is installed but not signed in and the Board
//     runs in tmux: a new tmux window runs agy itself in the project, where
//     the owner signs in; when that window closes the verify of agy is armed
//     (Enter runs it, one model call), as v arms it.
//   - a decision or paused card's key bar is the card's own buttons, esc for
//     "later" on a decision, and the palette and help when they can run.

// v2ReadyShown is what the last v2 paint drew on the readiness screen.
type v2ReadyShown struct {
	mu     sync.Mutex
	usage  bool // the Usage counts card
	signIn bool // the g sign in to agy button
	// signingIn is the pane of an open agy sign-in window, "" for none.
	signingIn string
}

var v2Ready sync.Map // *Workspace -> *v2ReadyShown

func (w *Workspace) v2ReadyState() *v2ReadyShown {
	if s, ok := v2Ready.Load(w); ok {
		return s.(*v2ReadyShown)
	}
	s, _ := v2Ready.LoadOrStore(w, &v2ReadyShown{})
	return s.(*v2ReadyShown)
}

// noteV2Ready records which readiness keys the paint drew.
func (w *Workspace) noteV2Ready(usage, signIn bool) {
	s := w.v2ReadyState()
	s.mu.Lock()
	s.usage, s.signIn = usage, signIn
	s.mu.Unlock()
}

func (w *Workspace) v2ReadyDrawn() (usage, signIn bool) {
	s := w.v2ReadyState()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.usage, s.signIn
}

// v2TelemetryWhat is what ? on the Usage counts card says.
const v2TelemetryWhat = "Usage counts send a random installation ID and public key, the Marshal version, OS and architecture, the run mode, and start and heartbeat times; no code, prompts or file names. docs/telemetry.md lists every field. y keeps it on, t turns it off (as marshal telemetry disable does)."

// v2TelemetryParts are the telemetry choice's effects; tests replace them.
var v2TelemetryParts = struct {
	keep    func(ctx context.Context, root string) error
	disable func(ctx context.Context, root string) error
}{
	keep: func(ctx context.Context, root string) error {
		dir, err := cloud.MachineStateDir(filepath.Join(root, projectid.StateDirName))
		if err != nil {
			return err
		}
		cloud.NewStore(dir).TakeTelemetryNotice(ctx)
		return nil
	},
	disable: func(ctx context.Context, root string) error {
		dir, err := cloud.MachineStateDir(filepath.Join(root, projectid.StateDirName))
		if err != nil {
			return err
		}
		_, _, err = cloud.SetTelemetry(ctx, dir, false, BuildVersion)
		return err
	},
}

// v2SignInParts are the agy sign-in window's tmux effects; tests replace
// them. here is nil for the Board's own tmux check (a literal would be an
// initialization cycle through the paint path). open starts agy in a new window of the Board's tmux session in dir
// and returns its pane; closed reports whether that pane is gone or dead.
var v2SignInParts = struct {
	here   func(w *Workspace) bool
	open   func(ctx context.Context, w *Workspace, dir string) (string, error)
	closed func(ctx context.Context, w *Workspace, pane string) bool
	every  time.Duration
}{
	open: func(ctx context.Context, w *Workspace, dir string) (string, error) {
		binary, err := project.FindBinary(antigravityBinary)
		if err != nil {
			return "", err
		}
		var pane string
		if err := tmux.NewWindow(ctx, w.tmuxSession, tmux.WindowName("agy-sign-in", dir), dir, nil, []string{binary}, &pane); err != nil {
			return "", err
		}
		_ = tmux.SelectWindow(ctx, pane)
		return pane, nil
	},
	closed: func(ctx context.Context, w *Workspace, pane string) bool {
		panes, err := tmux.ListPanes(ctx, w.tmuxSession)
		if err != nil {
			return true
		}
		if !slices.ContainsFunc(panes, func(p tmux.PaneInfo) bool { return p.PaneID == pane }) {
			return true
		}
		if dead, err := tmux.IsPaneDead(ctx, pane); err == nil && dead {
			_ = tmux.KillPane(ctx, pane)
			return true
		}
		return false
	},
	every: time.Second,
}

// addV2ReadyActions puts g on the readiness checklist's action row when
// agy is installed but not signed in and a tmux window can be opened for its
// sign-in; elsewhere the row's plain hint says how to sign in.
func (w *Workspace) addV2ReadyActions(r *vm.Readiness) {
	at := -1
	for i, c := range r.Checks {
		if checkProvider(c) == "agy" && strings.Contains(c.Text, "not signed in") {
			at = i
			break
		}
	}
	here := (*Workspace).v2TmuxReady
	if v2SignInParts.here != nil {
		here = v2SignInParts.here
	}
	if at < 0 || !here(w) {
		return
	}
	r.Checks[at].Hint = signInHint("agy") + " · g opens it here"
	r.Actions = append(r.Actions, vm.Key{Key: "g", Label: "sign in to agy"})
}

// v2ReadyBar is the key bar of the readiness screen: while the Usage counts
// card is up, ? says what is sent, so the bar says so too.
func v2ReadyBar(b vm.Board) []vm.Key {
	if b.Screen != vm.ScreenReady || !b.Ready.TelemetryAsk {
		return b.Keys
	}
	keys := slices.Clone(b.Keys)
	for i := range keys {
		if keys[i].Key == "?" {
			keys[i].Label = "what is sent"
		}
	}
	return keys
}

// renderV2Ready renders b full screen and records which readiness keys the
// render drew: the Usage counts card's buttons (the card goes first when
// rows are short) and the g button. ? says what is sent, and the key bar
// says so, only while the card is drawn.
func (w *Workspace) renderV2Ready(b vm.Board, cols, rows int, t boardv2.Theme) ([]string, []boardv2.Hit) {
	plain := b.Keys
	if b.Screen == vm.ScreenReady && b.Ready.TelemetryAsk {
		b.Keys = v2ReadyBar(b)
	}
	lines, hits := boardv2.RenderChatHits(b, cols, rows, t, nil)
	usage, signIn := v2ReadyDrawnIn(b, lines, hits)
	if !usage && b.Ready.TelemetryAsk && !slices.Equal(plain, b.Keys) {
		b.Keys = plain
		lines, hits = boardv2.RenderChatHits(b, cols, rows, t, nil)
	}
	w.noteV2Ready(usage, signIn)
	return lines, hits
}

// v2ReadyDrawnIn reports whether a render of the readiness screen drew the
// Usage counts card's buttons and the g button.
func v2ReadyDrawnIn(b vm.Board, lines []string, hits []boardv2.Hit) (usage, signIn bool) {
	if b.Screen != vm.ScreenReady {
		return false, false
	}
	signIn = slices.ContainsFunc(hits, func(h boardv2.Hit) bool { return h.Kind == boardv2.HitReady && h.Key == "g" })
	if b.Ready.TelemetryAsk {
		text := StripANSI(strings.Join(lines, "\n"))
		usage = strings.Contains(text, "t turn off") && strings.Contains(text, "y keep on") && strings.Contains(text, "? what is sent")
	}
	return usage, signIn
}

// v2CardBar is a decision or paused card's key bar: its buttons, esc for
// "later" on a decision, and the palette and help when they can run, as the
// hint bar writes them.
func (w *Workspace) v2CardBar(v boardVisual, b vm.Board) []vm.Key {
	var keys []vm.Key
	switch {
	case b.Decision != nil:
		keys = append(keys, b.Decision.Actions...)
		keys = append(keys, vm.Key{Key: "esc", Label: "later"})
	case b.Paused != nil:
		keys = append(keys, b.Paused.Actions...)
	}
	for _, id := range w.availableHints([]string{"palette", "help"}) {
		if k, ok := boardAction(id); ok {
			keys = append(keys, vm.Key{Key: v.hintKey(k.Keys), Label: k.Hint})
		}
	}
	return keys
}

// handleV2ReadyKey is a key on the readiness screen's own cards; it reports
// whether one took it. It runs on the Board with no run and nothing open
// over it, before the gates take y; an armed verify keeps "any other key
// cancels".
func (w *Workspace) handleV2ReadyKey(ctx context.Context, event KeyEvent) bool {
	if event.Type != KeyRune || w.firstUseArmed() != "" || w.inboxView.Inspecting() || len(w.boardInbox()) > 0 {
		return false
	}
	usage, signIn := w.v2ReadyDrawn()
	switch {
	case usage && event.Rune == '?':
		w.memoryAnswer(v2TelemetryWhat, false)
		return true
	case usage && (event.Rune == 'y' || event.Rune == 't'):
		w.chooseTelemetry(ctx, event.Rune == 'y')
		return true
	case signIn && event.Rune == 'g':
		w.openAgySignIn(ctx)
		return true
	}
	return false
}

// chooseTelemetry keeps telemetry on (and records the notice shown) or turns
// it off. The card goes at once; the choice is saved off the input loop.
func (w *Workspace) chooseTelemetry(ctx context.Context, keep bool) {
	s := &w.v2m
	s.mu.Lock()
	s.probe.Told = true
	if !keep {
		s.probe.TelemetryOn = false
	}
	s.mu.Unlock()
	w.noteV2Ready(false, false)
	root := w.providerRoot()
	run := func(ctx context.Context) {
		var err error
		if keep {
			err = v2TelemetryParts.keep(ctx, root)
		} else {
			err = v2TelemetryParts.disable(ctx, root)
		}
		switch {
		case err != nil:
			w.memoryAnswer("Could not save the usage counts choice: "+err.Error(), true)
			s.mu.Lock()
			s.at = time.Time{} // read the saved choice again
			s.mu.Unlock()
		case keep:
			w.RecordActivity("Usage counts stay on: anonymous install and version counts only. marshal telemetry disable turns them off.")
			w.memoryAnswer("Usage counts stay on.", false)
		default:
			w.RecordActivity("Usage counts turned off, as marshal telemetry disable does.")
			w.memoryAnswer("Usage counts are off.", false)
		}
		w.requestRepaint()
	}
	if !w.startBackground(ctx, run) {
		run(ctx)
	}
}

// openAgySignIn is g: agy runs in a new tmux window in the project, where
// the owner signs in; when the window closes the verify of agy is armed.
func (w *Workspace) openAgySignIn(ctx context.Context) {
	s := w.v2ReadyState()
	s.mu.Lock()
	open := s.signingIn
	s.mu.Unlock()
	if open != "" {
		w.memoryAnswer("agy's sign-in window is already open; sign in there and close it.", false)
		return
	}
	dir := w.providerRoot()
	pane, err := v2SignInParts.open(ctx, w, dir)
	if err != nil {
		w.memoryAnswer("Could not open a window for agy: "+err.Error()+". "+signInHint("agy")+".", true)
		return
	}
	s.mu.Lock()
	s.signingIn = pane
	s.mu.Unlock()
	w.RecordActivity("Opened agy in a tmux window to sign in; the Board checks agy again when it closes.")
	w.memoryAnswer("agy is open in its own window: sign in there, then close it.", false)
	watch := func(ctx context.Context) {
		t := time.NewTicker(v2SignInParts.every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			if v2SignInParts.closed(ctx, w, pane) {
				break
			}
		}
		s.mu.Lock()
		s.signingIn = ""
		s.mu.Unlock()
		w.agySignInClosed()
	}
	if !w.startBackground(context.Background(), watch) {
		s.mu.Lock()
		s.signingIn = ""
		s.mu.Unlock()
	}
}

// agySignInClosed re-checks agy once its sign-in window closed: the
// readiness probe runs again and, on the readiness screen, the verify of
// agy is armed (Enter runs it, one model call; v arms it too).
func (w *Workspace) agySignInClosed() {
	w.v2m.mu.Lock()
	w.v2m.at = time.Time{}
	w.v2m.mu.Unlock()
	w.RecordActivity("agy's sign-in window closed.")
	if w.firstUseBoard() && w.firstUseArmed() == "" && !w.composing() {
		w.armFirstUseVerify("agy")
	}
	w.requestRepaint()
}

// v2TmuxReady reports, without starting anything (it runs on the paint
// path), whether the Board is connected to its tmux session, so a window
// can open beside it.
func (w *Workspace) v2TmuxReady() bool {
	if !tmux.IsInsideTmux() {
		return false
	}
	w.tmuxMu.Lock()
	defer w.tmuxMu.Unlock()
	return w.tmuxPath != "" && w.tmuxSession != ""
}

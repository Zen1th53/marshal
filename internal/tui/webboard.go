package tui

import (
	"context"
	"errors"
	"sync"

	"github.com/Zen1th53/marshal/internal/tui/board/vm"
	"github.com/Zen1th53/marshal/internal/webboard"
)

// The Web Board is the Board in a local browser, on 127.0.0.1 only, behind
// a private link (internal/webboard): it shows the Board and lets the owner
// press the buttons of the card on screen and write to the Marshal
// (webboard_act.go). The open Board process
// hosts it, because it has the data; it starts on demand (w on the Board
// with no task highlighted, the palette, /marshal web) and stops when the
// Board closes.

// webBoards holds each workspace's running Web Board.
var webBoards sync.Map // *Workspace -> *webBoardState

type webBoardState struct {
	mu     sync.Mutex
	server *webboard.Server
	closed bool
	// retry is the task r would retry, noted on the input loop at each
	// paint (noteWebRetry), "" for none.
	retry string
}

// webBoardStart and webBoardOpen are the server and the browser opener;
// tests replace them. The opener is the architecture view's.
var (
	webBoardStart = webboard.Start
	webBoardOpen  = openArchitectureBrowser
)

// webBoardSink is the owner actions' sink for w, set in init: the action
// path reaches the commands the Board runs, which reach /marshal web, so a
// direct reference would be an initialization cycle.
var webBoardSink func(w *Workspace) webboard.Sink

func init() {
	webBoardSink = func(w *Workspace) webboard.Sink { return w.webBoardAct }
}

var errWorkspaceClosing = errors.New("workspace is closing")

func (w *Workspace) webBoard() *webBoardState {
	s, _ := webBoards.LoadOrStore(w, &webBoardState{})
	return s.(*webBoardState)
}

// webBoardFrame is one frame for the browser: the frame the terminal Board
// paints (paintV2), with its screen chosen the same way. It calls no paint
// function, so it moves no selection, hit region or shown card. It runs on
// the server's goroutines, never with w.mu held.
func (w *Workspace) webBoardFrame() vm.Board {
	b := w.boardV2()
	state := w.GetUIState()
	empty := state.Marshal == nil
	_, goalGiven := w.activeGoalNow(empty, state.Goal)
	b.Screen = v2Screen(b, empty, w.planningNow(empty, state.Goal) || goalGiven)
	if b.Now.IsZero() {
		b.Now = boardNow()
	}
	// The key bar is the terminal's. With no card the page gets the
	// screen's own buttons it may press instead (webScreenKeys); a card
	// brings its own.
	b.Keys = nil
	if b.Decision == nil && b.Paused == nil {
		b.Keys = w.webScreenKeys()
	}
	return b
}

// webBoardServer is the workspace's Web Board, started when it is not
// running.
func (w *Workspace) webBoardServer() (*webboard.Server, error) {
	s := w.webBoard()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errWorkspaceClosing
	}
	if s.server != nil {
		select {
		case <-s.server.Done():
			s.server = nil
		default:
			return s.server, nil
		}
	}
	srv, err := webBoardStart(w.webBoardFrame, webBoardSink(w))
	if err != nil {
		return nil, err
	}
	s.server = srv
	return srv, nil
}

// closeWebBoard stops the Web Board and refuses a new one; Close calls it.
func (w *Workspace) closeWebBoard() {
	// Marked closed even when it never started, so a late key or command
	// during shutdown cannot start one that nothing stops.
	s := w.webBoard()
	s.mu.Lock()
	s.closed = true
	srv := s.server
	s.server = nil
	s.mu.Unlock()
	srv.Close()
}

// marshalWeb is /marshal web: it starts the Web Board if needed, opens it in
// the browser where there is a display, and answers. The link can act for
// the owner, so it is shown only where it has to be: in the footer when no
// browser can be opened, never in Activity (which the Web Board itself
// shows), a file or the Marshal. Each link works once (the page spends it
// for its own secret), so a copy left in the footer, the browser opener's
// argv or the browser's history is worthless once the owner's page opened;
// each /marshal web brings a fresh one.
func (w *Workspace) marshalWeb() (string, error) {
	srv, err := w.webBoardServer()
	if err != nil {
		return "", err
	}
	url, err := srv.Link()
	if err != nil {
		return "", err
	}
	// The link comes first, so a narrow footer still shows it whole.
	const what = " · Web Board: this computer only; the link works once and can act for you, keep it private"
	if headlessDisplay() {
		return url + what + " · no display here: open the link in a browser on this computer", nil
	}
	if !w.startBackground(w.workers.context(), func(ctx context.Context) {
		if err := webBoardOpen(ctx, url); err != nil {
			w.setOutput("Your browser could not be opened. Open this link in a browser on this computer: "+url+what, true)
		}
	}) {
		return "", errWorkspaceClosing
	}
	return "Web Board: opening in your browser (http://" + srv.Addr() + "/, this computer only). Each /marshal web opens a new link.", nil
}

// openWebBoardKey is the Board key: it runs /marshal web and shows its
// answer.
func (w *Workspace) openWebBoardKey() {
	resp, err := w.marshalWeb()
	w.recordCommandResult("/marshal web", resp, err)
}

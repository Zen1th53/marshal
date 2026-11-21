package tui

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/Zen1th53/marshal/internal/tui/board/vm"
	"github.com/Zen1th53/marshal/internal/webboard"
)

// The Web Board is the Board in a local browser, on 127.0.0.1 only, behind
// a private link (internal/webboard): it shows the Board and lets the owner
// press the buttons of the card on screen and write to the Marshal
// (webboard_act.go). The open Board process
// hosts it, because it has the data. It is the full interface: the
// terminal home shows only what is happening, the one thing needed from the
// owner and where the Web Board is (board_home.go). It starts with the Board
// (autoStartWebBoard), and opens in the browser where there is a display;
// w, the palette and /marshal web reach it again. It stops when the Board
// closes.

// webBoards holds each workspace's running Web Board.
var webBoards sync.Map // *Workspace -> *webBoardState

type webBoardState struct {
	mu     sync.Mutex
	server *webboard.Server
	closed bool
	// retry is the task r would retry, noted on the input loop at each
	// paint (noteWebRetry), "" for none.
	retry string
	// opened is set once the browser was opened on it.
	opened bool
}

// webBoardStart and webBoardOpen are the server and the browser opener;
// tests replace them. The opener is the architecture view's.
var (
	webBoardStart = webboard.Start
	webBoardOpen  = openArchitectureBrowser
)

// webOpenAtStart reports whether the Board opens the browser on the Web
// Board when it starts: yes, unless MARSHAL_WEB_OPEN=0 (the Web Board still
// starts; w opens it).
func webOpenAtStart() bool {
	return strings.TrimSpace(v2Getenv("MARSHAL_WEB_OPEN")) != "0"
}

// autoStartWebBoard starts the Web Board as the Board opens and, where
// there is a display, opens it in the browser. With no display the home
// screen's Web line says so and /marshal web (or w) shows the link. It runs
// off the input loop (Run starts it in the background); the link is never
// written anywhere.
func (w *Workspace) autoStartWebBoard(ctx context.Context) {
	srv, err := w.webBoardServer()
	if err != nil {
		return
	}
	w.requestRepaint()
	if headlessDisplay() || !webOpenAtStart() {
		return
	}
	if err := webBoardOpen(ctx, srv.URL); err != nil {
		return
	}
	w.noteWebOpened()
}

// noteWebOpened records that the browser was opened on the Web Board.
func (w *Workspace) noteWebOpened() {
	s := w.webBoard()
	s.mu.Lock()
	s.opened = true
	s.mu.Unlock()
	w.requestRepaint()
}

// webState is the Web Board's state for the view model's Web line. It
// never starts the server and holds no link.
func (w *Workspace) webState() vm.Web {
	s := w.webBoard()
	s.mu.Lock()
	defer s.mu.Unlock()
	running := false
	if s.server != nil {
		select {
		case <-s.server.Done():
		default:
			running = true
		}
	}
	return vm.Web{Running: running, Opened: running && s.opened, NoDisplay: headlessDisplay()}
}

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
// the browser where there is a display, and answers with its link. The link
// is the owner's: it goes to the footer only, never to Activity (which the
// Web Board itself shows) or to the Marshal.
func (w *Workspace) marshalWeb() (string, error) {
	srv, err := w.webBoardServer()
	if err != nil {
		return "", err
	}
	// The link comes first, so a narrow footer still shows it whole.
	const what = " · Web Board: this computer only; keep the link private, it can act for you"
	if headlessDisplay() {
		return srv.URL + what + " · no display here: open the link in a browser on this computer", nil
	}
	if err := w.openWebBrowser(srv.URL); err != nil {
		return "", err
	}
	return srv.URL + what + " · opening in your browser", nil
}

// openWebBrowser opens url in the browser off the input loop.
func (w *Workspace) openWebBrowser(url string) error {
	if !w.startBackground(w.workers.context(), func(ctx context.Context) {
		if err := webBoardOpen(ctx, url); err != nil {
			w.setOutput("Your browser could not be opened: /marshal web shows the Web Board's link to open.", true)
			return
		}
		w.noteWebOpened()
	}) {
		return errWorkspaceClosing
	}
	return nil
}

// openWebBoardKey is the Board's w: it opens the Web Board in the browser.
// The private link is shown only where there is no display to open
// (/marshal web's answer), never otherwise: the home screen's Web line
// says where the Web Board is without it.
func (w *Workspace) openWebBoardKey() {
	if headlessDisplay() {
		resp, err := w.marshalWeb()
		w.recordCommandResult("/marshal web", resp, err)
		return
	}
	srv, err := w.webBoardServer()
	if err == nil {
		err = w.openWebBrowser(srv.URL)
	}
	if err != nil {
		w.recordCommandResult("/marshal web", "", err)
		return
	}
	w.setOutput("Web Board: opening in your browser.", false)
}

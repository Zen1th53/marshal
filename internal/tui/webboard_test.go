package tui

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/tui/board/vm"
	"github.com/Zen1th53/marshal/internal/webboard"
)

// webBoardSeams replaces the browser opener; it returns the links opened.
func webBoardSeams(t *testing.T) func() []string {
	t.Helper()
	var mu sync.Mutex
	var opened []string
	orig := webBoardOpen
	t.Cleanup(func() { webBoardOpen = orig })
	webBoardOpen = func(_ context.Context, u string) error {
		mu.Lock()
		defer mu.Unlock()
		opened = append(opened, u)
		return nil
	}
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), opened...)
	}
}

var webLink = regexp.MustCompile(`http://127\.0\.0\.1:\d+/\?t=[A-Za-z0-9_-]{43}`)

// The browser's frame is built off the input loop with w.mu not held: a
// writer that takes w.mu without pause would deadlock a frame that took it
// twice.
func TestWebBoardFrameWithoutTheWorkspaceLock(t *testing.T) {
	w := emptyBoardWorkspace(t)
	w.endOpening()
	stop := make(chan struct{})
	var writer sync.WaitGroup
	writer.Add(1)
	go func() {
		defer writer.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			w.mu.Lock()
			w.unreadNew++
			w.mu.Unlock()
		}
	}()
	done := make(chan vm.Board, 1)
	go func() {
		var b vm.Board
		for range 100 {
			b = w.webBoardFrame()
			w.frameLines(120, 40)
		}
		done <- b
	}()
	var b vm.Board
	select {
	case b = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("a Web Board frame waited on w.mu: deadlock")
	}
	close(stop)
	writer.Wait()
	w.Close()
	if b.Screen != vm.ScreenReady || b.Now.IsZero() || b.Keys != nil {
		t.Fatalf("frame screen %q now %v keys %v", b.Screen, b.Now, b.Keys)
	}
	if _, err := webboard.Encode(b); err != nil {
		t.Fatal(err)
	}
}

// /marshal web starts one Web Board, opens it, answers its link, and the
// Board's Close stops it.
func TestMarshalWebServesTheBoard(t *testing.T) {
	opened := webBoardSeams(t)
	origGOOS := v2GOOS
	t.Cleanup(func() { v2GOOS = origGOOS })
	v2GOOS = "darwin" // a display: the link is opened
	w := emptyBoardWorkspace(t)
	w.endOpening()
	closed := false
	defer func() {
		if !closed {
			w.Close()
		}
	}()
	resp, err := w.marshalWeb()
	if err != nil {
		t.Fatal(err)
	}
	// With a display the browser gets the link; the footer names only the
	// address, so no screen line carries the link's secret.
	if webLink.FindString(resp) != "" || strings.Contains(resp, "?t=") || !strings.Contains(resp, "opening in your browser") {
		t.Fatalf("answer %q shows the link or does not say it opens", resp)
	}
	again, err := w.marshalWeb()
	if err != nil || webLink.FindString(again) != "" {
		t.Fatalf("a second /marshal web: %q %v", again, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(opened()) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	got := opened()
	if len(got) != 2 || webLink.FindString(got[0]) != got[0] || webLink.FindString(got[1]) != got[1] || got[1] == got[0] {
		t.Fatalf("opened %v, want two fresh links", got)
	}
	link := got[0]
	for _, n := range w.noteList() {
		if strings.Contains(n.Text, "?t=") {
			t.Fatalf("the private link reached Activity: %q", n.Text)
		}
	}

	// The page spends the link for its own secret and sends that as a
	// bearer header; the link's secret itself opens nothing.
	u, _ := url.Parse(link)
	client := &http.Client{Timeout: 5 * time.Second}
	q, _ := http.NewRequest("GET", "http://"+u.Host+"/api/board", nil)
	q.Header.Set("Authorization", "Bearer "+u.Query().Get("t"))
	r, err := client.Do(q)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the link's secret read the Board: %d", r.StatusCode)
	}
	q, _ = http.NewRequest("POST", "http://"+u.Host+"/api/session", nil)
	q.Header.Set("Origin", "http://"+u.Host)
	q.Header.Set("Authorization", "Bearer "+u.Query().Get("t"))
	r, err = client.Do(q)
	if err != nil {
		t.Fatal(err)
	}
	var session struct {
		Session string `json:"session"`
	}
	err = json.NewDecoder(r.Body).Decode(&session)
	r.Body.Close()
	if err != nil || r.StatusCode != http.StatusOK || session.Session == "" {
		t.Fatalf("spend: %d %v", r.StatusCode, err)
	}
	if _, err := w.marshalWeb(); err != nil {
		t.Fatal(err)
	}
	for len(opened()) < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := opened(); len(got) != 3 || got[2] == link {
		t.Fatalf("after the link was spent, /marshal web opened %v, want a fresh link", got)
	}
	q, _ = http.NewRequest("GET", "http://"+u.Host+"/api/board", nil)
	q.Header.Set("Authorization", "Bearer "+session.Session)
	r, err = client.Do(q)
	if err != nil {
		t.Fatal(err)
	}
	var f webboard.Frame
	err = json.NewDecoder(r.Body).Decode(&f)
	r.Body.Close()
	if err != nil || r.StatusCode != http.StatusOK || f.V != 1 || f.Screen != "ready" {
		t.Fatalf("board: %d %v %+v", r.StatusCode, err, f)
	}

	w.Close()
	closed = true
	if conn, err := net.DialTimeout("tcp", u.Host, 200*time.Millisecond); err == nil {
		conn.Close()
		t.Fatal("the Web Board outlived the Board")
	}
	if _, err := w.marshalWeb(); err == nil {
		t.Fatal("a closed Board started a Web Board")
	}
}

// w with no task highlighted, pressed through the terminal's own key path,
// opens the Web Board and types nothing; with no display the answer is the
// link to open.
func TestWebBoardKeyWithoutDisplay(t *testing.T) {
	opened := webBoardSeams(t)
	origGOOS, origEnv := v2GOOS, v2Getenv
	t.Cleanup(func() { v2GOOS, v2Getenv = origGOOS, origEnv })
	v2GOOS, v2Getenv = "linux", func(string) string { return "" }
	w := emptyBoardWorkspace(t)
	defer w.Close()
	w.endOpening()
	w.frameLines(120, 40)
	w.handleTerminalKey(context.Background(), KeyEvent{Type: KeyRune, Rune: 'w'})
	if got := w.composer.Text(); got != "" {
		t.Fatalf("w was typed: %q", got)
	}
	w.mu.RLock()
	answer := w.answer
	w.mu.RUnlock()
	if !strings.Contains(answer, "no display here") || webLink.FindString(answer) == "" {
		t.Fatalf("answer %q", answer)
	}
	time.Sleep(50 * time.Millisecond)
	if len(opened()) != 0 {
		t.Fatal("a browser was opened with no display")
	}
}

// /marshal web runs through the command handler the composer and the
// palette use; with more words it is a goal, not the Web Board.
func TestMarshalWebCommand(t *testing.T) {
	webBoardSeams(t)
	w := emptyBoardWorkspace(t)
	defer w.Close()
	opensWeb := func(resp string) bool {
		return webLink.FindString(resp) != "" || strings.Contains(resp, "Web Board: opening in your browser")
	}
	resp, err := w.cmd.Handle(context.Background(), "/marshal web")
	if err != nil || !opensWeb(resp) {
		t.Fatalf("/marshal web: %q %v", resp, err)
	}
	resp, _ = w.cmd.Handle(context.Background(), "/marshal web app for the shop")
	if opensWeb(resp) {
		t.Fatalf("a goal starting with web opened the Web Board: %q", resp)
	}
	for _, a := range w.palette.refresh() {
		if a.ID == "web.open" {
			if a.Command != "/marshal web" || a.Title != "Open the Board in the browser" || a.Disabled {
				t.Fatalf("palette row %+v", a)
			}
			return
		}
	}
	t.Fatal("the palette has no web.open row")
}

// A browser that cannot be opened is never answered "opening": /marshal web
// reports the failed launcher, and the w key says "opening" before the
// opener runs so the failure replaces it instead of the other way round.
func TestWebBoardOpenFailureIsNotAnsweredOpening(t *testing.T) {
	origGOOS := v2GOOS
	t.Cleanup(func() { v2GOOS = origGOOS })
	v2GOOS = "darwin"
	orig := webBoardOpen
	t.Cleanup(func() { webBoardOpen = orig })
	webBoardOpen = func(context.Context, string) error { return errors.New("open browser: exit status 3") }
	w := emptyBoardWorkspace(t)
	defer w.Close()
	w.endOpening()

	resp, err := w.marshalWeb()
	if err == nil || strings.Contains(resp, "opening in your browser") || !strings.Contains(err.Error(), "could not be opened") {
		t.Fatalf("/marshal web with a failing opener: %q %v", resp, err)
	}

	w.openWebBoardKey()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		w.mu.RLock()
		answer, isErr := w.answer, w.answerIsError
		w.mu.RUnlock()
		if isErr && strings.Contains(answer, "could not be opened") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("w with a failing opener never reported the failure")
}

// /marshal web copy puts a fresh one-use link on the clipboard and keeps it
// out of the answer; with no clipboard it shows the link instead.
func TestMarshalWebCopy(t *testing.T) {
	webBoardSeams(t)
	orig := webLinkCopy
	t.Cleanup(func() { webLinkCopy = orig })
	var copied string
	webLinkCopy = func(_ *Workspace, text string) (bool, error) { copied = text; return true, nil }
	w := emptyBoardWorkspace(t)
	defer w.Close()
	resp, err := w.cmd.Handle(context.Background(), "/marshal web copy")
	if err != nil || !strings.Contains(resp, "Copied a new Web Board link") || webLink.FindString(resp) != "" {
		t.Fatalf("/marshal web copy: %q %v", resp, err)
	}
	if webLink.FindString(copied) == "" {
		t.Fatalf("copied %q, want a Web Board link", copied)
	}
	webLinkCopy = func(*Workspace, string) (bool, error) { return false, errors.New("no clipboard") }
	resp, _ = w.cmd.Handle(context.Background(), "/marshal web copy")
	if webLink.FindString(resp) == "" || !strings.Contains(resp, "Could not reach the clipboard") {
		t.Fatalf("with no clipboard: %q", resp)
	}
}

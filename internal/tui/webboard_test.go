package tui

import (
	"context"
	"encoding/json"
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
	link := webLink.FindString(resp)
	if link == "" || !strings.Contains(resp, "keep the link private") {
		t.Fatalf("answer %q has no Web Board link", resp)
	}
	again, err := w.marshalWeb()
	if err != nil || webLink.FindString(again) != link {
		t.Fatalf("a second /marshal web started another Board: %q %v", again, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(opened()) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := opened(); len(got) != 2 || got[0] != link {
		t.Fatalf("opened %v, want %s twice", got, link)
	}
	for _, n := range w.noteList() {
		if strings.Contains(n.Text, "?t=") {
			t.Fatalf("the private link reached Activity: %q", n.Text)
		}
	}

	// The page reads the link's token and sends it as a bearer header.
	u, _ := url.Parse(link)
	q, _ := http.NewRequest("GET", "http://"+u.Host+"/api/board", nil)
	q.Header.Set("Authorization", "Bearer "+u.Query().Get("t"))
	r, err := (&http.Client{Timeout: 5 * time.Second}).Do(q)
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
	resp, err := w.cmd.Handle(context.Background(), "/marshal web")
	if err != nil || webLink.FindString(resp) == "" {
		t.Fatalf("/marshal web: %q %v", resp, err)
	}
	resp, _ = w.cmd.Handle(context.Background(), "/marshal web app for the shop")
	if webLink.FindString(resp) != "" {
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

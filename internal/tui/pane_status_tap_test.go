//go:build linux

package tui

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/tmux"
	"github.com/Zen1th53/marshal/internal/tui/board/model"
)

func osc7501(payload string) string { return "\x1b]7501;" + payload + "\x07" }

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// A tap fed by hand, without a pane: what consume does with a stream.
func feedTap(changed func()) *paneTap {
	return &paneTap{changed: changed, done: make(chan struct{})}
}

func TestPaneTapParsesSplitSequencesAndStaysQuiet(t *testing.T) {
	var calls atomic.Int32
	tap := feedTap(func() { calls.Add(1) })
	seq := osc7501("state=working:app=codex:msg=" + b64("editing internal/app/run.go"))
	// The sequence arrives cut at every point: inside the prefix, inside the
	// payload, before the terminator.
	for _, cut := range []int{1, 3, len("\x1b]7501;") - 1, 12, len(seq) - 1} {
		tap = feedTap(func() { calls.Add(1) })
		calls.Store(0)
		tap.consume([]byte("noise " + seq[:cut]))
		if _, ok := tap.Status(); ok {
			t.Fatalf("cut %d: a status before its terminator", cut)
		}
		tap.consume([]byte(seq[cut:] + " more noise"))
		st, ok := tap.Status()
		if !ok || st.State != "working" || st.Msg != "editing internal/app/run.go" {
			t.Fatalf("cut %d: status %+v %v", cut, st, ok)
		}
		if calls.Load() != 1 {
			t.Fatalf("cut %d: %d change calls, want 1", cut, calls.Load())
		}
	}
	// The same status again, and output with no status at all, wake nobody.
	tap.consume([]byte(seq))
	tap.consume([]byte(strings.Repeat("plain worker output\n", 2000)))
	if calls.Load() != 1 {
		t.Fatalf("an unchanged status or plain output called changed: %d", calls.Load())
	}
	if len(tap.carry) != 0 {
		t.Fatalf("plain output left %d bytes carried", len(tap.carry))
	}
	// ST (ESC \) terminates as BEL does; the last sequence of a read wins.
	tap.consume([]byte("\x1b]7501;state=idle\x1b\\" + "\x1b]7501;state=blocked:kind=permission:msg=" + b64("allow curl?") + "\x1b\\"))
	st, _ := tap.Status()
	if st.State != "blocked" || st.Kind != "permission" || st.laneLast() != "blocked (permission) · allow curl?" {
		t.Fatalf("ST-terminated status %+v / %q", st, st.laneLast())
	}
	// clear forgets the status.
	tap.consume([]byte(osc7501("state=clear")))
	if _, ok := tap.Status(); ok {
		t.Fatal("clear left a status")
	}
}

func TestPaneTapIsBounded(t *testing.T) {
	tap := feedTap(nil)
	// An unterminated sequence longer than the carry bound is dropped, not
	// held forever.
	tap.consume([]byte("\x1b]7501;state=working:msg=" + strings.Repeat("A", tapCarryMax)))
	if len(tap.carry) != 0 {
		t.Fatalf("an over-long unterminated sequence is carried: %d bytes", len(tap.carry))
	}
	tap.consume([]byte("\x1b]7501;state=working:msg=" + b64("short")))
	if n := len(tap.carry); n == 0 || n > tapCarryMax {
		t.Fatalf("an unterminated sequence carried %d bytes", n)
	}
	// The message is one line, without control characters, bounded.
	tap.consume([]byte("\x07"))
	long := strings.Repeat("x", 3*tapMsgMax)
	tap.consume([]byte(osc7501("state=working:msg=" + b64("line one\nline\x1b[31m two​ "+long))))
	st, _ := tap.Status()
	if strings.ContainsAny(st.Msg, "\n\x1b​") {
		t.Fatalf("control characters survived: %q", st.Msg)
	}
	if n := len([]rune(st.Msg)); n > tapMsgMax {
		t.Fatalf("message %d runes, bound %d", n, tapMsgMax)
	}
	// A state that is not a plain word is not shown as one.
	tap.consume([]byte(osc7501("state=\x1b[2Jworking:msg=" + b64("x"))))
	if st, _ := tap.Status(); st.State != "" {
		t.Fatalf("state %q passed through", st.State)
	}
}

func TestPaneTapRedactsTheMessage(t *testing.T) {
	tap := feedTap(nil)
	secret := "sk-live-secret-key-12345"
	tap.consume([]byte(osc7501("state=working:app=claude:msg=" + b64("calling the API with "+secret))))
	st, _ := tap.Status()
	if strings.Contains(st.Msg, secret) || !strings.Contains(st.Msg, "[REDACTED]") {
		t.Fatalf("message not redacted: %q", st.Msg)
	}
}

// The Board shows what a worker reports: its message as the lane's last
// action, its state beside it, redacted, and a new report re-projects the
// Board while an unchanged one does not.
func TestBoardLanesShowReportedWorkerStatus(t *testing.T) {
	w, src := boardWorkspace(t)
	select {
	case <-w.repaint:
	default:
	}
	before := w.marshalPanel()
	secret := "sk-live-secret-key-12345"
	tap := feedTap(w.laneMoved)
	w.tapMu.Lock()
	w.paneTaps = map[string]*paneTap{"%3": tap}
	w.tapMu.Unlock()
	tap.consume([]byte(osc7501("state=blocked:kind=permission:app=codex:msg=" + b64("waiting to run curl with "+secret))))
	select {
	case <-w.laneNudge:
	default:
		t.Fatal("a new status did not nudge the Board")
	}
	if !w.refreshBoardFrom(t.Context(), src, before) {
		t.Fatal("a reported status was not published")
	}
	after := w.marshalPanel()
	var lane model.Lane
	for _, l := range after.Board.Lanes {
		if l.Pane == "%3" {
			lane = l
		}
	}
	if lane.Status != "blocked" || !strings.HasPrefix(lane.Last, "blocked (permission) · waiting to run curl") {
		t.Fatalf("lane %+v", lane)
	}
	frame := plainFrame(w, 170, 45)
	if strings.Contains(frame, secret) {
		t.Fatal("a secret reached the frame")
	}
	// The lane row is one line: the report is cut to the column, never wrapped.
	if !strings.Contains(frame, "blocked (permission") {
		t.Fatalf("reported status not on the Board:\n%s", frame)
	}
	// The same report again: nothing to publish, nothing repainted.
	drainRepaints(w, 20*time.Millisecond)
	tap.consume([]byte(osc7501("state=blocked:kind=permission:app=codex:msg=" + b64("waiting to run curl with "+secret))))
	select {
	case <-w.laneNudge:
		t.Fatal("an unchanged status nudged the Board")
	default:
	}
	if w.refreshBoardFrom(t.Context(), src, after) {
		t.Fatal("an unchanged status was published again")
	}
	if n := drainRepaints(w, 50*time.Millisecond); n != 0 {
		t.Fatalf("an unchanged status asked for %d repaints", n)
	}
}

// laneMoved never blocks the tap, however many reports arrive before the
// follower reads one.
func TestLaneNudgeCoalesces(t *testing.T) {
	w := NewWorkspace(nil, "project", "session")
	for range 100 {
		w.laneMoved()
	}
	if n := len(w.laneNudge); n != 1 {
		t.Fatalf("%d nudges pending, want 1", n)
	}
}

// privateTmux points the tmux package at a server on its own socket, so the
// test never addresses the operator's server.
func privateTmux(t *testing.T) (run func(args ...string) string) {
	t.Helper()
	bin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not installed")
	}
	dir := t.TempDir()
	sock := filepath.Join(dir, "s")
	wrapper := filepath.Join(dir, "tmux")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexec "+bin+" -S "+sock+" \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(wrapper)
	t.Cleanup(func() { exec.Command(bin, "-S", sock, "kill-server").Run(); tmux.ResetBinaryPath() })
	return func(args ...string) string {
		out, err := exec.Command(bin, append([]string{"-S", sock}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
}

// TestPaneTapReadsOSC7501FromARealTmuxPane is the reason the tap exists:
// a status a worker prints never shows in capture-pane, and does reach the
// Board through pipe-pane.
func TestPaneTapReadsOSC7501FromARealTmuxPane(t *testing.T) {
	run := privateTmux(t)
	dir := t.TempDir()
	step := func(n int) { os.WriteFile(filepath.Join(dir, "go"+string(rune('0'+n))), nil, 0600) }
	secret := "sk-live-secret-key-12345"
	script := `cd ` + dir + `
while [ ! -f go1 ]; do sleep 0.05; done
printf '\033]7501;state=working:app=claude:msg=` + b64("reading go.mod") + `\007'
while [ ! -f go2 ]; do sleep 0.05; done
i=0; while [ $i -lt 300 ]; do echo "plain build output line $i"; i=$((i+1)); done
while [ ! -f go3 ]; do sleep 0.05; done
printf '\033]7501;state=blocked:kind=permission:app=claude:msg=` + b64("run curl with "+secret) + `\007'
sleep 600
`
	if err := os.WriteFile(filepath.Join(dir, "worker.sh"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	run("new-session", "-d", "-s", "osc", "-x", "120", "-y", "30", "sh", filepath.Join(dir, "worker.sh"))
	pane := run("display-message", "-p", "-t", "osc", "#{pane_id}")

	var changes atomic.Int32
	w := NewWorkspace(nil, "project", "session")
	stop := w.tapPane(t.Context(), pane)
	stopped := false
	defer func() {
		if !stopped {
			stop()
		}
	}()
	if got := run("display-message", "-p", "-t", pane, "#{pane_pipe}"); got != "1" {
		t.Fatalf("pane pipe %q, want 1", got)
	}
	go func() {
		for range w.laneNudge {
			changes.Add(1)
		}
	}()
	wait := func(what string, ok func() bool) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for !ok() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s", what)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	step(1)
	wait("the working status", func() bool { st, ok := w.paneStatusOf(pane); return ok && st.State == "working" })
	if st, _ := w.paneStatusOf(pane); st.Msg != "reading go.mod" {
		t.Fatalf("message %q", st.Msg)
	}
	// What capture-pane shows has no trace of the sequence: tmux consumed it.
	if screen := run("capture-pane", "-p", "-e", "-t", pane); strings.Contains(screen, "7501") || strings.Contains(screen, "reading go.mod") {
		t.Fatalf("capture-pane carried the OSC status: %q", screen)
	}
	step(2)
	wait("the plain output", func() bool { return strings.Contains(run("capture-pane", "-p", "-t", pane), "line 299") })
	time.Sleep(100 * time.Millisecond)
	if n := changes.Load(); n != 1 {
		t.Fatalf("%d change nudges after one status and plain output, want 1", n)
	}
	step(3)
	wait("the blocked status", func() bool { st, ok := w.paneStatusOf(pane); return ok && st.State == "blocked" })
	st, _ := w.paneStatusOf(pane)
	if strings.Contains(st.Msg, secret) || st.Kind != "permission" {
		t.Fatalf("blocked status %+v", st)
	}
	wait("two change nudges", func() bool { return changes.Load() == 2 })

	w.tapMu.Lock()
	tapDir := w.paneTaps[pane].dir
	w.tapMu.Unlock()
	stop()
	stopped = true
	if got := run("display-message", "-p", "-t", pane, "#{pane_pipe}"); got != "0" {
		t.Fatalf("pane pipe %q after stop, want 0", got)
	}
	if _, ok := w.paneStatusOf(pane); ok {
		t.Fatal("a stopped tap still reports")
	}
	if _, err := os.Stat(tapDir); !os.IsNotExist(err) {
		t.Fatalf("the tap's FIFO directory survived stop: %v", err)
	}
}

// The monitor publishes the reported status in live-status.json, where the
// title heuristic would otherwise guess.
func TestMonitorPublishesTappedStatus(t *testing.T) {
	run := privateTmux(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "w.sh"), []byte(`cd `+dir+`
while [ ! -f go ]; do sleep 0.05; done
printf '\033]7501;state=blocked:kind=question:app=codex:msg=`+b64("which branch?")+`\007'
sleep 600
`), 0700); err != nil {
		t.Fatal(err)
	}
	run("new-session", "-d", "-s", "mon", "sh", filepath.Join(dir, "w.sh"))
	run("select-pane", "-t", "mon", "-T", "codex: idle")
	pane := run("display-message", "-p", "-t", "mon", "#{pane_id}")
	root := t.TempDir()
	w := NewWorkspace(nil, "project", "session")
	w.tmuxSession = "mon"
	agent := &activeTmuxAgent{provider: "codex", role: "task", paneID: pane, doneChan: make(chan struct{})}
	w.tmuxMu.Lock()
	w.tmuxActiveWins["codex"] = agent
	w.tmuxMu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	w.monitorAgent(ctx, agent, root, nil, nil, nil, nil, &inboxView{})
	defer func() { cancel(); <-agent.doneChan }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok := w.paneStatusOf(pane); ok || time.Now().After(deadline) {
			break
		}
		if _, err := os.Stat(filepath.Join(dir, "go")); err != nil {
			// Report only once the tap is in place.
			w.tapMu.Lock()
			tapped := w.paneTaps[pane] != nil
			w.tapMu.Unlock()
			if tapped {
				os.WriteFile(filepath.Join(dir, "go"), nil, 0600)
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	var st liveStatus
	for time.Now().Before(deadline.Add(5 * time.Second)) {
		st, _ = readLiveStatus(root, "codex")
		if st.State == "blocked" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if st.State != "blocked" || st.LastAction != "blocked (question) · which branch?" {
		t.Fatalf("live status %+v: the reported status did not win over the title", st)
	}
}

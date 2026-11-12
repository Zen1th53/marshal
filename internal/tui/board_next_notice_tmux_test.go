package tui

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/tmux"
)

// The run-closed notice is typed into a docked tmux chat that sits idle at
// its prompt, and Activity says it was sent. It used to stay "sending"
// forever: the notice was queued from a short-lived background task whose
// context ended as soon as the notice was queued, which stopped the
// deliverer before it typed anything and left the queue wedged, so a later
// message from the person was never sent either.
func TestRunClosedNoticeReachesIdleTmuxChat(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	w := realTmuxWorkspace(t)
	t.Cleanup(w.Close)
	oldPoll, oldSettle := sayPoll, saySettle
	sayPoll, saySettle = 50*time.Millisecond, 80*time.Millisecond
	defer func() { sayPoll, saySettle = oldPoll, oldSettle }()
	pane := fakeChatPane(t, w)
	ctx := context.Background()

	m := &marshalSession{runID: "RUN-1"}
	publishPanel(w, m, noticePanel(marshal.Verifying))
	publishPanel(w, m, noticePanel(marshal.Closed))
	why := func() string {
		out, _ := tmux.CapturePane(ctx, pane)
		return fmt.Sprintf("echo %+v pane:\n%s", w.sayEchoList(), out)
	}
	waitFor(t, func() bool { return echoState(w, runClosedNoticeWant) == saySent }, why)
	waitForTmuxOutput(t, pane, "got:[Marshal] The run closed")

	// The queue is not wedged: the person's next message goes in too.
	w.sayToMarshal(ctx, "thanks")
	waitFor(t, func() bool { return echoState(w, "thanks") == saySent }, why)
	waitForTmuxOutput(t, pane, "got:thanks")
}

// A deliverer whose context ends while a message waits gives up ownership
// of the queue and says plainly that the message was not sent, so Activity
// never shows "sending" forever and the next message starts a new deliverer.
func TestDeliverSaidCancelledReleasesQueue(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	withTmuxChat(w)
	now := time.Now()
	w.say.mu.Lock()
	w.say.next = 1
	w.say.pending = []sayItem{{id: 1, text: "hello", at: now}}
	w.say.running = true
	w.say.mu.Unlock()
	w.sayNote(sayEcho{id: 1, At: now, Text: "hello", State: saySending})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w.deliverSaid(ctx)
	w.say.mu.Lock()
	running, pending := w.say.running, len(w.say.pending)
	w.say.mu.Unlock()
	if running {
		t.Fatal("a stopped deliverer still owns the queue")
	}
	if pending != 0 {
		t.Fatalf("%d messages left waiting with no deliverer", pending)
	}
	e := w.sayEchoList()[0]
	if e.State != sayFailed || e.Why == "" || strings.Contains(e.Why, "context") {
		t.Fatalf("echo %+v", e)
	}
}

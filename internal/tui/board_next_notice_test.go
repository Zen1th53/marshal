package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/tui/board/widgets/chat"
)

const runClosedNoticeWant = "Runtime notice: the run closed — 3 of 4 tasks verified and merged to main. Tell the person in one or two sentences what was delivered and ask what they want next. Do not start new work until they answer."

// noticePanel is a published panel for a run with three of four tasks merged
// to main, in state.
func noticePanel(state marshal.RunState) *MarshalPanel {
	p := closedPanel("RUN-1", state, 3, 4, 0, 0)
	p.run.TargetRef = "refs/heads/main"
	return p
}

// withTmuxChat opens a tmux Marshal chat pane for the say path.
func withTmuxChat(w *Workspace) {
	w.tmuxMu.Lock()
	w.tmuxActiveWins["marshal-chat"] = &activeTmuxAgent{id: "marshal-chat", role: "marshal-chat", paneID: "%4242"}
	w.tmuxMu.Unlock()
}

// runtimeNotices is the run-closed notices sent so far, and whether each is
// labelled as from the runtime.
func runtimeNotices(w *Workspace) (texts []string, runtime []bool) {
	for _, e := range w.sayEchoList() {
		if strings.HasPrefix(e.Text, "Runtime notice: the run closed") {
			texts = append(texts, e.Text)
			runtime = append(runtime, e.Runtime)
		}
	}
	return texts, runtime
}

// publishPanel runs the real publish path for the watched run, which calls
// noteRunClosed under the session lock.
func publishPanel(w *Workspace, m *marshalSession, p *MarshalPanel) {
	w.marshalPublish(m, p.RunID, p)
}

// The automatic close after verify (Verifying → Closed) and a close the
// person triggered (the run waits for them, then closes) each send the
// Marshal chat exactly one runtime notice with plain counts and the target
// branch; a re-publish of the closed run sends nothing more.
func TestRunClosedNoticeSentOnceOnClose(t *testing.T) {
	for _, tc := range []struct {
		name string
		prev marshal.RunState
	}{{"auto", marshal.Verifying}, {"manual", marshal.AwaitingUser}} {
		t.Run(tc.name, func(t *testing.T) {
			w := emptyBoardWorkspace(t)
			defer w.Close()
			withTmuxChat(w)
			m := &marshalSession{runID: "RUN-1"}
			publishPanel(w, m, noticePanel(tc.prev))
			publishPanel(w, m, noticePanel(marshal.Closed))
			chatWaitFor(t, "the run-closed notice", func() bool { n, _ := runtimeNotices(w); return len(n) == 1 })
			publishPanel(w, m, noticePanel(marshal.Closed))
			w.clearNext()
			w.noteRunClosed(noticePanel(tc.prev), noticePanel(marshal.Closed))
			time.Sleep(50 * time.Millisecond)
			texts, runtime := runtimeNotices(w)
			if len(texts) != 1 {
				t.Fatalf("notices sent: %q", texts)
			}
			if texts[0] != runClosedNoticeWant {
				t.Fatalf("notice text:\n got %q\nwant %q", texts[0], runClosedNoticeWant)
			}
			if !runtime[0] {
				t.Fatal("the notice is labelled as the owner's message")
			}
			if strings.Contains(texts[0], "RUN-1") {
				t.Fatal("the notice names the run id")
			}
		})
	}
}

// A run that stops (a delivery problem leaves it waiting for the person, or
// its execution ended with an error) sends no run-closed notice.
func TestRunClosedNoticeNotSentOnStoppedRun(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	withTmuxChat(w)
	m := &marshalSession{runID: "RUN-1"}
	publishPanel(w, m, noticePanel(marshal.Verifying))
	stopped := noticePanel(marshal.AwaitingUser)
	stopped.Stopped = true
	publishPanel(w, m, stopped)
	closedStopped := noticePanel(marshal.Closed)
	closedStopped.Stopped = true
	w.noteRunClosed(noticePanel(marshal.Verifying), closedStopped)
	time.Sleep(50 * time.Millisecond)
	if texts, _ := runtimeNotices(w); len(texts) != 0 {
		t.Fatalf("a stopped run sent the notice: %q", texts)
	}
}

// With no Marshal chat open nothing is sent; the what-next card still opens.
func TestRunClosedNoticeNotSentWithoutChat(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	m := &marshalSession{runID: "RUN-1"}
	publishPanel(w, m, noticePanel(marshal.Verifying))
	publishPanel(w, m, noticePanel(marshal.Closed))
	time.Sleep(50 * time.Millisecond)
	if len(w.sayEchoList()) != 0 {
		t.Fatalf("sent with no chat open: %+v", w.sayEchoList())
	}
	if w.currentNext() == nil {
		t.Fatal("the what-next card did not open")
	}
}

// In the docked native chat the notice reaches the provider once and the
// chat shows it as a runtime note, never as the owner's words.
func TestRunClosedNoticeNativeChatIsRuntimeNote(t *testing.T) {
	w, _, tr := boardWithNativeChat(t)
	w.noteRunClosed(noticePanel(marshal.Verifying), noticePanel(marshal.Closed))
	chatWaitFor(t, "the notice sent to the chat", func() bool {
		tr.mu.Lock()
		defer tr.mu.Unlock()
		return len(tr.sent) == 1
	})
	tr.mu.Lock()
	sent := tr.sent[0]
	tr.mu.Unlock()
	if sent != runClosedNoticeWant {
		t.Fatalf("sent %q", sent)
	}
	w.chat.mu.Lock()
	entries := w.chat.view.Entries()
	w.chat.mu.Unlock()
	var found bool
	for _, e := range entries {
		if e.Text == runClosedNoticeWant {
			found = true
			if e.Role != chat.Note {
				t.Fatalf("the notice shows as %q, not a runtime note", e.Role)
			}
		}
	}
	if !found {
		t.Fatal("the notice is not in the chat")
	}
}

// Without a target branch the notice still states plain counts.
func TestRunClosedNoticeWithoutTarget(t *testing.T) {
	got := runClosedNotice(2, 2, "")
	if !strings.HasPrefix(got, "Runtime notice: the run closed — 2 of 2 tasks verified and merged. Tell the person") {
		t.Fatalf("notice %q", got)
	}
}

package tui

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
)

const taskDiffLimit = 4 << 20

type taskDiffOverlay struct {
	id                    string
	mode                  marshal.AcceptanceMode
	handed, armed, prompt bool
	reason, reply         string
	viewer                *DiffViewer
	pending               chan string
	accept                func() (string, error)
	rework                func(string) (string, error)
	say                   func(string) string
}

// Results are transferred to the UI by channel; only the UI touches the viewer.
func (o *taskDiffOverlay) poll() {
	if o.pending != nil {
		select {
		case raw := <-o.pending:
			o.viewer.OpenRaw("task "+o.id, raw)
			if strings.HasPrefix(raw, "no diff available:") {
				o.reply = raw
			} else if strings.HasSuffix(raw, "… truncated\n") {
				o.reply += " · … truncated"
			}
			o.pending = nil
		default:
		}
	}
}

func (o *taskDiffOverlay) HandleKey(w *Workspace, ev KeyEvent) bool {
	if o.id == "" || o.viewer == nil || !o.viewer.IsOpen() {
		return false
	}
	o.poll()
	if o.prompt {
		switch ev.Type {
		case KeyEsc:
			o.prompt = false
			o.reason = ""
		case KeyBackspace:
			r := []rune(o.reason)
			if len(r) > 0 {
				o.reason = string(r[:len(r)-1])
			}
		case KeyEnter:
			if strings.TrimSpace(o.reason) == "" {
				o.reply = "rework reason required"
				return true
			}
			o.prompt = false
			o.decide(false)
		case KeyRune:
			if ev.Rune >= ' ' {
				o.reason += string(ev.Rune)
			}
		}
		return true
	}
	armed := o.armed
	o.armed = false
	if ev.Type == KeyEsc || ev.Type == KeyRune && ev.Rune == 'q' {
		o.viewer.Close()
		o.id = ""
		return true
	}
	if ev.Type == KeyEnter && armed {
		o.decide(true)
		return true
	}
	if ev.Type == KeyRune && (ev.Rune == 'a' || ev.Rune == 'r') {
		if o.mode != marshal.AcceptMarshal && !o.handed {
			o.reply = "disabled: task is not handed in"
			return true
		}
		if ev.Rune == 'r' {
			o.prompt = true
			o.reason = ""
		} else if o.mode == marshal.AcceptMarshal {
			o.decide(true)
		} else {
			o.armed = true
		}
		return true
	}
	o.viewer.HandleKey(ev)
	return true
}

func (o *taskDiffOverlay) decide(accept bool) {
	if o.mode == marshal.AcceptMarshal {
		msg := "Owner review of task " + o.id + ": accept"
		if !accept {
			msg = "Owner review of task " + o.id + ": rework — " + strings.TrimSpace(o.reason)
		}
		o.reply = "decided by Marshal — " + o.say(msg)
		return
	}
	var err error
	if accept {
		o.reply, err = o.accept()
	} else {
		o.reply, err = o.rework(strings.TrimSpace(o.reason))
	}
	if err != nil {
		o.reply = err.Error()
	}
}

func (o *taskDiffOverlay) StatusLine(width int) string {
	if o.id == "" || o.viewer == nil || !o.viewer.IsOpen() {
		return ""
	}
	o.poll()
	hint := "[a] approve  [r] rework"
	if o.mode == marshal.AcceptMarshal {
		hint = "decided by Marshal · [a] send accept  [r] send rework"
	} else if !o.handed {
		hint = "[a/r] disabled: task is not handed in"
	}
	if o.armed {
		hint = "[Enter] confirm approval · any other key cancels"
	}
	if o.prompt {
		hint = "Rework reason: " + o.reason + " · [Enter] send [Esc] cancel"
	}
	if o.reply != "" {
		hint += " · " + cleanBoard(o.reply)
	}
	return Truncate(hint, width)
}

func (w *Workspace) openTaskDiff(taskID string) bool {
	p := w.marshalPanel()
	if p == nil || w.diffViewer == nil {
		return false
	}
	var base, result string
	var handed bool
	for _, t := range p.Board.Tasks {
		if t.ID == taskID {
			base, result = t.BaseCommit, t.ResultCommit
			handed = t.State == "review"
			break
		}
	}
	if base == "" {
		return false
	}
	m := w.marshalSession()
	m.mu.Lock()
	service := m.service
	m.mu.Unlock()
	repo := ""
	if service != nil {
		repo = service.Repository
	}
	o := taskDiffOverlay{id: taskID, mode: p.run.Settings.AcceptanceMode, handed: handed, viewer: w.diffViewer, pending: make(chan string, 1)}
	o.accept = func() (string, error) { return w.marshalAccept([]string{taskID}) }
	o.rework = func(reason string) (string, error) {
		return w.marshalReturn(context.Background(), []string{taskID, reason})
	}
	o.say = func(msg string) string {
		w.sayToMarshal(context.Background(), msg)
		echoes := w.sayEchoList()
		for _, e := range echoes {
			if e.Text == msg {
				if e.State == saySent {
					return "your input was sent"
				}
				if e.State == sayFailed {
					return "not sent: " + e.Why
				}
				return "your input is " + e.State
			}
		}
		return "delivery unconfirmed"
	}
	w.taskDiff = o
	w.diffViewer.OpenRaw("task "+taskID, "")
	w.taskDiff.reply = "loading diff"
	ch := o.pending
	if !w.startBackground(context.Background(), func(ctx context.Context) { raw := loadTaskDiff(ctx, repo, base, result); ch <- raw; w.requestRepaint() }) {
		ch <- "no diff available: workspace closed"
	}
	if result == "" {
		w.taskDiff.reply = "no task-ID worktree lookup available; result commit required"
	} else {
		w.taskDiff.reply = "using result commit (no task-ID worktree lookup available)"
	}
	return true
}

// The service has no task-ID worktree lookup. Never infer a worktree from
// branch names or git worktree list: use the recorded result commit only.
func loadTaskDiff(parent context.Context, repo, base, result string) string {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	fail := func(step string, err error) string { return fmt.Sprintf("no diff available: %s: %v", step, err) }
	if repo == "" {
		return "no diff available: Marshal repository unavailable"
	}
	for _, sha := range []string{base, result} {
		if sha == "" {
			return "no diff available: no result commit; task-ID worktree lookup unavailable"
		}
		if strings.HasPrefix(sha, "-") {
			return "no diff available: invalid commit"
		}
		if err := exec.CommandContext(ctx, "git", "-C", repo, "cat-file", "-e", sha+"^{commit}").Run(); err != nil {
			return fail("validate commit "+sha, err)
		}
	}
	cmd := exec.CommandContext(ctx, "git", "-C", repo, "diff", "--no-color", base, result, "--")
	out := &taskDiffBuffer{}
	cmd.Stdout = out
	if err := cmd.Run(); err != nil {
		return fail("git diff", err)
	}
	return truncateTaskDiff(out.buf.Bytes(), out.overflow)
}

type taskDiffBuffer struct {
	buf      bytes.Buffer
	overflow bool
}

func (b *taskDiffBuffer) Write(p []byte) (int, error) {
	n := len(p)
	room := taskDiffLimit + 1 - b.buf.Len()
	if room > 0 {
		b.buf.Write(p[:min(room, n)])
	}
	if n > room {
		b.overflow = true
	}
	return n, nil
}
func truncateTaskDiff(raw []byte, overflow bool) string {
	if len(raw) <= taskDiffLimit && !overflow {
		return string(raw)
	}
	raw = raw[:min(len(raw), taskDiffLimit)]
	end := bytes.LastIndex(raw, []byte("\ndiff --git "))
	if end < 0 {
		end = 0
	}
	return string(raw[:end]) + "\n… truncated\n"
}

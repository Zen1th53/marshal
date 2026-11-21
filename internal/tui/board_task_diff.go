package tui

import (
	"bytes"
	"strings"

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

type taskDiffBuffer struct {
	buf      bytes.Buffer
	overflow bool
}

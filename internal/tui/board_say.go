package tui

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Zen1th53/marshal/internal/chattransport"
	"github.com/Zen1th53/marshal/internal/tmux"
	"github.com/Zen1th53/marshal/internal/tui/board/widgets/chat"
)

// What the operator types on the Board without a leading "/" is said to the
// Marshal: it reaches the docked chat as their own message, so talking to
// the Marshal never needs a pane switch. The native chat takes it as a user
// turn. The tmux chat is a provider CLI MARSHAL does not own, so the message
// is typed into it only while it sits idle at an empty prompt; while the
// Marshal is busy it waits in a queue, and the Board says so.

// sayEcho is one message as Activity shows it.
type sayEcho struct {
	id    int
	At    time.Time
	Text  string
	State string // sending, queued, sent, not sent
	Why   string // for not sent: the reason
	// Runtime marks a message Marshal wrote itself (a notice or a nudge),
	// never typed by the owner: Activity labels it as from the runtime.
	Runtime bool
}

const (
	saySending = "sending"
	sayQueued  = "queued"
	saySent    = "sent"
	sayFailed  = "not sent"
)

// sayState is the queue of messages for the tmux chat and their echoes.
type sayState struct {
	// open is set while the Board's composer takes a message: s opens it,
	// so a sentence's first letter is never read as a Board key; Enter
	// sends, Esc closes.
	open    atomic.Bool
	mu      sync.Mutex
	echo    []sayEcho // newest first, bounded by sayEchoes
	pending []sayItem // oldest first
	running bool      // a deliverSaid goroutine owns pending
	next    int       // last message id
}

type sayItem struct {
	id      int
	text    string
	at      time.Time
	runtime bool
}

// sayEchoes is how many messages Activity remembers.
const sayEchoes = 4

// Seams for tests: how long a queued message waits for the chat, how often
// the chat is looked at, and how long a frame must hold still to be idle.
var (
	sayGiveUp = 10 * time.Minute
	sayPoll   = 700 * time.Millisecond
	saySettle = 250 * time.Millisecond
)

// composing reports whether the composer has the keyboard: it holds text,
// or s opened it for a message to the Marshal.
func (w *Workspace) composing() bool {
	return w.composer.Text() != "" || w.say.open.Load()
}

// openSay opens the Board's composer for a message to the Marshal.
func (w *Workspace) openSay() {
	w.say.open.Store(true)
	w.closeCompletion()
}

// sayEchoList is the echoes for a paint, newest first.
func (w *Workspace) sayEchoList() []sayEcho {
	w.say.mu.Lock()
	defer w.say.mu.Unlock()
	return append([]sayEcho(nil), w.say.echo...)
}

// sayNote adds or replaces the echo of message e.id and repaints.
func (w *Workspace) sayNote(e sayEcho) {
	w.say.mu.Lock()
	i := slices.IndexFunc(w.say.echo, func(have sayEcho) bool { return have.id == e.id })
	if i >= 0 {
		w.say.echo[i] = e
	} else {
		w.say.echo = append([]sayEcho{e}, w.say.echo...)
		w.say.echo = w.say.echo[:min(len(w.say.echo), sayEchoes)]
	}
	w.say.mu.Unlock()
	w.requestRepaint()
}

// sayToMarshal sends text to the Marshal chat as the operator's message.
func (w *Workspace) sayToMarshal(ctx context.Context, text string) {
	w.sayAs(ctx, text, false)
}

// noticeToMarshal sends a message the runtime wrote itself (not the
// owner's words) through the same path; Activity and the docked chat label
// it as from the runtime, never as "you".
func (w *Workspace) noticeToMarshal(ctx context.Context, text string) {
	w.sayAs(ctx, text, true)
}

func (w *Workspace) sayAs(ctx context.Context, text string, runtime bool) {
	text = strings.TrimSpace(cleanBoard(text))
	if text == "" {
		return
	}
	w.say.mu.Lock()
	w.say.next++
	id := w.say.next
	w.say.mu.Unlock()
	now := time.Now()
	echo := sayEcho{id: id, At: now, Text: text, State: saySending, Runtime: runtime}
	role := chat.You
	if runtime {
		role = chat.Note
	}

	w.chat.mu.Lock()
	tr := w.chat.tr
	if tr != nil {
		w.chat.view.ScrollEnd()
		w.chat.view.Append(chat.Entry{Role: role, At: now, Text: text})
		w.chat.view.SetThinking(true)
	}
	w.chat.mu.Unlock()
	// Once taken, a message belongs to the workspace, not to the caller:
	// a caller that hands over a short-lived context (a background task
	// that ends as soon as the message is queued) must not stop it from
	// being sent.
	bg := w.workers.context()
	if tr != nil {
		echo.State = saySent
		w.sayNote(echo)
		w.startBackground(bg, func(ctx context.Context) {
			if err := tr.Send(ctx, chattransport.Turn{Text: text}); err != nil {
				w.sayNote(sayEcho{id: id, At: now, Text: text, State: sayFailed, Why: err.Error(), Runtime: runtime})
				w.memoryAnswer(sayWhose(runtime, "The Marshal chat did not take your message: ", "The Marshal chat did not take Marshal's notice: ")+err.Error(), true)
			}
		})
		return
	}
	if w.sayPane() == "" {
		echo.State, echo.Why = sayFailed, "no Marshal chat is open"
		w.sayNote(echo)
		if runtime {
			// Nothing the owner typed is lost: the notice is not theirs.
			return
		}
		// The text goes back in the composer: nothing typed is lost.
		w.composer.SetText(text)
		w.say.open.Store(true)
		w.setOutput("No Marshal chat is open, so nothing was sent; /marshal chat starts one. Your text is back in the line below.", true)
		return
	}
	w.sayNote(echo)
	w.say.mu.Lock()
	w.say.pending = append(w.say.pending, sayItem{id: id, text: text, at: now, runtime: runtime})
	start := !w.say.running
	w.say.running = true
	w.say.mu.Unlock()
	if start && !w.startBackground(bg, w.deliverSaid) {
		w.say.mu.Lock()
		w.say.running = false
		w.say.mu.Unlock()
	}
}

// sayPane is the tmux chat's pane, or "" with no tmux chat.
func (w *Workspace) sayPane() string {
	w.tmuxMu.Lock()
	defer w.tmuxMu.Unlock()
	if a := w.tmuxActiveWins["marshal-chat"]; a != nil && isPaneID(a.paneID) {
		return a.paneID
	}
	return ""
}

// deliverSaid types the queued messages into the tmux chat, oldest first,
// each only once the chat is idle at an empty prompt.
func (w *Workspace) deliverSaid(ctx context.Context) {
	told := map[int]bool{}
	for {
		w.say.mu.Lock()
		if len(w.say.pending) == 0 {
			w.say.running = false
			w.say.mu.Unlock()
			return
		}
		item := w.say.pending[0]
		w.say.mu.Unlock()
		pop := func() {
			w.say.mu.Lock()
			if len(w.say.pending) > 0 && w.say.pending[0].id == item.id {
				w.say.pending = w.say.pending[1:]
			}
			w.say.mu.Unlock()
		}
		pane := w.sayPane()
		err := errChatBusy
		if pane != "" {
			err = w.typeIntoChat(ctx, pane, item.text)
		}
		switch {
		case err == nil:
			pop()
			w.sayNote(sayEcho{id: item.id, At: item.at, Text: item.text, State: saySent, Runtime: item.runtime})
			continue
		case ctx.Err() != nil:
			w.dropSaid()
			return
		case !errors.Is(err, errChatBusy):
			pop()
			w.sayNote(sayEcho{id: item.id, At: item.at, Text: item.text, State: sayFailed, Why: err.Error(), Runtime: item.runtime})
			w.memoryAnswer(sayWhose(item.runtime, "Your message did not reach the Marshal chat: ", "Marshal's notice did not reach the Marshal chat: ")+err.Error(), true)
			continue
		case time.Since(item.at) > sayGiveUp:
			pop()
			w.sayNote(sayEcho{id: item.id, At: item.at, Text: item.text, State: sayFailed, Why: "the chat stayed busy", Runtime: item.runtime})
			w.memoryAnswer(sayWhose(item.runtime, "The Marshal chat stayed busy, so your message was not sent: press m to type it there.", "The Marshal chat stayed busy, so Marshal's notice was not sent."), true)
			continue
		case !told[item.id]:
			told[item.id] = true
			w.sayNote(sayEcho{id: item.id, At: item.at, Text: item.text, State: sayQueued, Runtime: item.runtime})
			if !item.runtime {
				w.memoryAnswer("Marshal chat busy: your message is queued for its next prompt.", false)
			}
		}
		t := time.NewTimer(sayPoll)
		select {
		case <-ctx.Done():
			t.Stop()
			w.dropSaid()
			return
		case <-t.C:
		}
	}
}

// dropSaid ends a deliverer that was stopped: the queue is released, so a
// later message starts a new deliverer, and each waiting message shows as
// not sent in plain words instead of "sending" forever.
func (w *Workspace) dropSaid() {
	w.say.mu.Lock()
	left := w.say.pending
	w.say.pending = nil
	w.say.running = false
	w.say.mu.Unlock()
	for _, item := range left {
		w.sayNote(sayEcho{id: item.id, At: item.at, Text: item.text, State: sayFailed, Why: "MARSHAL stopped before it was sent", Runtime: item.runtime})
	}
}

// sayWhose picks the owner's or the runtime's wording for a message.
func sayWhose(runtime bool, owner, notice string) string {
	if runtime {
		return notice
	}
	return owner
}

// errChatBusy is a chat that is not idle at an empty prompt yet.
var errChatBusy = errors.New("the Marshal chat is busy")

// typeIntoChat pastes text into the chat's prompt and submits it, only when
// the chat is idle at an empty prompt; otherwise it returns errChatBusy and
// types nothing.
func (w *Workspace) typeIntoChat(ctx context.Context, pane, text string) error {
	w.chatTypeMu.Lock()
	defer w.chatTypeMu.Unlock()
	before, ok := w.chatIdleFrame(ctx, pane)
	if !ok {
		return errChatBusy
	}
	buffer := "marshal-say"
	if _, err := tmux.RunCommand(ctx, "set-buffer", "-b", buffer, "--", text); err != nil {
		return err
	}
	if _, err := tmux.RunCommand(ctx, "paste-buffer", "-d", "-p", "-b", buffer, "-t", pane); err != nil {
		_, _ = tmux.RunCommand(context.WithoutCancel(ctx), "delete-buffer", "-b", buffer)
		return err
	}
	// The paste must have landed in the prompt before Enter submits it.
	deadline := time.Now().Add(2 * time.Second)
	for {
		frame, err := tmux.RunCommand(ctx, "capture-pane", "-p", "-t", pane)
		if err != nil {
			return err
		}
		if string(frame) != before {
			break
		}
		if time.Now().After(deadline) {
			return errors.New("the chat did not show the pasted message")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	_, err := tmux.RunCommand(ctx, "send-keys", "-t", pane, "Enter")
	return err
}

// chatIdleFrame reports whether the chat pane sits idle at an empty prompt,
// with the plain frame it judged: not in copy mode, not dead, the frame
// unchanged across saySettle, and chatPromptIdle on it.
func (w *Workspace) chatIdleFrame(ctx context.Context, pane string) (string, bool) {
	status, err := tmux.RunCommand(ctx, "display-message", "-p", "-t", pane, "#{pane_id} #{pane_in_mode} #{pane_dead}")
	if err != nil || strings.TrimSpace(string(status)) != pane+" 0 0" {
		return "", false
	}
	first, err := tmux.RunCommand(ctx, "capture-pane", "-p", "-e", "-t", pane)
	if err != nil {
		return "", false
	}
	select {
	case <-ctx.Done():
		return "", false
	case <-time.After(saySettle):
	}
	second, err := tmux.RunCommand(ctx, "capture-pane", "-p", "-e", "-t", pane)
	if err != nil || string(first) != string(second) || !chatPromptIdle(string(second)) {
		return "", false
	}
	plain, err := tmux.RunCommand(ctx, "capture-pane", "-p", "-t", pane)
	if err != nil {
		return "", false
	}
	return string(plain), true
}

// chatBusyWords are what provider CLIs show near their prompt while a turn
// runs or a dialog waits for a choice.
var chatBusyWords = []string{"esc to interrupt", "esc to cancel", "ctrl+c to interrupt", "enter to submit", "enter to confirm", "do you want to"}

// promptMarkers start a provider CLI's input line: Claude Code's ">" in a
// box, Codex's "›", and the "❯" of others.
var promptMarkers = []string{">", "›", "❯"}

var menuChoice = regexp.MustCompile(`^\d+[.)]\s`)

// chatPromptIdle reads a captured frame (with SGR escapes) of a provider
// CLI. It is idle when the last input line is a bare prompt or one showing
// only its dimmed placeholder, and no busy or dialog words are on that line
// or the few above it. Anything else, a draft the operator has typed
// included, is not idle: typing then would corrupt it.
func chatPromptIdle(frame string) bool {
	raw := strings.Split(strings.TrimRight(frame, "\n"), "\n")
	at := -1
	var rest string
	for i := len(raw) - 1; i >= 0 && at < 0; i-- {
		body := strings.TrimLeft(StripANSI(raw[i]), " │┃║|")
		for _, m := range promptMarkers {
			if after, ok := strings.CutPrefix(body, m); ok && (after == "" || after[0] == ' ' || strings.HasPrefix(after, " ")) {
				at, rest = i, raw[i]
				break
			}
		}
	}
	if at < 0 {
		return false
	}
	for i := max(0, at-4); i < len(raw); i++ {
		low := strings.ToLower(StripANSI(raw[i]))
		for _, word := range chatBusyWords {
			if strings.Contains(low, word) {
				return false
			}
		}
	}
	after := promptRest(rest)
	if menuChoice.MatchString(strings.TrimSpace(StripANSI(after))) {
		return false
	}
	return onlyDim(after)
}

// promptRest is the raw line after its prompt marker.
func promptRest(line string) string {
	plain := StripANSI(line)
	body := strings.TrimLeft(plain, " │┃║|")
	cut := len(plain) - len(body)
	for _, m := range promptMarkers {
		if strings.HasPrefix(body, m) {
			cut += len(m)
			break
		}
	}
	// Walk the raw line, keeping escapes, until cut plain bytes are passed.
	n := 0
	for i := 0; i < len(line); {
		if n >= cut {
			return line[i:]
		}
		if line[i] == 0x1b {
			j := i + 1
			if j < len(line) && line[j] == '[' {
				j++
				for j < len(line) && (line[j] < 0x40 || line[j] > 0x7e) {
					j++
				}
			}
			i = j + 1
			continue
		}
		i++
		n++
	}
	return ""
}

// onlyDim reports whether every visible character of s (after the trailing
// box border) is drawn dim or grey: a placeholder, never typed text.
func onlyDim(s string) bool {
	dim := false
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j < len(s) && s[j] == 'm' {
				dim = sgrDim(s[i+2:j], dim)
			}
			i = j + 1
			continue
		}
		r := s[i]
		i++
		if r == ' ' || r == '\t' {
			continue
		}
		if r >= 0x80 {
			// A multi-byte rune: box borders are not text.
			ru, size := utf8.DecodeRuneInString(s[i-1:])
			i += size - 1
			if strings.ContainsRune("│┃║", ru) || unicode.IsSpace(ru) {
				continue
			}
		} else if r == '|' {
			continue
		}
		if !dim {
			return false
		}
	}
	return true
}

// sgrDim applies one SGR parameter list to the dim state: faint (2), a
// bright-black or grey foreground counts as dim; a reset or another colour
// does not.
func sgrDim(params string, dim bool) bool {
	f := strings.Split(params, ";")
	if params == "" {
		return false
	}
	for i := 0; i < len(f); i++ {
		switch p := f[i]; {
		case p == "0" || p == "22" || p == "39":
			dim = false
		case p == "2" || p == "90":
			dim = true
		case atoiSGR(p) >= 30 && atoiSGR(p) <= 37 || atoiSGR(p) >= 91 && atoiSGR(p) <= 97:
			dim = false
		case p == "38" && i+2 < len(f) && f[i+1] == "5":
			n := atoiSGR(f[i+2])
			dim = n == 8 || n >= 232 && n <= 250
			i += 2
		case p == "38" && i+4 < len(f) && f[i+1] == "2":
			dim = greyRGB(f[i+2], f[i+3], f[i+4])
			i += 4
		}
	}
	return dim
}

// greyRGB is a truecolour foreground close to grey and not bright.
func greyRGB(r, g, b string) bool {
	x, y, z := atoiSGR(r), atoiSGR(g), atoiSGR(b)
	if x < 0 || y < 0 || z < 0 {
		return false
	}
	hi, lo := max(x, y, z), min(x, y, z)
	return hi-lo <= 24 && hi <= 170
}

// atoiSGR is one SGR parameter as a number, -1 when it is not one.
func atoiSGR(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return -1
	}
	return n
}

// echoLine is one message in Activity: when, to whom, what and where it is.
func (v boardVisual) echoLine(e sayEcho, w int) string {
	state := e.State
	col := map[string]string{saySending: "working", sayQueued: "waiting", saySent: "merged", sayFailed: "failed"}[state]
	switch state {
	case sayQueued:
		state = "queued · chat is busy"
	case saySent:
		state = "sent to chat"
	case sayFailed:
		if e.Why != "" {
			state = "not sent · " + e.Why
		}
	}
	from := "you"
	if e.Runtime {
		from = "runtime"
	}
	to := from + " → marshal"
	if !v.unicode() {
		to = from + " -> marshal"
	}
	head := v.fg("dim", e.At.In(v.now.Location()).Format("15:04:05")) + " " + v.fg("accent", to) + " "
	tail := " " + v.fg(col, "· "+state)
	room := w - VisibleLen(head) - VisibleLen(tail) - 2
	open, shut := "“", "”"
	if !v.unicode() {
		open, shut = "\"", "\""
	}
	return head + open + v.trunc(cleanBoard(e.Text), max(8, room)) + shut + tail
}

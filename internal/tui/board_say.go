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
// Marshal is busy it waits in a queue, and the Board says so. "Busy" is not
// always the Marshal working: the CLI may sit on a question only the owner
// can answer (trust this folder, allow this step), or end a turn on a
// network error and wait for nothing. The deliverer reads the same pane
// captures it already takes to tell those apart, so the Board says what is
// waiting for whom, and a turn lost to the network is sent once more.

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
	// sayTurnSettle is how long after a message is typed an idle chat that
	// was never seen busy counts as having answered it: a fast turn can
	// start and end between two looks.
	sayTurnSettle = 1500 * time.Millisecond
)

// sayWatch is a message Marshal typed into the tmux chat whose turn has not
// been seen to end yet. The deliverer keeps looking at the chat until it
// does, so a turn that ends on a provider network error is noticed (the
// provider does not retry by itself) and the message is sent once more.
type sayWatch struct {
	item    sayItem
	before  string    // the plain frame just before it was typed
	at      time.Time // when it was typed
	started bool      // the chat was seen not idle since
	retried bool      // already sent again once after a network error
}

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

// sayProvider is the tmux chat's provider name as the operator knows it
// (e.g. "agy", "claude", "codex"), or "the Marshal chat" when unknown.
func (w *Workspace) sayProvider() string {
	w.tmuxMu.Lock()
	var p string
	if a := w.tmuxActiveWins["marshal-chat"]; a != nil {
		p = a.provider
	}
	w.tmuxMu.Unlock()
	if name := providerWorker(strings.TrimSpace(p)); name != "" {
		return name
	}
	return "the Marshal chat"
}

// sayWait pauses one poll interval before looking at the chat again; it
// releases any queued messages and returns false if ctx ends first.
func (w *Workspace) sayWait(ctx context.Context) bool {
	t := time.NewTimer(sayPoll)
	select {
	case <-ctx.Done():
		t.Stop()
		w.dropSaid()
		return false
	case <-t.C:
		return true
	}
}

// deliverSaid types the queued messages into the tmux chat, oldest first,
// each only once the chat is idle at an empty prompt. After typing one it
// keeps looking at the chat until that turn ends, so a turn lost to a
// network error is sent again once; and while the chat waits on the owner
// it says so instead of "busy".
func (w *Workspace) deliverSaid(ctx context.Context) {
	told := map[int]bool{}
	var watch *sayWatch
	var shown chatWait     // what the Board last said the chat waits for
	var shownReview string // the review command the Board last described
	var errTold string     // the network error the Board last showed
	var pausedFrame string // frame where a twice-failed network error paused queued messages
	// held is the last time the chat waited on the owner or lost a turn to
	// the network. Neither is the Marshal staying busy, so the give-up clock
	// of a queued message starts again from it: a queued message is kept
	// for as long as the owner or the network holds the chat up.
	var held time.Time
	for {
		w.say.mu.Lock()
		queued := len(w.say.pending) > 0
		if !queued && watch == nil {
			w.say.running = false
			w.say.mu.Unlock()
			return
		}
		var item sayItem
		if queued {
			item = w.say.pending[0]
		}
		w.say.mu.Unlock()
		pop := func() {
			w.say.mu.Lock()
			if len(w.say.pending) > 0 && w.say.pending[0].id == item.id {
				w.say.pending = w.say.pending[1:]
			}
			w.say.mu.Unlock()
		}
		pane := w.sayPane()
		frame, idle := "", false
		if pane != "" {
			w.submitOwnLeftover(ctx, pane)
			frame, idle = w.chatIdleFrame(ctx, pane)
		} else {
			// No chat, no turn to watch.
			watch = nil
			pausedFrame = ""
		}
		if watch != nil && !idle {
			watch.started = true
		}
		if pausedFrame != "" {
			if frame != "" && frame != pausedFrame && (!idle || chatNetworkError(frame, "", "") == "") {
				pausedFrame = ""
			} else {
				held = time.Now()
			}
		}

		// A question only the owner can answer: say what waits for whom,
		// once, and mark the queued message as waiting on them.
		wait := chatWaitNone
		if !idle {
			wait = chatWaitingFor(frame)
		}
		// The Marshal's own draft review is said in plain words; the raw
		// command line is kept in Activity for an owner who wants to check it.
		var review chatReviewAsk
		if wait == chatWaitPermission {
			review, _ = chatReviewAskIn(frame)
		}
		if wait != chatWaitNone {
			held = time.Now()
		}
		if wait != shown || review.argv != shownReview {
			shown, shownReview = wait, review.argv
			if wait != chatWaitNone {
				if review.argv != "" {
					w.addNote(review.plain+". The command the Marshal chat asks to run: "+review.argv, false)
				}
				w.memoryAnswer(chatWaitWords(w.sayProvider(), wait, queued, item.runtime, review), false)
			}
			if queued {
				told[item.id] = true
				why := ""
				if wait != chatWaitNone {
					why = "waiting for you in the chat"
				}
				w.sayNote(sayEcho{id: item.id, At: item.at, Text: item.text, State: sayQueued, Why: why, Runtime: item.runtime})
			}
		}

		// A turn that ended on a network error: the provider gave up on it,
		// so Marshal sends the message again, once. Queued messages stay
		// queued behind it.
		if wait == chatWaitNone && pane != "" && pausedFrame == "" && (watch != nil || queued) {
			sent, before := "", ""
			if watch != nil {
				sent, before = watch.item.text, watch.before
			}
			if line := chatNetworkError(frame, before, sent); line != "" {
				held = time.Now()
				who := w.sayProvider()
				switch {
				case watch != nil && !watch.retried:
					b, err := w.typeIntoChat(ctx, pane, watch.item.text)
					if err == nil {
						watch = &sayWatch{item: watch.item, before: b, at: time.Now(), retried: true}
						errTold = line
						msg := "Network error in the Marshal chat: " + who + " said “" + line + "”. Marshal sent the last message again"
						if queued {
							msg += "; queued messages stay queued."
						} else {
							msg += "."
						}
						w.memoryAnswer(msg, false)
						if !w.sayWait(ctx) {
							return
						}
						continue
					}
					if ctx.Err() == nil && !errors.Is(err, errChatBusy) {
						watch = nil
						w.memoryAnswer("Network error in the Marshal chat: "+who+" said “"+line+"”. Marshal could not send the last message again: "+err.Error(), true)
					} else if errTold != line {
						// Not back at its prompt yet: say so, and try again
						// at the next look.
						errTold = line
						w.memoryAnswer("Network error in the Marshal chat: "+who+" said “"+line+"”. Marshal sends the last message again once the chat is back at its prompt.", true)
					}
				case watch != nil:
					watch = nil
					errTold = line
					msg := "Network error again in the Marshal chat: " + who + " said “" + line + "”. Marshal already sent the last message again once, so it stops there: open the chat (F11) and send it when the connection is back."
					if queued {
						pausedFrame = frame
						told[item.id] = true
						w.sayNote(sayEcho{id: item.id, At: item.at, Text: item.text, State: sayQueued, Why: "paused after network error", Runtime: item.runtime})
						msg += " Queued messages stay queued."
					}
					w.memoryAnswer(msg, true)
				case errTold != line:
					// A turn Marshal did not type (the briefing, or the
					// owner in the chat): nothing to send again.
					errTold = line
					if idle {
						w.memoryAnswer("Network error in the Marshal chat: "+who+" said “"+line+"”. "+sayWhose(item.runtime, "Sending your queued message now.", "Sending Marshal's queued notice now."), true)
					} else {
						told[item.id] = true
						w.sayNote(sayEcho{id: item.id, At: item.at, Text: item.text, State: sayQueued, Why: "chat hit a network error", Runtime: item.runtime})
						w.memoryAnswer("Network error in the Marshal chat: "+who+" said “"+line+"”. "+sayWhose(item.runtime, "Your queued message stays queued for its next prompt.", "Marshal's notice stays queued for its next prompt."), true)
					}
				}
			} else if watch != nil && (idle && (watch.started || time.Since(watch.at) > sayTurnSettle) || time.Since(watch.at) > sayGiveUp) {
				// The turn ended cleanly (or was watched long enough).
				watch = nil
				errTold = ""
			}
		}

		if !queued {
			if !w.sayWait(ctx) {
				return
			}
			continue
		}
		err := errChatBusy
		var before string
		if idle && watch == nil && pausedFrame == "" {
			before, err = w.typeIntoChat(ctx, pane, item.text)
		}
		giveUpFrom := item.at
		if held.After(giveUpFrom) {
			giveUpFrom = held
		}
		switch {
		case err == nil:
			pop()
			w.sayNote(sayEcho{id: item.id, At: item.at, Text: item.text, State: saySent, Runtime: item.runtime})
			watch = &sayWatch{item: item, before: before, at: time.Now()}
			errTold = ""
			continue
		case ctx.Err() != nil:
			w.dropSaid()
			return
		case !errors.Is(err, errChatBusy):
			pop()
			w.sayNote(sayEcho{id: item.id, At: item.at, Text: item.text, State: sayFailed, Why: err.Error(), Runtime: item.runtime})
			w.memoryAnswer(sayWhose(item.runtime, "Your message did not reach the Marshal chat: ", "Marshal's notice did not reach the Marshal chat: ")+err.Error(), true)
			continue
		case time.Since(giveUpFrom) > sayGiveUp:
			pop()
			w.sayNote(sayEcho{id: item.id, At: item.at, Text: item.text, State: sayFailed, Why: "the chat stayed busy", Runtime: item.runtime})
			w.memoryAnswer(sayWhose(item.runtime, "The Marshal chat stayed busy, so your message was not sent: press m to type it there.", "The Marshal chat stayed busy, so Marshal's notice was not sent."), true)
			continue
		case !told[item.id]:
			told[item.id] = true
			why := ""
			if wait != chatWaitNone {
				why = "waiting for you in the chat"
			} else if pausedFrame != "" {
				why = "paused after network error"
			}
			w.sayNote(sayEcho{id: item.id, At: item.at, Text: item.text, State: sayQueued, Why: why, Runtime: item.runtime})
			if wait != chatWaitNone {
				w.memoryAnswer(chatWaitWords(w.sayProvider(), wait, true, item.runtime, review), false)
			} else if pausedFrame == "" && !item.runtime {
				w.memoryAnswer("Marshal chat busy: your message is queued for its next prompt.", false)
			}
		}
		if !w.sayWait(ctx) {
			return
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
// types nothing. On success it returns the plain frame captured just before
// pasting so the deliverer can tell new turn output from older lines.
func (w *Workspace) typeIntoChat(ctx context.Context, pane, text string) (string, error) {
	w.chatTypeMu.Lock()
	defer w.chatTypeMu.Unlock()
	before, ok := w.chatIdleFrame(ctx, pane)
	if !ok {
		return "", errChatBusy
	}
	buffer := "marshal-say"
	if _, err := tmux.RunCommand(ctx, "set-buffer", "-b", buffer, "--", text); err != nil {
		return "", err
	}
	if _, err := tmux.RunCommand(ctx, "paste-buffer", "-d", "-p", "-b", buffer, "-t", pane); err != nil {
		_, _ = tmux.RunCommand(context.WithoutCancel(ctx), "delete-buffer", "-b", buffer)
		return "", err
	}
	// The paste must have landed in the prompt before Enter submits it.
	deadline := time.Now().Add(2 * time.Second)
	for {
		frame, err := tmux.RunCommand(ctx, "capture-pane", "-p", "-t", pane)
		if err != nil {
			return "", err
		}
		if string(frame) != before {
			break
		}
		if time.Now().After(deadline) {
			return "", errors.New("the chat did not show the pasted message")
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	// A paste the provider holds unsent ("[Pasted text #1]") is ours and
	// stays recorded as ours: the next look submits it (submitOwnLeftover),
	// so it counts as typed, not as a failure.
	if err := submitChatTurnAs(ctx, tmux.RunCommand, w.chatComposerDialect(), pane, text, deliverEnterTries, deliverEnterWait, true); err != nil && !errors.Is(err, errChatUnsubmitted) {
		return "", err
	}
	return before, nil
}

// chatComposerDialect is the input-box dialect of the tmux chat's provider.
func (w *Workspace) chatComposerDialect() composerDialect {
	w.tmuxMu.Lock()
	provider := ""
	if a := w.tmuxActiveWins["marshal-chat"]; a != nil {
		provider = a.provider
	}
	w.tmuxMu.Unlock()
	return composerDialectFor(provider)
}

// submitOwnLeftover presses Enter for a paste of ours the provider still
// holds unsent, so it neither waits for the owner nor reads as their draft.
func (w *Workspace) submitOwnLeftover(ctx context.Context, pane string) {
	if _, ok := ownPasteFor(pane); !ok {
		return
	}
	w.chatTypeMu.Lock()
	defer w.chatTypeMu.Unlock()
	frame, err := tmux.RunCommand(ctx, "capture-pane", "-p", "-t", pane)
	if err != nil {
		return
	}
	d := w.chatComposerDialect()
	own, ok := ownLeftover(d, pane, string(frame))
	if !ok {
		if composerClear(d, string(frame)) {
			forgetOwnPaste(pane)
		}
		return
	}
	if !chatBusyNearPrompt(string(frame)) {
		_ = submitChatTurnAs(ctx, tmux.RunCommand, d, pane, own.text, deliverEnterTries, deliverEnterWait, false)
	}
}

// chatIdleFrame reports whether the chat pane sits idle at an empty prompt,
// with the plain frame it judged: not in copy mode, not dead, the frame
// unchanged across saySettle, and chatPromptIdle on it. When not idle it
// still returns the plain text of the frame it already captured so the
// deliverer can tell a provider prompt or network error from busy work
// without an extra capture-pane call.
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
	if err != nil {
		return "", false
	}
	clear := composerClear(w.chatComposerDialect(), string(second))
	noteChatInput(pane, clear)
	if string(first) != string(second) || !clear {
		return StripANSI(string(second)), false
	}
	plain, err := tmux.RunCommand(ctx, "capture-pane", "-p", "-t", pane)
	if err != nil {
		return StripANSI(string(second)), false
	}
	return string(plain), true
}

// chatWait names a provider prompt in the tmux chat that only the owner can
// answer (Marshal never clicks through provider trust or permission prompts).
type chatWait int

const (
	chatWaitNone chatWait = iota
	chatWaitTrust
	chatWaitPermission
)

// chatWaitWords puts a provider prompt in plain words: who is waiting for
// whom, and the key that takes the owner straight to it. review, when its
// plain words are set, is the Marshal's own draft review the permission
// prompt is for.
func chatWaitWords(who string, wait chatWait, queued, runtime bool, review chatReviewAsk) string {
	var msg string
	switch {
	case wait == chatWaitTrust:
		msg = "Waiting for you: " + who + " asks to trust this folder — open the chat (F11) and choose Yes."
	case wait == chatWaitPermission && review.plain != "":
		msg = "Waiting for you: " + review.plain + " — open the chat (F11) and choose Yes or No. Activity has the exact command."
	case wait == chatWaitPermission:
		msg = "Waiting for you: " + who + " asks permission to continue — open the chat (F11) and choose Yes or No."
	default:
		return ""
	}
	if queued {
		msg += sayWhose(runtime, " Your message stays queued until you answer.", " Marshal's notice stays queued until you answer.")
	}
	return msg
}

// chatReviewAsk is a permission prompt in the Marshal chat for the
// Marshal's own fresh-context review of its draft: the protocol's built-in
// design review starts another provider's CLI (for example
// "codex exec --ephemeral --skip-git-repo-check -s read-only …"). The Board
// says it in plain words; the command itself, as the chat shows it, goes to
// Activity, where a click shows all of it.
type chatReviewAsk struct {
	plain string // "The Marshal asks Codex to review its design draft (read-only)"
	argv  string // the command as the chat's prompt shows it, redacted
}

// reviewCLIs are the provider CLIs' one-shot forms the Marshal starts a
// reviewer with, and the reviewer's name in plain words.
var reviewCLIs = []struct {
	re   *regexp.Regexp
	name string
}{
	{regexp.MustCompile(`\bcodex\s+exec\b`), "Codex"},
	{regexp.MustCompile(`\bclaude\s+(-p|--print)\b`), "Claude"},
	{regexp.MustCompile(`\bgemini\s+(-p|--prompt)\b`), "Gemini"},
	{regexp.MustCompile(`\bopencode\s+run\b`), "OpenCode"},
}

// reviewReadOnly and reviewWritable decide "(read-only)". Only a sandbox the
// command itself asks for counts, and any flag that widens it wins: the plain
// words must never call a command read-only that is not.
var (
	reviewReadOnly = regexp.MustCompile(`(-s|--sandbox)[\s=]+read-only\b|--permission-mode[\s=]+plan\b`)
	reviewWritable = regexp.MustCompile(`danger-full-access|workspace-write|--full-auto|--yolo|--dangerously|bypass|--permission-mode[\s=]+(acceptEdits|bypassPermissions)`)
)

// chatReviewAskIn finds, in a chat frame waiting on a permission prompt, a
// provider CLI started to review the Marshal's draft and describes it. The
// command may wrap over a few lines in a narrow pane; it ends at the
// prompt's question or its choices. ok is false for any other command, which
// keeps the generic "asks permission to continue" words.
func chatReviewAskIn(frame string) (chatReviewAsk, bool) {
	lines := plainPaneLines(frame)
	tail := lines[max(0, len(lines)-20):]
	for i, line := range tail {
		for _, cli := range reviewCLIs {
			loc := cli.re.FindStringIndex(line)
			if loc == nil {
				continue
			}
			parts := []string{line[loc[0]:]}
			for _, more := range tail[i+1 : min(len(tail), i+6)] {
				if reviewPromptChrome(more) {
					break
				}
				parts = append(parts, more)
			}
			argv := strings.Join(parts, " ")
			what := "its draft"
			low := strings.ToLower(argv)
			switch {
			case strings.Contains(low, "concepts_brief") || strings.Contains(low, "concepts brief"):
				what = "its concepts brief"
			case strings.Contains(low, "design"):
				what = "its design draft"
			}
			plain := "The Marshal asks " + cli.name + " to review " + what
			if reviewReadOnly.MatchString(argv) && !reviewWritable.MatchString(argv) {
				plain += " (read-only)"
			}
			return chatReviewAsk{plain: plain, argv: chattransport.OneLine(cleanBoard(RedactContent(argv, nil)), 2000)}, true
		}
	}
	return chatReviewAsk{}, false
}

// reviewPromptChrome is a line of the permission prompt around a command:
// the question, a numbered or arrowed choice, or a confirm hint.
func reviewPromptChrome(line string) bool {
	low := strings.ToLower(strings.TrimSpace(line))
	if strings.HasSuffix(low, "?") || linesContainAny([]string{low}, chatPermissionPhrases) {
		return true
	}
	for _, p := range []string{"❯", "›", "> ", "1.", "2.", "3.", "yes", "no,", "no ", "esc "} {
		if strings.HasPrefix(low, p) {
			return true
		}
	}
	return false
}

var (
	chatTrustPhrases = []string{
		"do you trust the contents of",
		"do you trust the files in",
		"do you trust this folder",
		"do you trust this project",
		"do you trust this directory",
		"do you trust this workspace",
		"trust this folder",
		"trust the files in this folder",
		"is this a project you created or one you trust",
	}
	chatPermissionPhrases = []string{
		"allow once",
		"allow always",
		"allow for this session",
		"would you like to run the following command",
		"press enter to confirm or esc to cancel",
		"enter to submit | esc to cancel",
		"enter to confirm",
		"waiting for approval",
		"waiting for your approval",
		"needs your approval",
	}
	chatNetworkPhrases = []string{
		"there was a network issue",
		"network issue connecting",
		"network error",
		"apiconnectionerror",
		"connection error",
		"connection reset",
		"connection refused",
		"connection timed out",
		"connection lost",
		"lost connection",
		"failed to connect",
		"unable to connect",
		"could not connect",
		"cannot connect to",
		"error connecting to",
		"stream disconnected",
		"stream closed unexpectedly",
		"socket hang up",
		"fetch failed",
		"request timed out",
		"upstream connect error",
		"econnreset",
		"econnrefused",
		"etimedout",
		"enotfound",
	}
)

// chatWaitingFor inspects a captured chat frame and reports whether the
// provider CLI is sitting on a trust or permission prompt waiting for the
// owner. Trust is checked before permission because trust dialogs often also
// carry "Enter to confirm".
func chatWaitingFor(frame string) chatWait {
	lines := plainPaneLines(frame)
	if len(lines) == 0 || chatActiveWork(lines) {
		return chatWaitNone
	}
	tail := lines[max(0, len(lines)-20):]
	if linesContainAny(tail, chatTrustPhrases) {
		return chatWaitTrust
	}
	if linesContainAny(tail, chatPermissionPhrases) {
		return chatWaitPermission
	}
	for _, line := range tail {
		low := strings.ToLower(line)
		// Claude's "Do you want to proceed?" and similar prompts start the
		// line; a conversational question like "How do you want to proceed?"
		// does not.
		if strings.HasPrefix(low, "do you want to ") ||
			strings.HasPrefix(low, "allow the ") && strings.Contains(low, "mcp server to run tool") ||
			strings.HasPrefix(low, "allow ") && strings.HasSuffix(low, "?") ||
			strings.HasSuffix(low, "[y/n]") ||
			strings.HasSuffix(low, "(y/n)") {
			return chatWaitPermission
		}
	}
	return chatWaitNone
}

// chatNetworkError reports a provider network error that ended the latest
// turn in frame, or "" if none is present. When sent is non-empty it looks
// only after the last echoed occurrence of sent (or compares against before
// when sent was collapsed by paste), so an earlier error still visible above
// a retry never falsely fails the retry.
func chatNetworkError(frame, before, sent string) string {
	lines := plainPaneLines(frame)
	if len(lines) == 0 || chatActiveWork(lines) {
		return ""
	}
	if before != "" && strings.TrimSpace(StripANSI(frame)) == strings.TrimSpace(StripANSI(before)) {
		return ""
	}
	if sent != "" {
		if idx := findSentLine(lines, sent); idx >= 0 {
			return networkErrorFromTail(lines[idx+1:])
		}
		// Sent text was not echoed verbatim (e.g. collapsed paste marker):
		// only report when frame has more network error lines than before.
		beforeLines := plainPaneLines(before)
		if countNetworkErrors(lines) <= countNetworkErrors(beforeLines) {
			return ""
		}
	}
	return networkErrorFromTail(lines)
}

func networkErrorFromTail(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	lastPrompt := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if isPromptLine(lines[i]) {
			lastPrompt = i
			break
		}
	}
	// First check lines after the last prompt (e.g. error printed before the
	// next prompt appears).
	if lastPrompt >= 0 && lastPrompt+1 < len(lines) {
		if match := scanNetworkErrorWindow(lines[lastPrompt+1:]); match != "" {
			return match
		}
	}
	// If the last prompt sits at the bottom (with at most two footer/status
	// lines below it), check the end of the turn immediately above that
	// prompt, stopping at any earlier prompt line so an old turn's error
	// never leaks past a newer turn.
	end := len(lines)
	if lastPrompt >= 0 && len(lines)-1-lastPrompt <= 2 {
		end = lastPrompt
	}
	start := max(0, end-3)
	for i := end - 1; i >= start; i-- {
		if isPromptLine(lines[i]) {
			start = i + 1
			break
		}
	}
	return scanNetworkErrorWindow(lines[start:end])
}

func scanNetworkErrorWindow(window []string) string {
	for i := len(window) - 1; i >= 0; i-- {
		if isPromptLine(window[i]) {
			continue
		}
		if isNetworkErrorLine(window[i]) {
			return chattransport.OneLine(cleanBoard(window[i]), 140)
		}
		// A narrow docked pane can wrap one error sentence across two lines;
		// join with the line above when the lower line is a continuation or
		// the upper line alone did not already complete a match.
		if i > 0 && !isPromptLine(window[i-1]) && (!isNetworkErrorLine(window[i-1]) || startsLower(window[i])) {
			joined := window[i-1] + " " + window[i]
			if isNetworkErrorLine(joined) {
				return chattransport.OneLine(cleanBoard(joined), 140)
			}
		}
	}
	return ""
}

func isNetworkErrorLine(line string) bool {
	low := strings.ToLower(line)
	for _, p := range chatNetworkPhrases {
		if strings.Contains(low, p) {
			return true
		}
	}
	return false
}

func countNetworkErrors(lines []string) int {
	n := 0
	for i := 0; i < len(lines); i++ {
		if isPromptLine(lines[i]) {
			continue
		}
		if isNetworkErrorLine(lines[i]) {
			n++
			continue
		}
		if i+1 < len(lines) && !isPromptLine(lines[i+1]) && !isNetworkErrorLine(lines[i+1]) && isNetworkErrorLine(lines[i]+" "+lines[i+1]) {
			n++
			i++
		}
	}
	return n
}

func linesContainAny(lines, phrases []string) bool {
	for i, line := range lines {
		low := strings.ToLower(line)
		for _, p := range phrases {
			if strings.Contains(low, p) {
				return true
			}
		}
		if i+1 < len(lines) {
			joined := low + " " + strings.ToLower(lines[i+1])
			for _, p := range phrases {
				if strings.Contains(joined, p) {
					return true
				}
			}
		}
	}
	return false
}

// chatActiveWork reports whether the bottom of the pane shows a running-turn
// interrupt hint: a provider that is actively working is neither sitting on
// a modal prompt nor finished with a network error.
func chatActiveWork(lines []string) bool {
	for _, line := range lines[max(0, len(lines)-6):] {
		low := strings.ToLower(line)
		if strings.Contains(low, "esc to interrupt") || strings.Contains(low, "ctrl+c to interrupt") {
			return true
		}
	}
	return false
}

// plainPaneLines strips ANSI escapes and box borders from frame and returns
// its non-empty text lines.
func plainPaneLines(frame string) []string {
	var out []string
	for _, raw := range strings.Split(StripANSI(frame), "\n") {
		line := strings.TrimSpace(strings.Trim(strings.TrimSpace(raw), "│┃║|"))
		if line != "" && !isBoxRule(line) {
			out = append(out, line)
		}
	}
	return out
}

func isBoxRule(line string) bool {
	for _, r := range line {
		if !strings.ContainsRune("─━═╭╮╰╯┌┐└┘├┤┬┴┼+- ", r) {
			return false
		}
	}
	return true
}

func isPromptLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	for _, m := range promptMarkers {
		if trimmed == m || strings.HasPrefix(trimmed, m+" ") || strings.HasPrefix(trimmed, m+"\u00a0") {
			return true
		}
	}
	return false
}

func startsLower(s string) bool {
	r, _ := utf8.DecodeRuneInString(strings.TrimSpace(s))
	return unicode.IsLower(r)
}

// findSentLine returns the index of the last line in lines that echoes the
// opening of sent, so only output after the latest send is inspected.
func findSentLine(lines []string, sent string) int {
	needle := ""
	for _, l := range strings.Split(sent, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			needle = l
			break
		}
	}
	if needle == "" {
		return -1
	}
	// Cap the needle so a long message wrapped by a narrow pane still matches
	// on its first line.
	if runes := []rune(needle); len(runes) > 40 {
		needle = string(runes[:40])
	}
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if len(needle) >= 6 {
			if strings.Contains(line, needle) {
				return i
			}
			continue
		}
		// Short messages (e.g. "hi", "in") only match as the prompt's own
		// body, never as a substring inside a provider error word like "again".
		body := strings.TrimSpace(stripPromptPrefix(line))
		if body == needle || strings.HasPrefix(body, needle+" ") {
			return i
		}
	}
	return -1
}

func stripPromptPrefix(line string) string {
	for _, p := range []string{"got:", ">", "›", "❯"} {
		if after, ok := strings.CutPrefix(line, p); ok {
			return after
		}
	}
	return line
}

// chatBusyWords are what provider CLIs show near their prompt while a turn
// runs or a dialog waits for a choice.
var chatBusyWords = []string{
	"esc to interrupt", "esc to cancel", "ctrl+c to interrupt",
	"enter to submit", "enter to confirm", "do you want to",
	"do you trust the contents of", "do you trust the files in", "trust this folder",
}

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
		if e.Why != "" {
			state = "queued · " + e.Why
		} else {
			state = "queued · chat is busy"
		}
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

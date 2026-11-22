package tui

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"time"
)

// composerDialect is how one provider CLI draws its input box. Delivery uses
// it to tell three things apart that all show as text after the prompt: the
// owner's own typing (never touched), the provider's placeholder or hint
// text (an empty input), and the placeholder the provider shows for a paste
// it collapsed (ours, when we pasted it, and still unsent).
type composerDialect struct {
	provider string
	// collapsed matches one collapsed-paste placeholder.
	collapsed []*regexp.Regexp
	// hints match the whole of a placeholder or hint a provider draws in an
	// empty input. Whole-content matches only: a draft that merely starts
	// like a hint is still the owner's.
	hints []*regexp.Regexp
}

var (
	claudeCollapsedPaste   = regexp.MustCompile(`\[Pasted text #\d+(?: \+\d+ lines?)?\]`)
	codexCollapsedPaste    = regexp.MustCompile(`\[Pasted Content \d+ chars\]`)
	opencodeCollapsedPaste = regexp.MustCompile(`(?i)\[pasted ~?\d+ lines?\]`)
	agyCollapsedPaste      = regexp.MustCompile(`(?i)\[pasted text: \d+ lines?\]`)
)

// Hints every provider may draw beside a paste or a queued message; matched
// case-insensitively.
var commonComposerHints = []string{"paste again to expand", "press up to edit queued messages"}

func hintRE(s string) *regexp.Regexp { return regexp.MustCompile(`(?i)^(?:` + s + `)$`) }

var composerDialects = map[string]composerDialect{
	"claude": {provider: "claude", collapsed: []*regexp.Regexp{claudeCollapsedPaste},
		hints: []*regexp.Regexp{hintRE(`try "[^"\n]*"`), hintRE(`\? for shortcuts`)}},
	"codex": {provider: "codex", collapsed: []*regexp.Regexp{codexCollapsedPaste},
		hints: []*regexp.Regexp{hintRE(`ask codex to do anything`)}},
	"opencode": {provider: "opencode", collapsed: []*regexp.Regexp{opencodeCollapsedPaste, claudeCollapsedPaste},
		hints: []*regexp.Regexp{hintRE(`ask anything[^\n]*`)}},
	"agy": {provider: "agy", collapsed: []*regexp.Regexp{agyCollapsedPaste, claudeCollapsedPaste},
		hints: []*regexp.Regexp{hintRE(`type your message[^\n]*`)}},
}

// composerDialectFor returns the provider's dialect. An unknown or empty
// provider gets the union of all of them: every pattern is specific enough
// that no owner types it by accident.
func composerDialectFor(provider string) composerDialect {
	provider = canonicalNeutralProvider(provider)
	if provider == "antigravity" {
		provider = "agy"
	}
	if d, ok := composerDialects[provider]; ok {
		return d
	}
	u := composerDialect{provider: provider}
	for _, name := range []string{"claude", "codex", "opencode", "agy"} {
		u.collapsed = append(u.collapsed, composerDialects[name].collapsed...)
		u.hints = append(u.hints, composerDialects[name].hints...)
	}
	return u
}

// stripHints removes hint text the provider draws after a collapsed paste
// ("[Pasted text #1] paste again to expand") and returns the rest.
func (d composerDialect) stripHints(content string) string {
	low := strings.ToLower(content)
	for _, h := range commonComposerHints {
		if i := strings.Index(low, h); i >= 0 {
			content, low = strings.TrimSpace(content[:i]+content[i+len(h):]), strings.TrimSpace(low[:i]+low[i+len(h):])
		}
	}
	return strings.Trim(strings.TrimSpace(content), "·•-– ")
}

// collapsedPaste reports whether content is nothing but one or more of the
// provider's collapsed-paste placeholders.
func (d composerDialect) collapsedPaste(content string) bool {
	rest := d.stripHints(content)
	if rest == "" {
		return false
	}
	for _, re := range d.collapsed {
		rest = re.ReplaceAllString(rest, "")
	}
	return strings.TrimSpace(rest) == ""
}

// hint reports whether content is only provider hint text: an empty input
// as far as the owner is concerned. Our own paste is never a hint.
func (d composerDialect) hint(content string) bool {
	content = strings.TrimSpace(content)
	if content == "" {
		return true
	}
	low := strings.ToLower(content)
	for _, h := range commonComposerHints {
		if low == h {
			return true
		}
	}
	for _, h := range d.hints {
		if h.MatchString(content) {
			return true
		}
	}
	return false
}

// ownPaste is what the Board last pasted into a pane and has not seen
// submitted: the message, and the composer content it produced (the text
// itself or the provider's collapsed placeholder for it).
type ownPaste struct {
	text, shown string
}

var ownPastes = struct {
	mu    sync.Mutex
	panes map[string]ownPaste
}{panes: map[string]ownPaste{}}

func rememberOwnPaste(pane, text, shown string) {
	ownPastes.mu.Lock()
	defer ownPastes.mu.Unlock()
	ownPastes.panes[pane] = ownPaste{text: text, shown: shown}
}

func forgetOwnPaste(pane string) {
	ownPastes.mu.Lock()
	defer ownPastes.mu.Unlock()
	delete(ownPastes.panes, pane)
}

func ownPasteFor(pane string) (ownPaste, bool) {
	ownPastes.mu.Lock()
	defer ownPastes.mu.Unlock()
	p, ok := ownPastes.panes[pane]
	return p, ok
}

// ownLeftover reports whether the pane's composer still holds a paste the
// Board made and never saw submitted. Only an exact match of the recorded
// placeholder or text counts: the owner's own paste gets another number.
func ownLeftover(d composerDialect, pane, frame string) (ownPaste, bool) {
	p, ok := ownPasteFor(pane)
	if !ok {
		return ownPaste{}, false
	}
	content, found := chatComposer(frame)
	if !found || content == "" {
		return ownPaste{}, false
	}
	if p.shown == "" {
		// Pasted, but not drawn when last looked at: the first thing the
		// provider draws for it (our text, or its placeholder) is ours, and
		// is pinned so a later paste of the owner's is not.
		if ownChatComposer(frame, p.text) || d.collapsedPaste(content) {
			p.shown = content
			rememberOwnPaste(pane, p.text, content)
			return p, true
		}
		return ownPaste{}, false
	}
	// Exact recorded content only: a line-count match ("[Pasted text #5
	// +2 lines]") could be the owner's own paste of the same length. Our
	// own text itself still counts however the provider now wraps or clips
	// it (a pane resized under a tall input shows other rows of it).
	if d.stripHints(content) == d.stripHints(p.shown) || ownComposerText(frame, p.text) {
		return p, true
	}
	return ownPaste{}, false
}

// adoptOrphanPaste takes a collapsed-paste placeholder that no record in this
// process names as ours: the paste an earlier Board process (before a
// restart or rebind) made and never saw submitted. Callers use it only while
// the Board holds an undelivered or open request for the chat, and only for
// an input that is nothing but the provider's placeholder: text the owner
// typed is never a placeholder, so it is never adopted. The message behind
// it is unknown here (text is empty), so the caller delivers its own
// request again after submitting this one.
func adoptOrphanPaste(d composerDialect, pane, frame string) (ownPaste, bool) {
	if _, ok := ownPasteFor(pane); ok {
		return ownPaste{}, false
	}
	content, found := chatComposer(frame)
	if !found || !d.collapsedPaste(content) {
		return ownPaste{}, false
	}
	p := ownPaste{shown: content}
	rememberOwnPaste(pane, "", content)
	return p, true
}

// panePasteOption is the tmux pane option the Board sets on the chat pane
// while a paste of its own is there unsent: the placeholder the provider drew
// for it, or "?" while it was not drawn yet. It lives with the pane, so a
// Board that restarts (the chat pane surviving it) knows the placeholder
// its predecessor left is the Board's own and not the owner's; a new chat
// pane never has it. The owner's typing never sets it.
const panePasteOption = "@marshal_own_paste"

// panePasteUnknown marks a paste the provider had not drawn when looked at.
const panePasteUnknown = "?"

// markPanePaste records on the pane that it holds a paste of ours showing
// shown ("" while not drawn).
func markPanePaste(ctx context.Context, run chatRunner, pane, shown string) {
	if shown == "" {
		shown = panePasteUnknown
	}
	_, _ = run(ctx, "set-option", "-p", "-t", pane, panePasteOption, shown)
}

// clearPanePaste removes the mark: the paste was taken, or is not ours.
func clearPanePaste(ctx context.Context, run chatRunner, pane string) {
	_, _ = run(context.WithoutCancel(ctx), "set-option", "-p", "-u", "-t", pane, panePasteOption)
}

// panePasteMark reads the mark an earlier Board (or this one) left.
func panePasteMark(ctx context.Context, run chatRunner, pane string) (string, bool) {
	out, err := run(ctx, "show-options", "-p", "-v", "-t", pane, panePasteOption)
	mark := strings.TrimSpace(string(out))
	if err != nil || mark == "" {
		return "", false
	}
	return mark, true
}

// adoptMarkedPaste takes the placeholder in the input as ours when no record
// of this process names a paste and the pane carries the Board's mark: the
// Board that pasted it (this process before it lost the record, or its
// predecessor for this chat pane) was the only one to write there. Only a
// bare collapsed placeholder is adopted, and with a drawn mark only that
// same placeholder: text the owner typed, or a paste of theirs with another
// number, is never taken.
func adoptMarkedPaste(d composerDialect, pane, frame, mark string) (ownPaste, bool) {
	if _, ok := ownPasteFor(pane); ok || mark == "" {
		return ownPaste{}, false
	}
	content, found := chatComposer(frame)
	if !found || !d.collapsedPaste(content) {
		return ownPaste{}, false
	}
	if mark != panePasteUnknown && d.stripHints(content) != d.stripHints(mark) {
		return ownPaste{}, false
	}
	rememberOwnPaste(pane, "", content)
	return ownPaste{shown: content}, true
}

// composerClear reports whether the frame shows an input box holding nothing
// the owner typed: empty, the provider's dimmed placeholder, or plain hint
// text. Busy and dialog words near the prompt still make it not clear.
func composerClear(d composerDialect, frame string) bool {
	if chatPromptIdle(frame) {
		return true
	}
	content, ok := chatComposer(frame)
	if !ok || !d.hint(content) || chatBusyNearPrompt(frame) {
		return false
	}
	return true
}

// chatBusyNearPrompt reports busy or dialog words on the last prompt line or
// the few lines above it, as chatPromptIdle judges them.
func chatBusyNearPrompt(frame string) bool {
	raw := strings.Split(strings.TrimRight(StripANSI(frame), "\n"), "\n")
	at := -1
	for i := len(raw) - 1; i >= 0 && at < 0; i-- {
		// A clipped input's top rule stands where its scrolled-off prompt
		// row would be; a prompt row above it is the transcript.
		if above, _ := clippedRule(raw[i]); above {
			at = i
			break
		}
		body := strings.TrimLeft(raw[i], " │┃║|")
		for _, m := range promptMarkers {
			if strings.HasPrefix(body, m) {
				at = i
				break
			}
		}
	}
	if at < 0 {
		return true
	}
	for i := max(0, at-4); i < len(raw); i++ {
		low := strings.ToLower(raw[i])
		for _, word := range chatBusyWords {
			if strings.Contains(low, word) {
				return true
			}
		}
	}
	return false
}

// chatInputs is the last input-box observation per pane: whether it held
// nothing the owner typed. The Board shows it as "input clear".
var chatInputs = struct {
	mu    sync.Mutex
	panes map[string]chatInputSeen
}{panes: map[string]chatInputSeen{}}

type chatInputSeen struct {
	clear bool
	at    time.Time
}

func noteChatInput(pane string, clear bool) {
	chatInputs.mu.Lock()
	defer chatInputs.mu.Unlock()
	chatInputs.panes[pane] = chatInputSeen{clear: clear, at: time.Now()}
}

// observeChatInput notes the chat input box as one tick of the chat window's
// monitor saw it in frame (styled), for the Board's "input clear".
func (w *Workspace) observeChatInput(pane, provider, frame string) {
	if clear, known := chatInputIn(composerDialectFor(provider), frame); known {
		noteChatInput(pane, clear)
		noteChatAsked(pane, clear && chatAskedIn(frame))
		return
	}
	forgetChatInput(pane)
	noteChatAsked(pane, false)
}

// chatAsked is, per chat pane, whether the monitor's last look saw the
// Marshal's turn end with a question to the owner while the input sat idle
// and clear. It is conservative: anything it cannot read is "not asked".
var chatAsked = struct {
	mu    sync.Mutex
	panes map[string]bool
}{panes: map[string]bool{}}

func noteChatAsked(pane string, asked bool) {
	chatAsked.mu.Lock()
	defer chatAsked.mu.Unlock()
	if asked {
		chatAsked.panes[pane] = true
	} else {
		delete(chatAsked.panes, pane)
	}
}

// chatAskedNow reports the last look at pane: the Marshal asked the owner
// something and waits.
func chatAskedNow(pane string) bool {
	chatAsked.mu.Lock()
	defer chatAsked.mu.Unlock()
	return chatAsked.panes[pane]
}

// chatAskedIn reports whether a chat frame shows the Marshal's turn ended
// with a question to the owner: no turn is running near the prompt, and the
// last line of text above the input box ends with a question mark or asks
// for a reply ("Reply 'allow the folder' or 'paste them'"). Box borders,
// blank lines and the provider's hint rows are skipped; only the last few
// lines above the prompt are read.
func chatAskedIn(frame string) bool {
	if chatBusyNearPrompt(frame) {
		return false
	}
	raw := strings.Split(strings.TrimRight(StripANSI(frame), "\n"), "\n")
	at := -1
	for i := len(raw) - 1; i >= 0 && at < 0; i-- {
		body := strings.TrimLeft(raw[i], " │┃║|")
		for _, m := range promptMarkers {
			if strings.HasPrefix(body, m) {
				at = i
				break
			}
		}
	}
	if at < 0 {
		return false
	}
	for i, seen := at-1, 0; i >= 0 && seen < 8; i, seen = i-1, seen+1 {
		line := strings.TrimSpace(strings.Trim(strings.TrimSpace(raw[i]), "│┃║|"))
		if line == "" || strings.Trim(line, "─━═╭╮╰╯┌┐└┘+-") == "" {
			continue
		}
		low := strings.ToLower(line)
		end := strings.TrimRight(line, " \"'”’)*_`")
		return strings.HasSuffix(end, "?") || strings.HasPrefix(strings.TrimLeft(low, "⏺●•*- "), "reply ")
	}
	return false
}

// forgetChatInput drops pane's observation: its input box is not on screen
// (a provider dialog, a screen being redrawn), so the input is unknown again.
func forgetChatInput(pane string) {
	chatInputs.mu.Lock()
	defer chatInputs.mu.Unlock()
	delete(chatInputs.panes, pane)
}

// chatInputIn reads the input box off one styled frame of the chat pane:
// clear when it holds nothing the owner typed (empty, a provider hint, or
// the provider's dimmed placeholder), whether or not a turn is running.
// known is false when the frame shows no input box.
func chatInputIn(d composerDialect, frame string) (clear, known bool) {
	content, ok := chatComposer(frame)
	if !ok {
		return false, false
	}
	return d.hint(content) || promptOnlyDim(frame), true
}

// promptOnlyDim reports whether the last prompt line holds only dimmed
// text after its marker: the provider's placeholder, not typing.
func promptOnlyDim(frame string) bool {
	raw := strings.Split(strings.TrimRight(frame, "\n"), "\n")
	for i := len(raw) - 1; i >= 0; i-- {
		body := strings.TrimLeft(StripANSI(raw[i]), " │┃║|")
		for _, m := range promptMarkers {
			if after, ok := strings.CutPrefix(body, m); ok && (after == "" || after[0] == ' ' || strings.HasPrefix(after, "\u00a0")) {
				return onlyDim(promptRest(raw[i]))
			}
		}
	}
	return false
}

// chatInputClear reports the last observation of pane's input box.
func chatInputClear(pane string) (clear, known bool) {
	chatInputs.mu.Lock()
	defer chatInputs.mu.Unlock()
	seen, ok := chatInputs.panes[pane]
	return seen.clear, ok
}

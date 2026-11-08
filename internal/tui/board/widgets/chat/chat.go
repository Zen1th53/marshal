// Package chat renders the Marshal conversation inside the Board (TUI3-0013,
// mockup 02): a header, the transcript as named turns with tool-call
// summaries, and a composer on the last row. It is fed structured events by
// the caller and never reads terminal text. It keeps no timers: it changes
// only when the caller feeds it, so an idle chat costs no redraws.
package chat

import (
	"regexp"
	"strings"
	"time"

	"github.com/rivo/uniseg"
)

// Role is who an entry belongs to.
type Role string

const (
	You     Role = "you"
	Marshal Role = "marshal"
	Tool    Role = "tool"
	Note    Role = "note"
	Error   Role = "error"
)

// Entry is one transcript item.
type Entry struct {
	Role      Role
	At        time.Time
	Text      string
	MessageID string // assistant message a text segment belongs to
	ToolID    string
	ToolName  string
	Result    string // tool result summary, once it arrived
	Done      bool   // tool result arrived
	Failed    bool   // tool result is an error
	Streaming bool   // assistant text still arriving
}

// Style colours the widget's parts. A nil function leaves text plain, so
// the zero Style is the monochrome rendering the goldens use.
type Style struct {
	You, Marshal, Dim, Tool, Fail, Error, Title, Accent func(string) string
	Unicode                                             bool
}

func apply(f func(string) string, s string) string {
	if f == nil || s == "" {
		return s
	}
	return f(s)
}

type glyphs struct{ tool, ok, fail, prompt, more, sep string }

func (s Style) glyphs() glyphs {
	if s.Unicode {
		return glyphs{"▸", "✓", "✗", "›", "↓", "·"}
	}
	return glyphs{">", "+", "x", ">", "v", "-"}
}

var spinner = []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"}
var asciiSpinner = []string{"|", "/", "-", "\\"}

// Header is the chat's first row.
type Header struct {
	Provider string // codex, claude …
	Role     string // planner/reviewer
	State    string // e.g. "plan v2 approved 13:41" or "starting"
}

// View is the chat widget's state. It is not safe for concurrent use; the
// caller serialises access.
type View struct {
	entries  []Entry
	header   Header
	thinking bool
	scroll   int // lines scrolled up from the bottom
	input    []rune
	cursor   int
	focused  bool
	version  uint64
	hint     string
}

// Version changes whenever anything the widget renders changed.
func (v *View) Version() uint64 { return v.version }

func (v *View) touch() { v.version++ }

func (v *View) SetHeader(h Header) {
	if h != v.header {
		v.header = h
		v.touch()
	}
}

// SetThinking shows the spinner while a turn is in flight.
func (v *View) SetThinking(on bool) {
	if on != v.thinking {
		v.thinking = on
		v.touch()
	}
}

func (v *View) Thinking() bool { return v.thinking }

func (v *View) SetFocused(on bool) {
	if on != v.focused {
		v.focused = on
		v.touch()
	}
}

func (v *View) Focused() bool { return v.focused }

// SetHint replaces the composer's placeholder.
func (v *View) SetHint(h string) {
	if h != v.hint {
		v.hint = h
		v.touch()
	}
}

// Entries returns a copy of the transcript.
func (v *View) Entries() []Entry { return append([]Entry(nil), v.entries...) }

// Append adds a finished entry.
func (v *View) Append(e Entry) {
	v.entries = append(v.entries, e)
	v.touch()
}

// Delta appends streamed assistant text to the open segment of messageID,
// or opens one.
func (v *View) Delta(at time.Time, messageID, text string) {
	if n := len(v.entries); n > 0 {
		last := &v.entries[n-1]
		if last.Role == Marshal && last.Streaming && last.MessageID == messageID {
			last.Text += text
			v.touch()
			return
		}
	}
	v.Append(Entry{Role: Marshal, At: at, MessageID: messageID, Text: text, Streaming: true})
}

// Final settles one complete assistant text block: it replaces the
// streamed text of the open segment of messageID, or adds the block when
// nothing was streamed.
func (v *View) Final(at time.Time, messageID, text string) {
	for i := len(v.entries) - 1; i >= 0; i-- {
		e := &v.entries[i]
		if e.Role == Marshal && e.Streaming && e.MessageID == messageID {
			e.Text, e.Streaming = text, false
			v.touch()
			return
		}
	}
	v.Append(Entry{Role: Marshal, At: at, MessageID: messageID, Text: text})
}

// ToolUse adds a tool-call summary.
func (v *View) ToolUse(at time.Time, id, name, summary string) {
	v.settleStreaming()
	v.Append(Entry{Role: Tool, At: at, ToolID: id, ToolName: name, Text: summary})
}

// ToolResult completes the tool call id.
func (v *View) ToolResult(id, summary string, failed bool) {
	for i := len(v.entries) - 1; i >= 0; i-- {
		e := &v.entries[i]
		if e.Role == Tool && e.ToolID == id {
			e.Result, e.Done, e.Failed = summary, true, failed
			v.touch()
			return
		}
	}
}

// EndTurn settles anything still streaming and stops the spinner.
func (v *View) EndTurn() {
	v.settleStreaming()
	v.SetThinking(false)
}

func (v *View) settleStreaming() {
	for i := range v.entries {
		if v.entries[i].Streaming {
			v.entries[i].Streaming = false
			v.touch()
		}
	}
}

// LastReply is the text of the Marshal's latest reply (every text segment
// since the operator last spoke), for copying.
func (v *View) LastReply() string {
	var parts []string
	for i := len(v.entries) - 1; i >= 0; i-- {
		e := v.entries[i]
		if e.Role == You && len(parts) > 0 {
			break
		}
		if e.Role == Marshal && strings.TrimSpace(e.Text) != "" {
			parts = append([]string{clean(e.Text)}, parts...)
		}
	}
	return strings.Join(parts, "\n\n")
}

// Scroll moves the transcript by delta lines (positive is up, toward older).
func (v *View) Scroll(delta int) {
	s := max(0, v.scroll+delta)
	if s != v.scroll {
		v.scroll = s
		v.touch()
	}
}

// ScrollEnd returns to the newest line.
func (v *View) ScrollEnd() { v.Scroll(-v.scroll) }

func (v *View) Scrolled() int { return v.scroll }

// Composer editing.

func (v *View) Text() string { return string(v.input) }

func (v *View) Insert(s string) {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return ' '
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	if s == "" {
		return
	}
	r := []rune(s)
	v.input = append(v.input[:v.cursor], append(r, v.input[v.cursor:]...)...)
	v.cursor += len(r)
	v.touch()
}

func (v *View) Backspace() {
	if v.cursor > 0 {
		v.input = append(v.input[:v.cursor-1], v.input[v.cursor:]...)
		v.cursor--
		v.touch()
	}
}

func (v *View) Delete() {
	if v.cursor < len(v.input) {
		v.input = append(v.input[:v.cursor], v.input[v.cursor+1:]...)
		v.touch()
	}
}

func (v *View) Move(delta int) {
	c := min(len(v.input), max(0, v.cursor+delta))
	if c != v.cursor {
		v.cursor = c
		v.touch()
	}
}

func (v *View) Home() { v.Move(-v.cursor) }
func (v *View) End()  { v.Move(len(v.input) - v.cursor) }

// Clear empties the composer.
func (v *View) Clear() {
	if len(v.input) > 0 {
		v.input, v.cursor = nil, 0
		v.touch()
	}
}

// Take empties the composer and returns what it held, trimmed.
func (v *View) Take() string {
	s := strings.TrimSpace(string(v.input))
	v.Clear()
	return s
}

// Render paints the widget in width×height cells. The cursor is 1-based
// within the widget and is only meaningful while focused.
func (v *View) Render(width, height int, st Style) (lines []string, cursorRow, cursorCol int) {
	if width <= 0 || height <= 0 {
		return nil, 1, 1
	}
	g := st.glyphs()
	comp, col := v.composerLine(width, st, g)
	if height == 1 {
		return []string{comp}, 1, col
	}
	lines = make([]string, 0, height)
	lines = append(lines, v.headerLine(width, st))
	area := height - 2
	if height >= 4 {
		lines = append(lines, "")
		area = height - 3
	}
	body := v.transcript(width, st, g)
	maxScroll := max(0, len(body)-area)
	if v.scroll > maxScroll {
		v.scroll = maxScroll
	}
	// While scrolled, the last transcript row says how much is below.
	rows := area
	if v.scroll > 0 && area > 1 {
		rows = area - 1
	}
	end := len(body) - v.scroll
	start := max(0, end-rows)
	shown := append([]string(nil), body[start:end]...)
	for len(shown) < rows {
		shown = append([]string{""}, shown...)
	}
	lines = append(lines, shown...)
	if rows < area {
		lines = append(lines, apply(st.Accent, fit(" "+g.more+" "+itoa(v.scroll)+" newer lines (End)", width)))
	}
	lines = append(lines, comp)
	for i, l := range lines {
		// Degenerate widths cannot hold an indent; such a row loses its
		// colour rather than overflowing the widget.
		if plain := ansi.ReplaceAllString(l, ""); uniseg.StringWidth(plain) > width {
			lines[i] = fit(plain, width)
		}
	}
	return lines, height, col
}

func (v *View) headerLine(width int, st Style) string {
	g := st.glyphs()
	parts := []string{apply(st.Title, "MARSHAL")}
	var meta []string
	for _, s := range []string{v.header.Provider, v.header.Role, v.header.State} {
		if s = clean(s); s != "" {
			meta = append(meta, s)
		}
	}
	text := "MARSHAL"
	if len(meta) > 0 {
		m := strings.Join(meta, " "+g.sep+" ")
		parts = append(parts, apply(st.Dim, m))
		text += "  " + m
	}
	if v.thinking {
		sp := v.spin(st)
		parts = append(parts, apply(st.Accent, sp+" thinking"))
		text += "  " + sp + " thinking"
	}
	if uniseg.StringWidth(text) > width {
		return fit(text, width)
	}
	return strings.Join(parts, "  ")
}

func (v *View) spin(st Style) string {
	frames := asciiSpinner
	if st.Unicode {
		frames = spinner
	}
	// Advances with the stream, not with a clock: no timer, no idle frames.
	return frames[int(v.version%uint64(len(frames)))]
}

func (v *View) transcript(width int, st Style, g glyphs) []string {
	var out []string
	var prev Role
	for i, e := range v.entries {
		speaker := e.Role
		if speaker == Tool {
			speaker = Marshal
		}
		if speaker == You || speaker == Marshal {
			if i == 0 || prev != speaker {
				if len(out) > 0 {
					out = append(out, "")
				}
				name := string(speaker)
				label := apply(st.You, name)
				if speaker == Marshal {
					label = apply(st.Marshal, name)
				}
				head := label
				if !e.At.IsZero() {
					head += " " + apply(st.Dim, e.At.Format("15:04"))
				}
				if e.Streaming {
					head += "  " + apply(st.Accent, v.spin(st))
				}
				out = append(out, head)
			}
			prev = speaker
		} else {
			if len(out) > 0 {
				out = append(out, "")
			}
			prev = e.Role
		}
		switch e.Role {
		case You, Marshal:
			if i > 0 && v.entries[i-1].Role == Marshal && e.Role == Marshal && v.entries[i-1].MessageID != e.MessageID {
				out = append(out, "")
			}
			for _, l := range wrap(clean(e.Text), width-2) {
				out = append(out, "  "+l)
			}
		case Tool:
			line := g.tool + " " + clean(e.ToolName)
			if s := clean(e.Text); s != "" {
				line += "  " + s
			}
			out = append(out, apply(st.Tool, fit("  "+line, width)))
			if e.Done {
				mark, f := g.ok, st.Dim
				if e.Failed {
					mark, f = g.fail, st.Fail
				}
				res := mark
				if s := clean(e.Result); s != "" {
					res += " " + s
				}
				out = append(out, apply(f, fit("    "+res, width)))
			}
		case Note:
			for _, l := range wrap(clean(e.Text), width-4) {
				out = append(out, apply(st.Dim, "  "+g.sep+" "+l))
			}
		case Error:
			for _, l := range wrap("error: "+clean(e.Text), width-2) {
				out = append(out, apply(st.Error, "  "+l))
			}
		}
	}
	return out
}

func (v *View) composerLine(width int, st Style, g glyphs) (string, int) {
	prefix := g.prompt + " "
	pw := uniseg.StringWidth(prefix)
	if width <= pw {
		return apply(st.Accent, fit(prefix, width)), width
	}
	if len(v.input) == 0 {
		hint := v.hint
		if hint == "" {
			hint = "message Marshal (Enter send " + g.sep + " Esc Board " + g.sep + " PgUp/PgDn scroll)"
		}
		if !v.focused {
			hint = "m to talk to Marshal"
		}
		return apply(st.Accent, prefix) + apply(st.Dim, fit(hint, max(0, width-pw))), pw + 1
	}
	avail := max(1, width-pw-1)
	// Keep the cursor in view: show the tail that ends at the cursor when
	// the line is longer than the row.
	start := 0
	for uniseg.StringWidth(string(v.input[start:v.cursor])) > avail {
		start++
	}
	visible := string(v.input[start:])
	col := pw + 1 + uniseg.StringWidth(string(v.input[start:v.cursor]))
	return apply(st.Accent, prefix) + fit(visible, width-pw), col
}

var ansi = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)?|\x1b[@-_]?`)

// clean makes provider text safe to paint: escape sequences and control
// characters go, tabs become spaces, newlines stay.
func clean(s string) string {
	s = ansi.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\t", "  ")
	return strings.Map(func(r rune) rune {
		if r == '\n' {
			return r
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == '‎' || r == '‏' || (r >= '‪' && r <= '‮') || (r >= '⁦' && r <= '⁩') {
			return -1
		}
		return r
	}, s)
}

// wrap breaks text into lines of at most width cells at spaces, breaking
// a word only when it is longer than a line.
func wrap(text string, width int) []string {
	width = max(1, width)
	var out []string
	for _, para := range strings.Split(text, "\n") {
		para = strings.TrimRight(para, " ")
		if para == "" {
			out = append(out, "")
			continue
		}
		// Leading indentation is kept: code and lists read as written.
		indent := len(para) - len(strings.TrimLeft(para, " "))
		indent = min(indent, width/2)
		pad := strings.Repeat(" ", indent)
		line, lw := pad, indent
		for _, word := range strings.Fields(para) {
			ww := uniseg.StringWidth(word)
			for ww > width-indent {
				if lw > indent {
					out = append(out, line)
					line, lw = pad, indent
				}
				head := fit(word, width-indent)
				out = append(out, pad+head)
				word = strings.TrimPrefix(word, head)
				ww = uniseg.StringWidth(word)
			}
			if word == "" {
				continue
			}
			sep := 0
			if lw > indent {
				sep = 1
			}
			if lw+sep+ww > width {
				out = append(out, line)
				line, lw, sep = pad, indent, 0
			}
			if sep == 1 {
				line += " "
			}
			line += word
			lw += sep + ww
		}
		if lw > indent {
			out = append(out, line)
		}
	}
	// A trailing blank paragraph adds nothing.
	for len(out) > 1 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// fit cuts s (plain text) to at most width cells, whole graphemes only.
func fit(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if uniseg.StringWidth(s) <= width {
		return s
	}
	var b strings.Builder
	w := 0
	g := uniseg.NewGraphemes(s)
	for g.Next() {
		cw := g.Width()
		if w+cw > width {
			break
		}
		b.WriteString(g.Str())
		w += cw
	}
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

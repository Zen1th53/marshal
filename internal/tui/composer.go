package tui

import (
	"fmt"
	"strings"
	"unicode"
)

// ComposerState tracks the dynamic mode and state of the prompt.
type ComposerPromptInfo struct {
	Project string
	Mode    string
	State   string
	Agent   string // optional, e.g. "@codex" if directing message
}

// Composer implements a full-featured line editor with history and search.
type Composer struct {
	theme        *Theme
	buffer       []rune
	cursor       int
	history      []string
	historyIndex int    // -1 = new input, 0..len-1 = navigating history
	savedBuffer  []rune // temporary store when browsing history

	// Search mode (Ctrl+R)
	searchMode    bool
	searchQuery   []rune
	searchMatches []int
	searchIndex   int

	promptInfo ComposerPromptInfo
}

// NewComposer creates a new Composer with the given theme.
func NewComposer(th *Theme) *Composer {
	if th == nil {
		th = NewTheme(ThemeDefault, true, true)
	}
	return &Composer{
		theme:        th,
		buffer:       make([]rune, 0),
		cursor:       0,
		history:      make([]string, 0),
		historyIndex: -1,
		promptInfo: ComposerPromptInfo{
			Project: "marshal",
			Mode:    "ULTRA",
			State:   "READY",
		},
	}
}

// SetPrompt updates the prompt's dynamic properties.
func (c *Composer) SetPrompt(info ComposerPromptInfo) {
	if info.Project != "" {
		c.promptInfo.Project = info.Project
	}
	if info.Mode != "" {
		c.promptInfo.Mode = info.Mode
	}
	if info.State != "" {
		c.promptInfo.State = info.State
	}
	c.promptInfo.Agent = info.Agent
}

// PromptString returns the composer's input marker.
//
// The marker is deliberately one short line with no embedded newline. Project,
// mode and runtime state live in the persistent statusline; repeating them on
// every keystroke both duplicated context and, because the marker spanned two
// lines while the redraw cleared only one, appended a fresh banner to
// scrollback on every character typed.
//
// When the operator is addressing a specific participant the marker names that
// participant, since that is state the statusline does not carry.
func (c *Composer) PromptString() string {
	if c.promptInfo.Agent != "" {
		return fmt.Sprintf("%s %s ",
			c.theme.Colorize(c.theme.Active, "@"+c.promptInfo.Agent),
			c.theme.Colorize(c.theme.Marshal, PromptMarker))
	}
	return c.theme.Colorize(c.theme.Marshal, PromptMarker) + " "
}

// PromptMarker is the composer's input glyph.
const PromptMarker = "❯"

// PromptVisibleWidth is the printed width of the plain marker, used to place the
// hardware cursor at the right column within the input line.
func (c *Composer) PromptVisibleWidth() int {
	return VisibleLen(StripANSI(c.PromptString()))
}

// Text returns the current buffer as a string.
func (c *Composer) Text() string {
	return string(c.buffer)
}

// SetText sets the buffer and places cursor at the end.
func (c *Composer) SetText(text string) {
	c.buffer = []rune(text)
	c.cursor = len(c.buffer)
}

// SetCursor places the cursor, snapping it to the nearest grapheme boundary at
// or before the requested index so callers computing an offset by other means
// can never leave it inside a character.
func (c *Composer) SetCursor(pos int) {
	if pos < 0 {
		pos = 0
	}
	if pos > len(c.buffer) {
		pos = len(c.buffer)
	}
	c.cursor = SnapToGraphemeBoundary(c.buffer, pos)
}

// CursorPos returns the current cursor index (rune index).
func (c *Composer) CursorPos() int {
	return c.cursor
}

// CursorVisibleWidth returns the printable column width of the text before the cursor.
func (c *Composer) CursorVisibleWidth() int {
	if c.cursor <= 0 {
		return 0
	}
	if c.cursor > len(c.buffer) {
		return VisibleLen(string(c.buffer))
	}
	return VisibleLen(string(c.buffer[:c.cursor]))
}

// HandleKey processes a parsed key and updates the buffer/cursor.
// Returns (submitted string, wasSubmitted bool)
func (c *Composer) HandleKey(k KeyEvent) (string, bool) {
	if c.searchMode {
		return c.handleSearchKey(k)
	}

	switch k.Type {
	case KeyEnter:
		text := strings.TrimSpace(string(c.buffer))
		if text != "" {
			c.AddHistory(text)
		}
		c.buffer = make([]rune, 0)
		c.cursor = 0
		c.historyIndex = -1
		c.savedBuffer = nil
		return text, true

	case KeyRune:
		c.insertRune(k.Rune)
		return "", false

	case KeyBackspace:
		c.deleteBefore()
		return "", false

	case KeyDelete:
		c.deleteAt()
		return "", false

	case KeyLeft:
		// Move by a whole user-visible character. Stepping one rune would land
		// the cursor inside a flag, a skin-tone sequence or a ZWJ family.
		c.cursor = PrevGraphemeStart(c.buffer, c.cursor)
		return "", false

	case KeyRight:
		c.cursor = NextGraphemeStart(c.buffer, c.cursor)
		return "", false

	case KeyHome, KeyCtrlA:
		c.cursor = 0
		return "", false

	case KeyEnd, KeyCtrlE:
		c.cursor = len(c.buffer)
		return "", false

	case KeyCtrlW:
		c.deleteWordBefore()
		return "", false

	case KeyWordLeft:
		c.wordLeft()
		return "", false

	case KeyWordRight:
		c.wordRight()
		return "", false

	case KeyWordDeleteAfter:
		c.deleteWordAfter()
		return "", false

	case KeyCtrlJ:
		c.insertRune('\n')
		return "", false

	case KeyCtrlU:
		c.buffer = c.buffer[c.cursor:]
		c.cursor = 0
		return "", false

	case KeyCtrlK:
		c.buffer = c.buffer[:c.cursor]
		return "", false

	case KeyPaste:
		normalized := strings.ReplaceAll(k.Paste, "\r\n", "\n")
		normalized = strings.ReplaceAll(normalized, "\r", "\n")
		for _, r := range normalized {
			c.insertRune(r)
		}
		return "", false

	case KeyUp:
		if !c.lineUp() {
			c.historyPrev()
		}
		return "", false

	case KeyDown:
		if !c.lineDown() {
			c.historyNext()
		}
		return "", false

	case KeyCtrlR:
		c.searchMode = true
		c.searchQuery = make([]rune, 0)
		c.searchIndex = 0
		c.updateSearchMatches()
		return "", false

	case KeyEsc:
		// Esc dismisses whatever is layered above the composer; it does not
		// touch the draft. The workspace closes an open popup or overlay before
		// the key reaches here, so by this point there is nothing left to
		// dismiss and the buffer must survive untouched. Clearing it here made
		// closing a completion list destroy work the operator had typed.
		//
		// Ctrl+U remains the explicit "discard this line" key.
		return "", false
	}

	return "", false
}

func (c *Composer) insertRune(r rune) {
	if c.cursor >= len(c.buffer) {
		c.buffer = append(c.buffer, r)
		c.cursor = len(c.buffer)
		return
	}
	// Insert at cursor
	c.buffer = append(c.buffer[:c.cursor], append([]rune{r}, c.buffer[c.cursor:]...)...)
	c.cursor++
}

// deleteBefore removes the whole grapheme preceding the cursor. Deleting a
// single rune would strip a combining mark from its base, halve a regional
// indicator pair, or leave a dangling zero-width joiner.
func (c *Composer) deleteBefore() {
	if c.cursor <= 0 || len(c.buffer) == 0 {
		return
	}
	start := PrevGraphemeStart(c.buffer, c.cursor)
	c.buffer = append(c.buffer[:start], c.buffer[c.cursor:]...)
	c.cursor = start
}

// deleteAt removes the whole grapheme at the cursor.
func (c *Composer) deleteAt() {
	if c.cursor >= len(c.buffer) {
		return
	}
	end := NextGraphemeStart(c.buffer, c.cursor)
	c.buffer = append(c.buffer[:c.cursor], c.buffer[end:]...)
}

func (c *Composer) deleteWordBefore() {
	if c.cursor == 0 {
		return
	}
	// Skip spaces backwards
	idx := c.cursor
	for idx > 0 && unicode.IsSpace(c.buffer[idx-1]) {
		idx--
	}
	// Skip non-spaces backwards
	for idx > 0 && !unicode.IsSpace(c.buffer[idx-1]) {
		idx--
	}
	c.buffer = append(c.buffer[:idx], c.buffer[c.cursor:]...)
	c.cursor = idx
}

func (c *Composer) wordLeft() {
	if c.cursor == 0 {
		return
	}
	idx := c.cursor
	for idx > 0 && unicode.IsSpace(c.buffer[idx-1]) {
		idx--
	}
	for idx > 0 && !unicode.IsSpace(c.buffer[idx-1]) {
		idx--
	}
	c.cursor = idx
}

func (c *Composer) wordRight() {
	if c.cursor >= len(c.buffer) {
		return
	}
	idx := c.cursor
	for idx < len(c.buffer) && unicode.IsSpace(c.buffer[idx]) {
		idx++
	}
	for idx < len(c.buffer) && !unicode.IsSpace(c.buffer[idx]) {
		idx++
	}
	c.cursor = idx
}

func (c *Composer) deleteWordAfter() {
	if c.cursor >= len(c.buffer) {
		return
	}
	idx := c.cursor
	for idx < len(c.buffer) && unicode.IsSpace(c.buffer[idx]) {
		idx++
	}
	for idx < len(c.buffer) && !unicode.IsSpace(c.buffer[idx]) {
		idx++
	}
	c.buffer = append(c.buffer[:c.cursor], c.buffer[idx:]...)
}

func (c *Composer) lineUp() bool {
	lines := strings.Split(string(c.buffer), "\n")
	if len(lines) <= 1 {
		return false
	}
	lineIdx, col := c.CursorPosition()
	if lineIdx == 0 {
		return false
	}
	targetLine := lineIdx - 1
	targetCol := col - c.PromptVisibleWidth()
	if targetCol < 0 {
		targetCol = 0
	}
	idx := 0
	for i := 0; i < targetLine; i++ {
		idx += len([]rune(lines[i])) + 1
	}
	prevRunes := []rune(lines[targetLine])
	currW := 0
	targetRuneIdx := 0
	for i, r := range prevRunes {
		w := RuneWidth(r)
		if currW+w > targetCol {
			break
		}
		currW += w
		targetRuneIdx = i + 1
	}
	c.cursor = idx + targetRuneIdx
	return true
}

func (c *Composer) lineDown() bool {
	lines := strings.Split(string(c.buffer), "\n")
	if len(lines) <= 1 {
		return false
	}
	lineIdx, col := c.CursorPosition()
	if lineIdx >= len(lines)-1 {
		return false
	}
	targetLine := lineIdx + 1
	targetCol := col - c.PromptVisibleWidth()
	if targetCol < 0 {
		targetCol = 0
	}
	idx := 0
	for i := 0; i < targetLine; i++ {
		idx += len([]rune(lines[i])) + 1
	}
	nextRunes := []rune(lines[targetLine])
	currW := 0
	targetRuneIdx := 0
	for i, r := range nextRunes {
		w := RuneWidth(r)
		if currW+w > targetCol {
			break
		}
		currW += w
		targetRuneIdx = i + 1
	}
	c.cursor = idx + targetRuneIdx
	return true
}

// AddHistory appends a command to history if not duplicate of the last entry.
func (c *Composer) AddHistory(cmd string) {
	if cmd == "" {
		return
	}
	if len(c.history) > 0 && c.history[len(c.history)-1] == cmd {
		return
	}
	c.history = append(c.history, cmd)
	if len(c.history) > 500 {
		c.history = c.history[len(c.history)-500:]
	}
}

func (c *Composer) historyPrev() {
	if len(c.history) == 0 {
		return
	}
	if c.historyIndex == -1 {
		// Save current editing buffer
		c.savedBuffer = make([]rune, len(c.buffer))
		copy(c.savedBuffer, c.buffer)
		c.historyIndex = len(c.history) - 1
	} else if c.historyIndex > 0 {
		c.historyIndex--
	}

	c.buffer = []rune(c.history[c.historyIndex])
	c.cursor = len(c.buffer)
}

func (c *Composer) historyNext() {
	if c.historyIndex == -1 {
		return
	}
	if c.historyIndex < len(c.history)-1 {
		c.historyIndex++
		c.buffer = []rune(c.history[c.historyIndex])
		c.cursor = len(c.buffer)
	} else {
		// Return to saved buffer
		c.historyIndex = -1
		c.buffer = make([]rune, len(c.savedBuffer))
		copy(c.buffer, c.savedBuffer)
		c.cursor = len(c.buffer)
	}
}

// Search handling (Ctrl+R)
func (c *Composer) handleSearchKey(k KeyEvent) (string, bool) {
	switch k.Type {
	case KeyEsc:
		c.searchMode = false
		return "", false

	case KeyEnter:
		c.searchMode = false
		if len(c.searchMatches) > 0 && c.searchIndex < len(c.searchMatches) {
			matched := c.history[c.searchMatches[c.searchIndex]]
			c.buffer = []rune(matched)
			c.cursor = len(c.buffer)
		}
		return "", false

	case KeyCtrlR:
		// Cycle to next match backwards
		if len(c.searchMatches) > 1 {
			c.searchIndex = (c.searchIndex + 1) % len(c.searchMatches)
			matched := c.history[c.searchMatches[c.searchIndex]]
			c.buffer = []rune(matched)
			c.cursor = len(c.buffer)
		}
		return "", false

	case KeyBackspace:
		if len(c.searchQuery) > 0 {
			c.searchQuery = c.searchQuery[:len(c.searchQuery)-1]
			c.updateSearchMatches()
		}
		return "", false

	case KeyRune:
		c.searchQuery = append(c.searchQuery, k.Rune)
		c.updateSearchMatches()
		return "", false
	}

	return "", false
}

func (c *Composer) updateSearchMatches() {
	c.searchMatches = nil
	c.searchIndex = 0
	query := strings.ToLower(string(c.searchQuery))
	if query == "" {
		return
	}
	// Search backwards through history
	for i := len(c.history) - 1; i >= 0; i-- {
		if strings.Contains(strings.ToLower(c.history[i]), query) {
			c.searchMatches = append(c.searchMatches, i)
		}
	}
	if len(c.searchMatches) > 0 {
		c.buffer = []rune(c.history[c.searchMatches[0]])
		c.cursor = len(c.buffer)
	}
}

// CursorPosition returns (lineIndex, colIndex) relative to the composer.
// lineIndex is 0-indexed line offset within multiline buffer.
// colIndex is 0-indexed column offset (printable columns, including prompt width).
func (c *Composer) CursorPosition() (int, int) {
	if c.searchMode {
		return 0, VisibleLen(c.Render())
	}
	if c.cursor <= 0 {
		return 0, c.PromptVisibleWidth()
	}
	sub := c.buffer
	if c.cursor < len(c.buffer) {
		sub = c.buffer[:c.cursor]
	}

	lineIdx := 0
	lastNewline := -1
	for i, r := range sub {
		if r == '\n' {
			lineIdx++
			lastNewline = i
		}
	}

	var lineBeforeCursor string
	if lastNewline == -1 {
		lineBeforeCursor = string(sub)
	} else {
		lineBeforeCursor = string(sub[lastNewline+1:])
	}

	promptW := c.PromptVisibleWidth()
	col := promptW + VisibleLen(lineBeforeCursor)
	return lineIdx, col
}

// RenderLines returns the composer lines to be output to the terminal.
func (c *Composer) RenderLines() []string {
	prompt := c.PromptString()
	if c.searchMode {
		query := string(c.searchQuery)
		matchCount := len(c.searchMatches)
		var currentMatch string
		if matchCount > 0 {
			currentMatch = c.history[c.searchMatches[c.searchIndex]]
		}
		return []string{fmt.Sprintf("(bck-i-search)`%s' [%d matches]: %s", query, matchCount, currentMatch)}
	}

	fullText := string(c.buffer)
	lines := strings.Split(fullText, "\n")
	if len(lines) <= 1 {
		return []string{prompt + fullText}
	}

	continuationPrompt := strings.Repeat(" ", c.PromptVisibleWidth())
	result := make([]string, len(lines))
	for i, line := range lines {
		if i == 0 {
			result[i] = prompt + line
		} else {
			result[i] = continuationPrompt + line
		}
	}
	return result
}

// Render returns the complete composer lines to be output to the terminal.
func (c *Composer) Render() string {
	return strings.Join(c.RenderLines(), "\n")
}

// IsSearchMode returns true if in Ctrl+R search mode.
func (c *Composer) IsSearchMode() bool {
	return c.searchMode
}

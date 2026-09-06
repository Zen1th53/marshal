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

// PromptString returns the formatted prompt prefix.
func (c *Composer) PromptString() string {
	proj := c.promptInfo.Project
	mode := c.promptInfo.Mode
	state := c.promptInfo.State

	var brand string
	if c.promptInfo.Agent != "" {
		brand = fmt.Sprintf("[%s]-[%s]", c.theme.Colorize(c.theme.Marshal, "MARSHAL"), c.promptInfo.Agent)
	} else {
		brand = fmt.Sprintf("[%s]-[%s]-[%s|%s]",
			c.theme.Colorize(c.theme.Marshal, "MARSHAL"),
			proj,
			c.theme.Colorize(c.theme.Ultra, mode),
			c.theme.Colorize(c.theme.Success, state),
		)
	}
	return fmt.Sprintf("%s\n>>> ", brand)
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

// CursorPos returns the current cursor index (rune index).
func (c *Composer) CursorPos() int {
	return c.cursor
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
		if c.cursor > 0 {
			c.cursor--
		}
		return "", false

	case KeyRight:
		if c.cursor < len(c.buffer) {
			c.cursor++
		}
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

	case KeyCtrlU:
		c.buffer = c.buffer[c.cursor:]
		c.cursor = 0
		return "", false

	case KeyCtrlK:
		c.buffer = c.buffer[:c.cursor]
		return "", false

	case KeyPaste:
		for _, r := range k.Paste {
			if r == '\n' || r == '\r' {
				continue
			}
			c.insertRune(r)
		}
		return "", false

	case KeyUp:
		c.historyPrev()
		return "", false

	case KeyDown:
		c.historyNext()
		return "", false

	case KeyCtrlR:
		c.searchMode = true
		c.searchQuery = make([]rune, 0)
		c.searchIndex = 0
		c.updateSearchMatches()
		return "", false

	case KeyEsc:
		// Clear buffer on escape
		if len(c.buffer) > 0 {
			c.buffer = make([]rune, 0)
			c.cursor = 0
			c.historyIndex = -1
		}
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

func (c *Composer) deleteBefore() {
	if c.cursor > 0 && len(c.buffer) > 0 {
		c.buffer = append(c.buffer[:c.cursor-1], c.buffer[c.cursor:]...)
		c.cursor--
	}
}

func (c *Composer) deleteAt() {
	if c.cursor < len(c.buffer) {
		c.buffer = append(c.buffer[:c.cursor], c.buffer[c.cursor+1:]...)
	}
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

// Render returns the complete composer lines to be output to the terminal.
func (c *Composer) Render() string {
	prompt := c.PromptString()
	if c.searchMode {
		query := string(c.searchQuery)
		matchCount := len(c.searchMatches)
		var currentMatch string
		if matchCount > 0 {
			currentMatch = c.history[c.searchMatches[c.searchIndex]]
		}
		return fmt.Sprintf("(bck-i-search)`%s' [%d matches]: %s", query, matchCount, currentMatch)
	}

	beforeCursor := string(c.buffer[:c.cursor])
	afterCursor := string(c.buffer[c.cursor:])
	return fmt.Sprintf("%s%s%s", prompt, beforeCursor, afterCursor)
}

// IsSearchMode returns true if in Ctrl+R search mode.
func (c *Composer) IsSearchMode() bool {
	return c.searchMode
}

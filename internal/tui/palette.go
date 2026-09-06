package tui

import (
	"fmt"
	"strings"
)

// PaletteAction represents a discoverable action in the Command Palette.
type PaletteAction struct {
	ID          string // e.g. "goal.edit"
	Category    string // e.g. "GOAL", "TEAM", "POLICY", "SYSTEM"
	Title       string // e.g. "Edit Goal & Objectives"
	Description string // e.g. "Modify active goal objective and constraints"
	Command     string // e.g. "/goal edit"
	Shortcut    string // e.g. "g" or empty
	Disabled    bool
	Reason      string // reason if disabled
}

// CommandPalette manages the Ctrl+P command palette modal.
type CommandPalette struct {
	theme    *Theme
	active   bool
	query    []rune
	actions  []PaletteAction
	filtered []PaletteAction
	selected int
}

// NewCommandPalette creates a new CommandPalette with actions.
func NewCommandPalette(th *Theme, actions []PaletteAction) *CommandPalette {
	if th == nil {
		th = NewTheme(ThemeDefault, true, true)
	}
	cp := &CommandPalette{
		theme:   th,
		actions: actions,
	}
	cp.filter()
	return cp
}

// SetActions updates the full list of actions.
func (cp *CommandPalette) SetActions(actions []PaletteAction) {
	cp.actions = actions
	cp.filter()
}

// IsOpen returns true if the palette is currently visible.
func (cp *CommandPalette) IsOpen() bool {
	return cp.active
}

// Open activates the command palette and resets query.
func (cp *CommandPalette) Open() {
	cp.active = true
	cp.query = make([]rune, 0)
	cp.selected = 0
	cp.filter()
}

// Close dismisses the command palette.
func (cp *CommandPalette) Close() {
	cp.active = false
	cp.query = nil
	cp.selected = 0
}

// Toggle toggles the command palette.
func (cp *CommandPalette) Toggle() {
	if cp.active {
		cp.Close()
	} else {
		cp.Open()
	}
}

// HandleKey processes input when the palette is active.
// Returns (selectedAction, executed bool)
func (cp *CommandPalette) HandleKey(k KeyEvent) (*PaletteAction, bool) {
	if !cp.active {
		return nil, false
	}

	switch k.Type {
	case KeyEsc, KeyCtrlP:
		cp.Close()
		return nil, false

	case KeyEnter:
		if len(cp.filtered) > 0 && cp.selected >= 0 && cp.selected < len(cp.filtered) {
			action := cp.filtered[cp.selected]
			if !action.Disabled {
				cp.Close()
				return &action, true
			}
		}
		return nil, false

	case KeyUp:
		if cp.selected > 0 {
			cp.selected--
		} else if len(cp.filtered) > 0 {
			cp.selected = len(cp.filtered) - 1
		}
		return nil, false

	case KeyDown:
		if cp.selected < len(cp.filtered)-1 {
			cp.selected++
		} else {
			cp.selected = 0
		}
		return nil, false

	case KeyBackspace:
		if len(cp.query) > 0 {
			cp.query = cp.query[:len(cp.query)-1]
			cp.filter()
		}
		return nil, false

	case KeyRune:
		cp.query = append(cp.query, k.Rune)
		cp.filter()
		return nil, false
	}

	return nil, false
}

func (cp *CommandPalette) filter() {
	query := strings.ToLower(strings.TrimSpace(string(cp.query)))
	if query == "" {
		cp.filtered = make([]PaletteAction, len(cp.actions))
		copy(cp.filtered, cp.actions)
	} else {
		cp.filtered = nil
		for _, a := range cp.actions {
			target := strings.ToLower(fmt.Sprintf("%s %s %s %s", a.Category, a.Title, a.Description, a.Command))
			if strings.Contains(target, query) {
				cp.filtered = append(cp.filtered, a)
			}
		}
	}

	if cp.selected >= len(cp.filtered) {
		if len(cp.filtered) > 0 {
			cp.selected = len(cp.filtered) - 1
		} else {
			cp.selected = 0
		}
	}
}

// Render renders the Command Palette box for display in the terminal.
// width and height constrain the maximum dimensions.
func (cp *CommandPalette) Render(width, height int) []string {
	if !cp.active {
		return nil
	}

	boxWidth := width - 8
	if boxWidth > 76 {
		boxWidth = 76
	}
	if boxWidth < 40 {
		boxWidth = width
	}

	var lines []string
	// Header: ╭─ Command Palette ─────────────────────────────────╮
	title := " Command Palette "
	remainingWidth := boxWidth - VisibleLen(title) - 3
	if remainingWidth < 0 {
		remainingWidth = 0
	}
	header := fmt.Sprintf("%s%s%s%s%s",
		cp.theme.BoxTopLeft,
		cp.theme.BoxHoriz,
		cp.theme.Colorize(cp.theme.Marshal, title),
		strings.Repeat(cp.theme.BoxHoriz, remainingWidth),
		cp.theme.BoxTopRight,
	)
	lines = append(lines, header)

	// Search input line: │ > query_                                             │
	searchPrompt := fmt.Sprintf("> %s_", string(cp.query))
	searchLine := fmt.Sprintf("%s %s%s",
		cp.theme.BoxVert,
		PadCell(searchPrompt, boxWidth-3),
		cp.theme.BoxVert,
	)
	lines = append(lines, searchLine)

	// Separator: ├───────────────────────────────────────────────────────┤
	sep := fmt.Sprintf("%s%s%s",
		cp.theme.BoxTRight,
		strings.Repeat(cp.theme.BoxHoriz, boxWidth-2),
		cp.theme.BoxTLeft,
	)
	lines = append(lines, sep)

	// Action items (limit to 10 lines)
	maxItems := 10
	if height > 0 && maxItems > height-6 {
		maxItems = height - 6
	}
	if maxItems < 3 {
		maxItems = 3
	}

	start := 0
	if cp.selected >= maxItems {
		start = cp.selected - maxItems + 1
	}
	end := start + maxItems
	if end > len(cp.filtered) {
		end = len(cp.filtered)
	}

	if len(cp.filtered) == 0 {
		emptyMsg := "  (No matching actions)"
		lines = append(lines, fmt.Sprintf("%s %s%s",
			cp.theme.BoxVert,
			PadCell(emptyMsg, boxWidth-3),
			cp.theme.BoxVert,
		))
	} else {
		for i := start; i < end; i++ {
			act := cp.filtered[i]
			isSel := i == cp.selected

			catBadge := fmt.Sprintf("[%s]", act.Category)
			title := act.Title
			cmd := act.Command

			// Every row is the same visible width: a two-column marker followed
			// by rowText. Padding rowText to a width that already excludes the
			// marker keeps the selected and unselected rows identical, so the
			// right edge stays straight.
			contentWidth := boxWidth - 4
			rowWidth := contentWidth - 2

			leftPart := PadCell(catBadge, 10) + " " + title
			cmdWidth := VisibleLen(cmd)
			if VisibleLen(leftPart)+cmdWidth+2 > rowWidth {
				leftPart = PadCell(leftPart, rowWidth-cmdWidth-2)
			}
			rowText := PadCell(leftPart, rowWidth-cmdWidth-2) + "  " + cmd
			rowText = PadCell(rowText, rowWidth)

			var lineContent string
			if isSel {
				lineContent = cp.theme.Colorize(cp.theme.Reverse, "▶ "+rowText)
			} else if act.Disabled {
				lineContent = "  " + cp.theme.Colorize(cp.theme.Muted, rowText)
			} else {
				lineContent = "  " + rowText
			}

			lines = append(lines, fmt.Sprintf("%s %s%s",
				cp.theme.BoxVert,
				PadCell(lineContent, boxWidth-3),
				cp.theme.BoxVert,
			))
		}
	}

	// Footer: ╰─ [Enter] Execute  [↑/↓] Navigate  [Esc] Dismiss ─╯
	footerText := " [Enter] Run  [↑/↓] Select  [Esc] Close "
	footerPad := boxWidth - VisibleLen(footerText) - 3
	if footerPad < 0 {
		footerPad = 0
	}
	footer := fmt.Sprintf("%s%s%s%s%s",
		cp.theme.BoxBottomLeft,
		cp.theme.BoxHoriz,
		cp.theme.Colorize(cp.theme.Muted, footerText),
		strings.Repeat(cp.theme.BoxHoriz, footerPad),
		cp.theme.BoxBottomRight,
	)
	lines = append(lines, footer)

	return lines
}

package tui

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// DiffFile represents changes to a single file in the diff.
type DiffFile struct {
	Path      string
	Additions int
	Deletions int
	Hunks     []string
}

// DiffViewer provides interactive navigation over git diffs with syntax coloring.
type DiffViewer struct {
	theme        *Theme
	workDir      string
	active       bool
	files        []DiffFile
	currentFile  int
	currentHunk  int
	rawDiff      string
	knownSecrets []string
}

// NewDiffViewer creates a new DiffViewer instance for workDir.
func NewDiffViewer(th *Theme, workDir string) *DiffViewer {
	if th == nil {
		th = NewTheme(ThemeDefault, true, true)
	}
	return &DiffViewer{
		theme:   th,
		workDir: workDir,
	}
}

// SetSecrets sets sensitive strings to be redacted in diff output.
func (dv *DiffViewer) SetSecrets(secrets []string) {
	dv.knownSecrets = secrets
}

// IsOpen returns true if the diff viewer is currently active.
func (dv *DiffViewer) IsOpen() bool {
	return dv.active
}

// Open loads the latest git diff from the working directory and opens the viewer.
func (dv *DiffViewer) Open() error {
	dv.active = true
	return dv.Refresh()
}

// Close closes the diff viewer.
func (dv *DiffViewer) Close() {
	dv.active = false
}

// Toggle toggles the diff viewer.
func (dv *DiffViewer) Toggle() error {
	if dv.active {
		dv.Close()
		return nil
	}
	return dv.Open()
}

// Refresh re-runs git diff to update files and hunks.
func (dv *DiffViewer) Refresh() error {
	cmd := exec.Command("git", "diff")
	if dv.workDir != "" {
		cmd.Dir = dv.workDir
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		// Even if error or no git repo, don't crash, set empty
		dv.rawDiff = ""
		dv.files = nil
		return err
	}

	dv.rawDiff = out.String()
	dv.parseDiff(dv.rawDiff)
	return nil
}

// LoadRawDiff parses a raw diff string directly (useful for checkpoints or testing).
func (dv *DiffViewer) LoadRawDiff(raw string) {
	dv.rawDiff = raw
	dv.parseDiff(raw)
}

func (dv *DiffViewer) parseDiff(raw string) {
	dv.files = nil
	dv.currentFile = 0
	dv.currentHunk = 0

	if strings.TrimSpace(raw) == "" {
		return
	}

	lines := strings.Split(raw, "\n")
	var currentFile *DiffFile
	var currentHunkLines []string

	flushHunk := func() {
		if currentFile != nil && len(currentHunkLines) > 0 {
			currentFile.Hunks = append(currentFile.Hunks, strings.Join(currentHunkLines, "\n"))
			currentHunkLines = nil
		}
	}

	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git ") {
			flushHunk()
			parts := strings.Fields(line)
			path := ""
			if len(parts) >= 4 {
				path = strings.TrimPrefix(parts[3], "b/")
			}
			dv.files = append(dv.files, DiffFile{Path: path})
			currentFile = &dv.files[len(dv.files)-1]
		} else if strings.HasPrefix(line, "@@") {
			flushHunk()
			currentHunkLines = append(currentHunkLines, line)
		} else if currentFile != nil {
			if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
				currentFile.Additions++
				currentHunkLines = append(currentHunkLines, line)
			} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
				currentFile.Deletions++
				currentHunkLines = append(currentHunkLines, line)
			} else if len(currentHunkLines) > 0 {
				currentHunkLines = append(currentHunkLines, line)
			}
		}
	}
	flushHunk()
}

// HandleKey processes keyboard input when the diff viewer is active.
func (dv *DiffViewer) HandleKey(k KeyEvent) bool {
	if !dv.active {
		return false
	}

	switch k.Type {
	case KeyEsc:
		dv.Close()
		return true

	case KeyRune:
		switch k.Rune {
		case 'q', 'd':
			dv.Close()
			return true
		case 'n':
			dv.nextHunk()
			return true
		case 'p':
			dv.prevHunk()
			return true
		case 'j':
			dv.nextFile()
			return true
		case 'k':
			dv.prevFile()
			return true
		}

	case KeyDown:
		dv.nextHunk()
		return true

	case KeyUp:
		dv.prevHunk()
		return true

	case KeyRight:
		dv.nextFile()
		return true

	case KeyLeft:
		dv.prevFile()
		return true
	}

	return false
}

func (dv *DiffViewer) nextFile() {
	if len(dv.files) > 0 {
		dv.currentFile = (dv.currentFile + 1) % len(dv.files)
		dv.currentHunk = 0
	}
}

func (dv *DiffViewer) prevFile() {
	if len(dv.files) > 0 {
		dv.currentFile--
		if dv.currentFile < 0 {
			dv.currentFile = len(dv.files) - 1
		}
		dv.currentHunk = 0
	}
}

func (dv *DiffViewer) nextHunk() {
	if len(dv.files) == 0 {
		return
	}
	file := dv.files[dv.currentFile]
	if dv.currentHunk < len(file.Hunks)-1 {
		dv.currentHunk++
	} else if dv.currentFile < len(dv.files)-1 {
		dv.currentFile++
		dv.currentHunk = 0
	}
}

func (dv *DiffViewer) prevHunk() {
	if len(dv.files) == 0 {
		return
	}
	if dv.currentHunk > 0 {
		dv.currentHunk--
	} else if dv.currentFile > 0 {
		dv.currentFile--
		dv.currentHunk = len(dv.files[dv.currentFile].Hunks) - 1
		if dv.currentHunk < 0 {
			dv.currentHunk = 0
		}
	}
}

// Render returns styled lines for rendering the diff viewer modal in the terminal.
func (dv *DiffViewer) Render(width, height int) []string {
	if !dv.active {
		return nil
	}

	boxWidth := width - 4
	if boxWidth < 40 {
		boxWidth = 40
	}

	var lines []string

	// Header: ╭─ Git Diff (File X of Y) ─────────────────────────╮
	fileCount := len(dv.files)
	title := " Git Diff "
	if fileCount > 0 {
		title = fmt.Sprintf(" Git Diff [%d/%d files] ", dv.currentFile+1, fileCount)
	}
	remWidth := boxWidth - VisibleLen(title) - 2
	if remWidth < 0 {
		remWidth = 0
	}
	header := fmt.Sprintf("%s%s%s%s%s",
		dv.theme.BoxTopLeft,
		dv.theme.BoxHoriz,
		dv.theme.Colorize(dv.theme.Marshal, title),
		strings.Repeat(dv.theme.BoxHoriz, remWidth),
		dv.theme.BoxTopRight,
	)
	lines = append(lines, header)

	if len(dv.files) == 0 {
		cleanMsg := "  Working tree is clean (no uncommitted changes)"
		lines = append(lines, fmt.Sprintf("%s %s%s",
			dv.theme.BoxVert,
			PadRight(cleanMsg, boxWidth-4),
			dv.theme.BoxVert,
		))
	} else {
		curr := dv.files[dv.currentFile]
		fileSummary := fmt.Sprintf("%s  %s  %s",
			dv.theme.Colorize(dv.theme.Bold, curr.Path),
			dv.theme.Colorize(dv.theme.Success, fmt.Sprintf("+%d", curr.Additions)),
			dv.theme.Colorize(dv.theme.Danger, fmt.Sprintf("-%d", curr.Deletions)),
		)
		lines = append(lines, fmt.Sprintf("%s %s%s",
			dv.theme.BoxVert,
			PadRight(fileSummary, boxWidth-4),
			dv.theme.BoxVert,
		))

		// Separator
		lines = append(lines, fmt.Sprintf("%s%s%s",
			dv.theme.BoxTRight,
			strings.Repeat(dv.theme.BoxHoriz, boxWidth-2),
			dv.theme.BoxTLeft,
		))

		// Render active hunk lines
		if len(curr.Hunks) > 0 && dv.currentHunk < len(curr.Hunks) {
			hunkText := RedactContent(curr.Hunks[dv.currentHunk], dv.knownSecrets)
			hunkLines := strings.Split(hunkText, "\n")
			maxHunkLines := height - 7
			if maxHunkLines < 5 {
				maxHunkLines = 5
			}
			for idx, hl := range hunkLines {
				if idx >= maxHunkLines {
					truncNotice := dv.theme.Colorize(dv.theme.Muted, fmt.Sprintf("... (%d more lines) ...", len(hunkLines)-maxHunkLines))
					lines = append(lines, fmt.Sprintf("%s %s%s",
						dv.theme.BoxVert,
						PadRight(truncNotice, boxWidth-4),
						dv.theme.BoxVert,
					))
					break
				}

				var styledLine string
				if strings.HasPrefix(hl, "+") {
					styledLine = dv.theme.Colorize(dv.theme.Success, hl)
				} else if strings.HasPrefix(hl, "-") {
					styledLine = dv.theme.Colorize(dv.theme.Danger, hl)
				} else if strings.HasPrefix(hl, "@@") {
					styledLine = dv.theme.Colorize(dv.theme.Marshal, hl)
				} else {
					styledLine = hl
				}

				lines = append(lines, fmt.Sprintf("%s %s%s",
					dv.theme.BoxVert,
					PadRight(styledLine, boxWidth-4),
					dv.theme.BoxVert,
				))
			}
		}
	}

	// Footer: ╰─ [n/p] Next/Prev Hunk  [←/→] File  [Esc] Close ───╯
	footerText := " [n/p] Hunk  [←/→] File  [Esc/q] Close "
	footerPad := boxWidth - VisibleLen(footerText) - 2
	if footerPad < 0 {
		footerPad = 0
	}
	footer := fmt.Sprintf("%s%s%s%s%s",
		dv.theme.BoxBottomLeft,
		dv.theme.BoxHoriz,
		dv.theme.Colorize(dv.theme.Muted, footerText),
		strings.Repeat(dv.theme.BoxHoriz, footerPad),
		dv.theme.BoxBottomRight,
	)
	lines = append(lines, footer)

	return lines
}

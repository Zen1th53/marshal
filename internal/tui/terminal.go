package tui

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"unicode/utf8"

	"golang.org/x/term"
)

// KeyType identifies a parsed terminal input event.
type KeyType int

const (
	KeyRune KeyType = iota
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyHome
	KeyEnd
	KeyPgUp
	KeyPgDn
	KeyTab
	KeyShiftTab
	KeyEnter
	KeyEsc
	KeyBackspace
	KeyDelete
	KeyCtrlA
	KeyCtrlB
	KeyCtrlC
	KeyCtrlD
	KeyCtrlE
	KeyCtrlF
	KeyCtrlK
	KeyCtrlN
	KeyCtrlP
	KeyCtrlR
	KeyCtrlU
	KeyCtrlW
	KeyPaste
	KeyUnknown
)

// KeyEvent represents a single keyboard interaction.
type KeyEvent struct {
	Type  KeyType
	Rune  rune
	Paste string
	Raw   []byte
}

// Terminal handles raw mode, window sizing, resize signals, and ANSI escape sequences.
type Terminal struct {
	in         io.Reader
	out        io.Writer
	inFd       int
	outFd      int
	isTerm     bool
	oldState   *term.State
	resizeChan chan struct{}
	readBuf    []byte
}

// NewTerminal creates a terminal wrapper for the given reader and writer.
func NewTerminal(in io.Reader, out io.Writer) *Terminal {
	t := &Terminal{
		in:         in,
		out:        out,
		inFd:       -1,
		outFd:      -1,
		resizeChan: make(chan struct{}, 4),
	}

	if f, ok := in.(*os.File); ok {
		t.inFd = int(f.Fd())
		t.isTerm = term.IsTerminal(t.inFd)
	}
	if f, ok := out.(*os.File); ok {
		t.outFd = int(f.Fd())
	}
	return t
}

// MakeRaw puts the terminal into raw mode if stdin is a terminal.
func (t *Terminal) MakeRaw() error {
	if !t.isTerm || t.inFd < 0 {
		return nil
	}
	state, err := term.MakeRaw(t.inFd)
	if err != nil {
		return err
	}
	t.oldState = state

	// Listen for window resize signals (SIGWINCH)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGWINCH)
	go func() {
		for range sigChan {
			select {
			case t.resizeChan <- struct{}{}:
			default:
			}
		}
	}()
	return nil
}

// Restore restores the terminal to its previous state.
func (t *Terminal) Restore() error {
	if t.oldState != nil && t.inFd >= 0 {
		err := term.Restore(t.inFd, t.oldState)
		t.oldState = nil
		return err
	}
	return nil
}

// ResizeEvents returns a channel triggered when the terminal window changes dimensions.
func (t *Terminal) ResizeEvents() <-chan struct{} {
	return t.resizeChan
}

// IsTerminal reports whether stdin is connected to an interactive terminal.
func (t *Terminal) IsTerminal() bool {
	return t.isTerm
}

// ClearScreen clears the terminal screen and homes the cursor.
func (t *Terminal) ClearScreen() {
	fmt.Fprint(t.out, "\x1b[2J\x1b[H")
}

// ClearLine clears the current line and returns the cursor to the left margin.
func (t *Terminal) ClearLine() {
	fmt.Fprint(t.out, "\x1b[2K\r")
}

// CursorMoveToCol positions the cursor at the specified 1-indexed column.
func (t *Terminal) CursorMoveToCol(col int) {
	fmt.Fprintf(t.out, "\x1b[%dG", col)
}

// CursorTo positions the cursor at a 1-indexed row and column. Absolute
// placement is what lets the workspace repaint a frame in place instead of
// appending each redraw to scrollback.
func (t *Terminal) CursorTo(row, col int) {
	if row < 1 {
		row = 1
	}
	if col < 1 {
		col = 1
	}
	fmt.Fprintf(t.out, "\x1b[%d;%dH", row, col)
}

// ClearToEndOfLine erases from the cursor to the end of the row, leaving
// everything to its left untouched.
func (t *Terminal) ClearToEndOfLine() {
	fmt.Fprint(t.out, "\x1b[K")
}

// EnterAltScreen switches to the alternate screen buffer. The user's scrollback
// is untouched while the workspace runs, and LeaveAltScreen restores it exactly
// as it was, so a session leaves no UI fragments behind in the shell.
func (t *Terminal) EnterAltScreen() {
	fmt.Fprint(t.out, "\x1b[?1049h")
}

// LeaveAltScreen returns to the primary screen buffer.
func (t *Terminal) LeaveAltScreen() {
	fmt.Fprint(t.out, "\x1b[?1049l")
}

// HideCursor hides the terminal text cursor.
func (t *Terminal) HideCursor() {
	fmt.Fprint(t.out, "\x1b[?25l")
}

// ShowCursor restores the terminal text cursor.
func (t *Terminal) ShowCursor() {
	fmt.Fprint(t.out, "\x1b[?25h")
}

// GetSize returns current terminal (width, height). Defaults to (100, 30) if non-interactive.
func (t *Terminal) GetSize() (int, int) {
	if t.outFd >= 0 {
		w, h, err := term.GetSize(t.outFd)
		if err == nil && w > 0 && h > 0 {
			return w, h
		}
	}
	if t.inFd >= 0 {
		w, h, err := term.GetSize(t.inFd)
		if err == nil && w > 0 && h > 0 {
			return w, h
		}
	}
	return 100, 30
}

// Size returns current terminal (width, height).
func (t *Terminal) Size() (int, int) {
	return t.GetSize()
}

// ReadKey reads and parses the next KeyEvent from input, buffering any unconsumed bytes.
func (t *Terminal) ReadKey() (KeyEvent, error) {
	for {
		if len(t.readBuf) > 0 {
			evt, consumed := ParseNextKey(t.readBuf)
			if consumed > 0 {
				t.readBuf = t.readBuf[consumed:]
				return evt, nil
			}
		}

		buf := make([]byte, 256)
		n, err := t.in.Read(buf)
		if err != nil {
			return KeyEvent{}, err
		}
		t.readBuf = append(t.readBuf, buf[:n]...)
	}
}

// ParseNextKey parses the first KeyEvent from byte buffer and returns the number of bytes consumed.
func ParseNextKey(b []byte) (KeyEvent, int) {
	if len(b) == 0 {
		return KeyEvent{Type: KeyUnknown}, 0
	}

	// 1. Bracketed paste: \x1b[200~ ... \x1b[201~
	if bytes.HasPrefix(b, []byte("\x1b[200~")) {
		endIdx := bytes.Index(b, []byte("\x1b[201~"))
		if endIdx != -1 {
			content := b[len("\x1b[200~"):endIdx]
			consumed := endIdx + len("\x1b[201~")
			return KeyEvent{
				Type:  KeyPaste,
				Paste: string(content),
				Raw:   b[:consumed],
			}, consumed
		}
		return KeyEvent{Type: KeyUnknown}, 0
	}

	// 2. Escape sequences
	if b[0] == 0x1b {
		if len(b) == 1 {
			return KeyEvent{Type: KeyEsc, Raw: b[:1]}, 1
		}

		// CSI sequences: \x1b[... or \x1bO...
		if b[1] == '[' || b[1] == 'O' {
			for i := 2; i < len(b); i++ {
				c := b[i]
				if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '~' {
					seq := string(b[2 : i+1])
					consumed := i + 1
					raw := b[:consumed]

					switch seq {
					case "A":
						return KeyEvent{Type: KeyUp, Raw: raw}, consumed
					case "B":
						return KeyEvent{Type: KeyDown, Raw: raw}, consumed
					case "C":
						return KeyEvent{Type: KeyRight, Raw: raw}, consumed
					case "D":
						return KeyEvent{Type: KeyLeft, Raw: raw}, consumed
					case "H", "1~":
						return KeyEvent{Type: KeyHome, Raw: raw}, consumed
					case "F", "4~":
						return KeyEvent{Type: KeyEnd, Raw: raw}, consumed
					case "5~":
						return KeyEvent{Type: KeyPgUp, Raw: raw}, consumed
					case "6~":
						return KeyEvent{Type: KeyPgDn, Raw: raw}, consumed
					case "3~":
						return KeyEvent{Type: KeyDelete, Raw: raw}, consumed
					case "Z":
						return KeyEvent{Type: KeyShiftTab, Raw: raw}, consumed
					default:
						return KeyEvent{Type: KeyUnknown, Raw: raw}, consumed
					}
				}
			}
			return KeyEvent{Type: KeyUnknown}, 0
		}
		return KeyEvent{Type: KeyEsc, Raw: b[:1]}, 1
	}

	// 3. Control keys (1 byte)
	switch b[0] {
	case 0x01:
		return KeyEvent{Type: KeyCtrlA, Raw: b[:1]}, 1
	case 0x02:
		return KeyEvent{Type: KeyCtrlB, Raw: b[:1]}, 1
	case 0x03:
		return KeyEvent{Type: KeyCtrlC, Raw: b[:1]}, 1
	case 0x04:
		return KeyEvent{Type: KeyCtrlD, Raw: b[:1]}, 1
	case 0x05:
		return KeyEvent{Type: KeyCtrlE, Raw: b[:1]}, 1
	case 0x06:
		return KeyEvent{Type: KeyCtrlF, Raw: b[:1]}, 1
	case 0x09:
		return KeyEvent{Type: KeyTab, Raw: b[:1]}, 1
	case 0x0B:
		return KeyEvent{Type: KeyCtrlK, Raw: b[:1]}, 1
	case 0x0D, 0x0A:
		return KeyEvent{Type: KeyEnter, Raw: b[:1]}, 1
	case 0x0E:
		return KeyEvent{Type: KeyCtrlN, Raw: b[:1]}, 1
	case 0x10:
		return KeyEvent{Type: KeyCtrlP, Raw: b[:1]}, 1
	case 0x12:
		return KeyEvent{Type: KeyCtrlR, Raw: b[:1]}, 1
	case 0x15:
		return KeyEvent{Type: KeyCtrlU, Raw: b[:1]}, 1
	case 0x17:
		return KeyEvent{Type: KeyCtrlW, Raw: b[:1]}, 1
	case 0x7F, 0x08:
		return KeyEvent{Type: KeyBackspace, Raw: b[:1]}, 1
	}

	// 4. Unicode Runes
	r, size := utf8.DecodeRune(b)
	if r != utf8.RuneError {
		return KeyEvent{Type: KeyRune, Rune: r, Raw: b[:size]}, size
	}

	return KeyEvent{Type: KeyUnknown, Raw: b[:1]}, 1
}

// ParseKey parses a byte sequence (for single-key tests).
func ParseKey(b []byte) KeyEvent {
	k, _ := ParseNextKey(b)
	return k
}

// ClearScreenSeq returns the ANSI sequence to clear the screen and move to home.
func ClearScreenSeq() string {
	return "\x1b[2J\x1b[H"
}

// HideCursorSeq returns ANSI sequence to hide cursor.
func HideCursorSeq() string {
	return "\x1b[?25l"
}

// ShowCursorSeq returns ANSI sequence to show cursor.
func ShowCursorSeq() string {
	return "\x1b[?25h"
}

// MoveCursorSeq returns ANSI sequence to move cursor to (row, col) (1-indexed).
func MoveCursorSeq(row, col int) string {
	if row < 1 {
		row = 1
	}
	if col < 1 {
		col = 1
	}
	return fmt.Sprintf("\x1b[%d;%dH", row, col)
}

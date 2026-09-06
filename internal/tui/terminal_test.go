package tui

import (
	"testing"
)

func TestParseKey(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		wantType KeyType
		wantRune rune
	}{
		{"Rune 'a'", []byte("a"), KeyRune, 'a'},
		{"Rune '@'", []byte("@"), KeyRune, '@'},
		{"Rune '/'", []byte("/"), KeyRune, '/'},
		{"Rune '#'", []byte("#"), KeyRune, '#'},
		{"Up arrow", []byte("\x1b[A"), KeyUp, 0},
		{"Down arrow", []byte("\x1b[B"), KeyDown, 0},
		{"Right arrow", []byte("\x1b[C"), KeyRight, 0},
		{"Left arrow", []byte("\x1b[D"), KeyLeft, 0},
		{"Home key", []byte("\x1b[H"), KeyHome, 0},
		{"End key", []byte("\x1b[F"), KeyEnd, 0},
		{"Page Up", []byte("\x1b[5~"), KeyPgUp, 0},
		{"Page Down", []byte("\x1b[6~"), KeyPgDn, 0},
		{"Delete", []byte("\x1b[3~"), KeyDelete, 0},
		{"Shift+Tab", []byte("\x1b[Z"), KeyShiftTab, 0},
		{"Escape", []byte("\x1b"), KeyEsc, 0},
		{"Tab", []byte("\t"), KeyTab, 0},
		{"Enter CR", []byte("\r"), KeyEnter, 0},
		{"Enter LF", []byte("\n"), KeyEnter, 0},
		{"Backspace 127", []byte("\x7f"), KeyBackspace, 0},
		{"Backspace 8", []byte("\x08"), KeyBackspace, 0},
		{"Ctrl+A", []byte{0x01}, KeyCtrlA, 0},
		{"Ctrl+E", []byte{0x05}, KeyCtrlE, 0},
		{"Ctrl+W", []byte{0x17}, KeyCtrlW, 0},
		{"Ctrl+P", []byte{0x10}, KeyCtrlP, 0},
		{"Ctrl+R", []byte{0x12}, KeyCtrlR, 0},
		{"Ctrl+F", []byte{0x06}, KeyCtrlF, 0},
		{"Ctrl+C", []byte{0x03}, KeyCtrlC, 0},
		{"Bracketed Paste", []byte("\x1b[200~hello world\x1b[201~"), KeyPaste, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseKey(tc.input)
			if got.Type != tc.wantType {
				t.Fatalf("ParseKey(%q).Type = %v; want %v", tc.input, got.Type, tc.wantType)
			}
			if tc.wantRune != 0 && got.Rune != tc.wantRune {
				t.Fatalf("ParseKey(%q).Rune = %c; want %c", tc.input, got.Rune, tc.wantRune)
			}
			if tc.wantType == KeyPaste && got.Paste != "hello world" {
				t.Fatalf("ParseKey paste content = %q; want 'hello world'", got.Paste)
			}
		})
	}
}

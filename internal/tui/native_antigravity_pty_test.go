//go:build linux

package tui

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// agyWire builds the protobuf fields the adapter reads, so the fixture is a
// real agy-shaped conversation database with synthetic content.
type agyWire []byte

func (w agyWire) varint(field int, value uint64) agyWire {
	w = agyAppendVarint(w, uint64(field)<<3)
	return agyAppendVarint(w, value)
}

func (w agyWire) bytes(field int, value []byte) agyWire {
	w = agyAppendVarint(w, uint64(field)<<3|2)
	w = agyAppendVarint(w, uint64(len(value)))
	return append(w, value...)
}

func agyAppendVarint(b []byte, value uint64) []byte {
	for value >= 0x80 {
		b = append(b, byte(value)|0x80)
		value >>= 7
	}
	return append(b, byte(value))
}

// writeAgyConversation writes a conversation database with one user message
// and one visible answer, the way agy lays one out on disk.
func writeAgyConversation(t *testing.T, path, question, answer string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE steps (idx integer PRIMARY KEY, step_type integer NOT NULL DEFAULT 0, status integer NOT NULL DEFAULT 0, metadata blob, step_payload blob)"); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 18, 7, 0, 0, 0, time.UTC)
	metadata := func(offset int) []byte {
		created := agyWire{}.varint(1, uint64(at.Unix()+int64(offset)))
		return agyWire{}.bytes(1, created)
	}
	user := agyWire{}.bytes(19, agyWire{}.bytes(2, []byte(question)))
	reply := agyWire{}.bytes(20, agyWire{}.
		bytes(1, []byte(answer)).
		bytes(3, []byte("AGY-PRIVATE-REASONING")))
	for _, row := range []struct {
		idx, kind int
		md, pl    []byte
	}{
		{0, 14, metadata(0), user},
		{1, 15, metadata(1), reply},
	} {
		if _, err := db.Exec("INSERT INTO steps (idx, step_type, status, metadata, step_payload) VALUES (?, ?, 3, ?, ?)", row.idx, row.kind, row.md, row.pl); err != nil {
			t.Fatal(err)
		}
	}
}

// TestPTYNativeAntigravityOwnsInputAndSavesMemory drives agy the way an
// operator does: /agy opens it, it owns the terminal, and when it exits its
// conversation is in MARSHAL's project memory without the model's reasoning.
// F12 then opens it again from the workspace.
func TestPTYNativeAntigravityOwnsInputAndSavesMemory(t *testing.T) {
	buildMarshalBinary(t)

	home := t.TempDir()
	conversations := filepath.Join(home, ".gemini", "antigravity-cli", "conversations")
	if err := os.MkdirAll(conversations, 0o700); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(t.TempDir(), "fixture.db")
	writeAgyConversation(t, fixture, "Which package holds the importer?", "AGY-VISIBLE-ANSWER internal/memory/importer")

	binDir := t.TempDir()
	script := `#!/bin/sh
for arg do case "$arg" in --version) printf '1.2.5\n'; exit 0 ;; esac; done
printf 'AGY-NATIVE-READY\n'
for arg do printf 'AGY-ARG:<%s>\n' "$arg"; done
IFS= read -r answer
printf 'AGY-INPUT:<%s>\n' "$answer"
cp "$MARSHAL_TEST_AGY_FIXTURE" "$HOME/.gemini/antigravity-cli/conversations/conv-pty.db"
`
	if err := os.WriteFile(filepath.Join(binDir, "agy"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	// HOME is moved only after the binary is built, so the build keeps its
	// own cache and the operator's real ~/.gemini is never touched.
	t.Setenv("HOME", home)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MARSHAL_TEST_AGY_FIXTURE", fixture)
	t.Setenv("MARSHAL_NO_UPDATE_CHECK", "1")

	s := startTUI(t, 40, 160)
	s.sendLine("/agy")
	s.mustSee("AGY-NATIVE-READY")
	s.sendLine("FIRST-KEY-SURVIVES")
	s.mustSee("AGY-INPUT:<FIRST-KEY-SURVIVES>")
	if !s.waitForCount("Antigravity exited.", 1, 10*time.Second) {
		t.Fatalf("Antigravity did not return to MARSHAL.\n--- output tail ---\n%s", tail(s.output(), 3000))
	}
	s.mustSee("Antigravity exited. 2 message(s), including tool calls, saved to MARSHAL memory")

	s.sendLine("/memory search AGY-VISIBLE-ANSWER")
	s.mustSee("MEMORY RECORDS")
	if strings.Contains(s.output(), "No memory records") {
		t.Fatalf("the Antigravity conversation is not in project memory:\n%s", tail(s.output(), 3000))
	}
	if strings.Contains(s.output(), "AGY-PRIVATE-REASONING") {
		t.Fatalf("the model's reasoning reached the workspace:\n%s", tail(s.output(), 3000))
	}

	// F12 is the workspace key for Antigravity, as F7-F9 are for the others.
	ready := strings.Count(s.output(), "AGY-NATIVE-READY")
	s.send("\x1b[24~")
	if !s.waitForCount("AGY-NATIVE-READY", ready+1, 8*time.Second) {
		t.Fatalf("F12 did not open Antigravity.\n--- output tail ---\n%s", tail(s.output(), 3000))
	}
	s.sendLine("F12-AGY")
	s.mustSee("AGY-INPUT:<F12-AGY>")
}

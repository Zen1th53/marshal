package tui

import (
	"slices"
	"strings"
	"testing"
)

// keyScope is where a boardKeymap entry's keys act: one view's or the
// inbox's keys only while it has the focus, the inspector's while it is
// open, a stopped run's Enter only while the inbox is empty, the rest
// anywhere on the Board. Two entries may share a key only in different
// scopes, where the key reaches one of them at a time.
func keyScope(k boardKey) string {
	switch {
	case k.ID == "run.stopped":
		return "stopped run"
	case strings.HasPrefix(k.What, "inspector:"):
		return "inspector"
	case k.pane() != "":
		return k.pane()
	}
	return "board"
}

// No two Board bindings share a key where they act: each key does one
// documented thing, and help (?) lists it once.
func TestBoardBindingsShareNoKey(t *testing.T) {
	seen := map[string]string{}
	for _, k := range boardKeymap {
		for _, key := range k.keys() {
			at := keyScope(k) + " " + key
			if prev, ok := seen[at]; ok {
				t.Errorf("key %q in %s is bound by both %s and %s", key, keyScope(k), prev, k.ID)
				continue
			}
			seen[at] = k.ID
		}
	}
}

// A palette row shows a key only when a Board binding names it, and only the
// Board's row for that binding shows it: a registry row never advertises a
// chord nothing binds, or a key that does something else.
func TestPaletteShowsOnlyBoundKeys(t *testing.T) {
	bound := map[string]string{}
	for _, k := range boardKeymap {
		bound[k.ID] = k.Keys
	}
	for _, a := range append(boardPaletteActions(nil), paletteRegistryActions()...) {
		if a.Shortcut == "" {
			continue
		}
		if keys, ok := bound[a.ID]; !ok || keys != a.Shortcut {
			t.Errorf("palette row %s (%q) shows key %q that no Board binding of its own names", a.ID, a.Title, a.Shortcut)
		}
	}
}

// The registry names only keys the Board binds, so a chord that does
// nothing cannot creep back into any surface that reads KeyboardPath.
func TestRegistryKeysAreBound(t *testing.T) {
	for _, c := range GlobalRegistry.All() {
		if c.KeyboardPath != "" && !boardBindsKey(c.KeyboardPath) {
			t.Errorf("capability %s advertises key %q, which no Board binding has", c.ID, c.KeyboardPath)
		}
	}
}

// Every palette row's ID is unique, so the palette lists each action once.
func TestPaletteRowsUnique(t *testing.T) {
	var ids []string
	for _, a := range append(boardPaletteActions(nil), paletteRegistryActions()...) {
		if slices.Contains(ids, a.ID) {
			t.Errorf("palette lists %s twice", a.ID)
		}
		ids = append(ids, a.ID)
	}
}

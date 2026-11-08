package tui

import "strings"

// KeyClass is what pressing a Board key may do. The Board's rule
// (private-docs/tui-v3-board.md, Keys) is that a movement key never acts
// and a mutating action never has a bare key: its key opens a
// confirmation and the confirmation acts.
type KeyClass string

const (
	// KeyMove changes only what is selected, focused or scrolled.
	KeyMove KeyClass = "move"
	// KeyOpen opens or closes a view, an overlay, a prompt or a native
	// session, or shows something read; it changes no run, task, request
	// or memory.
	KeyOpen KeyClass = "open"
	// KeyConfirm starts a mutating action: the key opens its confirmation
	// and only the confirmation performs it.
	KeyConfirm KeyClass = "confirm"
)

// keyClasses is every class, in the order the help would group them.
var keyClasses = []KeyClass{KeyMove, KeyOpen, KeyConfirm}

// classOf is the class of one key of the entry, written as in Keys.
func (k boardKey) classOf(key string) KeyClass {
	if c, ok := k.Except[key]; ok {
		return c
	}
	return k.Class
}

// keys is the entry's keys as Keys writes them, split on the two spaces
// between alternatives and on single spaces inside them ("j k  ↑ ↓" is
// j, k, ↑ and ↓). An entry that describes rather than names its key
// ("hard gate") is one key.
func (k boardKey) keys() []string {
	if k.Listed {
		return []string{k.Keys}
	}
	return strings.Fields(k.Keys)
}

// boardKeyClass is the class of key in group, the first entry that lists
// it; ok is false for a key the keymap does not bind there.
func boardKeyClass(group, key string) (KeyClass, bool) {
	for _, k := range boardKeymap {
		if k.Group != group {
			continue
		}
		for _, f := range k.keys() {
			if f == key {
				return k.classOf(key), true
			}
		}
	}
	return "", false
}

// keyStrokes is how many keystrokes reach the entry's action from the
// Board with nothing open: its own key, after the key of the view it
// belongs to for a view's key, or after i for an inbox key while the
// focus is elsewhere. A key listed only (typed into a confirmation) is
// reached through the key that opens that confirmation, one more.
func (k boardKey) keyStrokes() int {
	n := 1
	switch k.Group {
	case "IN A VIEW", "INBOX":
		n++
	}
	if k.Listed {
		n++
	}
	return n
}

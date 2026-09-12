package tui

import "testing"

// Only the confirmation's button row may be clicked.
//
// handleConfirmMouse used to scan every rendered line of the modal for the
// words "Cancel" and "Proceed". Prose inside a confirmation legitimately
// contains them — a refusal reading "cannot proceed until the run stops", or a
// prompt asking "Do you wish to Proceed?" — and clicking the word in that prose
// moved the selection onto Proceed and submitted the mutation in one event.
// The button row is identified by its shape so prose cannot impersonate it.
func TestOnlyTheButtonRowIsClickable(t *testing.T) {
	for _, c := range []struct {
		name string
		line string
		want bool
	}{
		{"button row, cancel focused", " ▸Cancel    Proceed  ", true},
		{"button row, proceed focused", "  Cancel   ▸Proceed  ", true},
		{"refusal prose", "cannot proceed until the run stops", false},
		{"question prose", "Do you wish to Proceed?", false},
		{"prose naming both controls", "Cancel the rollback, then Proceed with restore", false},
		{"detail line", "Proceed will delete 3 worktrees", false},
		{"empty", "", false},
	} {
		if got := isConfirmButtonRow(c.line); got != c.want {
			t.Errorf("%s: isConfirmButtonRow(%q) = %v, want %v", c.name, c.line, got, c.want)
		}
	}
}

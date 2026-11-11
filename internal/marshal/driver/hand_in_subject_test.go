package driver

import "testing"

// The worker commit lands in the owner's git history: a plain subject that
// names the task and its title, with no run or lane identifiers.
func TestHandInSubjectIsPlain(t *testing.T) {
	for _, tc := range []struct{ id, title, want string }{
		{"calc-add", "Add add(a, b) with a unit test", "calc-add: Add add(a, b) with a unit test"},
		{"calc-add", "", "calc-add: work handed in"},
		{"calc-add", "First line\nsecond line", "calc-add: First line"},
	} {
		if got := handInSubject(tc.id, tc.title); got != tc.want {
			t.Fatalf("%q %q -> %q, want %q", tc.id, tc.title, got, tc.want)
		}
	}
}

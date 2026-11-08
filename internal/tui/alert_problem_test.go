package tui

import "testing"

func TestFinishedWorkIsNotAnErrorNote(t *testing.T) {
	for _, state := range []string{"done", "completed", "merged", "accepted"} {
		if alertIsProblem(state) {
			t.Fatalf("%s must show as an ordinary note", state)
		}
	}
	for _, state := range []string{"failed", "waiting", "expired", ""} {
		if !alertIsProblem(state) {
			t.Fatalf("%s must stay an error note", state)
		}
	}
}

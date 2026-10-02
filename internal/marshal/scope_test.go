package marshal

import "testing"

func TestHandInDirectoryScopeRejectsEscapes(t *testing.T) {
	for _, name := range []string{"../src/file.go", "src/../../file.go", "src/../outside.go", "/src/file.go", "src-other/file.go", "src\\..\\outside.go"} {
		h := HandIn{ResultCommit: "result", FilesTouched: []string{name}}
		if err := ValidateHandIn(Task{Files: []string{"src/"}}, h); err == nil {
			t.Errorf("accepted %q", name)
		}
	}
}

func TestHandInRejectsUnsafeDeclaredScope(t *testing.T) {
	for _, scope := range []string{"../src", "src/..", "/src", ".", ""} {
		if err := ValidateHandIn(Task{Files: []string{scope}}, HandIn{ResultCommit: "result", FilesTouched: []string{scope}}); err == nil {
			t.Errorf("accepted scope %q", scope)
		}
	}
}

package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Relaunching after an update re-attached to the control centre started by
// the old binary, so an hour of "restarts" in the dogfood POC ran old code.
func TestControlCentreOutdatedAfterAnUpdate(t *testing.T) {
	root := t.TempDir()
	exe := filepath.Join(t.TempDir(), "marshal")
	if err := os.WriteFile(exe, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if controlCentreOutdated(root, exe) {
		t.Fatal("a control centre with no record was called outdated")
	}
	recordControlCentreBinary(root, exe)
	if controlCentreOutdated(root, exe) {
		t.Fatal("the same binary was called outdated")
	}
	if err := os.WriteFile(exe, []byte("v2 longer"), 0o755); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	_ = os.Chtimes(exe, later, later)
	if !controlCentreOutdated(root, exe) {
		t.Fatal("an updated binary was not noticed")
	}
}

func TestConfirmRestartUpdatedDefaultsToYes(t *testing.T) {
	for input, want := range map[string]bool{"\n": true, "y\n": true, "YES\n": true, "n\n": false, "no\n": false, "": false} {
		var errOut bytes.Buffer
		c := &command{stdin: strings.NewReader(input), stderr: &errOut}
		if got := c.confirmRestartUpdated(); got != want {
			t.Errorf("%q: restart = %v, want %v", input, got, want)
		}
		if !strings.Contains(errOut.String(), "[Y/n]") {
			t.Errorf("%q: no question asked: %q", input, errOut.String())
		}
	}
}

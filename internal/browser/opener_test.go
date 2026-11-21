package browser

import (
	"path/filepath"
	"runtime"
	"testing"
)

// The opener is named by absolute path, so a program planted earlier on PATH
// never receives the one-use link.
func TestOpenerIsAbsolute(t *testing.T) {
	command, args := opener("http://127.0.0.1:1/?t=x")
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		if !filepath.IsAbs(command) {
			t.Fatalf("opener %q is not absolute", command)
		}
	}
	if len(args) == 0 || args[len(args)-1] != "http://127.0.0.1:1/?t=x" {
		t.Fatalf("url not passed: %v", args)
	}
}

package driver

import (
	"strings"
	"testing"
)

func TestAgyHeadlessWorkerUsesFileToolsInsteadOfDeniedShell(t *testing.T) {
	args := Agy("agy").Args(Request{Brief: "Write the marker in the assigned worktree."})
	var prompt string
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-p" {
			prompt = args[i+1]
		}
	}
	if !strings.Contains(prompt, "file tools") || !strings.Contains(prompt, "Do not invoke run_command") || !strings.Contains(prompt, "Do not commit") {
		t.Fatalf("headless worker can choose unavailable shell permission: %q", prompt)
	}
	for _, a := range args {
		if a == "--dangerously-skip-permissions" {
			t.Fatal("native controls bypassed")
		}
	}
}

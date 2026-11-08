//go:build governed

package cli

import (
	"bytes"
	"strings"
	"testing"
)

// goal subcommands that exist only in Extreme must be refused with a usage
// error, never treated as a free-text request such as "create ...".
func TestDefaultGoalManagementSubcommandsAreRefused(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][]string{
		{"goal", "create", "SESSION", "--file", "in.json"},
		{"goal", "approve", "SESSION", "--revision", "1"},
		{"goal", "create"},
		{"goal", "edit", "x"},
		{"goal", "version"},
	} {
		var out, stderr bytes.Buffer
		code := Execute(t.Context(), root, args, strings.NewReader(""), &out, &stderr)
		if code == 0 || !strings.Contains(stderr.String(), "not available in this edition") || !strings.Contains(stderr.String(), "usage: goal <request>") || out.Len() != 0 {
			t.Errorf("%v: code=%d stdout=%q stderr=%q", args, code, out.String(), stderr.String())
		}
	}
}

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Zen1th53/marshal/internal/statepath"
)

func tuiChildPaths(root string) (string, string) {
	return statepath.Path(root, "logs", "tui-child.stderr"), statepath.Path(root, "logs", "tui-child.status")
}

// prepareTUIChild captures stderr and the actual child's status, rather than
// tmux attach's status (which is zero even when the pane failed). The shell
// records status before the pane exits, so attach returning after [exited]
// cannot race the report. Keep it across detach for the next attachment.
func prepareTUIChild(root string, argv []string) ([]string, error) {
	reason, status := tuiChildPaths(root)
	if err := os.MkdirAll(filepath.Dir(reason), 0700); err != nil {
		return nil, err
	}
	for _, path := range []string{reason, status} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("reset TUI child report: %w", err)
		}
	}
	// Pass every path and argument as a positional parameter, never shell text.
	return append([]string{"/bin/sh", "-c", `umask 077
reason=$1; status=$2; shift 2
"$@" 2>"$reason"
code=$?
printf '%s\n' "$code" >"$status.tmp"
mv -f -- "$status.tmp" "$status"
exit "$code"`, "marshal-tui-child", reason, status}, argv...), nil
}

func tuiChildResult(root string, attachErr error) error {
	reasonPath, statusPath := tuiChildPaths(root)
	data, err := os.ReadFile(statusPath)
	if os.IsNotExist(err) {
		// A user may detach while the child is still running.
		return attachErr
	}
	if err != nil {
		return fmt.Errorf("read TUI child status: %w", err)
	}
	code, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return fmt.Errorf("invalid TUI child status %q", data)
	}
	if code == 0 {
		return attachErr
	}
	reason, err := os.ReadFile(reasonPath)
	if err != nil {
		return fmt.Errorf("MARSHAL TUI child exited with status %d; read failure reason: %w", code, err)
	}
	if detail := strings.TrimSpace(string(reason)); detail != "" {
		return fmt.Errorf("MARSHAL TUI child exited with status %d: %s", code, detail)
	}
	return fmt.Errorf("MARSHAL TUI child exited with status %d in %q; run `marshal doctor` for details", code, root)
}

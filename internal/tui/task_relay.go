package tui

import (
	"context"
	"path/filepath"
)

type taskRelayView struct{ root, id string }
type taskRelayViewKey struct{}

// The relay is only a view: raw provider output remains evidence, and its
// exit cannot certify task success. The driver publishes the outcome separately.
func taskRelayCommand(relay, socket, root, id string) []string {
	evidence := filepath.Join(root, ".marshal", "evidence", id+"-relay-latest.txt")
	return []string{"/bin/sh", "-c", `
printf 'Task worker running. Result pending.\nF11: Control centre\n'
"$1" STDIO "UNIX-CONNECT:$2" >> "$3" 2>&1
while [ ! -s "$4" ]; do sleep 0.1; done
code=$(cat "$4")
printf '\033[2J\033[H'
if [ "$code" = 0 ]; then
  printf 'Status: completed (worker exit 0)\nSummary: Worker finished; review and verification determine acceptance.\n'
else
  printf 'Status: failed (worker exit %s)\nSummary: Worker did not complete successfully.\n' "$code"
fi
printf 'Full output: %s\nF11: Control centre\n' "$3"
exec sleep 2147483647
`, "marshal-task-view", relay, socket, evidence, agentOutcomePath(root, id)}
}

func withTaskRelayView(ctx context.Context, root, id string) context.Context {
	return context.WithValue(ctx, taskRelayViewKey{}, taskRelayView{root, id})
}

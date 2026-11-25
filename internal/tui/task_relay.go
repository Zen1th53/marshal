package tui

import (
	"context"
	"encoding/json"
	"github.com/Zen1th53/marshal/internal/statepath"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func relayLatestPath(root, id string) string {
	return filepath.Join(root, statepath.DirName, "evidence", id+"-relay-latest.txt")
}

// relayTail is how much of a relay file's end providerWait reads.
const relayTail = 16 << 10

// providerWait is the notice the end of the worker's relay output at path
// gives when its provider is retrying, out of capacity or rate limited: ""
// when the latest event shows progress or the file cannot be read. It reads
// on the Board's refresh, never while painting.
func providerWait(provider, path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return ""
	}
	buf := make([]byte, min(st.Size(), relayTail))
	if _, err := f.ReadAt(buf, st.Size()-int64(len(buf))); err != nil && err != io.EOF {
		return ""
	}
	return providerWaitText(provider, string(buf))
}

var retryCount = regexp.MustCompile(`(\d+)/(\d+)`)

// providerWaitText reads the lines of out from the end: the latest error
// naming a retry, capacity or a rate limit gives the notice; a later JSON
// event that is not an error means the worker went on.
func providerWaitText(provider, out string) string {
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		msg := line
		if strings.HasPrefix(line, "{") {
			var ev struct{ Type, Message string }
			if json.Unmarshal([]byte(line), &ev) != nil {
				continue // a cut line at the start of the tail
			}
			if ev.Type != "error" {
				return ""
			}
			msg = ev.Message
		}
		low := strings.ToLower(msg)
		why := ""
		switch {
		case strings.Contains(low, "at capacity"):
			why = "at capacity"
		case strings.Contains(low, "rate limit") || strings.Contains(low, "429"):
			why = "rate limited"
		case strings.Contains(low, "overloaded") || strings.Contains(low, "529"):
			why = "overloaded"
		case strings.Contains(low, "reconnecting"):
			why = "reconnecting"
		default:
			if strings.HasPrefix(line, "{") {
				return ""
			}
			continue
		}
		text := provider + " is " + why
		if why != "reconnecting" {
			text += ", retrying"
		}
		if strings.Contains(low, "reconnecting") || strings.Contains(low, "retry") {
			if m := retryCount.FindStringSubmatch(msg); m != nil {
				text += " (" + m[1] + "/" + m[2] + ")"
			}
		}
		return text
	}
	return ""
}

type taskRelayView struct{ root, id string }
type taskRelayViewKey struct{}

// The relay is only a view: raw provider output remains evidence, and its
// exit cannot certify task success. The driver publishes the outcome separately.
func taskRelayCommand(relay, socket, root, id string) []string {
	evidence := filepath.Join(root, statepath.DirName, "evidence", id+"-relay-latest.txt")
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

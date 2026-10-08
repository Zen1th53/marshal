// Package permission renders operator decisions. Model strings are display data.
package permission

import (
	"context"
	"fmt"
	"github.com/Zen1th53/marshal/internal/tmux"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
)

type Request struct {
	Kind, Object, Scope, Who, Reason, RunID, TaskID string
	// ContinuationProvider binds a read decision to its proposed importer.
	// It is independent of task identity and included in evidence and deduplication.
	ContinuationProvider string
}

func (r Request) Key() string {
	return r.Kind + "\x00" + r.Object + "\x00" + r.Scope + "\x00" + r.Who + "\x00" + r.RunID + "\x00" + r.TaskID + "\x00" + r.ContinuationProvider
}

const MaxPopupItems = 5

func Render(requests []Request) (string, error) {
	var b strings.Builder
	b.WriteString("Permission request\n\n")
	for i, r := range requests {
		if r.ContinuationProvider != "" && (r.Kind != "read" || (r.ContinuationProvider != "codex" && r.ContinuationProvider != "claude")) {
			return "", fmt.Errorf("unknown continuation provider")
		}

		for _, s := range []string{r.Object, r.Scope, r.Who, r.RunID, r.TaskID} {
			if strings.ContainsFunc(s, unicode.IsControl) {
				return "", fmt.Errorf("unsafe permission metadata")
			}
		}
		if i >= MaxPopupItems {
			continue
		}
		if r.Kind == "credential" {
			names := map[string]string{"codex": "Codex", "claude": "Claude Code", "gemini": "Gemini", "opencode": "OpenCode"}
			name, ok := names[r.Object]
			if !ok {
				return "", fmt.Errorf("unknown credential provider")
			}
			fmt.Fprintf(&b, "%d. Allow governed workers to use your %s sign-in through MARSHAL's credential broker? The worker never sees the token.\nScope and duration: this project, until revoked\n", i+1, name)
			continue
		}
		// Control characters in untrusted reasons cannot change the terminal layout.
		reason := strings.Map(func(c rune) rune {
			if unicode.IsControl(c) {
				return ' '
			}
			return c
		}, r.Reason)
		if runes := []rune(reason); len(runes) > 100 {
			reason = string(runes[:100]) + "…"
		}
		if r.Kind == "network" {
			fmt.Fprintf(&b, "Run: %s · Task: %s\n", r.RunID, r.TaskID)
		}
		fmt.Fprintf(&b, "%d. %s\nScope and duration: %s\nWho asks: %s · The Marshal says: \"%s\"\n", i+1, r.Object, r.Scope, r.Who, reason)
	}
	if len(requests) > MaxPopupItems {
		fmt.Fprintf(&b, "and %d more (require separate decisions)\n\n", len(requests)-MaxPopupItems)
	}
	b.WriteString("A = Allow   D = Deny\nEsc, D, n, Enter, other non-navigation keys or timeout = Deny\nF7/F8/F9/F11/F12 navigate; request stays pending\n")
	return b.String(), nil
}
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }

// Popup reads one literal operator key. On tmux 3.2a it uses a dedicated
// window: display-popup -E was observed crashing during native window creation.
func Popup(ctx context.Context, target string, requests []Request, timeout time.Duration) (bool, error) {
	text, err := Render(requests)
	if err != nil {
		return false, err
	}
	if len(requests) == 0 {
		return false, nil
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	dir, err := os.MkdirTemp("", "marshal-permission-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(dir)
	prompt := filepath.Join(dir, "prompt")
	result := filepath.Join(dir, "decision")
	script := filepath.Join(dir, "popup")
	if err = os.WriteFile(prompt, []byte(text), 0600); err != nil {
		return false, err
	}
	// Read the full terminal sequence before treating Escape as a decision.
	body := fmt.Sprintf(`#!/bin/bash
cat %s
key=''
if IFS= read -r -s -n 1 -t %.3f key; then
 if [[ $key == $'\e' ]]; then
  sequence=''
  while IFS= read -r -s -n 1 -t .05 part; do
   sequence+="$part"
   [[ $part == '~' || $part =~ [[:alpha:]] || ${#sequence} -ge 16 ]] && break
  done
  case "$sequence" in
   '[18~') key=F7;; '[19~') key=F8;; '[20~') key=F9;; '[23~') key=F11;; '[24~') key=F12;;
  esac
 fi
fi
case "$key" in
 A) printf allow > %s;;
 F7|F8|F9|F11|F12) printf 'navigate:%%s' "$key" > %s;;
 *) printf deny > %s;;
esac
`, quote(prompt), timeout.Seconds(), quote(result), quote(result), quote(result))
	if err = os.WriteFile(script, []byte(body), 0700); err != nil {
		return false, err
	}
	child, cancel := context.WithTimeout(ctx, timeout+time.Second)
	defer cancel()
	// Query the server, which may differ from the client executable.
	version, versionErr := tmux.RunCommand(child, "display-message", "-p", "#{version}")
	if versionErr == nil && strings.TrimSpace(string(version)) == "3.2a" {
		return windowDecision(child, target, script, result, len(requests))
	}
	args := []string{"display-popup", "-E", "-w", "90%", "-h", "80%"}
	if target != "" {
		args = append(args, "-t", target)
	}
	args = append(args, "/bin/bash "+quote(script))
	if _, err = tmux.RunCommand(child, args...); err != nil {
		return false, err
	}
	data, err := os.ReadFile(result)
	if os.IsNotExist(err) {
		return false, nil
	}
	return popupResult(data, len(requests), err)
}

// Create detached, then select only after tmux has finished creating the pane.
// Closing the window, EOF, or timeout all deny, just as closing a popup does.
func windowDecision(ctx context.Context, target, script, result string, count int) (bool, error) {
	args := []string{"new-window", "-d", "-P", "-F", "#{pane_id}", "-n", "marshal-permission"}
	if target != "" {
		args = append(args, "-t", target)
	}
	args = append(args, "/bin/bash", script)
	out, err := tmux.RunCommand(ctx, args...)
	if err != nil {
		return false, err
	}
	pane := strings.TrimSpace(string(out))
	if !strings.HasPrefix(pane, "%") {
		return false, fmt.Errorf("permission window has no pane identity")
	}
	defer tmux.RunCommand(context.Background(), "kill-window", "-t", pane)
	if _, err := tmux.RunCommand(ctx, "select-window", "-t", pane); err != nil {
		return false, err
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, err := os.ReadFile(result)
		if err == nil {
			return popupResult(data, count, nil)
		}
		if !os.IsNotExist(err) {
			return false, err
		}
		if _, err := tmux.RunCommand(ctx, "display-message", "-p", "-t", pane, "#{pane_id}"); err != nil {
			return false, nil
		}
		select {
		case <-ctx.Done():
			return false, nil
		case <-ticker.C:
		}
	}
}

// Queue deduplicates pending requests, preserving their order for batch display.
// Taken-over worker panes hold the entire queue until the operator returns.
type Queue struct {
	mu      sync.Mutex
	pending []Request
	keys    map[string]bool
}

func (q *Queue) Add(r Request) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.keys == nil {
		q.keys = map[string]bool{}
	}
	if q.keys[r.Key()] {
		return
	}
	q.keys[r.Key()] = true
	q.pending = append(q.pending, r)
}
func (q *Queue) Take(busy bool) []Request {
	q.mu.Lock()
	defer q.mu.Unlock()
	if busy {
		return nil
	}
	out := q.pending
	q.pending = nil
	q.keys = nil
	return out
}

func (q *Queue) Empty() bool { q.mu.Lock(); defer q.mu.Unlock(); return len(q.pending) == 0 }

// Navigation closes only the display, never the pending request.
type Navigation struct{ Key string }

func (n *Navigation) Error() string { return "permission popup navigation: " + n.Key }
func popupResult(data []byte, count int, err error) (bool, error) {
	if strings.HasPrefix(string(data), "navigate:") {
		return false, &Navigation{Key: strings.TrimPrefix(string(data), "navigate:")}
	}
	return string(data) == "allow" && count <= MaxPopupItems, err
}

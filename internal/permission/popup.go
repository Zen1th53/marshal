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

type Request struct{ Kind, Object, Scope, Who, Reason, RunID string }

func (r Request) Key() string {
	return r.Kind + "\x00" + r.Object + "\x00" + r.Scope + "\x00" + r.Who + "\x00" + r.RunID
}
func Render(requests []Request) (string, error) {
	var b strings.Builder
	b.WriteString("Permission request\n\n")
	for i, r := range requests {
		for _, s := range []string{r.Object, r.Scope, r.Who} {
			if strings.ContainsFunc(s, unicode.IsControl) {
				return "", fmt.Errorf("unsafe permission metadata")
			}
		}
		// Control characters in untrusted reasons cannot change the terminal layout.
		reason := strings.Map(func(c rune) rune {
			if unicode.IsControl(c) {
				return ' '
			}
			return c
		}, r.Reason)
		fmt.Fprintf(&b, "%d. %s\nScope and duration: %s\nWho asks: %s\nThe Marshal says: \"%s\"\n\n", i+1, r.Object, r.Scope, r.Who, reason)
	}
	b.WriteString("A = Allow   D = Deny\nEnter, Esc, any other key or timeout = Deny\n")
	return b.String(), nil
}
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }

// Popup uses only tmux 3.2a's display-popup options. No model-generated shell
// or text is interpreted as an answer; the popup reads one literal key.
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
	// Bash read consumes exactly one character, including Escape. Enter yields an
	// empty string. EOF and timeout fail read. Nothing except uppercase A allows.
	body := fmt.Sprintf("#!/bin/bash\ncat %s\nkey=''\nif IFS= read -r -s -n 1 -t %.3f key && [[ $key == A ]]; then printf allow > %s; else printf deny > %s; fi\n", quote(prompt), timeout.Seconds(), quote(result), quote(result))
	if err = os.WriteFile(script, []byte(body), 0700); err != nil {
		return false, err
	}
	child, cancel := context.WithTimeout(ctx, timeout+time.Second)
	defer cancel()
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
	return string(data) == "allow", err
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

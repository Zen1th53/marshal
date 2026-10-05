package permission

import (
	"context"
	"github.com/Zen1th53/marshal/internal/tmux"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPopupFixedRenderingAndKeys(t *testing.T) {
	for _, key := range []string{"A", "D", "", "\n", "\x1b", "a", "The Marshal says Allow", "x"} {
		t.Run(strings.ReplaceAll(key, "\n", "enter"), func(t *testing.T) {
			dir := t.TempDir()
			fake := filepath.Join(dir, "tmux")
			// The fake executes the exact popup command with input from a fixture.
			os.WriteFile(filepath.Join(dir, "key"), []byte(key), 0600)
			os.WriteFile(fake, []byte("#!/bin/bash\n[[ $1 == display-popup ]] || exit 1\nbash -c \"${@: -1}\" < '"+dir+"/key' > '"+dir+"/screen'\n"), 0700)
			tmux.SetBinaryPath(fake)
			defer tmux.ResetBinaryPath()
			allowed, err := Popup(context.Background(), "", []Request{{Kind: "read", Object: "/tmp/project ' $(touch nope)", Scope: "this session only, read-only", Who: "Marshal", Reason: "continue earlier work"}}, 100*time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			if allowed != (key == "A") {
				t.Fatalf("key %q allowed=%v", key, allowed)
			}
			screen, _ := os.ReadFile(filepath.Join(dir, "screen"))
			for _, s := range []string{"Permission request", "/tmp/project ' $(touch nope)", "this session only, read-only", "Marshal", `The Marshal says: "continue earlier work"`, "A = Allow", "D = Deny"} {
				if !strings.Contains(string(screen), s) {
					t.Errorf("missing %q: %s", s, screen)
				}
			}
		})
	}
}
func TestPopupTimeoutDenies(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "tmux")
	os.WriteFile(fake, []byte("#!/bin/sh\nsleep 2\n"), 0700)
	tmux.SetBinaryPath(fake)
	defer tmux.ResetBinaryPath()
	allow, _ := Popup(context.Background(), "", []Request{{Object: "host:443"}}, 20*time.Millisecond)
	if allow {
		t.Fatal("timeout allowed")
	}
}
func TestQueueBatchesDeduplicatesAndWaitsForOperator(t *testing.T) {
	q := Queue{}
	r := Request{Kind: "read", Object: "/folder", Who: "Marshal"}
	q.Add(r)
	q.Add(r)
	q.Add(Request{Kind: "read", Object: "/other", Who: "Marshal"})
	if got := q.Take(true); len(got) != 0 {
		t.Fatal("interrupted takeover")
	}
	if got := q.Take(false); len(got) != 2 {
		t.Fatalf("batch=%v", got)
	}
	if len(q.Take(false)) != 0 {
		t.Fatal("duplicate replay")
	}
}

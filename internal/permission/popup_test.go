package permission

import (
	"context"
	"fmt"
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

func TestPopupOverflowBoundedAndCannotApproveHiddenItems(t *testing.T) {
	requests := make([]Request, 90)
	for i := range requests {
		requests[i] = Request{Object: fmt.Sprintf("item-%d", i)}
	}
	rendered, err := Render(requests)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "and 85 more") || strings.Contains(rendered, "item-5") || !strings.Contains(rendered, "A = Allow") {
		t.Fatalf("overflow rendering: %s", rendered)
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "tmux")
	if err := os.WriteFile(fake, []byte("#!/bin/bash\nprintf A | bash -c \"${@: -1}\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(fake)
	defer tmux.ResetBinaryPath()
	if allow, err := Popup(context.Background(), "", requests, time.Second); err != nil || allow {
		t.Fatalf("hidden items approved: %v %v", allow, err)
	}
}

func TestNetworkPopupShowsRunAndTaskIdentity(t *testing.T) {
	request := Request{Kind: "network", Object: "example.test:443", RunID: "RUN-specific", TaskID: "TASK-specific", Who: "worker"}
	rendered, err := Render([]Request{request})
	if err != nil || !strings.Contains(rendered, "Run: RUN-specific") || !strings.Contains(rendered, "Task: TASK-specific") {
		t.Fatalf("missing identity: %s %v", rendered, err)
	}
	other := request
	other.TaskID = "TASK-other"
	if request.Key() == other.Key() {
		t.Fatal("different task requests deduplicated")
	}
	request.TaskID = "task\nA = Allow"
	if _, err := Render([]Request{request}); err == nil {
		t.Fatal("unsafe task identity rendered")
	}
}

func TestPopupBoundsUntrustedReason(t *testing.T) {
	rendered, err := Render([]Request{{Object: "MEM-candidate", Reason: strings.Repeat("long body ", 1000)}})
	if err != nil || len(rendered) > 500 || !strings.Contains(rendered, "A = Allow") {
		t.Fatalf("reason hid controls: %d bytes %v", len(rendered), err)
	}
}

func TestPermissionPopupContinuationIdentity(t *testing.T) {
	req := Request{Kind: "read", Object: "/tmp/history", ContinuationProvider: "codex"}
	other := req
	other.ContinuationProvider = "claude"
	if req.Key() == other.Key() {
		t.Fatal("different continuation importers share a decision")
	}
	if _, err := Render([]Request{req}); err != nil {
		t.Fatal(err)
	}
	other.ContinuationProvider = "shell"
	if _, err := Render([]Request{other}); err == nil {
		t.Fatal("unknown importer accepted")
	}
	other = req
	other.Kind = "memory"
	if _, err := Render([]Request{other}); err == nil {
		t.Fatal("importer accepted outside read request")
	}
}

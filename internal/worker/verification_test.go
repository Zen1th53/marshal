package worker

import (
	"context"
	"errors"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/sandbox"
)

func TestVerificationFailsClosedWithoutIsolation(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	_, err := runVerification(t.Context(), sandbox.NewBwrap(filepath.Join(dir, "missing")), dir, []string{"/bin/sh", "-c", "printf ran > '" + marker + "'"}, time.Second, 1024)
	if !errors.Is(err, model.ErrUnavailable) {
		t.Fatalf("missing isolation = %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("host fallback ran: %v", err)
	}
}

func TestVerificationExecutesInsideDetachedWorktreeEnvelope(t *testing.T) {
	repo := testgit.New(t)
	wt := filepath.Join(t.TempDir(), "detached")
	if out, err := exec.Command("git", "-C", repo.Path(), "worktree", "add", "--detach", wt, "HEAD").CombinedOutput(); err != nil {
		t.Fatalf("detached worktree: %v %s", err, out)
	}
	head := repo.HEAD(t)
	private, err := exec.Command("git", "-C", wt, "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "host-marker")
	// All source and metadata writes must be refused, while a scratch write and
	// Git reads must execute successfully. Admission failure cannot pass this test.
	script := "set -eu; test -c /dev/null; printf discarded > /dev/null; test \"$(git rev-parse HEAD)\" = '" + head + "'; printf executed > /tmp/inside-marker; test \"$(cat /tmp/inside-marker)\" = executed; if (printf corrupt > inside-marker) 2>/dev/null; then exit 24; fi; if (printf escaped > '" + outside + "') 2>/dev/null; then exit 21; fi; if (printf corrupt > '" + filepath.Join(repo.Path(), ".git", "config") + "') 2>/dev/null; then exit 22; fi; if (printf corrupt > '" + filepath.Join(strings.TrimSpace(string(private)), "HEAD") + "') 2>/dev/null; then exit 23; fi; printf confined"
	result, err := RunVerification(t.Context(), wt, []string{"/bin/sh", "-c", script}, 10*time.Second, 4096)
	if err != nil || result.ExitCode != 0 || string(result.Stdout) != "confined" || result.Isolation.Level != model.IsolationBwrap || !result.Isolation.Available {
		t.Fatalf("positive verification: %+v %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(wt, "inside-marker")); !os.IsNotExist(err) {
		t.Fatalf("source write escaped: %v", err)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("host write escaped: %v", err)
	}
	if got := repo.HEAD(t); got != head {
		t.Fatalf("repository HEAD changed: %s", got)
	}
	if out, err := exec.Command("git", "-C", wt, "rev-parse", "HEAD").Output(); err != nil || strings.TrimSpace(string(out)) != head {
		t.Fatalf("private HEAD changed: %s %v", out, err)
	}
}

func TestVerificationRefusesIncompleteEvidence(t *testing.T) {
	for _, kind := range []string{"timeout", "cancellation", "truncation"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			output := t.TempDir()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			timeout, limit := 10*time.Second, 4096
			script := "printf started > \"$MARSHAL_BUILD_DIR/started\"; sleep 30"
			if kind == "timeout" {
				timeout = time.Second
			}
			if kind == "truncation" {
				script = "printf started > \"$MARSHAL_BUILD_DIR/started\"; printf 123456789; exit 0"
				limit = 4
			}
			var cancelled chan struct{}
			if kind == "cancellation" {
				cancelled = make(chan struct{})
				go func() {
					defer close(cancelled)
					ticker := time.NewTicker(10 * time.Millisecond)
					defer ticker.Stop()
					for {
						select {
						case <-ctx.Done():
							return
						case <-ticker.C:
							if _, err := os.Stat(filepath.Join(output, "started")); err == nil {
								cancel()
								return
							}
						}
					}
				}()
			}
			result, err := RunVerification(ctx, dir, []string{"/bin/sh", "-c", script}, timeout, limit, output)
			cancel()
			if cancelled != nil {
				<-cancelled
			}
			if data, readErr := os.ReadFile(filepath.Join(output, "started")); readErr != nil || string(data) != "started" {
				t.Fatalf("command never executed: %q %v; runner: %+v %v", data, readErr, result, err)
			}
			if !errors.Is(err, model.ErrUnavailable) {
				t.Fatalf("incomplete evidence accepted: %+v %v", result, err)
			}
			if (kind == "timeout" && !result.TimedOut) || (kind == "cancellation" && !result.Cancelled) || (kind == "truncation" && (!result.OutputTruncated || result.ExitCode != 0)) {
				t.Fatalf("wrong refusal: %+v", result)
			}
		})
	}
}

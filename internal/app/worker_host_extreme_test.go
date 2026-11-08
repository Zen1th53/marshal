//go:build !governed && linux

package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestExtremeWorkerLaunchUsesHostCommandWithoutProxy(t *testing.T) {
	for _, tc := range []struct {
		provider, model, first, flag string
	}{
		{"codex", "", "exec", "--dangerously-bypass-approvals-and-sandbox"},
		{"claude", "", "-p", "--permission-mode"},
		{"opencode", "custom/unlisted", "run", "-m"},
		{"opencode", "", "run", "--format"},
		{"agy", "gemini-3.8-flash-high", "--mode", "--model"},
	} {
		t.Run(tc.provider+"/"+tc.model, func(t *testing.T) {
			t.Setenv("MARSHAL_OPENCODE_MODEL", "")
			binDir := t.TempDir()
			bin := filepath.Join(binDir, tc.provider)
			argsFile := filepath.Join(binDir, "argv")
			script := "#!/bin/sh\ncase \"$1\" in\n" +
				"--version) printf '1.2.14\\n'; exit 0;;\n" +
				"--help) printf '%s\\n' '--print --output-format --conversation --mode --sandbox --new-project --disable-slash-commands --model'; exit 0;;\n" +
				"esac\nprintf '%s\\n' \"$@\" > '" + argsFile + "'\nprintf '{}\\n'\n"
			if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			r := &Runtime{}
			task := model.Task{ID: "TASK-host", Title: "host worker", Risk: model.R2}
			socket, cleanup, err := r.startProviderEgress(t.Context(), "RUN-host", "", tc.provider, tc.model, task, "worker", "session", model.RoleDeveloper)
			if err != nil || socket != "" || cleanup == nil {
				t.Fatalf("host egress: socket=%q cleanup=%v err=%v", socket, cleanup != nil, err)
			}
			defer cleanup()
			worktree := t.TempDir()
			ctx := context.Context(t.Context())
			if tc.provider == "agy" {
				if err := privateHarnessProbeWorktree(tc.provider, worktree); err != nil {
					t.Fatal(err)
				}
				ctx = markHarnessVerification(ctx)
			}
			client, _, err := r.resolveAdapter(ctx, tc.provider, task, worktree, "worker", true, tc.model, socket)
			if err != nil {
				t.Fatal(err)
			}
			// The host worker still has its scan identity: hand-in, recovery
			// and provider-failure checks refuse a worktree without one.
			r.honeypotMu.Lock()
			trap := r.honeypots[worktree]
			r.honeypotMu.Unlock()
			if trap == nil {
				t.Fatal("host worker launched without an armed honeypot scan identity")
			}
			if tc.provider == "agy" {
				if _, err := client.Probe(ctx); err != nil {
					t.Fatal(err)
				}
			}
			result, err := client.Run(ctx, adapter.Request{TaskID: task.ID, Title: task.Title, Worktree: worktree, Model: tc.model})
			if err != nil {
				t.Fatal(err)
			}
			if result.Isolation.Level != model.IsolationProcessOnly {
				t.Fatalf("worker isolation = %s", result.Isolation.Level)
			}
			data, err := os.ReadFile(argsFile)
			if err != nil {
				t.Fatalf("adapter did not launch host CLI: %v", err)
			}
			argv := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(argv) == 0 || argv[0] != tc.first || !strings.Contains(string(data), tc.flag) || tc.provider == "agy" && !strings.Contains(string(data), "--print") {
				t.Fatalf("unexpected %s argv: %q", tc.provider, argv)
			}
		})
	}
}

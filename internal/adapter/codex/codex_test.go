package codex

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/slaves/internal/adapter"
)

type capturingRunner struct {
	command adapter.Command
	result  adapter.ProcessResult
}

func (r *capturingRunner) Run(_ context.Context, command adapter.Command) (adapter.ProcessResult, error) {
	r.command = command
	return r.result, nil
}

func TestProbeRequiresCurrentNativeFlags(t *testing.T) {
	binary := fakeProbeBinary(t, true)
	client := New(binary, &capturingRunner{})
	probe, err := client.Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !probe.Available || probe.Version != "codex-cli 0.test.0" {
		t.Fatalf("probe = %#v", probe)
	}
}

func TestProbeRejectsCLIWithoutRequiredFlags(t *testing.T) {
	binary := fakeProbeBinary(t, false)
	client := New(binary, &capturingRunner{})
	if _, err := client.Probe(context.Background()); err == nil {
		t.Fatal("probe accepted incompatible CLI help")
	}
}

func TestRunUsesNarrowNativeSurfaceAndNormalizesJSONL(t *testing.T) {
	runner := &capturingRunner{result: adapter.ProcessResult{
		Stdout: []byte("{\"type\":\"thread.started\",\"thread_id\":\"thread-123\"}\n" +
			"{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"done\"}}\n"),
		ExitCode:  0,
		StartedAt: time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC),
		EndedAt:   time.Date(2026, 8, 11, 12, 0, 1, 0, time.UTC),
	}}
	client := New("/usr/bin/codex", runner)
	result, err := client.Run(context.Background(), adapter.Request{
		TaskID: "TASK-001", Title: "change one file", Worktree: "/repo/task",
		BaseCommit: "abc", HeadCommit: "abc",
		AllowedOperations: []string{"filesystem.write", "shell.execute"},
		EvidenceRequired:  []string{"go test ./..."},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"exec", "--json", "-C", "/repo/task", "-s", "workspace-write", "--ephemeral", "--ignore-user-config", "-"}
	if !slices.Equal(runner.command.Args, want) {
		t.Fatalf("args = %#v, want %#v", runner.command.Args, want)
	}
	joined := strings.Join(runner.command.Args, " ")
	for _, forbidden := range []string{"dangerously-bypass", "danger-full-access", "--search"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("forbidden flag %q in %s", forbidden, joined)
		}
	}
	if runner.command.Dir != "/repo/task" || !strings.Contains(string(runner.command.Stdin), "TASK-001") {
		t.Fatalf("command = %#v", runner.command)
	}
	if result.Status != adapter.StatusSuccess || result.SessionID != "thread-123" ||
		result.FinalText != "done" || result.ExitCode != 0 {
		t.Fatalf("result = %#v", result)
	}
}

func TestRunNormalizesNonzeroExitAsFailure(t *testing.T) {
	runner := &capturingRunner{result: adapter.ProcessResult{ExitCode: 7, Stderr: []byte("failed")}}
	result, err := New("/usr/bin/codex", runner).Run(context.Background(), adapter.Request{
		TaskID: "TASK-001", Title: "fail", Worktree: "/repo/task",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != adapter.StatusFailure || result.ExitCode != 7 {
		t.Fatalf("result = %#v", result)
	}
}

func TestRunForwardsHeartbeatToWorker(t *testing.T) {
	runner := &capturingRunner{result: adapter.ProcessResult{ExitCode: 0}}
	heartbeat := func() {}
	_, err := New("/usr/bin/codex", runner).Run(context.Background(), adapter.Request{
		TaskID: "TASK-001", Title: "heartbeat", Worktree: "/repo/task",
		Heartbeat: heartbeat, HeartbeatInterval: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if runner.command.Heartbeat == nil || runner.command.HeartbeatInterval != 5*time.Second {
		t.Fatalf("heartbeat command = %#v", runner.command)
	}
}

func TestRunReportsTruncatedEvidenceAsBlocked(t *testing.T) {
	runner := &capturingRunner{result: adapter.ProcessResult{
		ExitCode: 0, OutputTruncated: true,
	}}
	result, err := New("/usr/bin/codex", runner).Run(context.Background(), adapter.Request{
		TaskID: "TASK-001", Title: "truncated", Worktree: "/repo/task",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != adapter.StatusBlocked {
		t.Fatalf("status = %s, want blocked", result.Status)
	}
}

func fakeProbeBinary(t *testing.T, compatible bool) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "codex")
	help := "--json --sandbox --ephemeral --ignore-user-config --cd"
	if !compatible {
		help = "--json"
	}
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"--version\" ]; then echo 'codex-cli 0.test.0'; exit 0; fi\n" +
		"if [ \"$1\" = \"exec\" ] && [ \"$2\" = \"--help\" ]; then echo '" + help + "'; exit 0; fi\n" +
		"exit 2\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

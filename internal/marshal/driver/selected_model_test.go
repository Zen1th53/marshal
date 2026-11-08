package driver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Run a real supervised CLI process, rather than only inspecting Args.
func TestNativeLaunchResolvedModelReachesEveryProvider(t *testing.T) {
	for _, constructor := range []func(string) Native{Codex, Claude, Agy, OpenCode} {
		t.Run(constructor("").Provider, func(t *testing.T) {
			task, wt := newTask(t)
			capture := filepath.Join(t.TempDir(), "argv")
			binary := filepath.Join(t.TempDir(), "provider")
			t.Setenv("MARSHAL_TEST_ARGV", capture)
			if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$MARSHAL_TEST_ARGV\"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			native := constructor(binary)
			native.ResolveModel = func(context.Context, Request) (string, error) { return "selected-verified", nil }
			handle, err := native.Launch(t.Context(), Request{Task: task, Worktree: wt, Brief: "work", Model: "stale-request"})
			if err != nil {
				t.Fatal(err)
			}
			<-handle.done
			raw, err := os.ReadFile(capture)
			if err != nil {
				t.Fatal(err)
			}
			argv := strings.Split(strings.TrimSpace(string(raw)), "\n")
			flag := "--model"
			if native.Provider == "opencode" {
				flag = "-m"
			}
			index := slices.Index(argv, flag)
			if index < 0 || index+1 >= len(argv) || argv[index+1] != "selected-verified" || slices.Contains(argv, "stale-request") {
				t.Fatalf("selected model lost: %q", argv)
			}
		})
	}
}

func TestNativeModelResolutionFailureNeverStartsProvider(t *testing.T) {
	task, wt := newTask(t)
	for _, empty := range []bool{false, true} {
		sentinel := errors.New("profile expired")
		native := Agy("must-not-execute")
		native.ResolveModel = func(context.Context, Request) (string, error) {
			if empty {
				return "", nil
			}
			return "", sentinel
		}
		_, err := native.Launch(t.Context(), Request{Task: task, Worktree: wt, Brief: "work"})
		if err == nil || (!empty && !errors.Is(err, sentinel)) {
			t.Fatalf("invalid resolution reached process launch: %v", err)
		}
	}
}

package sandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/slaves/internal/model"
)

func TestWrapBindsOnlyDeclaredWritablePathsAndDeniesNetwork(t *testing.T) {
	worktree := t.TempDir()
	scratch := t.TempDir()
	auth := t.TempDir()
	backend := NewBwrap("/sbin/bwrap")
	spec, err := backend.Wrap(model.SandboxRequest{
		Worktree:       worktree,
		WritableDirs:   []string{scratch},
		ReadOnlyBinds:  []model.Bind{{Source: auth, Target: "/home/slaves/.codex"}},
		NetworkAllowed: false,
	}, []string{"/usr/bin/codex", "exec"})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Isolation.Level != model.IsolationBwrap || spec.Isolation.Network {
		t.Fatalf("isolation = %#v", spec.Isolation)
	}
	for _, required := range []string{
		"--unshare-net", "--unshare-pid", "--tmpfs", "/tmp",
		"/home/slaves/.codex",
		"--bind", worktree, worktree, "--bind", scratch, scratch,
		"--ro-bind", auth, "/home/slaves/.codex", "--", "/usr/bin/codex", "exec",
	} {
		if !slices.Contains(spec.Args, required) {
			t.Fatalf("args missing %q: %#v", required, spec.Args)
		}
	}
	if slices.Contains(spec.Args, ".slaves/runtime.sock") {
		t.Fatalf("runtime socket exposed: %#v", spec.Args)
	}
	authTarget := slices.Index(spec.Args, "/home/slaves/.codex")
	if authTarget < 1 || spec.Args[authTarget-1] != "--dir" {
		t.Fatalf("Codex auth target parent is not created before binding: %#v", spec.Args)
	}
}

func TestChooseIsolationNeverSilentlyDropsNetworkDenialOrStrongRisk(t *testing.T) {
	unavailable := model.IsolationCapability{Level: model.IsolationProcessOnly, Available: false, Reason: "missing"}
	tests := []struct {
		name           string
		risk           model.Risk
		networkAllowed bool
		wantLevel      model.IsolationLevel
		wantErr        bool
	}{
		{name: "low risk network allowed", risk: model.R1, networkAllowed: true, wantLevel: model.IsolationProcessOnly},
		{name: "low risk network denied", risk: model.R1, networkAllowed: false, wantLevel: model.IsolationBlocked, wantErr: true},
		{name: "high risk", risk: model.R2, networkAllowed: true, wantLevel: model.IsolationBlocked, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ChooseIsolation(unavailable, tt.risk, tt.networkAllowed)
			if (err != nil) != tt.wantErr || got.Level != tt.wantLevel {
				t.Fatalf("got=%#v err=%v", got, err)
			}
		})
	}
}

func TestProbeExecutesNamespaceShapeNotOnlyVersion(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	fake := filepath.Join(dir, "bwrap")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"" + argsFile + "\"\nexit 0\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	backend := NewBwrap(fake)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	capability := backend.Probe(ctx)
	if !capability.Available || capability.Level != model.IsolationBwrap {
		t.Fatalf("capability = %#v", capability)
	}
	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	args := string(data)
	for _, required := range []string{"--unshare-pid", "--ro-bind", "/usr/bin/true"} {
		if !strings.Contains(args, required) {
			t.Fatalf("probe args missing %q: %s", required, args)
		}
	}
}

func TestWrapRejectsMissingPathsAndCommand(t *testing.T) {
	backend := NewBwrap("/sbin/bwrap")
	if _, err := backend.Wrap(model.SandboxRequest{Worktree: "/missing"}, []string{"true"}); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("missing worktree error = %v", err)
	}
	if _, err := backend.Wrap(model.SandboxRequest{Worktree: t.TempDir()}, nil); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("empty command error = %v", err)
	}
}

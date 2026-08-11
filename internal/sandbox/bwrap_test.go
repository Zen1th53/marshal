package sandbox

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/slaves/internal/model"
)

func TestBwrapExecutesInsideWritableWorktree(t *testing.T) {
	binary, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skip("bwrap unavailable")
	}
	backend := NewBwrap(binary)
	if capability := backend.Probe(context.Background()); !capability.Available {
		t.Skip(capability.Reason)
	}
	worktree := t.TempDir()
	spec, err := backend.Wrap(model.SandboxRequest{Worktree: worktree, NetworkAllowed: false}, []string{"/bin/sh", "-c", "printf proof > runtime-proof"})
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(spec.Path, spec.Args...)
	command.Env = spec.Env
	command.Dir = spec.Dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("bwrap: %v: %s", err, output)
	}
	data, err := os.ReadFile(filepath.Join(worktree, "runtime-proof"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "proof" {
		t.Fatalf("proof = %q", data)
	}
}

func TestWrapBindsOnlyDeclaredWritablePathsAndDeniesNetwork(t *testing.T) {
	worktree := t.TempDir()
	scratch := t.TempDir()
	auth := t.TempDir()
	gitMetadata := t.TempDir()
	backend := NewBwrap("/sbin/bwrap")
	spec, err := backend.Wrap(model.SandboxRequest{
		Worktree:     worktree,
		WritableDirs: []string{scratch},
		ReadOnlyBinds: []model.Bind{
			{Source: auth, Target: "/home/slaves/.codex"},
			{Source: gitMetadata, Target: gitMetadata},
		},
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
		gitMetadata,
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

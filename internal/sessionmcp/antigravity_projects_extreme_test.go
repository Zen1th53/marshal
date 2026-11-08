//go:build !governed && linux

package sessionmcp_test

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/sandbox"
	"github.com/Zen1th53/marshal/internal/sessionmcp"
)

func agyProjectsRequest(t *testing.T) model.SandboxRequest {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".agents"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, done, err := sessionmcp.AntigravityProbeContext(t.Context(), root, "PROBE-project-cache")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(done)
	req, err := sessionmcp.SandboxRequest(ctx, model.SandboxRequest{Worktree: root})
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestAntigravityProjectBookkeepingRealBwrap(t *testing.T) {
	if !bwrapListsSize(t) {
		t.Skip("installed bubblewrap lacks --size; the exact 16 MiB bound is asserted only where it can apply (fallback: TestAntigravityProjectCacheFallsBackWithoutBwrapSize)")
	}
	req := agyProjectsRequest(t)
	const command = `set -eu
test "$(cat /home/marshal/.gemini/config/mcp_config.json)" = '{"mcpServers":{}}'
test "$(( $(stat -f -c %b /home/marshal/.gemini/config/projects) * $(stat -f -c %S /home/marshal/.gemini/config/projects) ))" -eq 16777216
printf ordinary > ordinary.txt
printf home > /home/marshal/ordinary.txt
mkdir /home/marshal/.gemini/config/projects/synthetic
printf '{"id":"synthetic","name":"synthetic","projectResources":{"resources":[]}}' > /home/marshal/.gemini/config/projects/synthetic/project.json
test -s /home/marshal/.gemini/config/projects/synthetic/project.json
if printf foreign > /home/marshal/.gemini/config/mcp_config.json; then exit 21; fi
if printf foreign > /home/marshal/.gemini/config/unrelated.json; then exit 22; fi
if mv /home/marshal/.gemini/config /home/marshal/.gemini/config-old; then exit 23; fi
if mv /home/marshal/.gemini/config/projects /home/marshal/.gemini/config/projects-old; then exit 24; fi
ln -s ../mcp_config.json /home/marshal/.gemini/config/projects/escape
if printf foreign > /home/marshal/.gemini/config/projects/escape; then exit 25; fi
test "$(cat /home/marshal/.gemini/config/mcp_config.json)" = '{"mcpServers":{}}'
printf PROJECT_CACHE_OK`
	spec, err := sandbox.NewBwrap("/usr/bin/bwrap").Wrap(req, []string{"/bin/sh", "-c", command})
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(t.Context(), spec.Path, spec.Args...).CombinedOutput()
	if err != nil {
		t.Fatalf("project cache unavailable despite sealed MCP: %v %s", err, out)
	}
	for _, bind := range req.ReadOnlyBinds {
		if bind.Target == "/home/marshal/.gemini/config" {
			entries, err := os.ReadDir(filepath.Join(bind.Source, "projects"))
			if err != nil || len(entries) != 0 {
				t.Fatal("worker project bookkeeping leaked into generated host configuration")
			}
		}
	}
	if body, err := os.ReadFile(filepath.Join(req.Worktree, "ordinary.txt")); err != nil || string(body) != "ordinary" {
		t.Fatal("ordinary workspace writes unavailable")
	}
}

func TestAntigravityProjectCacheRejectsForeignProviderSeal(t *testing.T) {
	t.Setenv("MARSHAL_MCP_BINARY", "/bin/true")
	req := agyProjectsRequest(t)
	ctx, err := sessionmcp.WithBoundWorker(t.Context(), req.Worktree, "TASK-other-provider", func(context.Context, io.Reader, io.Writer) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	_, _, done, err := sessionmcp.Configure(ctx, "codex", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	if _, err := sessionmcp.SandboxRequest(ctx, req); err == nil {
		t.Fatal("Codex context accepted AGY project-cache authority")
	}
	if _, err := sessionmcp.SandboxRequest(context.Background(), req); err == nil {
		t.Fatal("ordinary context accepted AGY project-cache authority")
	}
}

func TestAntigravityProjectCacheRefusesChangedGeneratedParent(t *testing.T) {
	for _, kind := range []string{"symlink", "missing", "nonempty", "replaced", "retimestamped"} {
		t.Run(kind, func(t *testing.T) {
			req := agyProjectsRequest(t)
			var parent string
			for _, bind := range req.ReadOnlyBinds {
				if bind.Target == "/home/marshal/.gemini/config" {
					parent = filepath.Join(bind.Source, "projects")
				}
			}
			// RED intentionally requires the trusted generated parent to exist.
			if _, err := os.Stat(parent); err != nil {
				t.Fatalf("generated native project parent absent: %v", err)
			}
			if kind == "retimestamped" {
				info, err := os.Stat(parent)
				if err != nil {
					t.Fatal(err)
				}
				changed := info.ModTime().Add(time.Second)
				if err := os.Chtimes(parent, changed, changed); err != nil {
					t.Fatal(err)
				}
			} else if kind == "nonempty" {
				if err := os.WriteFile(filepath.Join(parent, "foreign.json"), []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			} else if kind == "replaced" {
				// Allocate the replacement while the original still exists, so
				// it is a different directory. Removing first and then making
				// an identical empty directory can reuse the inode within one
				// timestamp tick: nothing observable then tells them apart
				// (and nothing about the empty mountpoint changed).
				replacement := parent + ".replacement"
				if err := os.Mkdir(replacement, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(parent); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(replacement, parent); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Remove(parent); err != nil {
					t.Fatal(err)
				}
				if kind == "symlink" {
					if err := os.Symlink(t.TempDir(), parent); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := sandbox.NewBwrap("/usr/bin/bwrap").Wrap(req, []string{"/bin/true"}); err == nil {
				t.Fatal("changed generated project parent accepted")
			}
		})
	}
}

func TestAntigravityProjectCacheRejectsCallerMountOverrides(t *testing.T) {
	for _, kind := range []string{"tmpfs", "tmpfs_ancestor", "tmpfs_sibling", "writable", "readonly", "readonly_parent"} {
		t.Run(kind, func(t *testing.T) {
			req := agyProjectsRequest(t)
			const target = "/home/marshal/.gemini/config/projects"
			switch kind {
			case "tmpfs":
				req.WritableTmpfs = append(req.WritableTmpfs, target)
			case "tmpfs_ancestor":
				req.WritableTmpfs = append(req.WritableTmpfs, "/home/marshal/.gemini/config")
			case "tmpfs_sibling":
				req.WritableTmpfs = append(req.WritableTmpfs, "/home/marshal/.gemini/config/other")
			case "writable":
				for _, b := range req.ReadOnlyBinds {
					if b.Target == "/home/marshal/.gemini/config" {
						req.WritableDirs = append(req.WritableDirs, filepath.Join(b.Source, "projects"))
					}
				}
				// A writable bind of the generated source must not gain authority.
			case "readonly":
				req.ReadOnlyBinds = append(req.ReadOnlyBinds, model.Bind{Source: t.TempDir(), Target: target})
			case "readonly_parent":
				req.ReadOnlyBinds = append(req.ReadOnlyBinds, model.Bind{Source: req.Worktree, Target: req.Worktree})
			}
			if _, err := sandbox.NewBwrap("/usr/bin/bwrap").Wrap(req, []string{"/bin/true"}); err == nil {
				t.Fatal("caller mount gained native project authority")
			}
		})
	}
}

//go:build !governed && linux

package sessionmcp_test

import (
	"context"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/sandbox"
	"github.com/Zen1th53/marshal/internal/sessionmcp"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAntigravityWorkspaceConfigurationRealBwrap(t *testing.T) {
	t.Setenv("MARSHAL_MCP_BINARY", "/bin/true")
	for _, kind := range []string{"present", "absent_file", "absent_parent"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			agents := filepath.Join(root, ".agents")
			target := filepath.Join(agents, "mcp_config.json")
			if kind != "absent_parent" {
				if e := os.Mkdir(agents, 0700); e != nil {
					t.Fatal(e)
				}
			}
			original := `{"mcpServers":{"foreign":{"command":"/forbidden"}}}`
			if kind == "present" {
				if e := os.WriteFile(target, []byte(original), 0600); e != nil {
					t.Fatal(e)
				}
			}
			ctx, e := sessionmcp.WithBoundWorker(t.Context(), root, "TASK-test", func(context.Context, io.Reader, io.Writer) error { return nil })
			if e != nil {
				t.Fatal(e)
			}
			_, _, cleanup, e := sessionmcp.Configure(ctx, "agy", nil, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer cleanup()
			req, e := sessionmcp.SandboxRequest(ctx, model.SandboxRequest{Worktree: root})
			if kind == "absent_parent" {
				if e == nil {
					t.Fatal("absent configuration parent accepted without containment")
				}
				if _, e = os.Stat(agents); !os.IsNotExist(e) {
					t.Fatal("absent parent created")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			args := []string{"-c", "cat \"$1\"; printf intrusion > \"$1\"", "sh", target}
			spec, e := sandbox.NewBwrap("/usr/bin/bwrap").Wrap(req, append([]string{"/bin/sh"}, args...))
			if e != nil {
				t.Fatal(e)
			}
			cmd := exec.CommandContext(t.Context(), spec.Path, spec.Args...)
			out, e := cmd.CombinedOutput()
			if e == nil {
				t.Fatal("native worker modified protected MCP path")
			}
			if kind == "present" {
				if len(out) < len(`{"mcpServers":{}}`) || string(out[:len(`{"mcpServers":{}}`)]) != `{"mcpServers":{}}` {
					t.Fatal("generated empty MCP not observed")
				}
				raw, e := os.ReadFile(target)
				if e != nil || string(raw) != original {
					t.Fatal("host configuration changed")
				}
			} else {
				if _, e = os.Stat(target); !os.IsNotExist(e) {
					t.Fatal("mask added absent host target")
				}
			}
		})
	}
}

func TestAntigravityConfigurationParentsCannotBeReplaced(t *testing.T) {
	t.Setenv("MARSHAL_MCP_BINARY", "/bin/true")
	for _, target := range []string{"workspace", "global", "gemini", "home", "home_root", "workspace_scratch", "global_scratch", "gemini_scratch", "home_scratch", "home_root_scratch"} {
		t.Run(target, func(t *testing.T) {
			root := t.TempDir()
			agents := filepath.Join(root, ".agents")
			if e := os.Mkdir(agents, 0700); e != nil {
				t.Fatal(e)
			}
			original := []byte(`{"mcpServers":{"foreign":{"command":"/forbidden"}}}`)
			host := filepath.Join(agents, "mcp_config.json")
			if e := os.WriteFile(host, original, 0600); e != nil {
				t.Fatal(e)
			}
			ctx, e := sessionmcp.WithBoundWorker(t.Context(), root, "TASK-parent", func(context.Context, io.Reader, io.Writer) error { return nil })
			if e != nil {
				t.Fatal(e)
			}
			_, _, cleanup, e := sessionmcp.Configure(ctx, "agy", nil, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer cleanup()
			base := model.SandboxRequest{Worktree: root}
			trapCheck := ""
			if strings.HasSuffix(target, "_scratch") {
				home := t.TempDir()
				base.ScratchHome = home
				for _, rel := range []string{".gemini/.keep", ".aws/credentials", ".config/gh/hosts.yml", ".env"} {
					source := filepath.Join(home, rel)
					if e := os.MkdirAll(filepath.Dir(source), 0700); e != nil {
						t.Fatal(e)
					}
					if e := os.WriteFile(source, []byte("synthetic-unissued-fixture"), 0600); e != nil {
						t.Fatal(e)
					}
				}
				trapCheck = `for p in /home/marshal/.aws/credentials /home/marshal/.config/gh/hosts.yml /home/marshal/.env; do test -f "$p" && test -r "$p"; done; `
			}
			req, e := sessionmcp.SandboxRequest(ctx, base)
			if e != nil {
				t.Fatal(e)
			}
			target = strings.TrimSuffix(target, "_scratch")
			path := agents
			if target == "global" {
				path = "/home/marshal/.gemini/config"
			}
			if target == "gemini" {
				path = "/home/marshal/.gemini"
			}
			if target == "home" {
				path = "/home/marshal"
			}
			if target == "home_root" {
				path = "/home"
			}
			control, e := sandbox.NewBwrap("/usr/bin/bwrap").Wrap(req, []string{"/bin/sh", "-c", `set -e; printf ordinary > ordinary.txt; printf home > /home/marshal/ordinary-home.txt; test "$(cat /home/marshal/ordinary-home.txt)" = home; ` + trapCheck + `cat "$1"; cat /home/marshal/.gemini/config/mcp_config.json`, "sh", host})
			if e != nil {
				t.Fatal(e)
			}
			output, e := exec.CommandContext(t.Context(), control.Path, control.Args...).CombinedOutput()
			if e != nil || !strings.HasPrefix(string(output), `{"mcpServers":{}}`) || !strings.Contains(string(output), "marshal") {
				t.Fatalf("control launch/config read failed: %v %s", e, output)
			}
			ordinary, e := os.ReadFile(filepath.Join(root, "ordinary.txt"))
			if e != nil || string(ordinary) != "ordinary" {
				t.Fatal("ordinary workspace writes unavailable")
			}
			command := []string{"/bin/sh", "-c", `mv "$1" "$1-old" && mkdir "$1" && printf intrusion > "$1/mcp_config.json"`, "sh", path}
			spec, e := sandbox.NewBwrap("/usr/bin/bwrap").Wrap(req, command)
			if e != nil {
				t.Fatal(e)
			}
			out, e := exec.CommandContext(t.Context(), spec.Path, spec.Args...).CombinedOutput()
			if e == nil {
				t.Fatalf("configuration parent replaced: %s", out)
			}
			raw, e := os.ReadFile(host)
			if e != nil || string(raw) != string(original) {
				t.Fatal("host workspace configuration changed")
			}
		})
	}
}

func TestAntigravityEnvelopePreservesExistingTrapPathsAndSyntheticToken(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".agents"), 0700); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	for _, path := range []string{".aws/credentials", ".config/gh/hosts.yml", ".env", ".gemini/antigravity-cli/antigravity-oauth-token"} {
		source := filepath.Join(home, path)
		if err := os.MkdirAll(filepath.Dir(source), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(source, []byte("synthetic-unissued-fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cleanup, err := sessionmcp.AntigravityProbeContext(t.Context(), root, "PROBE-traps")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	token := filepath.Join(home, ".gemini", "antigravity-cli", "antigravity-oauth-token")
	req, err := sessionmcp.SandboxRequest(ctx, model.SandboxRequest{Worktree: root, ScratchHome: home, WritableTmpfs: []string{"/home/marshal/.gemini/antigravity-cli"}, ReadOnlyBinds: []model.Bind{{Source: token, Target: "/home/marshal/.gemini/antigravity-cli/antigravity-oauth-token"}}})
	if err != nil {
		t.Fatal(err)
	}
	command := []string{"/bin/sh", "-c", `for p in /home/marshal/.aws/credentials /home/marshal/.config/gh/hosts.yml /home/marshal/.env /home/marshal/.gemini/antigravity-cli/antigravity-oauth-token; do test -f "$p" && test -r "$p" || exit 9; done; test -z "$DBUS_SESSION_BUS_ADDRESS"; cat /home/marshal/.gemini/config/mcp_config.json`}
	spec, err := sandbox.NewBwrap("/usr/bin/bwrap").Wrap(req, command)
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.CommandContext(t.Context(), spec.Path, spec.Args...).CombinedOutput()
	if err != nil || string(output) != `{"mcpServers":{}}` {
		t.Fatalf("trap/token/config existence control failed: %v %s", err, output)
	}
}

func TestAntigravitySealRejectsMountReordering(t *testing.T) {
	t.Setenv("MARSHAL_MCP_BINARY", "/bin/true")
	root := t.TempDir()
	agents := filepath.Join(root, ".agents")
	if e := os.Mkdir(agents, 0700); e != nil {
		t.Fatal(e)
	}
	target := filepath.Join(agents, "mcp_config.json")
	if e := os.WriteFile(target, []byte(`{"mcpServers":{"foreign":{"command":"/forbidden"}}}`), 0600); e != nil {
		t.Fatal(e)
	}
	ctx, e := sessionmcp.WithBoundWorker(t.Context(), root, "TASK-reordered", func(context.Context, io.Reader, io.Writer) error { return nil })
	if e != nil {
		t.Fatal(e)
	}
	_, _, cleanup, e := sessionmcp.Configure(ctx, "agy", nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer cleanup()
	req, e := sessionmcp.SandboxRequest(ctx, model.SandboxRequest{Worktree: root})
	if e != nil {
		t.Fatal(e)
	}
	parent, file := -1, -1
	for i, b := range req.ReadOnlyBinds {
		if b.Target == agents {
			parent = i
		}
		if b.Target == target {
			file = i
		}
	}
	req.ReadOnlyBinds[parent], req.ReadOnlyBinds[file] = req.ReadOnlyBinds[file], req.ReadOnlyBinds[parent]
	spec, e := sandbox.NewBwrap("/usr/bin/bwrap").Wrap(req, []string{"/bin/cat", target})
	if e != nil {
		return
	}
	output, e := exec.CommandContext(t.Context(), spec.Path, spec.Args...).CombinedOutput()
	if e != nil {
		t.Fatalf("RED must launch before exposing imported MCP: %v %s", e, output)
	}
	t.Fatalf("reordered mount graph accepted and exposed workspace configuration: %s", output)
}

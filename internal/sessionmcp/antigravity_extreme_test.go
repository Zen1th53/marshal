//go:build !governed && unix

package sessionmcp_test

import (
	"context"
	"encoding/json"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/sandbox"
	"github.com/Zen1th53/marshal/internal/sessionmcp"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAntigravityFreshScopedConfigurationMasksWorkspace(t *testing.T) {
	t.Setenv("MARSHAL_MCP_BINARY", "/bin/true")
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".agents"), 0700); err != nil {
		t.Fatal(err)
	}
	hostile := filepath.Join(root, ".agents", "mcp_config.json")
	sentinel := `{"mcpServers":{"foreign":{"command":"/host-auth-reader"}}}`
	if err := os.WriteFile(hostile, []byte(sentinel), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, e := sessionmcp.WithBoundWorker(t.Context(), root, "TASK-current", func(context.Context, io.Reader, io.Writer) error { return nil })
	if e != nil {
		t.Fatal(e)
	}
	_, env, cleanup, e := sessionmcp.Configure(ctx, "agy", []string{"--print", "work"}, []string{"UNTRUSTED_COMMAND_ENV=not-forwarded"})
	if e != nil {
		t.Fatal(e)
	}
	defer cleanup()
	if len(env) != 0 {
		t.Fatal("unexpected generic environment forwarding")
	}
	request, e := sessionmcp.SandboxRequest(ctx, model.SandboxRequest{Worktree: root})
	if e != nil {
		t.Fatal(e)
	}
	targets := map[string]string{}
	for _, b := range request.ReadOnlyBinds {
		targets[b.Target] = b.Source
	}
	for _, target := range []string{"/home/marshal/.gemini/antigravity-cli/settings.json", "/home/marshal/.gemini/config", hostile} {
		if targets[target] == "" {
			t.Fatalf("missing governed config bind %s", target)
		}
	}
	var cfg map[string]map[string]any
	raw, e := os.ReadFile(targets["/home/marshal/.gemini/config"] + "/mcp_config.json")
	if e != nil || json.Unmarshal(raw, &cfg) != nil || len(cfg["mcpServers"]) != 1 || cfg["mcpServers"]["marshal"] == nil || strings.Contains(string(raw), "foreign") {
		t.Fatal("host/workspace MCP imported")
	}
	raw, e = os.ReadFile(targets[hostile])
	if e != nil || strings.Contains(string(raw), "foreign") {
		t.Fatal("workspace MCP not masked")
	}
	raw, e = os.ReadFile(hostile)
	if e != nil || string(raw) != sentinel {
		t.Fatal("repository MCP mutated")
	}
	cleanup()
	for target, source := range targets {
		if strings.Contains(target, ".gemini") || target == hostile {
			if _, e := os.Stat(source); !os.IsNotExist(e) {
				t.Fatal("generated config survived cleanup")
			}
		}
	}
}

func TestAntigravityConfigurationSealRefusesForeignAndChangedSources(t *testing.T) {
	t.Setenv("MARSHAL_MCP_BINARY", "/bin/true")
	for _, variant := range []string{"settings", "global", "workspace", "foreign_worktree", "foreign_target", "symlink", "foreign_scope", "duplicate", "missing_parent_mount", "missing_home_mount", "late_worktree", "late_home", "late_tmpfs", "writable_configuration"} {
		t.Run(variant, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, ".agents")
			if e := os.Mkdir(dir, 0700); e != nil {
				t.Fatal(e)
			}
			target := filepath.Join(dir, "mcp_config.json")
			if e := os.WriteFile(target, []byte(`{"mcpServers":{}}`), 0600); e != nil {
				t.Fatal(e)
			}
			ctx, e := sessionmcp.WithBoundWorker(t.Context(), root, "TASK-seal", func(context.Context, io.Reader, io.Writer) error { return nil })
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
			sources := map[string]string{}
			for _, bind := range req.ReadOnlyBinds {
				sources[bind.Target] = bind.Source
			}
			switch variant {
			case "missing_parent_mount":
				var kept []model.Bind
				for _, b := range req.ReadOnlyBinds {
					if b.Target != dir {
						kept = append(kept, b)
					}
				}
				req.ReadOnlyBinds = kept
			case "missing_home_mount":
				req.WritableTmpfs = nil
			case "late_worktree":
				req.ReadOnlyBinds = append(req.ReadOnlyBinds, model.Bind{Source: root, Target: root})
			case "late_home":
				req.ReadOnlyBinds = append(req.ReadOnlyBinds, model.Bind{Source: t.TempDir(), Target: "/home/marshal"})
			case "writable_configuration":
				req.WritableDirs = []string{dir}
			case "late_tmpfs":
				req.WritableTmpfs = append(req.WritableTmpfs, "/home")
			case "settings":
				e = os.WriteFile(sources["/home/marshal/.gemini/antigravity-cli/settings.json"], []byte(`{"toolPermission":"allow"}`), 0600)
			case "global":
				e = os.WriteFile(filepath.Join(sources["/home/marshal/.gemini/config"], "mcp_config.json"), []byte(`{"mcpServers":{"foreign":{}}}`), 0600)
			case "workspace":
				e = os.WriteFile(sources[target], []byte(`{"mcpServers":{"foreign":{}}}`), 0600)
			case "foreign_worktree":
				req.Worktree = t.TempDir()
			case "foreign_target":
				req.ReadOnlyBinds[len(req.ReadOnlyBinds)-1].Target = filepath.Join(root, "other.json")
			case "symlink":
				e = os.Remove(target)
				if e == nil {
					e = os.Symlink(sources[target], target)
				}
			case "foreign_scope":
				other := t.TempDir()
				e = os.Mkdir(filepath.Join(other, ".agents"), 0700)
				if e != nil {
					t.Fatal(e)
				}
				otherCtx, clean, e := sessionmcp.AntigravityProbeContext(t.Context(), other, "PROBE-other")
				if e != nil {
					t.Fatal(e)
				}
				defer clean()
				otherReq, e := sessionmcp.SandboxRequest(otherCtx, model.SandboxRequest{Worktree: other})
				if e != nil {
					t.Fatal(e)
				}
				_, e = sessionmcp.SandboxRequest(ctx, model.SandboxRequest{Worktree: root, WorkerMCPMask: otherReq.WorkerMCPMask})
				if e == nil {
					t.Fatal("foreign invocation seal accepted")
				}
				return
			case "duplicate":
				_, e = sessionmcp.SandboxRequest(ctx, model.SandboxRequest{Worktree: root, ReadOnlyBinds: []model.Bind{{Source: dir, Target: dir}}})
				if e == nil {
					t.Fatal("duplicate workspace configuration accepted")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if _, e := sandbox.NewBwrap("/usr/bin/bwrap").Wrap(req, []string{"/bin/true"}); e == nil {
				t.Fatal("changed or foreign sealed configuration accepted")
			}
		})
	}
}

// Headless agy cannot prompt for a command permission; without an allow rule it
// auto-denied the first command (denied_actions RunCommand) and ended with no
// output. Governed workers run commands inside MARSHAL's own confinement.
func TestAntigravityGovernedSettingsAllowCommandsAndMCP(t *testing.T) {
	t.Setenv("MARSHAL_MCP_BINARY", "/bin/true")
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".agents"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, e := sessionmcp.WithBoundWorker(t.Context(), root, "TASK-commands", func(context.Context, io.Reader, io.Writer) error { return nil })
	if e != nil {
		t.Fatal(e)
	}
	_, _, cleanup, e := sessionmcp.Configure(ctx, "agy", []string{"--print", "work"}, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer cleanup()
	request, e := sessionmcp.SandboxRequest(ctx, model.SandboxRequest{Worktree: root})
	if e != nil {
		t.Fatal(e)
	}
	source := ""
	for _, b := range request.ReadOnlyBinds {
		if b.Target == "/home/marshal/.gemini/antigravity-cli/settings.json" {
			source = b.Source
		}
	}
	raw, e := os.ReadFile(source)
	if e != nil {
		t.Fatalf("generated settings unreadable: %v", e)
	}
	var settings struct {
		ToolPermission string `json:"toolPermission"`
		Permissions    struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
	}
	if json.Unmarshal(raw, &settings) != nil || settings.ToolPermission != "proceed-in-sandbox" {
		t.Fatalf("governed settings lost their sandbox permission mode: %s", raw)
	}
	allowed := strings.Join(settings.Permissions.Allow, " ")
	if !strings.Contains(allowed, "mcp(marshal/*)") || !strings.Contains(allowed, "command(*)") {
		t.Fatalf("governed settings do not allow the bound MCP and commands: %s", raw)
	}
	if !request.WorkerMCPMask.ValidEnvelope(request) {
		t.Fatal("the sealed mount envelope refuses the generated settings")
	}
}

//go:build !governed && unix

package sessionmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Zen1th53/marshal/internal/model"
	"os"
	"path/filepath"
)

func ConfigureBoundAntigravity(ctx context.Context, args []string, task, worktree string) ([]string, []string, func(), error) {
	s, _ := ctx.Value(key{}).(*scope)
	if s == nil || s.server == nil {
		return nil, nil, func() {}, fmt.Errorf("%w: governed Antigravity requires a bound worker MCP host", model.ErrUnavailable)
	}
	resolved, err := filepath.EvalSymlinks(worktree)
	if err != nil || resolved != worktree || !filepath.IsAbs(worktree) || filepath.Clean(worktree) != worktree || task != s.task {
		return nil, nil, func() {}, fmt.Errorf("%w: foreign Antigravity invocation identity", model.ErrConflict)
	}
	configured, env, cleanup, err := Configure(ctx, "agy", args, nil)
	if err != nil {
		return nil, nil, cleanup, err
	}
	s.mu.Lock()
	s.bridge.agyWorktree = worktree
	s.mu.Unlock()
	return configured, env, cleanup, nil
}

func configureAntigravityFiles(b *bridge, binary string, args []string, probe bool) error {
	servers := map[string]any{}
	if !probe {
		servers["marshal"] = map[string]any{"command": binary, "args": args}
	}
	config, _ := json.Marshal(map[string]any{"mcpServers": servers})
	if err := os.Mkdir(filepath.Join(b.dir, "global"), 0700); err != nil {
		return err
	}
	// Native project bookkeeping and preferences live in invocation-only tmpfs. The generated
	// empty mountpoint carries no prior account, project or worker authority.
	if err := os.Mkdir(filepath.Join(b.dir, "global", "projects"), 0700); err != nil {
		return err
	}
	settings := map[string]any{"enableTelemetry": false, "notifications": false, "showTips": false, "showFeedbackSurvey": false}
	if !probe {
		settings["toolPermission"] = "proceed-in-sandbox"
		// A governed worker may run commands (its Process 05 operations
		// include shell.execute) inside MARSHAL's own confinement: no network,
		// writes only to its worktree. Headless agy cannot prompt, so without
		// this rule it auto-denied the first command and ended with no output.
		settings["permissions"] = map[string]any{"allow": []string{"mcp(marshal/*)", "command(*)"}}
	}
	data, _ := json.Marshal(settings)
	for name, body := range map[string][]byte{"settings.json": data, "global/mcp_config.json": config, "workspace-mcp.json": []byte(`{"mcpServers":{}}`)} {
		path := filepath.Join(b.dir, name)
		temp, err := os.CreateTemp(b.dir, ".config-")
		if err != nil {
			return err
		}
		tempPath := temp.Name()
		if _, err = temp.Write(body); err == nil {
			err = temp.Sync()
		}
		closeErr := temp.Close()
		if err == nil {
			err = closeErr
		}
		if err == nil {
			err = os.Rename(tempPath, path)
		}
		if err != nil {
			os.Remove(tempPath)
			return err
		}
	}
	b.agyFiles = true
	return nil
}

// AntigravityProbeContext creates configuration only: no relay, FIFO, server,
// task principal or subprocess can receive authority through this scope.
func AntigravityProbeContext(ctx context.Context, worktree, task string) (context.Context, func(), error) {
	if ctx.Err() != nil {
		return ctx, func() {}, ctx.Err()
	}
	if !filepath.IsAbs(worktree) || task == "" {
		return ctx, func() {}, fmt.Errorf("%w: invalid Antigravity probe scope", model.ErrInvalid)
	}
	dir, err := os.MkdirTemp("", "marshal-agy-probe-")
	if err != nil {
		return ctx, func() {}, err
	}
	b := &bridge{dir: dir, agyWorktree: worktree}
	done := func() { os.RemoveAll(dir) }
	if err := configureAntigravityFiles(b, "", nil, true); err != nil {
		done()
		return ctx, func() {}, err
	}
	return context.WithValue(ctx, key{}, &scope{root: worktree, task: task, bridge: b}), done, nil
}

func antigravitySandboxConfiguration(b *bridge, request model.SandboxRequest) (model.SandboxRequest, error) {
	if b.agyWorktree != "" && request.Worktree != b.agyWorktree {
		return model.SandboxRequest{}, fmt.Errorf("%w: foreign Antigravity worktree", model.ErrConflict)
	}
	if !filepath.IsAbs(request.Worktree) || filepath.Clean(request.Worktree) != request.Worktree {
		return model.SandboxRequest{}, fmt.Errorf("%w: invalid Antigravity workspace", model.ErrInvalid)
	}
	for source, target := range map[string]string{"settings.json": "/home/marshal/.gemini/antigravity-cli/settings.json", "global": "/home/marshal/.gemini/config"} {
		for _, bind := range request.ReadOnlyBinds {
			if bind.Target == target {
				return model.SandboxRequest{}, fmt.Errorf("%w: duplicate Antigravity configuration bind", model.ErrInvalid)
			}
		}
		request.ReadOnlyBinds = append(request.ReadOnlyBinds, model.Bind{Source: filepath.Join(b.dir, source), Target: target})
	}

	target := filepath.Join(request.Worktree, ".agents", "mcp_config.json")
	source := filepath.Join(b.dir, "workspace-mcp.json")
	if b.agyMask.Empty() {
		mask, err := model.SealWorkerMCPConfiguration(filepath.Join(b.dir, "global", "mcp_config.json"), source, request.Worktree)
		if err != nil {
			return model.SandboxRequest{}, err
		}
		b.agyMask = mask
	}
	if !request.WorkerMCPMask.Empty() && !request.WorkerMCPMask.Same(b.agyMask) {
		return model.SandboxRequest{}, fmt.Errorf("%w: foreign Antigravity configuration scope", model.ErrConflict)
	}
	if !b.agyMask.Valid(request.Worktree) {
		return model.SandboxRequest{}, fmt.Errorf("%w: stale Antigravity configuration scope", model.ErrConflict)
	}
	request.WorkerMCPMask = b.agyMask
	// Pin the highest mutable native configuration ancestor at a mount boundary.
	// Child tmpfs and exact synthetic token/settings binds follow this mount.
	request.WritableTmpfs = append([]string{"/home/marshal/.gemini"}, request.WritableTmpfs...)
	dir := filepath.Dir(target)
	for _, bind := range request.ReadOnlyBinds {
		if bind.Target == dir || bind.Target == target {
			return model.SandboxRequest{}, fmt.Errorf("%w: duplicate workspace configuration bind", model.ErrInvalid)
		}
	}
	request.ReadOnlyBinds = append(request.ReadOnlyBinds, model.Bind{Source: dir, Target: dir})
	if b.agyMask.WorkspaceConfigPresent() {
		request.ReadOnlyBinds = append(request.ReadOnlyBinds, model.Bind{Source: source, Target: target})
	}
	return request, nil
}

const governedAntigravityRequiresHost = true

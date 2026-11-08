//go:build !governed && unix

package model

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type workerMCPMaskAuthority struct {
	source, globalSource, worktree, target                                   string
	digest, globalDigest                                                     [32]byte
	original, parent                                                         os.FileInfo
	settingsDigest                                                           [32]byte
	sourceInfo, globalInfo, settingsInfo, globalDirectory, projectsDirectory os.FileInfo
}

// SealWorkerMCPConfiguration accepts only generated configuration owned by one
// invocation. Existing workspace files are masked; absent files stay absent.
func SealWorkerMCPConfiguration(globalSource, source, worktree string) (WorkerMCPMask, error) {
	if !canonicalMaskPath(worktree) || !canonicalMaskPath(source) || !canonicalMaskPath(globalSource) || filepath.Dir(source) != filepath.Dir(filepath.Dir(globalSource)) || filepath.Base(globalSource) != "mcp_config.json" || filepath.Base(filepath.Dir(globalSource)) != "global" || maskWithin(worktree, source) {
		return WorkerMCPMask{}, fmt.Errorf("%w: MCP configuration paths must be canonical", ErrInvalid)
	}
	target := filepath.Join(worktree, ".agents", "mcp_config.json")
	parent, err := os.Lstat(filepath.Dir(target))
	if err != nil || !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 {
		return WorkerMCPMask{}, fmt.Errorf("%w: AGY requires an existing regular .agents directory", ErrUnavailable)
	}
	original, err := os.Lstat(target)
	if err != nil && !os.IsNotExist(err) {
		return WorkerMCPMask{}, err
	}
	if err == nil && !original.Mode().IsRegular() {
		return WorkerMCPMask{}, fmt.Errorf("%w: MCP configuration target is not a regular file", ErrInvalid)
	}
	data, err := emptyMaskBody(source)
	if err != nil {
		return WorkerMCPMask{}, err
	}
	global, err := generatedGlobalMCPBody(globalSource)
	if err != nil {
		return WorkerMCPMask{}, err
	}
	settings, err := generatedSettingsBody(filepath.Join(filepath.Dir(source), "settings.json"))
	if err != nil {
		return WorkerMCPMask{}, err
	}
	sourceInfo, err := os.Lstat(source)
	if err != nil {
		return WorkerMCPMask{}, err
	}
	globalInfo, err := os.Lstat(globalSource)
	if err != nil {
		return WorkerMCPMask{}, err
	}
	settingsInfo, err := os.Lstat(filepath.Join(filepath.Dir(source), "settings.json"))
	if err != nil {
		return WorkerMCPMask{}, err
	}
	globalDirectory, err := os.Lstat(filepath.Dir(globalSource))
	if err != nil {
		return WorkerMCPMask{}, err
	}
	projectsDirectory, err := os.Lstat(filepath.Join(filepath.Dir(globalSource), "projects"))
	if err != nil {
		return WorkerMCPMask{}, err
	}
	return WorkerMCPMask{authority: &workerMCPMaskAuthority{source: source, globalSource: globalSource, worktree: worktree, target: target, digest: sha256.Sum256(data), globalDigest: sha256.Sum256(global), original: original, parent: parent, settingsDigest: sha256.Sum256(settings), sourceInfo: sourceInfo, globalInfo: globalInfo, settingsInfo: settingsInfo, globalDirectory: globalDirectory, projectsDirectory: projectsDirectory}}, nil
}
func canonicalMaskPath(path string) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false
	}
	resolved, err := filepath.EvalSymlinks(path)
	return err == nil && resolved == path
}
func generatedBody(path string, bound int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > bound {
		return nil, fmt.Errorf("%w: generated MCP configuration source invalid", ErrInvalid)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, bound+1))
	if err != nil || int64(len(data)) > bound {
		return nil, fmt.Errorf("%w: generated configuration exceeds bound", ErrInvalid)
	}
	return data, nil
}
func emptyMaskBody(path string) ([]byte, error) {
	data, err := generatedBody(path, 64)
	if err != nil || !bytes.Equal(data, []byte(`{"mcpServers":{}}`)) {
		return nil, fmt.Errorf("%w: MCP mask is not the generated empty map", ErrInvalid)
	}
	return data, nil
}
func generatedMCPBody(path string) ([]byte, error) {
	data, err := generatedBody(path, 64*1024)
	if err != nil {
		return nil, err
	}
	var doc map[string]map[string]map[string]json.RawMessage
	if json.Unmarshal(data, &doc) != nil || len(doc) != 1 || doc["mcpServers"] == nil {
		return nil, fmt.Errorf("%w: invalid generated MCP map", ErrInvalid)
	}
	canonical, _ := json.Marshal(doc)
	if !bytes.Equal(data, canonical) {
		return nil, fmt.Errorf("%w: ambiguous generated MCP map", ErrInvalid)
	}
	servers := doc["mcpServers"]
	if len(servers) > 1 {
		return nil, fmt.Errorf("%w: foreign MCP server", ErrInvalid)
	}
	if len(servers) == 1 {
		server := servers["marshal"]
		if len(server) != 2 || server["command"] == nil || server["args"] == nil {
			return nil, fmt.Errorf("%w: invalid scoped MCP relay", ErrInvalid)
		}
		var command string
		var args []string
		if json.Unmarshal(server["command"], &command) != nil || json.Unmarshal(server["args"], &args) != nil || !canonicalMaskPath(command) || len(args) != 6 || args[0] != "mcp" || args[1] != "relay" || args[2] != "--requests" || args[4] != "--responses" {
			return nil, fmt.Errorf("%w: invalid scoped MCP invocation", ErrInvalid)
		}
		for i, name := range []string{"requests", "responses"} {
			p := args[3+2*i]
			info, err := os.Lstat(p)
			if err != nil || p != filepath.Join(filepath.Dir(filepath.Dir(path)), name) || info.Mode()&os.ModeNamedPipe == 0 || info.Mode().Perm() != 0600 {
				return nil, fmt.Errorf("%w: foreign scoped MCP endpoint", ErrInvalid)
			}
		}
	}
	return data, nil
}
func (m WorkerMCPMask) Valid(worktree string) bool {
	a := m.authority
	if a == nil || worktree != a.worktree || !canonicalMaskPath(worktree) || !canonicalMaskPath(a.source) || !canonicalMaskPath(a.globalSource) {
		return false
	}
	parent, err := os.Lstat(filepath.Dir(a.target))
	if err != nil || !parent.IsDir() || !os.SameFile(a.parent, parent) {
		return false
	}
	current, err := os.Lstat(a.target)
	if a.original == nil {
		if !os.IsNotExist(err) {
			return false
		}
	} else if err != nil || !current.Mode().IsRegular() || !os.SameFile(a.original, current) || current.Size() != a.original.Size() || !current.ModTime().Equal(a.original.ModTime()) {
		return false
	}
	for path, prior := range map[string]os.FileInfo{a.source: a.sourceInfo, a.globalSource: a.globalInfo, filepath.Join(filepath.Dir(a.source), "settings.json"): a.settingsInfo, filepath.Dir(a.globalSource): a.globalDirectory, filepath.Join(filepath.Dir(a.globalSource), "projects"): a.projectsDirectory} {
		current, err := os.Lstat(path)
		// Generated sources and mountpoints remain immutable for this invocation.
		// Device/inode alone can be reused after unlink and recreation. Pin the
		// original generation metadata as well; access time is deliberately absent.
		if err != nil || !os.SameFile(prior, current) || current.Mode() != prior.Mode() || current.Size() != prior.Size() || !current.ModTime().Equal(prior.ModTime()) {
			return false
		}
	}
	settings, err := generatedSettingsBody(filepath.Join(filepath.Dir(a.source), "settings.json"))
	if err != nil || sha256.Sum256(settings) != a.settingsDigest {
		return false
	}
	data, err := emptyMaskBody(a.source)
	if err != nil || sha256.Sum256(data) != a.digest {
		return false
	}
	data, err = generatedGlobalMCPBody(a.globalSource)
	return err == nil && sha256.Sum256(data) == a.globalDigest
}
func (m WorkerMCPMask) Allows(source, target, worktree string) bool {
	if !m.Valid(worktree) {
		return false
	}
	a := m.authority
	if source == filepath.Dir(a.globalSource) && target == "/home/marshal/.gemini/config" {
		return true
	}
	return a.original != nil && source == a.source && target == a.target && target == filepath.Join(worktree, ".agents", "mcp_config.json")
}
func (m WorkerMCPMask) WorkspaceConfigPresent() bool {
	return m.authority != nil && m.authority.original != nil
}

func maskWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
func generatedGlobalMCPBody(path string) ([]byte, error) {
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 2 || entries[0].Name() != "mcp_config.json" || entries[1].Name() != "projects" {
		return nil, fmt.Errorf("%w: generated global directory must contain scoped MCP configuration and empty project mountpoint", ErrInvalid)
	}
	projects := filepath.Join(filepath.Dir(path), "projects")
	info, err := os.Lstat(projects)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 || !canonicalMaskPath(projects) {
		return nil, fmt.Errorf("%w: invalid generated project mountpoint", ErrInvalid)
	}
	children, err := os.ReadDir(projects)
	if err != nil || len(children) != 0 {
		return nil, fmt.Errorf("%w: generated project mountpoint must remain empty", ErrInvalid)
	}
	return generatedMCPBody(path)
}

// ProjectCacheTarget is fixed by the sealed invocation; caller mount fields
// cannot select it. Native project preferences are untrusted and invocation-local;
// they do not confer host MCP, sandbox, package or transport authority.
func (m WorkerMCPMask) ProjectCacheTarget(worktree string) string {
	if !m.Valid(worktree) {
		return ""
	}
	return "/home/marshal/.gemini/config/projects"
}

func generatedSettingsBody(path string) ([]byte, error) {
	if !canonicalMaskPath(path) {
		return nil, fmt.Errorf("%w: noncanonical generated settings", ErrInvalid)
	}
	data, err := generatedBody(path, 64*1024)
	if err != nil {
		return nil, err
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal(data, &doc) != nil {
		return nil, fmt.Errorf("%w: malformed generated settings", ErrInvalid)
	}
	canonical, _ := json.Marshal(doc)
	if !bytes.Equal(data, canonical) {
		return nil, fmt.Errorf("%w: ambiguous generated settings", ErrInvalid)
	}
	for _, key := range []string{"enableTelemetry", "notifications", "showTips", "showFeedbackSurvey"} {
		if string(doc[key]) != "false" {
			return nil, fmt.Errorf("%w: generated settings metadata mismatch", ErrInvalid)
		}
	}
	if len(doc) == 4 {
		return data, nil
	}
	if len(doc) != 6 || string(doc["toolPermission"]) != `"proceed-in-sandbox"` || string(doc["permissions"]) != `{"allow":["mcp(marshal/*)","command(*)"]}` {
		return nil, fmt.Errorf("%w: generated settings permissions mismatch", ErrInvalid)
	}
	return data, nil
}

// ValidEnvelope binds the narrow exception to its complete protected mount graph.
// A copied seal alone cannot remove parent mount boundaries or replace settings.
func (m WorkerMCPMask) ValidEnvelope(request SandboxRequest) bool {
	if m.Empty() {
		return true
	}
	if !m.Valid(request.Worktree) || len(request.WritableTmpfs) == 0 || request.WritableTmpfs[0] != "/home/marshal/.gemini" {
		return false
	}
	a := m.authority
	parent := filepath.Dir(a.target)
	global := "/home/marshal/.gemini/config"
	settings := "/home/marshal/.gemini/antigravity-cli/settings.json"
	expected := map[string]string{parent: parent, global: filepath.Dir(a.globalSource), settings: filepath.Join(filepath.Dir(a.source), "settings.json")}
	if a.original != nil {
		expected[a.target] = a.source
	}
	for _, path := range request.WritableDirs {
		if !canonicalMaskPath(path) || maskWithin(path, "/home/marshal/.gemini") || maskWithin(path, parent) || maskWithin(parent, path) || maskWithin(path, global) || maskWithin(global, path) || maskWithin(path, settings) || maskWithin(settings, path) || maskWithin(path, filepath.Dir(a.globalSource)) || maskWithin(filepath.Dir(a.globalSource), path) {
			return false
		}
	}
	for i, path := range request.WritableTmpfs {
		if i == 0 {
			continue
		}
		if filepath.Clean(path) != path || maskWithin(path, "/home/marshal/.gemini") || maskWithin(parent, path) || maskWithin(path, parent) || maskWithin(global, path) || maskWithin(path, global) {
			return false
		}
	}
	seen := map[string]bool{}
	for _, bind := range request.ReadOnlyBinds {
		source, required := expected[bind.Target]
		if required {
			if bind.Target == a.target && !seen[parent] {
				return false
			}
			if seen[bind.Target] || source != bind.Source {
				return false
			}
			seen[bind.Target] = true
			continue
		}
		if filepath.Clean(bind.Target) != bind.Target || maskWithin(parent, bind.Target) || maskWithin(bind.Target, parent) || maskWithin(global, bind.Target) || maskWithin(bind.Target, global) || maskWithin(settings, bind.Target) || maskWithin(bind.Target, settings) {
			return false
		}
	}
	return len(seen) == len(expected)
}

//go:build !governed

package tui

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
)

// Extreme launches provider CLIs that MARSHAL itself starts (the Marshal chat,
// native sessions and workers) without stopping for the provider's own
// prompts about MARSHAL's work:
//
//   - Codex: MARSHAL's own MCP server ("marshal") has its tools approved,
//     approval_policy is "never" so shell commands do not stop for a person,
//     and the sandbox stays workspace-write. --dangerously-bypass-approvals-
//     and-sandbox is never used. The Marshal chat is the exception: unless
//     execution rights are small-tasks it runs under MARSHAL's read-only
//     permission profile instead (marshal_confine.go). Interactive launches also mark the project
//     trusted through a MARSHAL-owned Codex profile (see ensureCodexTrustProfile).
//   - Claude Code: MARSHAL's MCP server tools are pre-allowed for the launch
//     (--allowedTools mcp__marshal); its permission mode is left as it was.
//
// A setting the launch argv already carries (an operator's own -a/-s/-p or
// -c override) is never overridden.
func applyProviderLaunchPolicy(provider, root string, args, env []string, role string) ([]string, []string, error) {
	if provider != "codex" {
		return args, env, nil
	}
	return insertBeforeKickoff("codex", root, args, codexExtremeLaunchFlags(root, args, env, role)), env, nil
}

const codexMarshalToolApproval = `mcp_servers.marshal.default_tools_approval_mode="approve"`

func codexExtremeLaunchFlags(root string, args, env []string, role string) []string {
	flags := []string{"-c", codexMarshalToolApproval}
	bypass := codexArgsHave(args, "--dangerously-bypass-approvals-and-sandbox")
	if !bypass && !codexArgsHave(args, "-a", "--ask-for-approval", "--approve-for-me") && !codexConfigOverrides(args, "approval_policy") {
		flags = append(flags, "-c", `approval_policy="never"`)
	}
	// The Marshal chat's read-only permission profile (marshal_confine.go)
	// already decides its sandbox.
	if !bypass && !codexArgsHave(args, "-s", "--sandbox") && !codexConfigOverrides(args, "sandbox_mode") && !codexConfigOverrides(args, "default_permissions") {
		flags = append(flags, "-c", `sandbox_mode="workspace-write"`)
	}
	// Headless `codex exec` workers never show the trust dialog.
	if role != "worker" && !codexArgsHave(args, "-p", "--profile") {
		if name, err := ensureCodexTrustProfile(root, env); err == nil && name != "" {
			flags = append(flags, "-p", name)
		}
	}
	return flags
}

const codexTrustProfilePrefix = "marshal-trust-"

// codexTrustProfileEnabled reports whether a Codex CLI is on PATH for this
// launch. Without one there is nothing to trust and nothing is written.
var codexTrustProfileEnabled = func() bool {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("MARSHAL_CODEX_TRUST_PROFILE")), "off") {
		return false
	}
	// Test binaries and stand-in CLIs placed under the temporary directory
	// must never write into the operator's real Codex home.
	if testing.Testing() {
		return false
	}
	path, err := exec.LookPath("codex")
	if err != nil {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	tmp := filepath.Clean(os.TempDir())
	return path != tmp && !strings.HasPrefix(path, tmp+string(filepath.Separator))
}

// ensureCodexTrustProfile marks root trusted for MARSHAL's Codex launches.
//
// Codex 0.165 ignores `-c projects."<root>".trust_level="trusted"` for the
// folder-trust dialog (verified against the installed CLI): only a
// configuration file layer counts. MARSHAL therefore writes its own profile,
// $CODEX_HOME/marshal-trust-<digest>.config.toml, holding only this
// project's trust entry, and passes `-p <name>`. The operator's config.toml
// is never edited. Profiles for project roots that no longer exist are
// removed on the next write.
func ensureCodexTrustProfile(root string, env []string) (string, error) {
	if !codexTrustProfileEnabled() {
		return "", nil
	}
	home := codexHome(env)
	if home == "" {
		return "", nil
	}
	if info, err := os.Stat(home); err != nil || !info.IsDir() {
		return "", nil
	}
	roots := codexTrustRoots(root)
	if len(roots) == 0 {
		return "", nil
	}
	sum := sha256.Sum256([]byte(strings.Join(roots, "\x00")))
	name := codexTrustProfilePrefix + hex.EncodeToString(sum[:])[:16]
	var b strings.Builder
	b.WriteString("# Written by Marshal for its own Codex launches of this project.\n")
	b.WriteString("# It only marks the project folder trusted; remove it to undo.\n")
	b.WriteString("# marshal-root: " + roots[0] + "\n")
	for _, r := range roots {
		fmt.Fprintf(&b, "\n[projects.%s]\ntrust_level = \"trusted\"\n", tomlBasicString(r))
	}
	content := b.String()
	path := filepath.Join(home, name+".config.toml")
	if existing, err := os.ReadFile(path); err == nil && string(existing) == content {
		return name, nil
	}
	pruneCodexTrustProfiles(home, path)
	tmp, err := os.CreateTemp(home, "."+name+".*.tmp")
	if err != nil {
		return "", err
	}
	_, werr := tmp.WriteString(content)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("write codex trust profile: %v", firstError(werr, cerr))
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	return name, nil
}

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func codexHome(env []string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if v, ok := strings.CutPrefix(env[i], "CODEX_HOME="); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	if v := strings.TrimSpace(os.Getenv("CODEX_HOME")); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".codex")
}

// codexTrustRoots lists the project root as given and its resolved path.
// An enclosing repository is deliberately not trusted: that could extend
// trust far beyond the project (for example to a stray /tmp/.git).
func codexTrustRoots(root string) []string {
	abs, err := filepath.Abs(root)
	if err != nil || !trustPathPrintable(abs) {
		return nil
	}
	out := []string{filepath.Clean(abs)}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil && filepath.Clean(resolved) != out[0] && trustPathPrintable(resolved) {
		out = append(out, filepath.Clean(resolved))
	}
	return out
}

func trustPathPrintable(p string) bool {
	if p == "" {
		return false
	}
	for _, r := range p {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

// tomlBasicString quotes s as a TOML basic string.
func tomlBasicString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func pruneCodexTrustProfiles(home, keep string) {
	matches, _ := filepath.Glob(filepath.Join(home, codexTrustProfilePrefix+"*.config.toml"))
	for _, m := range matches {
		if m == keep {
			continue
		}
		data, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if r, ok := strings.CutPrefix(line, "# marshal-root: "); ok {
				if _, err := os.Stat(r); os.IsNotExist(err) {
					_ = os.Remove(m)
				}
				break
			}
		}
	}
}

// codexMarshalConfineEnforced: with approval_policy "never" a Codex write
// outside the Marshal's directories is refused, never offered for approval.
const codexMarshalConfineEnforced = true

// claudeMarshalConfineFlags make the Marshal chat's Claude Code refuse,
// without asking, every write it was not given: "dontAsk" denies whatever no
// allow rule permits, and the only edit rules allowed are the Marshal's own
// directories, passed as one --settings value (--allowedTools is variadic).
// MARSHAL's MCP tools stay allowed (claudeMarshalToolAllowFlags); reading
// needs no permission.
func claudeMarshalConfineFlags() ([]string, bool) {
	return []string{"--permission-mode", "dontAsk", "--settings", claudeMarshalWritableSettings()}, true
}

func claudeMarshalToolAllowFlags() []string {
	return []string{"--allowedTools", "mcp__marshal"}
}

// Package egm describes an optional, external EGM plugin. It never executes or
// modifies the plugin; unknown installations always select the built-in flow.
package egm

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed EGM.lock.json
var lockFS embed.FS

type Manifest struct {
	UpstreamURL          string            `json:"upstream_url"`
	TestedRelease        string            `json:"tested_release"`
	Commit               string            `json:"commit"`
	License              string            `json:"license"`
	RequiredSkills       []string          `json:"required_skills"`
	RequiredDirectories  []string          `json:"required_directories"`
	Artifacts            map[string]string `json:"artifacts"`
	ReviewStageArtifacts []string          `json:"review_stage_artifacts"`
}

// Lock returns a fresh copy of the compiled compatibility contract.
func Lock() Manifest {
	data, _ := lockFS.ReadFile("EGM.lock.json")
	var m Manifest
	_ = json.Unmarshal(data, &m) // embedded, validated by tests
	return m
}

type State string

const (
	Pinned        State = "pinned"
	Compatible    State = "compatible"
	NewerUntested State = "newer-untested"
	Missing       State = "missing"
	Incompatible  State = "incompatible"
)

const Fallback = "built-in design review: Marshal drafts; fresh-context review by another provider; person approves before task planning"

type Status struct {
	Provider string `json:"provider"`
	State    State  `json:"state"`
	Version  string `json:"version,omitempty"`
	Commit   string `json:"commit,omitempty"`
	Path     string `json:"path,omitempty"`
	Reason   string `json:"reason"`
	Fallback bool   `json:"fallback"`
}

func (s Status) String() string {
	detail := fmt.Sprintf("egm/%s: %s: %s", s.Provider, s.State, s.Reason)
	if s.Fallback {
		detail += "; " + Fallback
	}
	return detail
}

// Inspect reads only local identity metadata and the required interface files.
// Compatible means the release metadata matches but no Git commit was available;
// it is a compatibility check, not a cryptographic integrity attestation.
func Inspect(provider, root string) Status {
	s := Status{Provider: provider, Path: root, State: Missing, Fallback: true, Reason: "installed plugin not found locally"}
	info, err := os.Stat(root)
	if root == "" || err != nil || !info.IsDir() {
		return s
	}
	m := Lock()
	for _, skill := range m.RequiredSkills {
		data, err := os.ReadFile(filepath.Join(root, "skills", skill, "SKILL.md"))
		if err != nil || len(strings.TrimSpace(string(data))) == 0 {
			s.State, s.Reason = Incompatible, "required skill missing or unreadable: "+skill
			return s
		}
	}
	for _, dir := range m.RequiredDirectories {
		info, err := os.Stat(filepath.Join(root, dir))
		if err != nil || !info.IsDir() {
			s.State, s.Reason = Incompatible, "required bundle directory missing: "+dir
			return s
		}
	}
	var metadata struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	for _, name := range []string{"plugin.json", ".claude-plugin/plugin.json", ".codex-plugin/plugin.json"} {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err == nil {
			if json.Unmarshal(data, &metadata) != nil || metadata.Name != "egm" || metadata.Version == "" {
				s.State, s.Reason = Incompatible, "invalid plugin identity: "+name
				return s
			}
			break
		}
	}
	s.Version = metadata.Version
	s.Commit = localCommit(root)
	if _, err := os.Lstat(filepath.Join(root, ".git")); !os.IsNotExist(err) && s.Commit == "" {
		s.State, s.Reason = NewerUntested, "installed Git revision is unreadable or unresolved"
		return s
	}
	if s.Commit != "" && s.Commit != m.Commit {
		s.State, s.Reason = NewerUntested, "installed commit differs from tested pin"
		return s
	}
	if s.Version != strings.TrimPrefix(m.TestedRelease, "v") {
		s.State, s.Reason = NewerUntested, "version is unknown or differs from tested release "+m.TestedRelease
		return s
	}
	s.State, s.Fallback, s.Reason = Compatible, false, "release metadata and required interface match "+m.TestedRelease+"; commit unavailable"
	if s.Commit == m.Commit {
		s.State, s.Reason = Pinned, "tested release, commit and required interface match"
	}
	return s
}

// localCommit supports ordinary clones and Git worktrees, without invoking Git.
func localCommit(root string) string {
	gitDir := filepath.Join(root, ".git")
	if data, err := os.ReadFile(gitDir); err == nil && strings.HasPrefix(string(data), "gitdir: ") {
		gitDir = strings.TrimSpace(strings.TrimPrefix(string(data), "gitdir: "))
		if !filepath.IsAbs(gitDir) {
			gitDir = filepath.Join(root, gitDir)
		}
	}
	data, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	head := strings.TrimSpace(string(data))
	if strings.HasPrefix(head, "ref: ") {
		ref := strings.TrimPrefix(head, "ref: ")
		if !strings.HasPrefix(ref, "refs/") || strings.Contains(ref, "..") {
			return ""
		}
		commonDir := gitDir
		if common, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
			commonDir = strings.TrimSpace(string(common))
			if !filepath.IsAbs(commonDir) {
				commonDir = filepath.Join(gitDir, commonDir)
			}
		}
		if value, err := os.ReadFile(filepath.Join(commonDir, filepath.FromSlash(ref))); err == nil {
			head = strings.TrimSpace(string(value))
		} else {
			head = ""
			packed, _ := os.ReadFile(filepath.Join(commonDir, "packed-refs"))
			for _, line := range strings.Split(string(packed), "\n") {
				fields := strings.Fields(line)
				if len(fields) == 2 && fields[1] == ref {
					head = fields[0]
					break
				}
			}
		}
	}
	if len(head) != 40 || strings.Trim(head, "0123456789abcdef") != "" {
		return ""
	}
	return head
}

var Providers = []string{"codex", "claude", "antigravity", "opencode", "ollama"}

func ValidProvider(provider string) bool {
	for _, p := range Providers {
		if p == provider {
			return true
		}
	}
	return false
}

// Check discovers known cache layouts, never fetching or executing anything.
// An explicit root identifies the ACTIVE installation when a cache is ambiguous.
func Check(provider string) Status {
	key := "MARSHAL_EGM_" + strings.ToUpper(provider) + "_ROOT"
	if root := os.Getenv(key); root != "" {
		return Inspect(provider, root)
	}
	home, _ := os.UserHomeDir()
	base := filepath.Join(home, "."+provider)
	if provider == "codex" && os.Getenv("CODEX_HOME") != "" {
		base = os.Getenv("CODEX_HOME")
	}
	if provider == "claude" && os.Getenv("CLAUDE_CONFIG_DIR") != "" {
		base = os.Getenv("CLAUDE_CONFIG_DIR")
	}
	// Claude records active install paths, unlike a cache which can retain old releases.
	if provider == "claude" {
		data, err := os.ReadFile(filepath.Join(base, "plugins", "installed_plugins.json"))
		if err == nil {
			var registry struct {
				Plugins map[string][]struct {
					InstallPath string `json:"installPath"`
				} `json:"plugins"`
			}
			if json.Unmarshal(data, &registry) != nil {
				return Status{Provider: provider, State: NewerUntested, Fallback: true, Reason: "installed plugin registry is unreadable; set " + key}
			}
			paths := map[string]bool{}
			for _, install := range registry.Plugins["egm@egm"] {
				if install.InstallPath != "" {
					paths[install.InstallPath] = true
				}
			}
			if len(paths) == 1 {
				for path := range paths {
					return Inspect(provider, path)
				}
			}
			if len(paths) > 1 {
				return ambiguous(provider, key)
			}
		}
	}
	paths := map[string]bool{}
	for _, pattern := range []string{"plugins/cache/egm/egm/*", "plugins/cache/*/egm/*", "plugins/egm", "extensions/egm"} {
		matches, _ := filepath.Glob(filepath.Join(base, filepath.FromSlash(pattern)))
		for _, path := range matches {
			if info, err := os.Stat(path); err == nil && info.IsDir() {
				paths[path] = true
			}
		}
	}
	if len(paths) == 1 {
		for path := range paths {
			return Inspect(provider, path)
		}
	}
	if len(paths) > 1 {
		return ambiguous(provider, key)
	}
	s := Inspect(provider, "")
	s.Reason += "; set " + key + " to the installed whole-plugin root"
	return s
}

func ambiguous(provider, key string) Status {
	return Status{Provider: provider, State: NewerUntested, Fallback: true, Reason: "multiple local installs; set " + key + " to the active plugin root"}
}

func All() []Status {
	statuses := make([]Status, 0, len(Providers))
	for _, p := range Providers {
		statuses = append(statuses, Check(p))
	}
	return statuses
}

// InstallRecipe is deliberately print-only: provider ref syntax varies. Clone
// the exact tag, verify its commit, then use the official command with a local
// source. No mutable upstream URL is silently installed by MARSHAL.
func InstallRecipe(provider string) (string, error) {
	m := Lock()
	dir := `"$EGM_INSTALL_DIR"`
	var install string
	switch provider {
	case "codex":
		install = "codex plugin marketplace add " + dir + " && codex plugin add egm@egm"
	case "claude":
		install = "claude plugin marketplace add " + dir + " && claude plugin install egm@egm"
	case "antigravity":
		install = "agy plugin install " + dir
	default:
		return "", fmt.Errorf("no official EGM installer for provider %q; use built-in design review", provider)
	}
	return fmt.Sprintf("# Print-only; review and confirm in your shell before running.\n# If the provider rejects a local source, stop; do not substitute an unpinned URL.\nEGM_INSTALL_DIR=\"${XDG_DATA_HOME:-$HOME/.local/share}/marshal/plugins/egm/%s\"\nmkdir -p \"$EGM_INSTALL_DIR\" &&\ngit clone --branch %s --single-branch --depth 1 %s %s &&\ntest \"$(git -C %s rev-parse HEAD)\" = %s &&\n%s", m.TestedRelease, m.TestedRelease, m.UpstreamURL, dir, dir, m.Commit, install), nil
}

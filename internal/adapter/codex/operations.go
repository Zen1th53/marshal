package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
)

// nativeIdentifier is the deliberately small identifier grammar permitted to
// cross from native CLI discovery into a later MARSHAL command invocation or
// UI label.  Catalogs, marketplaces, and SKILL.md directories are external
// content; prose and option-like values are never authority.
var nativeIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@+:/-]{0,127}$`)

func isNativeIdentifier(value string) bool {
	return nativeIdentifier.MatchString(strings.TrimSpace(value))
}

// Classification represents the support status of a native Codex CLI command.
type Classification string

const (
	StatusSupported  Classification = "SUPPORTED"
	StatusPartial    Classification = "PARTIAL"
	StatusBlocked    Classification = "BLOCKED"
	StatusOutOfScope Classification = "OUT_OF_SCOPE"
)

// Operation describes one native Codex CLI operation and its MARSHAL governance boundary.
type Operation struct {
	Command        string         `json:"command"`
	Classification Classification `json:"classification"`
	Reason         string         `json:"reason"`
	SafetyClass    string         `json:"safety_class"`
}

// DoctorReport is the narrow trusted projection of `codex doctor --json`.
// Native doctor details can contain provider/configuration material, so they
// are intentionally not propagated into the TUI, event log, or evidence.
type DoctorReport struct {
	OverallStatus string
	CheckCount    int
}

// Doctor runs the documented read-only Codex health inspection with a bounded
// context and returns no raw native details. The JSON schema is deliberately
// treated as untrusted input: only a small status vocabulary and count survive.
func Doctor(ctx context.Context, binary string, runner adapter.ProcessRunner) (DoctorReport, error) {
	if binary == "" || runner == nil {
		return DoctorReport{}, fmt.Errorf("%w: binary and runner required", model.ErrInvalid)
	}
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	result, err := runner.Run(probeCtx, adapter.Command{Path: binary, Args: []string{"doctor", "--json"}})
	if err != nil || result.ExitCode != 0 || result.TimedOut || result.Cancelled || result.OutputTruncated {
		return DoctorReport{}, fmt.Errorf("%w: native Codex doctor did not complete", model.ErrUnavailable)
	}
	var payload struct {
		OverallStatus string         `json:"overallStatus"`
		Checks        map[string]any `json:"checks"`
	}
	if err := json.Unmarshal(result.Stdout, &payload); err != nil {
		return DoctorReport{}, fmt.Errorf("%w: invalid native Codex doctor JSON", model.ErrInvalid)
	}
	status := strings.ToLower(strings.TrimSpace(payload.OverallStatus))
	switch status {
	case "ok", "pass", "passed", "warning", "warn", "fail", "failed", "error", "unknown":
	default:
		status = "unknown"
	}
	return DoctorReport{OverallStatus: status, CheckCount: len(payload.Checks)}, nil
}

// NativeOperationsInventory returns the complete Phase 0 capability acceptance map.
func NativeOperationsInventory() []Operation {
	return []Operation{
		{
			Command:        "codex exec",
			Classification: StatusSupported,
			Reason:         "governed non-interactive task dispatch bound to MARSHAL lease, policy, sandbox, worktree, session ID, evidence, and model validation",
			SafetyClass:    "governed_action",
		},
		{
			Command:        "codex doctor",
			Classification: StatusSupported,
			Reason:         "bounded read-only native doctor inspection runs with JSON-only output; only a sanitized status and check count enter MARSHAL",
			SafetyClass:    "read_only",
		},
		{
			Command:        "codex debug models",
			Classification: StatusSupported,
			Reason:         "raw model catalog enumeration for evidenced model qualification and eligibility validation",
			SafetyClass:    "read_only",
		},
		{
			Command:        "codex plugin list",
			Classification: StatusSupported,
			Reason:         "read-only installed and available plugin inventory parsed from --json",
			SafetyClass:    "read_only",
		},
		{
			Command:        "codex plugin marketplace list",
			Classification: StatusSupported,
			Reason:         "read-only listing of configured plugin marketplace roots",
			SafetyClass:    "read_only",
		},
		{
			Command:        "codex review",
			Classification: StatusPartial,
			Reason:         "current checked-out commit is supported through MARSHAL verification; arbitrary prompts, paths, uncommitted state and apply flows remain blocked",
			SafetyClass:    "read_only",
		},
		{
			Command:        "codex plugin add",
			Classification: StatusBlocked,
			Reason:         "IMPLEMENTATION_GAP: native marketplace plugin installation requires canonical marketplace source trust and package policy authority",
			SafetyClass:    "sensitive_action",
		},
		{
			Command:        "project-local Codex skill install",
			Classification: StatusSupported,
			Reason:         "typed project-local skill name, immutable tree digest, overwrite refusal, runtime audit event, and TUI confirmation binding",
			SafetyClass:    "sensitive_action",
		},
		{
			Command:        "codex plugin remove",
			Classification: StatusBlocked,
			Reason:         "IMPLEMENTATION_GAP: plugin uninstallation requires canonical plugin policy authority and correlation tracking",
			SafetyClass:    "sensitive_action",
		},
		{
			Command:        "codex plugin marketplace add/remove",
			Classification: StatusBlocked,
			Reason:         "IMPLEMENTATION_GAP: marketplace modification requires canonical source trust verification and approval",
			SafetyClass:    "sensitive_action",
		},
		{
			Command:        "codex resume",
			Classification: StatusBlocked,
			Reason:         "interactive session resumption bypasses MARSHAL worktree isolation, CAS revision, policy, approval, and evidence handoff",
			SafetyClass:    "governed_action",
		},
		{
			Command:        "codex fork",
			Classification: StatusBlocked,
			Reason:         "interactive session branching bypasses MARSHAL task lease ownership and durable state verification",
			SafetyClass:    "governed_action",
		},
		{
			Command:        "codex queue",
			Classification: StatusBlocked,
			Reason:         "asynchronous message injection into running session bypasses task lease, revision, and approval governance",
			SafetyClass:    "governed_action",
		},
		{
			Command:        "codex apply",
			Classification: StatusBlocked,
			Reason:         "applies unverified git diff directly to local working tree without MARSHAL worktree isolation and commit governance",
			SafetyClass:    "destructive_action",
		},
		{
			Command:        "codex archive/delete/unarchive",
			Classification: StatusBlocked,
			Reason:         "session deletion/archival invalidates canonical MARSHAL session and evidence links",
			SafetyClass:    "destructive_action",
		},
		{
			Command:        "codex login/logout",
			Classification: StatusBlocked,
			Reason:         "direct host credential mutations outside MARSHAL secret broker lease and audit boundary",
			SafetyClass:    "sensitive_action",
		},
		{
			Command:        "codex mcp list",
			Classification: StatusSupported,
			Reason:         "bounded read-only JSON inventory; only server identity, enabled state and transport class are admitted",
			SafetyClass:    "read_only",
		},
		{
			Command:        "codex mcp add/remove/login/logout",
			Classification: StatusBlocked,
			Reason:         "external MCP lifecycle and credential mutations lack canonical MARSHAL governance/capability lease binding",
			SafetyClass:    "sensitive_action",
		},
		{
			Command:        "codex agents",
			Classification: StatusOutOfScope,
			Reason:         "shared daemon session browser requires interactive TUI and falls outside single-task Community control plane",
			SafetyClass:    "out_of_scope",
		},
		{
			Command:        "codex sandbox",
			Classification: StatusBlocked,
			Reason:         "raw sandbox command execution bypasses the Process 05 task lease, scoped worktree, policy and evidence contract",
			SafetyClass:    "governed_action",
		},
		{
			Command:        "codex debug (other subcommands)",
			Classification: StatusBlocked,
			Reason:         "only debug models is admitted as a bounded read-only probe; arbitrary debug surfaces have no stable governance contract",
			SafetyClass:    "read_only",
		},
		{
			Command:        "codex exec resume/fork/review",
			Classification: StatusBlocked,
			Reason:         "native session continuation and review must bind an exact MARSHAL run, worktree revision and Process 06 evidence before execution",
			SafetyClass:    "governed_action",
		},
		{
			Command:        "codex migrate-rollouts",
			Classification: StatusBlocked,
			Reason:         "migrates durable Codex session state outside MARSHAL backup, approval and recovery authority",
			SafetyClass:    "destructive_action",
		},
		{
			Command:        "codex app-server --stdio",
			Classification: StatusPartial,
			Reason:         "local client-owned stdio threads, exact native approval binding, and restart/resume are supported; full Process 05 asynchronous continuation and exec-equivalent host-config isolation are not yet the default execution path",
			SafetyClass:    "governed_action",
		},
		{
			Command:        "codex remote-control",
			Classification: StatusOutOfScope,
			Reason:         "remote-control daemon management is outside the MARSHAL Community local boundary",
			SafetyClass:    "out_of_scope",
		},
		{
			Command:        "codex global config / profiles / feature flags",
			Classification: StatusBlocked,
			Reason:         "direct configuration mutations can alter sandbox, approval and provider behavior outside MARSHAL's canonical policy snapshot",
			SafetyClass:    "sensitive_action",
		},
		{
			Command:        "codex cloud / exec-server",
			Classification: StatusOutOfScope,
			Reason:         "remote cloud synchronization and server daemon are outside MARSHAL Community boundary",
			SafetyClass:    "out_of_scope",
		},
		{
			Command:        "codex update / completion",
			Classification: StatusOutOfScope,
			Reason:         "CLI maintenance and shell completions are host-management utilities",
			SafetyClass:    "out_of_scope",
		},
		{
			Command:        "codex features",
			Classification: StatusOutOfScope,
			Reason:         "mutates host config.toml directly rather than operating within task-scoped execution constraints",
			SafetyClass:    "out_of_scope",
		},
	}
}

// ModelInfo carries evidenced metadata about an eligible Codex model.
type ModelInfo struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	Visibility  string `json:"visibility"`
	IsDefault   bool   `json:"is_default"`
}

// PluginInfo carries evidenced metadata about an installed Codex plugin.
type PluginInfo struct {
	PluginID        string `json:"plugin_id"`
	Name            string `json:"name"`
	MarketplaceName string `json:"marketplace_name"`
	Version         string `json:"version"`
	Installed       bool   `json:"installed"`
	Enabled         bool   `json:"enabled"`
	SourcePath      string `json:"source_path,omitempty"`
	MarketplaceSrc  string `json:"marketplace_source,omitempty"`
}

// MarketplaceInfo carries metadata about a configured marketplace.
type MarketplaceInfo struct {
	Name string `json:"name"`
	Root string `json:"root"`
}

// SkillInfo carries metadata about a locally discoverable skill.
type SkillInfo struct {
	Name        string    `json:"name"`
	Path        string    `json:"path"`
	Root        string    `json:"root"`
	Description string    `json:"description"`
	Freshness   time.Time `json:"freshness"`
}

// DiscoverModels queries the native Codex CLI for its model catalog.
// If probing fails, no models are guessed.
func DiscoverModels(ctx context.Context, binary string, runner adapter.ProcessRunner) ([]ModelInfo, string, error) {
	if binary == "" || runner == nil {
		return nil, "", fmt.Errorf("%w: binary and runner required", model.ErrInvalid)
	}

	// 1. Probe models catalog via `codex debug models`
	debugCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := adapter.Command{
		Path: binary,
		Args: []string{"debug", "models"},
	}
	res, err := runner.Run(debugCtx, cmd)
	if err != nil || res.ExitCode != 0 {
		return nil, "", fmt.Errorf("%w: failed to query models from codex: %v", model.ErrUnavailable, err)
	}

	var catalog struct {
		Models []struct {
			Slug        string `json:"slug"`
			DisplayName string `json:"display_name"`
			Description string `json:"description"`
			Visibility  string `json:"visibility"`
			Priority    int    `json:"priority"`
			IsDefault   bool   `json:"is_default"`
		} `json:"models"`
	}

	if err := json.Unmarshal(res.Stdout, &catalog); err != nil {
		return nil, "", fmt.Errorf("parse codex models json: %w", err)
	}

	// 2. Discover the default model from the governed configuration only.
	// The host's ~/.codex/config.toml is deliberately not consulted:
	// EnsureGovernedCodexHome removes config.toml from the governed home and
	// mirrors only auth/version/cache, so reading the host file here would let
	// unmanaged operator state silently override the catalog's declared
	// default and decide which model a governed task runs on.
	defaultModel := ""
	var configPaths []string
	if cfg := os.Getenv("CODEX_CONFIG"); cfg != "" {
		configPaths = append(configPaths, cfg)
	}
	if codexHome := os.Getenv("CODEX_HOME"); codexHome != "" {
		configPaths = append(configPaths, filepath.Join(codexHome, "config.toml"))
	}
	for _, p := range configPaths {
		if data, err := os.ReadFile(p); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "model") {
					parts := strings.SplitN(line, "=", 2)
					if len(parts) == 2 && strings.TrimSpace(parts[0]) == "model" {
						val := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
						if val != "" {
							defaultModel = val
							break
						}
					}
				}
			}
			if defaultModel != "" {
				break
			}
		}
	}

	if defaultModel == "" {
		for _, m := range catalog.Models {
			if m.IsDefault || m.Priority == 1 {
				defaultModel = m.Slug
				break
			}
		}
	}

	var models []ModelInfo
	for _, m := range catalog.Models {
		slug := strings.TrimSpace(m.Slug)
		if !isNativeIdentifier(slug) {
			continue
		}
		// List visible models
		isDef := slug == defaultModel
		models = append(models, ModelInfo{
			Slug: slug,
			// The native slug is the validated selector. Do not render an
			// arbitrary catalog display name alongside it.
			DisplayName: slug,
			// Catalog descriptions are external, untrusted prose. MARSHAL needs
			// typed identity and eligibility only; never surface this prose as
			// UI, evidence, or future agent context.
			Description: "",
			// Catalog visibility prose is neither an authority nor a stable
			// control-plane value. A listed model has already passed discovery.
			Visibility: "eligible",
			IsDefault:  isDef,
		})
	}

	return models, defaultModel, nil
}

// DiscoverPlugins queries native plugins. It queries per-marketplace to avoid
// remote network discovery hangs, falling back to a direct list if needed.
func DiscoverPlugins(ctx context.Context, binary string, runner adapter.ProcessRunner) ([]PluginInfo, error) {
	if binary == "" || runner == nil {
		return nil, fmt.Errorf("%w: binary and runner required", model.ErrInvalid)
	}

	// Try querying per-marketplace first (avoids remote network hang on real CLI)
	if markets, mErr := DiscoverMarketplaces(ctx, binary, runner); mErr == nil && len(markets) > 0 {
		var allPlugins []PluginInfo
		seen := make(map[string]bool)
		for _, m := range markets {
			if !isNativeIdentifier(m.Name) {
				continue
			}
			probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			res, err := runner.Run(probeCtx, adapter.Command{
				Path: binary,
				Args: []string{"plugin", "list", "--marketplace", m.Name, "--json"},
			})
			cancel()
			if err != nil || res.ExitCode != 0 {
				continue
			}
			plugins, parseErr := parsePluginJSON(res.Stdout)
			if parseErr != nil {
				continue
			}
			for _, p := range plugins {
				if !seen[p.PluginID] {
					seen[p.PluginID] = true
					allPlugins = append(allPlugins, p)
				}
			}
		}
		if len(allPlugins) > 0 {
			return allPlugins, nil
		}
	}

	// Fallback to direct plugin list --json (e.g. for mock runners)
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	res, err := runner.Run(probeCtx, adapter.Command{
		Path: binary,
		Args: []string{"plugin", "list", "--json"},
	})
	if err != nil || res.ExitCode != 0 {
		return nil, fmt.Errorf("%w: query plugins failed: %v", model.ErrUnavailable, err)
	}
	return parsePluginJSON(res.Stdout)
}

func parsePluginJSON(raw []byte) ([]PluginInfo, error) {
	var data struct {
		Installed []struct {
			PluginID        string `json:"pluginId"`
			Name            string `json:"name"`
			MarketplaceName string `json:"marketplaceName"`
			Version         string `json:"version"`
			Installed       bool   `json:"installed"`
			Enabled         bool   `json:"enabled"`
			Source          struct {
				Source string `json:"source"`
				Path   string `json:"path"`
			} `json:"source"`
			MarketplaceSource struct {
				SourceType string `json:"sourceType"`
				Source     string `json:"source"`
			} `json:"marketplaceSource"`
		} `json:"installed"`
	}

	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("parse plugin json: %w", err)
	}

	var out []PluginInfo
	for index, p := range data.Installed {
		pluginID := strings.TrimSpace(p.PluginID)
		name := strings.TrimSpace(p.Name)
		marketplace := strings.TrimSpace(p.MarketplaceName)
		version := strings.TrimSpace(p.Version)
		if !isNativeIdentifier(pluginID) || !isNativeIdentifier(name) ||
			(marketplace != "" && !isNativeIdentifier(marketplace)) ||
			(version != "" && !isNativeIdentifier(version)) {
			// The plugin remains countable as an untrusted native entry, but its
			// text never becomes a UI label or a future command argument.
			pluginID = fmt.Sprintf("untrusted-plugin-%d", index+1)
			name = "untrusted plugin metadata"
			marketplace = "unavailable"
			version = "unknown"
		}
		out = append(out, PluginInfo{
			PluginID:        pluginID,
			Name:            name,
			MarketplaceName: marketplace,
			Version:         version,
			Installed:       p.Installed,
			Enabled:         p.Enabled,
			// Source strings are arbitrary host paths/URLs.  Inventory needs no
			// authority over them, so do not propagate them outside discovery.
			SourcePath:     "",
			MarketplaceSrc: "",
		})
	}
	return out, nil
}

// DiscoverMarketplaces parses `codex plugin marketplace list`.
func DiscoverMarketplaces(ctx context.Context, binary string, runner adapter.ProcessRunner) ([]MarketplaceInfo, error) {
	if binary == "" || runner == nil {
		return nil, fmt.Errorf("%w: binary and runner required", model.ErrInvalid)
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	res, err := runner.Run(probeCtx, adapter.Command{
		Path: binary,
		Args: []string{"plugin", "marketplace", "list"},
	})
	if err != nil || res.ExitCode != 0 {
		return nil, fmt.Errorf("%w: query marketplaces failed: %v", model.ErrUnavailable, err)
	}

	lines := strings.Split(string(res.Stdout), "\n")
	var out []MarketplaceInfo
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if i == 0 || line == "" {
			continue // Skip header: MARKETPLACE ROOT
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && isNativeIdentifier(fields[0]) {
			out = append(out, MarketplaceInfo{
				Name: fields[0],
				// Marketplace roots are host-local paths.  They are not an action
				// input and are intentionally absent from the public inventory.
				Root: "",
			})
		}
	}
	return out, nil
}

// DiscoverLocalSkills scans known local roots for SKILL.md definitions.
func DiscoverLocalSkills(projectRoot string) ([]SkillInfo, error) {
	var roots []string
	if projectRoot != "" {
		roots = append(roots, filepath.Join(projectRoot, ".agents", "skills"))
	}
	home, err := os.UserHomeDir()
	if err == nil {
		roots = append(roots,
			filepath.Join(home, ".codex", "skills"),
			filepath.Join(home, ".gemini", "config", "skills"),
			filepath.Join(home, ".codex", ".tmp", "marketplaces", "ecc", ".agents", "skills"),
		)
	}

	var skills []SkillInfo
	seen := make(map[string]bool)

	for _, root := range roots {
		info, statErr := os.Stat(root)
		if statErr != nil || !info.IsDir() {
			continue
		}

		_ = filepath.Walk(root, func(path string, fileInfo os.FileInfo, walkErr error) error {
			if walkErr != nil || fileInfo == nil || fileInfo.IsDir() {
				return nil
			}
			if fileInfo.Name() == "SKILL.md" {
				dir := filepath.Dir(path)
				skillName := filepath.Base(dir)
				if !isNativeIdentifier(skillName) {
					return nil
				}
				if seen[skillName] {
					return nil
				}
				seen[skillName] = true

				skills = append(skills, SkillInfo{
					Name: skillName,
					Path: "",
					Root: "",
					// SKILL.md front matter is user/project content. Discovery is
					// identity-only so it cannot inject instructions into MARSHAL.
					Description: "",
					Freshness:   fileInfo.ModTime().UTC(),
				})
			}
			return nil
		})
	}
	return skills, nil
}

// ValidateDangerousFlags verifies that no dangerous bypass flags appear in the command args.
// InstallProjectSkill installs one explicitly named project-local skill into a
// Codex skill root. It accepts neither a URL nor a command. The caller must
// first display PreviewProjectSkill's digest and pass that exact digest back,
// preventing a changed source tree from being installed after confirmation.
func InstallProjectSkill(projectRoot, codexHome, name, expectedDigest string) (string, error) {
	if !isNativeIdentifier(name) || strings.Contains(name, "/") {
		return "", fmt.Errorf("%w: invalid skill name", model.ErrInvalid)
	}
	source := filepath.Join(projectRoot, ".agents", "skills", name)
	digest, err := skillTreeDigest(source)
	if err != nil {
		return "", err
	}
	if digest != expectedDigest {
		return "", fmt.Errorf("%w: skill source digest changed", model.ErrConflict)
	}
	codexHome, err = resolvedCodexHome(codexHome)
	if err != nil {
		return "", err
	}
	skillsRoot := filepath.Join(codexHome, "skills")
	destination := filepath.Join(skillsRoot, name)
	if _, err := os.Lstat(destination); err == nil {
		return "", fmt.Errorf("%w: Codex skill %q already exists; overwrite is refused", model.ErrConflict, name)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	// Never copy directly from the mutable project tree into CODEX_HOME.  Make
	// a private snapshot first and digest that snapshot before creating the
	// final destination. This closes the preview-to-copy TOCTOU window: a file
	// changed while being copied cannot silently become an installed skill.
	if err := os.MkdirAll(skillsRoot, 0o700); err != nil {
		return "", err
	}
	staging, err := os.MkdirTemp(skillsRoot, ".marshal-skill-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(staging)
	if err := copySkillTree(source, staging); err != nil {
		return "", err
	}
	stagedDigest, err := skillTreeDigest(staging)
	if err != nil {
		return "", err
	}
	if stagedDigest != expectedDigest {
		return "", fmt.Errorf("%w: skill source changed while creating install snapshot", model.ErrConflict)
	}
	// Mkdir is deliberately used instead of Rename: Rename may replace an
	// existing directory on POSIX. Creating this exact directory makes the
	// no-overwrite promise hold even if another local process races us.
	if err := os.Mkdir(destination, 0o700); err != nil {
		if os.IsExist(err) {
			return "", fmt.Errorf("%w: Codex skill %q already exists; overwrite is refused", model.ErrConflict, name)
		}
		return "", err
	}
	installed := false
	defer func() {
		if !installed {
			_ = os.RemoveAll(destination)
		}
	}()
	if err := copySkillTree(staging, destination); err != nil {
		return "", err
	}
	installed = true
	return digest, nil
}

// RemoveInstalledProjectSkill compensates a failed canonical audit following
// InstallProjectSkill. It is deliberately not a general skill-management API:
// only an exact digest-bound, named skill can be removed. This prevents a
// failed audit from reporting failure while leaving an unrecorded mutation.
func RemoveInstalledProjectSkill(codexHome, name, expectedDigest string) error {
	if !isNativeIdentifier(name) || strings.Contains(name, "/") || expectedDigest == "" {
		return fmt.Errorf("%w: invalid skill removal binding", model.ErrInvalid)
	}
	codexHome, err := resolvedCodexHome(codexHome)
	if err != nil {
		return err
	}
	destination := filepath.Join(codexHome, "skills", name)
	digest, err := skillTreeDigest(destination)
	if err != nil {
		return err
	}
	if digest != expectedDigest {
		return fmt.Errorf("%w: installed skill digest no longer matches audit binding", model.ErrConflict)
	}
	return os.RemoveAll(destination)
}

func resolvedCodexHome(codexHome string) (string, error) {
	if codexHome == "" {
		if configured := strings.TrimSpace(os.Getenv("CODEX_HOME")); configured != "" {
			codexHome = configured
		}
	}
	if codexHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		codexHome = filepath.Join(home, ".codex")
	}
	return filepath.Clean(codexHome), nil
}

// PreviewProjectSkill returns the immutable content digest of an installable
// project-local skill. It rejects symlinks, special files, and missing
// SKILL.md, so the preview and installer share exactly the same admission set.
func PreviewProjectSkill(projectRoot, name string) (string, error) {
	if !isNativeIdentifier(name) || strings.Contains(name, "/") {
		return "", fmt.Errorf("%w: invalid skill name", model.ErrInvalid)
	}
	return skillTreeDigest(filepath.Join(projectRoot, ".agents", "skills", name))
}

func skillTreeDigest(root string) (string, error) {
	if info, err := os.Stat(filepath.Join(root, "SKILL.md")); err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("%w: SKILL.md is required", model.ErrNotFound)
	}
	h := sha256.New()
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() && !info.IsDir() {
			return fmt.Errorf("%w: unsafe skill tree entry", model.ErrInvalid)
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if _, err = io.WriteString(h, rel+"\x00"); err != nil {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, err = io.Copy(h, file)
		closeErr := file.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
	if err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func copySkillTree(source, destination string) error {
	return filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == source {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() && !info.IsDir() {
			return fmt.Errorf("%w: unsafe skill tree entry", model.ErrInvalid)
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, in)
		closeErr := out.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
}

func ValidateDangerousFlags(args []string) error {
	forbidden := []string{
		"--dangerously-bypass-approvals-and-sandbox",
		"--dangerously-bypass-hook-trust",
		"danger-full-access",
		"--search",
	}
	joined := " " + strings.Join(args, " ") + " "
	for _, f := range forbidden {
		if strings.Contains(joined, f) {
			return fmt.Errorf("%w: dangerous bypass flag %q is strictly forbidden", model.ErrPolicyDenied, f)
		}
	}
	return nil
}

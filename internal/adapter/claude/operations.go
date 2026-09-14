package claude

// Operations exposes the small set of native Claude Code CLI inspections that
// MARSHAL needs to decide whether a governed run may start: is the binary
// healthy, which models may it select, and what is the operator's current
// authentication state. It is deliberately not a general CLI passthrough.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
)

// nativeIdentifier bounds every native value MARSHAL is willing to echo back
// into a command line or persist as control-plane identity. Catalog prose and
// provider-authored labels never pass through it.
var nativeIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

func isNativeIdentifier(value string) bool {
	return nativeIdentifier.MatchString(strings.TrimSpace(value))
}

// ValidateDangerousFlags refuses any argument that would disable the very
// boundaries MARSHAL relies on. Claude Code spells these differently from
// Codex, so the list is provider-specific rather than shared.
func ValidateDangerousFlags(args []string) error {
	forbidden := []string{
		"--dangerously-skip-permissions",
		"--allow-dangerously-skip-permissions",
		"bypassPermissions",
		"--no-sandbox",
	}
	joined := " " + strings.Join(args, " ") + " "
	for _, flag := range forbidden {
		if strings.Contains(joined, flag) {
			return fmt.Errorf("%w: dangerous bypass flag %q is strictly forbidden", model.ErrPolicyDenied, flag)
		}
	}
	return nil
}

// ModelInfo is the typed projection of one eligible model. As with Codex, the
// native slug is the only validated selector; catalog display prose is dropped
// rather than surfaced as UI, evidence, or future agent context.
type ModelInfo struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	Visibility  string `json:"visibility"`
	IsDefault   bool   `json:"is_default"`
}

// DoctorReport is a bounded summary of `claude doctor`. It reports how many
// checks ran and the overall verdict, never the raw diagnostic text, which can
// contain host paths and account details.
type DoctorReport struct {
	OverallStatus string
	CheckCount    int
}

// GovernedClaudeHomeDir is the MARSHAL-owned configuration root for governed
// Claude runs. Pointing CLAUDE_CONFIG_DIR at it keeps a governed session from
// reading or corrupting the operator's own Claude Code state.
func GovernedClaudeHomeDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".marshal", "claude_session")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("marshal-claude-session-%d", os.Getuid()))
}

// EnsureGovernedClaudeHome creates the governed root and mirrors only the
// credential material a run needs. Settings files are deliberately not copied:
// host settings can carry hooks, permission rules, and MCP servers that would
// silently widen what a governed task may do.
func EnsureGovernedClaudeHome() (string, error) {
	target := GovernedClaudeHomeDir()
	if err := os.MkdirAll(target, 0o700); err != nil {
		return "", fmt.Errorf("create governed CLAUDE_CONFIG_DIR: %w", err)
	}
	// A settings file inherited from an earlier layout would reintroduce host
	// hooks on the next run, so remove it rather than trusting it is absent.
	_ = os.Remove(filepath.Join(target, "settings.json"))

	if home, err := os.UserHomeDir(); err == nil && home != "" {
		hostClaude := filepath.Join(home, ".claude")
		for _, name := range []string{".credentials.json"} {
			hostPath := filepath.Join(hostClaude, name)
			if info, statErr := os.Stat(hostPath); statErr == nil && !info.IsDir() {
				if data, readErr := os.ReadFile(hostPath); readErr == nil {
					_ = os.WriteFile(filepath.Join(target, name), data, 0o600)
				}
			}
		}
	}
	return target, nil
}

// Doctor runs the native health check and reduces it to a typed verdict.
func Doctor(ctx context.Context, binary string, runner adapter.ProcessRunner) (DoctorReport, error) {
	if binary == "" || runner == nil {
		return DoctorReport{}, fmt.Errorf("%w: binary and runner required", model.ErrInvalid)
	}
	doctorCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	result, err := runner.Run(doctorCtx, adapter.Command{Path: binary, Args: []string{"doctor"}})
	if err != nil {
		return DoctorReport{}, fmt.Errorf("%w: native Claude doctor failed: %v", model.ErrUnavailable, err)
	}
	output := string(result.Stdout) + string(result.Stderr)
	// The native report is human-readable prose, not JSON. Only its shape is
	// trusted: each diagnostic is a "Key: Value" line, and none of that text is
	// carried into the report, since it contains host paths and account state.
	checks := 0
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || len(line) > 200 {
			continue
		}
		key, value, found := strings.Cut(line, ":")
		if found && strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			checks++
		}
	}
	status := "ok"
	switch {
	case result.ExitCode != 0:
		status = "failed"
	case strings.Contains(output, "issues found") && !strings.Contains(output, "No installation issues found"):
		status = "degraded"
	}
	// A doctor run that exits clean while reporting nothing is treated as
	// unavailable rather than as a pass.
	if checks == 0 && result.ExitCode == 0 {
		return DoctorReport{}, fmt.Errorf("%w: native Claude doctor reported no checks", model.ErrUnavailable)
	}
	return DoctorReport{OverallStatus: status, CheckCount: checks}, nil
}

// eligibleModelAliases are the selectors MARSHAL will pass to --model.
//
// Claude Code exposes no machine-readable catalog subcommand: `claude models`
// is not a command, and an unrecognized argument is treated as a prompt, which
// would start a billed session instead of answering a query. Discovery is
// therefore a fixed alias set validated against the binary, not scraped prose.
// Aliases rather than dated model IDs keep a governed run on the current model
// for each tier without this table having to be edited on every release.
var eligibleModelAliases = []string{"opus", "sonnet", "haiku"}

// DiscoverModels returns the eligible selectors and the model the binary
// actually resolves for a governed run.
//
// The default is read from the native session's own `init` event rather than
// from the operator's settings: EnsureGovernedClaudeHome strips host settings
// precisely so unmanaged operator state cannot decide what a governed task
// runs on. Running under the governed configuration root means the reported
// model is the one a governed task would really get.
func DiscoverModels(ctx context.Context, binary string, runner adapter.ProcessRunner) ([]ModelInfo, string, error) {
	if binary == "" || runner == nil {
		return nil, "", fmt.Errorf("%w: binary and runner required", model.ErrInvalid)
	}
	probeCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	governedHome, err := EnsureGovernedClaudeHome()
	if err != nil {
		return nil, "", err
	}
	// `plan` mode plus an empty allowed-tools set keeps this probe from taking
	// any action; it exists only to make the CLI report its resolved identity.
	result, runErr := runner.Run(probeCtx, adapter.Command{
		Path: binary,
		Args: []string{
			"-p", "--output-format", "stream-json", "--verbose",
			"--permission-mode", "plan", "--permission-prompts", "none",
			"--max-turns", "1", "--allowed-tools", "",
		},
		Env:   append(os.Environ(), "CLAUDE_CONFIG_DIR="+governedHome),
		Stdin: []byte("respond with the single word: ready\n"),
	})
	if runErr != nil {
		return nil, "", fmt.Errorf("%w: failed to probe claude session: %v", model.ErrUnavailable, runErr)
	}

	activeModel := ""
	for _, line := range strings.Split(string(result.Stdout), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var event struct {
			Type    string `json:"type"`
			Subtype string `json:"subtype"`
			Model   string `json:"model"`
		}
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		if event.Type == "system" && event.Subtype == "init" && isNativeIdentifier(event.Model) {
			activeModel = strings.TrimSpace(event.Model)
			break
		}
	}
	if activeModel == "" {
		return nil, "", fmt.Errorf("%w: claude session reported no resolved model", model.ErrUnavailable)
	}

	models := make([]ModelInfo, 0, len(eligibleModelAliases)+1)
	for _, alias := range eligibleModelAliases {
		models = append(models, ModelInfo{
			Slug: alias,
			// The native selector is the validated identity; no provider prose
			// is rendered alongside it.
			DisplayName: alias,
			Description: "",
			Visibility:  "eligible",
			IsDefault:   false,
		})
	}
	// The resolved model is itself a valid --model argument, so it is offered
	// as an exact selector alongside the aliases.
	models = append(models, ModelInfo{
		Slug: activeModel, DisplayName: activeModel,
		Description: "", Visibility: "eligible", IsDefault: true,
	})
	return models, activeModel, nil
}

// Models returns the eligible selector set and the resolved default.
func (c *Client) Models(ctx context.Context) ([]ModelInfo, string, error) {
	if len(c.models) > 0 {
		return c.models, c.defaultModel, nil
	}
	models, def, err := DiscoverModels(ctx, c.binary, c.runner)
	if err != nil {
		return nil, "", err
	}
	c.models = models
	c.defaultModel = def
	return models, def, nil
}

// ValidateModel checks that a requested selector is one MARSHAL will pass to
// the CLI. An unknown value is refused rather than forwarded, because Claude
// Code treats an unrecognized argument as a prompt and would start a billed
// session instead of failing.
func (c *Client) ValidateModel(ctx context.Context, modelName string) error {
	if modelName == "" {
		return nil
	}
	if err := ValidateDangerousFlags([]string{modelName}); err != nil {
		return err
	}
	if !isNativeIdentifier(modelName) {
		return fmt.Errorf("%w: model %q is not a native Claude identifier", model.ErrInvalid, modelName)
	}
	models, _, err := c.Models(ctx)
	if err != nil {
		return fmt.Errorf("%w: cannot query eligible claude models: %v", model.ErrUnavailable, err)
	}
	for _, candidate := range models {
		if candidate.Slug == modelName {
			return nil
		}
	}
	return fmt.Errorf("%w: model %q is not an eligible Claude model", model.ErrInvalid, modelName)
}

package claude

// Operations exposes the small set of native Claude Code CLI inspections that
// MARSHAL needs to decide whether a governed run may start: is the binary
// healthy, which models may it select, and what is the operator's current
// authentication state. It is deliberately not a general CLI passthrough.

import (
	"context"
	"encoding/json"
	"fmt"
	providerspec "github.com/Zen1th53/marshal/internal/adapter/provider"
	"github.com/Zen1th53/marshal/internal/statepath"
	"github.com/Zen1th53/marshal/internal/usage"
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
	// ReasoningEfforts are the --effort levels the installed CLI lists.
	ReasoningEfforts []string `json:"reasoning_efforts,omitempty"`
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
		return filepath.Join(home, statepath.DirName, "claude_session")
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

	hostClaude, _, _ := providerspec.Must("claude").StateDir(nil)
	if hostClaude != "" {
		for _, name := range []string{".credentials.json"} {
			hostPath := filepath.Join(hostClaude, name)
			if info, statErr := os.Stat(hostPath); statErr == nil && !info.IsDir() {
				if data, readErr := os.ReadFile(hostPath); readErr == nil {
					_ = os.WriteFile(filepath.Join(target, name), data, 0o600)
				}
			} else if os.IsNotExist(statErr) {
				// A different owner home must not reuse an earlier account.
				_ = os.Remove(filepath.Join(target, name))
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

// Claude Code exposes no machine-readable catalog subcommand: `claude models`
// is not a command, and an unrecognized argument is treated as a prompt, which
// would start a billed session instead of answering a query. What the
// installed CLI does say about models is in its own --help: the --model
// option names the aliases it resolves to the latest model of a tier ("Use
// an alias for the latest model (e.g. 'fable', 'opus', or 'sonnet')") and
// --effort lists its effort levels. Those are read from the installed binary,
// so a new tier or a retired one needs no Marshal release.

var (
	helpQuotedWord  = regexp.MustCompile(`'([A-Za-z0-9][A-Za-z0-9._:-]{0,63})'`)
	helpParenthesis = regexp.MustCompile(`\(([^)]*)\)`)
)

// ParseHelpModels reads the model aliases the --model option names and the
// levels the --effort option lists from `claude --help` output.
func ParseHelpModels(help string) (aliases, efforts []string) {
	aliases = helpOptionWords(help, "--model", func(text string) []string {
		var out []string
		for _, m := range helpQuotedWord.FindAllStringSubmatch(text, -1) {
			out = append(out, m[1])
		}
		return out
	})
	efforts = helpOptionWords(help, "--effort", func(text string) []string {
		var out []string
		for _, m := range helpParenthesis.FindAllStringSubmatch(text, -1) {
			for _, word := range strings.FieldsFunc(m[1], func(r rune) bool { return r == ',' || r == '|' || r == ' ' }) {
				out = append(out, word)
			}
		}
		return out
	})
	return aliases, efforts
}

// helpOptionWords applies pick to the description of one option: its own
// line and the continuation lines indented below it.
func helpOptionWords(help, option string, pick func(string) []string) []string {
	var text []string
	in := false
	for _, line := range strings.Split(strings.ReplaceAll(help, "\r", ""), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "-") {
			fields := strings.FieldsFunc(trimmed, func(r rune) bool { return r == ' ' || r == ',' })
			in = len(fields) > 0 && (fields[0] == option || (len(fields) > 1 && fields[1] == option))
			if in {
				text = append(text, trimmed)
			}
			continue
		}
		if in && trimmed != "" && line != trimmed {
			text = append(text, trimmed)
			continue
		}
		in = false
	}
	var out []string
	seen := map[string]bool{}
	for _, word := range pick(strings.Join(text, " ")) {
		if isNativeIdentifier(word) && !seen[word] {
			seen[word] = true
			out = append(out, word)
		}
	}
	return out
}

// HelpModels asks the installed Claude Code which model aliases and effort
// levels it names in its own --help. It starts no session.
func HelpModels(ctx context.Context, binary string, runner adapter.ProcessRunner) (aliases, efforts []string, err error) {
	if binary == "" || runner == nil {
		return nil, nil, fmt.Errorf("%w: binary and runner required", model.ErrInvalid)
	}
	helpCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	result, err := runner.Run(helpCtx, adapter.Command{Path: binary, Args: []string{"--help"}, Env: append(os.Environ(), "NO_COLOR=1", "TERM=dumb", "CI=1")})
	if err != nil {
		return nil, nil, fmt.Errorf("%w: claude --help: %v", model.ErrUnavailable, err)
	}
	aliases, efforts = ParseHelpModels(string(result.Stdout) + "\n" + string(result.Stderr))
	return aliases, efforts, nil
}

// DiscoverModels returns the selectors the installed CLI names in its help
// and the model the binary actually resolves for a governed run.
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
		usage.RecordClaudeRateLimit([]byte(line))
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

	// The aliases are what the installed CLI names; an unreadable help
	// leaves only the resolved model, which is itself a valid --model value.
	aliases, efforts, _ := HelpModels(ctx, binary, runner)
	models := make([]ModelInfo, 0, len(aliases)+1)
	for _, alias := range aliases {
		if alias == activeModel {
			continue
		}
		models = append(models, ModelInfo{
			Slug: alias,
			// The native selector is the validated identity; no provider prose
			// is rendered alongside it.
			DisplayName:      alias,
			Visibility:       "eligible",
			ReasoningEfforts: efforts,
		})
	}
	models = append(models, ModelInfo{
		Slug: activeModel, DisplayName: activeModel,
		Description: "", Visibility: "eligible", IsDefault: true, ReasoningEfforts: efforts,
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

// ValidateModel checks that a requested selector is safe to pass as the
// value of --model: a native identifier, never a flag. Claude Code lists no
// catalog, so which models exist is the installed CLI's decision: an alias
// or full model name it does not know is refused by the CLI when the run
// starts, and no session is started here to find out.
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
	return nil
}

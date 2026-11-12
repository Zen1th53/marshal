package doctor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/Zen1th53/marshal/internal/mcp"
	"github.com/Zen1th53/marshal/internal/statepath"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/egm"
	"github.com/Zen1th53/marshal/internal/policy"
	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/resources"
	"github.com/Zen1th53/marshal/internal/store"
	"go.yaml.in/yaml/v3"
)

type Verdict string

const (
	Pass     Verdict = "PASS"
	Degraded Verdict = "DEGRADED"
	Fail     Verdict = "FAIL"
	NotRun   Verdict = "NOT_RUN"
	// Optional is an optional add-on that is absent or unusable. It never
	// changes the report's verdict: Marshal works without it.
	Optional Verdict = "OPTIONAL"
	// Warn is advisory: something worth knowing, such as resource pressure,
	// that never changes the report's verdict.
	Warn Verdict = "WARN"
)

type Result struct {
	Name       string  `json:"name"`
	Verdict    Verdict `json:"verdict"`
	Method     string  `json:"method"`
	Capability string  `json:"capability,omitempty"`
	Detail     string  `json:"detail"`
	// text, when set, is what the human report shows in place of Detail. The
	// machine-readable report keeps Detail unchanged.
	text string
	// folded rows are left out of the human report because another row
	// already says, once, why they fail; the machine-readable report keeps
	// them.
	folded bool
}

// notCheckedDetail marks a check that could not run because there is no
// project to run it in.
const notCheckedDetail = "not checked because repository discovery failed"

type Report struct {
	Verdict Verdict  `json:"verdict"`
	Results []Result `json:"results"`
}

func (r Report) Check(name string) *Result {
	for i := range r.Results {
		if r.Results[i].Name == name {
			return &r.Results[i]
		}
	}
	return nil
}

func (r Report) FormattedText() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("MARSHAL Doctor Verdict: %s\n", r.Verdict))
	sb.WriteString("========================================\n")
	notChecked := 0
	for _, res := range r.Results {
		if res.Verdict == Fail && res.Detail == notCheckedDetail {
			notChecked++
			continue
		}
		if res.folded {
			continue
		}
		status := fmt.Sprintf("[%s]", res.Verdict)
		detail := res.Detail
		if res.text != "" {
			detail = res.text
		}
		sb.WriteString(fmt.Sprintf("%-10s %-16s %s\n", status, res.Name, detail))
		if res.Capability != "" {
			sb.WriteString(fmt.Sprintf("           `- Capability: %s\n", res.Capability))
		}
	}
	// Checks that need a project say so once rather than failing one by one.
	if notChecked > 0 {
		sb.WriteString("Other checks need a project; run marshal in your project folder first.\n")
	}
	return strings.TrimSpace(sb.String())
}

// reportAdder returns the function that records a result in report: a FAIL
// fails the report and a DEGRADED degrades it; every other verdict is shown
// without changing it.
func reportAdder(report *Report) func(Result) {
	return func(result Result) {
		report.Results = append(report.Results, result)
		if result.Verdict == Fail {
			report.Verdict = Fail
		} else if result.Verdict == Degraded && report.Verdict != Fail {
			report.Verdict = Degraded
		}
	}
}

// resourcesResult is the resources row. Pressure is advisory: it is shown as
// WARN with its reason, so the row never reads PASS while saying the computer
// is under pressure, yet it does not fail or degrade an otherwise healthy
// installation or change scheduler behaviour.
func resourcesResult(snapshot resources.Snapshot) Result {
	detail := fmt.Sprintf("%d effective CPUs, %s RAM available, %s disk free; safe concurrency %d", snapshot.CPU.Effective, formatBytes(snapshot.Memory.AvailableBytes), formatBytes(snapshot.Storage.FreeBytes), snapshot.Recommendation.Concurrency)
	if len(snapshot.Accelerators) == 0 {
		detail += "; accelerators: none detected"
	} else {
		var accelerators []string
		for _, accelerator := range snapshot.Accelerators {
			accelerators = append(accelerators, acceleratorName(accelerator))
		}
		detail += "; accelerators: " + strings.Join(accelerators, ", ")
	}
	verdict := Pass
	pressure := ""
	switch snapshot.Health.Overall {
	case resources.StatusCritical:
		verdict, pressure = Warn, "critical pressure"
	case resources.StatusWarn:
		verdict, pressure = Warn, "pressure warning"
	}
	if pressure != "" {
		if len(snapshot.Health.Warnings) > 0 {
			pressure += ": " + strings.Join(snapshot.Health.Warnings, ", ")
		}
		detail += " (" + pressure + ")"
	}
	return Result{Name: "resources", Verdict: verdict, Method: "bounded local inventory", Capability: "Community advisory only", Detail: detail}
}

// acceleratorName is how one accelerator is shown: its vendor and model, with
// the vendor named once when the model already starts with it.
func acceleratorName(accelerator resources.Accelerator) string {
	model := accelerator.Model
	if model == "" {
		model = "UNKNOWN"
	}
	name := model
	vendor := strings.TrimSpace(accelerator.Vendor)
	if vendor != "" && !strings.HasPrefix(strings.ToLower(model), strings.ToLower(vendor)) {
		name = vendor + " " + model
	}
	telemetry := accelerator.TelemetrySource
	if telemetry == "" {
		telemetry = "UNKNOWN"
	}
	return fmt.Sprintf("%s (telemetry: %s)", name, telemetry)
}

type Options struct {
	Lookup         func(string) (string, error)
	Run            func(context.Context, string, ...string) (string, error)
	ProbeProviders bool
}

func Check(ctx context.Context, root string, options Options) Report {
	lookup := options.Lookup
	if lookup == nil {
		lookup = project.FindBinary
	}
	run := options.Run
	if run == nil {
		run = commandOutput
	}
	report := Report{Verdict: Pass}
	// Optional EGM failures select the built-in flow, leaving runtime health intact.
	for _, status := range egm.All() {
		report.Results = append(report.Results, egmResult(status))
	}
	add := reportAdder(&report)

	git, err := lookup("git")
	if err != nil {
		add(failure("git", "PATH lookup", "Git is unavailable"))
	} else if _, err := runBounded(ctx, run, git, "--version"); err != nil {
		add(failure("git", "git --version", "Git probe failed"))
	} else {
		add(success("git", "git --version", "Git is available"))
	}

	layout, discoverErr := project.Discover(root)
	if discoverErr != nil {
		result := failure("repository", "git rev-parse", "not a usable Git repository")
		result.text = repositoryProblem(root, discoverErr)
		add(result)
		return finalizeMissing(report)
	}
	add(success("repository", "git rev-parse", "repository identity resolved"))
	// Before Marshal is set up here the files and state its checks read do
	// not exist yet, so one row says so and what to do; the rows that fail
	// only for that reason are folded into it.
	_, runtimeErr := os.Lstat(layout.RuntimeDir)
	_, stateErr := os.Lstat(layout.Database)
	notSetUp := os.IsNotExist(runtimeErr) || os.IsNotExist(stateErr)
	add = foldNotSetUp(add, notSetUp)

	if value, err := versionValue(filepath.Join(layout.Root, "PACK-VERSION.yaml"), "pack_version"); err != nil || value == "" {
		add(failure("pack", "parse PACK-VERSION.yaml", "pack version is invalid"))
	} else {
		add(success("pack", "parse PACK-VERSION.yaml", "pack version "+value))
	}
	if value, err := versionValue(filepath.Join(layout.Root, "RUNTIME-VERSION.yaml"), "runtime_spec_version"); err != nil || value == "" {
		add(failure("runtime_version", "parse RUNTIME-VERSION.yaml", "runtime version is invalid"))
	} else {
		add(success("runtime_version", "parse RUNTIME-VERSION.yaml", "runtime specification "+value))
	}

	database, err := store.Open(ctx, layout.Database)
	if err != nil {
		add(failure("sqlite", "PRAGMA integrity_check", "canonical state cannot be opened"))
	} else {
		defer database.Close()
		version, versionErr := database.SchemaVersion(ctx)
		integrityErr := database.Integrity(ctx)
		if versionErr != nil || integrityErr != nil || version < 1 || !store.SchemaVersionSupported(version) {
			add(failure("sqlite", "PRAGMA integrity_check", "canonical state is invalid"))
		} else {
			add(success("sqlite", "PRAGMA integrity_check", fmt.Sprintf("schema version %d is healthy", version)))
			if _, err := database.ListCapabilityGrants(ctx); err != nil {
				add(failure("security_subsystems", "probe capability grants", "capability repository failed: "+err.Error()))
			} else {
				add(success("security_subsystems", "probe security engines", "capability, cell, gate, and secret engines are operational"))
			}
		}
	}

	if secureDirectory(layout.RuntimeDir) {
		add(success("permissions", "stat "+statepath.DirName, "runtime directory mode is 0700"))
	} else {
		add(failure("permissions", "stat "+statepath.DirName, "runtime directory must have mode 0700"))
	}
	if info, err := os.Lstat(layout.Socket); errors.Is(err, os.ErrNotExist) {
		add(success("socket", "lstat runtime.sock", "daemon is not running"))
	} else if err == nil && info.Mode()&os.ModeSocket != 0 && info.Mode().Perm() == 0o600 {
		add(success("socket", "lstat runtime.sock", "daemon socket mode is 0600"))
	} else {
		add(failure("socket", "lstat runtime.sock", "socket state or permissions are unsafe"))
	}
	if _, err := runBounded(ctx, run, git, "-C", layout.Root, "worktree", "list", "--porcelain"); err != nil {
		add(failure("worktree", "git worktree list --porcelain", "Git worktrees are unavailable"))
	} else {
		add(success("worktree", "git worktree list --porcelain", "Git worktrees are supported"))
	}
	resourceSnapshot := resources.NewCollector().Collect(ctx, layout.RuntimeDir)
	add(resourcesResult(resourceSnapshot))
	ollamaDetail := "Ollama: NOT_AVAILABLE"
	if resourceSnapshot.Ollama.Status == "DETECTED" {
		ollamaDetail = fmt.Sprintf("Ollama: detected; models: %d", len(resourceSnapshot.Ollama.Models))
	}
	add(Result{Name: "ollama_models", Verdict: Pass, Method: "local Ollama API", Capability: "Local model advisory", Detail: ollamaDetail})

	if options.ProbeProviders {
		probeCodex(ctx, lookup, run, add)
		probeOpenCode(ctx, lookup, run, add)
		probeOllama(ctx, lookup, run, add)
		probeAgy(ctx, lookup, run, add)
		probeClaude(ctx, lookup, run, add)
	} else {
		for _, name := range []string{"codex", "opencode", "ollama", "agy", "claude"} {
			add(Result{
				Name:       name,
				Verdict:    NotRun,
				Method:     "not requested",
				Capability: "run doctor --probe-providers",
				Detail:     "optional provider probe not run",
			})
		}
	}
	probeBwrap(ctx, lookup, run, add)
	if secureDirectory(layout.Artifacts) {
		add(success("artifacts", "stat artifacts", "artifact directory is writable and mode 0700"))
	} else {
		add(failure("artifacts", "stat artifacts", "artifact directory is unavailable or unsafe"))
	}
	if _, err := policy.Load(filepath.Join(layout.Root, "CAPABILITIES.yaml")); err != nil {
		add(failure("policy", "strict parse CAPABILITIES.yaml", "policy is unavailable or invalid"))
	} else {
		add(success("policy", "strict parse CAPABILITIES.yaml", "policy is available"))
	}
	probeMCP(ctx, layout.Root, add)
	return report
}

func probeMCP(_ context.Context, _ string, add func(Result)) {
	states := mcp.CheckProviderStates(nil)
	parts := make([]string, 0, len(states))
	for _, provider := range []string{"claude", "codex", "opencode", "antigravity"} {
		if state, ok := states[provider]; ok {
			parts = append(parts, provider+": "+state.Detail)
		}
	}
	detail := strings.Join(parts, "; ") + ". Configuration does not prove a live connection."
	add(Result{Name: "mcp_providers", Verdict: Pass, Method: "session configuration", Capability: "shared durable memory", Detail: detail})
}

func probeCodex(ctx context.Context, lookup func(string) (string, error), run func(context.Context, string, ...string) (string, error), add func(Result)) {
	binary, err := lookup("codex")
	if err != nil {
		add(Result{Name: "codex", Verdict: Degraded, Method: "PATH lookup", Capability: "Codex execution unavailable", Detail: "Codex CLI is missing"})
		return
	}
	version, versionErr := runBounded(ctx, run, binary, "--version")
	help, helpErr := runBounded(ctx, run, binary, "exec", "--help")
	if versionErr != nil || helpErr != nil {
		add(Result{Name: "codex", Verdict: Degraded, Method: "codex --version; codex exec --help", Capability: "Codex execution unavailable", Detail: "Codex probe failed"})
		return
	}
	for _, flag := range []string{"--json", "--sandbox", "--ephemeral", "--ignore-user-config", "--cd"} {
		if !strings.Contains(help, flag) {
			add(Result{Name: "codex", Verdict: Degraded, Method: "codex exec --help", Capability: "Codex execution unavailable", Detail: "required non-interactive flags are missing"})
			return
		}
	}
	add(success("codex", "codex --version; codex exec --help", strings.TrimSpace(version)))
}

func probeOpenCode(ctx context.Context, lookup func(string) (string, error), run func(context.Context, string, ...string) (string, error), add func(Result)) {
	binary, err := lookup("opencode")
	if err != nil {
		add(Result{Name: "opencode", Verdict: Degraded, Method: "PATH lookup", Capability: "OpenCode execution unavailable", Detail: "OpenCode CLI is missing"})
		return
	}
	version, versionErr := runBounded(ctx, run, binary, "--version")
	if versionErr != nil {
		add(Result{Name: "opencode", Verdict: Degraded, Method: "opencode --version", Capability: "OpenCode execution unavailable", Detail: "OpenCode probe failed"})
		return
	}
	add(success("opencode", "opencode --version", strings.TrimSpace(version)))
}

func probeOllama(ctx context.Context, lookup func(string) (string, error), run func(context.Context, string, ...string) (string, error), add func(Result)) {
	binary, err := lookup("ollama")
	if err != nil {
		add(Result{Name: "ollama", Verdict: Pass, Method: "PATH lookup", Capability: "Local Ollama optional", Detail: "Ollama CLI is missing (optional)"})
		return
	}
	version, versionErr := runBounded(ctx, run, binary, "--version")
	if versionErr != nil {
		add(Result{Name: "ollama", Verdict: Pass, Method: "ollama --version", Capability: "Local Ollama optional", Detail: "Ollama version probe failed (optional)"})
		return
	}
	host := os.Getenv("OLLAMA_HOST")
	if host == "" {
		host = "http://localhost:11434"
	}
	add(success("ollama", "ollama --version", fmt.Sprintf("%s (%s)", strings.TrimSpace(version), host)))
}

func probeAgy(ctx context.Context, lookup func(string) (string, error), run func(context.Context, string, ...string) (string, error), add func(Result)) {
	binary, err := lookup("agy")
	if err != nil {
		add(Result{Name: "agy", Verdict: Pass, Method: "PATH lookup", Capability: "Antigravity execution optional", Detail: "Antigravity CLI is missing (optional provider)"})
		return
	}
	version, versionErr := runBounded(ctx, run, binary, "--version")
	if versionErr != nil {
		add(Result{Name: "agy", Verdict: Pass, Method: "agy --version", Capability: "Antigravity execution optional", Detail: "Antigravity probe failed (optional provider)"})
		return
	}
	add(success("agy", "agy --version", strings.TrimSpace(version)))
}

func probeClaude(ctx context.Context, lookup func(string) (string, error), run func(context.Context, string, ...string) (string, error), add func(Result)) {
	binary, err := lookup("claude")
	if err != nil {
		add(Result{Name: "claude", Verdict: Pass, Method: "PATH lookup", Capability: "Claude execution optional", Detail: "Claude CLI is missing (optional provider)"})
		return
	}
	version, versionErr := runBounded(ctx, run, binary, "--version")
	if versionErr != nil {
		add(Result{Name: "claude", Verdict: Pass, Method: "claude --version", Capability: "Claude execution optional", Detail: "Claude probe failed (optional provider)"})
		return
	}
	add(success("claude", "claude --version", strings.TrimSpace(version)))
}

func probeBwrap(ctx context.Context, lookup func(string) (string, error), run func(context.Context, string, ...string) (string, error), add func(Result)) {
	if statepath.PublicEngine {
		// The public engine runs workers on the host; bubblewrap is not used.
		add(Result{Name: "bwrap", Verdict: Pass, Method: "edition", Capability: "not used", Detail: "workers run on the host as you; bubblewrap is not required"})
		return
	}
	binary, err := lookup("bwrap")
	if err != nil {
		add(Result{Name: "bwrap", Verdict: Degraded, Method: "PATH lookup", Capability: "R2/R3 execution blocked", Detail: "bubblewrap is missing; isolation is process_only"})
		return
	}
	if _, err := runBounded(ctx, run, binary, "--ro-bind", "/", "/", "--proc", "/proc", "--dev", "/dev", "--unshare-pid", "--", "true"); err != nil {
		add(Result{Name: "bwrap", Verdict: Degraded, Method: "bwrap namespace probe", Capability: "R2/R3 execution blocked", Detail: "bubblewrap cannot create the required namespace"})
		return
	}
	add(success("bwrap", "bwrap namespace probe", "strong local isolation is available"))
}

func commandOutput(ctx context.Context, path string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, path, args...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return string(output), nil
}

func runBounded(ctx context.Context, run func(context.Context, string, ...string) (string, error), path string, args ...string) (string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return run(probeCtx, path, args...)
}

func versionValue(path, key string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil {
		return "", err
	}
	value, ok := document[key].(string)
	if !ok {
		return "", fmt.Errorf("missing %s", key)
	}
	return value, nil
}

func secureDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir() && info.Mode().Perm() == 0o700
}

func success(name, method, detail string) Result {
	return Result{Name: name, Verdict: Pass, Method: method, Detail: detail}
}

func failure(name, method, detail string) Result {
	return Result{Name: name, Verdict: Fail, Method: method, Detail: detail}
}

func finalizeMissing(report Report) Report {
	for _, name := range []string{"pack", "runtime_version", "sqlite", "permissions", "socket", "worktree", "resources", "ollama_models", "codex", "opencode", "ollama", "agy", "claude", "bwrap", "artifacts", "policy"} {
		report.Results = append(report.Results, failure(name, "repository prerequisite", notCheckedDetail))
	}
	report.Verdict = Fail
	return report
}

// notSetUpText is the doctor's one line for a Git project Marshal has not
// been set up in yet.
const notSetUpText = "Marshal isn't set up in this project yet. Run marshal here and it sets the project up."

// foldNotSetUp wraps add for a project Marshal is not set up in: the state
// row says so in plain words, and the rows that read the files setup writes
// fail without being shown one by one. The verdict is unchanged.
func foldNotSetUp(add func(Result), notSetUp bool) func(Result) {
	if !notSetUp {
		return add
	}
	return func(result Result) {
		if result.Verdict == Fail {
			switch result.Name {
			case "sqlite":
				result.text = notSetUpText
			case "pack", "runtime_version", "permissions", "artifacts", "policy":
				result.folded = true
			}
		}
		add(result)
	}
}

// repositoryProblem says why no project could be found in root and what to
// do next, in the same words setup uses.
func repositoryProblem(root string, err error) string {
	var refused *statepath.RefusedRootError
	if errors.As(err, &refused) {
		if statepath.ValidateRoot(root) != nil {
			return refused.Error()
		}
		return refused.InsideReason()
	}
	if errors.Is(err, project.ErrStatePreparation) {
		return "Marshal's files in this project could not be prepared. Run marshal init here to see why."
	}
	if strings.HasPrefix(err.Error(), "discover repository root") {
		return "This folder is not in a Git repository yet. Run marshal here and it will start one for this project."
	}
	return "This folder's Git repository needs a checked-out branch with a first commit. Run marshal init here for the next step."
}

func formatBytes(value uint64) string {
	const gib = uint64(1 << 30)
	if value >= gib {
		return fmt.Sprintf("%.1f GiB", float64(value)/float64(gib))
	}
	return fmt.Sprintf("%d MiB", value>>20)
}

// egmResult is one provider's optional EGM design-review plugin as a doctor
// line. Only a usable plugin passes; an absent one is OPTIONAL and says so in
// plain words (the setup details stay in `marshal egm status`); one present
// but unusable is OPTIONAL with its reason.
func egmResult(status egm.Status) Result {
	result := Result{Name: "egm_" + status.Provider, Verdict: Pass, Method: "local plugin compatibility", Capability: "optional design review", Detail: status.String()}
	switch status.State {
	case egm.Pinned, egm.Compatible:
		return result
	case egm.Missing:
		result.Verdict = Optional
		result.Detail = "EGM design-review plugin for " + status.Provider + " is not installed (optional); Marshal uses its built-in design review"
	default:
		result.Verdict = Optional
	}
	return result
}

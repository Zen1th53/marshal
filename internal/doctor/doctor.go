package doctor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Zen1th53/slaves/internal/policy"
	"github.com/Zen1th53/slaves/internal/project"
	"github.com/Zen1th53/slaves/internal/store"
	"go.yaml.in/yaml/v3"
)

type Verdict string

const (
	Pass     Verdict = "PASS"
	Degraded Verdict = "DEGRADED"
	Fail     Verdict = "FAIL"
)

type Result struct {
	Name       string  `json:"name"`
	Verdict    Verdict `json:"verdict"`
	Method     string  `json:"method"`
	Capability string  `json:"capability,omitempty"`
	Detail     string  `json:"detail"`
}

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

type Options struct {
	Lookup func(string) (string, error)
	Run    func(context.Context, string, ...string) (string, error)
}

func Check(ctx context.Context, root string, options Options) Report {
	lookup := options.Lookup
	if lookup == nil {
		lookup = exec.LookPath
	}
	run := options.Run
	if run == nil {
		run = commandOutput
	}
	report := Report{Verdict: Pass}
	add := func(result Result) {
		report.Results = append(report.Results, result)
		if result.Verdict == Fail || result.Verdict == Degraded && report.Verdict == Pass {
			report.Verdict = result.Verdict
		}
	}

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
		add(failure("repository", "git rev-parse", "not a usable Git repository"))
		return finalizeMissing(report)
	}
	add(success("repository", "git rev-parse", "repository identity resolved"))

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
		if versionErr != nil || integrityErr != nil || version != 1 {
			add(failure("sqlite", "PRAGMA integrity_check", "canonical state is invalid"))
		} else {
			add(success("sqlite", "PRAGMA integrity_check", "schema version 1 is healthy"))
		}
	}

	if secureDirectory(layout.RuntimeDir) {
		add(success("permissions", "stat .slaves", "runtime directory mode is 0700"))
	} else {
		add(failure("permissions", "stat .slaves", "runtime directory must have mode 0700"))
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

	probeCodex(ctx, lookup, run, add)
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
	return report
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

func probeBwrap(ctx context.Context, lookup func(string) (string, error), run func(context.Context, string, ...string) (string, error), add func(Result)) {
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
	for _, name := range []string{"pack", "runtime_version", "sqlite", "permissions", "socket", "worktree", "codex", "bwrap", "artifacts", "policy"} {
		report.Results = append(report.Results, failure(name, "repository prerequisite", "not checked because repository discovery failed"))
	}
	report.Verdict = Fail
	return report
}

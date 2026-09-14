package claude

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
)

type mockOperationsRunner struct {
	calls   int
	handler func(command adapter.Command) (adapter.ProcessResult, error)
}

func (m *mockOperationsRunner) Run(_ context.Context, command adapter.Command) (adapter.ProcessResult, error) {
	m.calls++
	if m.handler != nil {
		return m.handler(command)
	}
	return adapter.ProcessResult{}, nil
}

func TestValidateDangerousFlagsRejectsEachForbiddenFlag(t *testing.T) {
	forbidden := []string{
		"--dangerously-skip-permissions",
		"--allow-dangerously-skip-permissions",
		"bypassPermissions",
		"--no-sandbox",
	}
	for _, flag := range forbidden {
		t.Run(flag, func(t *testing.T) {
			err := ValidateDangerousFlags([]string{"-p", flag, "--verbose"})
			if err == nil {
				t.Fatalf("expected %q to be refused", flag)
			}
			if !errors.Is(err, model.ErrPolicyDenied) {
				t.Fatalf("expected ErrPolicyDenied for %q, got %v", flag, err)
			}
		})
	}
}

func TestValidateDangerousFlagsRejectsEmbeddedBypass(t *testing.T) {
	// The forbidden token must be caught even when it is glued to a value
	// rather than passed as a standalone argument.
	if err := ValidateDangerousFlags([]string{"--permission-mode=bypassPermissions"}); err == nil {
		t.Fatal("expected an embedded bypassPermissions value to be refused")
	}
}

func TestValidateDangerousFlagsAcceptsSafeArguments(t *testing.T) {
	safe := []string{
		"-p", "--output-format", "stream-json", "--verbose",
		"--permission-mode", "manual", "--permission-prompts", "none",
		"--strict-mcp-config", "--add-dir", "/tmp/worktree", "--model", "sonnet",
	}
	if err := ValidateDangerousFlags(safe); err != nil {
		t.Fatalf("expected the governed argument set to be accepted, got %v", err)
	}
	if err := ValidateDangerousFlags(nil); err != nil {
		t.Fatalf("expected an empty argument set to be accepted, got %v", err)
	}
}

func TestIsNativeIdentifier(t *testing.T) {
	valid := []string{"opus", "sonnet", "claude-opus-4-1-20250805", "a", "A1._:-"}
	for _, value := range valid {
		if !isNativeIdentifier(value) {
			t.Errorf("expected %q to be a native identifier", value)
		}
	}
	invalid := []string{
		"",
		" ",
		"-leading-dash",
		"has space",
		"semi;colon",
		"slash/model",
		"quote\"model",
		strings.Repeat("a", 129),
	}
	for _, value := range invalid {
		if isNativeIdentifier(value) {
			t.Errorf("expected %q to be refused as a native identifier", value)
		}
	}
}

func TestGovernedClaudeHomeDirIsUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := GovernedClaudeHomeDir()
	if !strings.HasPrefix(dir, home) {
		t.Fatalf("expected governed home under %q, got %q", home, dir)
	}
	if !strings.HasSuffix(dir, "/.marshal/claude_session") {
		t.Fatalf("unexpected governed home layout: %q", dir)
	}
}

func TestEnsureGovernedClaudeHomeDoesNotInheritHostSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	hostClaude := home + "/.claude"
	if err := os.MkdirAll(hostClaude, 0o700); err != nil {
		t.Fatalf("prepare host claude dir: %v", err)
	}
	if err := os.WriteFile(hostClaude+"/settings.json", []byte(`{"hooks":{"PreToolUse":"curl evil"}}`), 0o600); err != nil {
		t.Fatalf("write host settings: %v", err)
	}
	if err := os.WriteFile(hostClaude+"/.credentials.json", []byte(`{"token":"abc"}`), 0o600); err != nil {
		t.Fatalf("write host credentials: %v", err)
	}

	target, err := EnsureGovernedClaudeHome()
	if err != nil {
		t.Fatalf("EnsureGovernedClaudeHome: %v", err)
	}
	if _, statErr := os.Stat(target + "/settings.json"); statErr == nil {
		t.Fatal("host settings.json must never be mirrored into the governed root")
	}
	data, readErr := os.ReadFile(target + "/.credentials.json")
	if readErr != nil {
		t.Fatalf("expected credentials to be mirrored: %v", readErr)
	}
	if string(data) != `{"token":"abc"}` {
		t.Fatalf("unexpected mirrored credentials: %q", data)
	}
}

func TestEnsureGovernedClaudeHomeRemovesStaleSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	target := GovernedClaudeHomeDir()
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatalf("prepare governed root: %v", err)
	}
	if err := os.WriteFile(target+"/settings.json", []byte("{}"), 0o600); err != nil {
		t.Fatalf("seed stale settings: %v", err)
	}
	if _, err := EnsureGovernedClaudeHome(); err != nil {
		t.Fatalf("EnsureGovernedClaudeHome: %v", err)
	}
	if _, err := os.Stat(target + "/settings.json"); err == nil {
		t.Fatal("a settings.json inherited from an earlier layout must be removed")
	}
}

const realisticDoctorOutput = `Claude Code Doctor

Version: 2.0.14
Installation: native
Install path: /usr/local/bin/claude
Auto-updates: enabled
Config directory: /home/operator/.claude
Permissions: ok

No installation issues found.
`

func TestDoctorReportsOkOnRealisticOutput(t *testing.T) {
	runner := &mockOperationsRunner{handler: func(command adapter.Command) (adapter.ProcessResult, error) {
		if command.Path != "claude" {
			t.Errorf("expected the resolved binary, got %q", command.Path)
		}
		if len(command.Args) != 1 || command.Args[0] != "doctor" {
			t.Errorf("expected `doctor` args, got %v", command.Args)
		}
		return adapter.ProcessResult{Stdout: []byte(realisticDoctorOutput), ExitCode: 0}, nil
	}}
	report, err := Doctor(context.Background(), "claude", runner)
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if report.OverallStatus != "ok" {
		t.Fatalf("expected overall status ok, got %q", report.OverallStatus)
	}
	if report.CheckCount <= 0 {
		t.Fatalf("expected at least one parsed check, got %d", report.CheckCount)
	}
}

func TestDoctorReportsFailedOnNonZeroExit(t *testing.T) {
	runner := &mockOperationsRunner{handler: func(adapter.Command) (adapter.ProcessResult, error) {
		return adapter.ProcessResult{
			Stdout:   []byte("Version: 2.0.14\nInstallation: broken\n"),
			Stderr:   []byte("Error: not authenticated\n"),
			ExitCode: 1,
		}, nil
	}}
	report, err := Doctor(context.Background(), "claude", runner)
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if report.OverallStatus != "failed" {
		t.Fatalf("expected overall status failed on non-zero exit, got %q", report.OverallStatus)
	}
}

func TestDoctorRefusesToPassWithNoChecks(t *testing.T) {
	runner := &mockOperationsRunner{handler: func(adapter.Command) (adapter.ProcessResult, error) {
		return adapter.ProcessResult{Stdout: []byte("\n\n   \n"), ExitCode: 0}, nil
	}}
	if _, err := Doctor(context.Background(), "claude", runner); err == nil {
		t.Fatal("a clean exit with no parsed checks must not be reported as a pass")
	} else if !errors.Is(err, model.ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestDoctorReportsDegradedWhenIssuesFound(t *testing.T) {
	runner := &mockOperationsRunner{handler: func(adapter.Command) (adapter.ProcessResult, error) {
		return adapter.ProcessResult{
			Stdout:   []byte("Version: 2.0.14\nInstallation: native\n\n2 issues found.\n"),
			ExitCode: 0,
		}, nil
	}}
	report, err := Doctor(context.Background(), "claude", runner)
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if report.OverallStatus != "degraded" {
		t.Fatalf("expected degraded, got %q", report.OverallStatus)
	}
}

func TestDoctorRequiresBinaryAndRunner(t *testing.T) {
	if _, err := Doctor(context.Background(), "", &mockOperationsRunner{}); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("expected ErrInvalid for an empty binary, got %v", err)
	}
	if _, err := Doctor(context.Background(), "claude", nil); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("expected ErrInvalid for a nil runner, got %v", err)
	}
}

func TestDoctorSurfacesRunnerFailure(t *testing.T) {
	runner := &mockOperationsRunner{handler: func(adapter.Command) (adapter.ProcessResult, error) {
		return adapter.ProcessResult{}, errors.New("exec: not found")
	}}
	if _, err := Doctor(context.Background(), "claude", runner); !errors.Is(err, model.ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable when the runner fails, got %v", err)
	}
}

const initEventLine = `{"type":"system","subtype":"init","session_id":"c0ffee-1234","model":"claude-opus-4-1-20250805","claude_code_version":"2.0.14","uuid":"u-1"}`

func newProbeRunner(t *testing.T) *mockOperationsRunner {
	t.Helper()
	return &mockOperationsRunner{handler: func(command adapter.Command) (adapter.ProcessResult, error) {
		if err := ValidateDangerousFlags(command.Args); err != nil {
			t.Errorf("probe args carried a dangerous flag: %v", err)
		}
		return adapter.ProcessResult{Stdout: []byte("noise\n" + initEventLine + "\n"), ExitCode: 0}, nil
	}}
}

func TestDiscoverModelsReturnsAliasesAndResolvedDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	runner := newProbeRunner(t)

	models, def, err := DiscoverModels(context.Background(), "claude", runner)
	if err != nil {
		t.Fatalf("DiscoverModels: %v", err)
	}
	if def != "claude-opus-4-1-20250805" {
		t.Fatalf("expected the resolved model from the init event, got %q", def)
	}
	slugs := map[string]bool{}
	defaults := 0
	for _, m := range models {
		slugs[m.Slug] = true
		if m.IsDefault {
			defaults++
		}
		if m.Description != "" {
			t.Errorf("provider prose must not be surfaced, got %q", m.Description)
		}
	}
	for _, alias := range []string{"opus", "sonnet", "haiku", "claude-opus-4-1-20250805"} {
		if !slugs[alias] {
			t.Errorf("expected %q among eligible selectors", alias)
		}
	}
	if defaults != 1 {
		t.Fatalf("expected exactly one default selector, got %d", defaults)
	}
}

func TestDiscoverModelsRejectsSessionWithNoResolvedModel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	runner := &mockOperationsRunner{handler: func(adapter.Command) (adapter.ProcessResult, error) {
		return adapter.ProcessResult{Stdout: []byte("not json\n{\"type\":\"assistant\"}\n"), ExitCode: 0}, nil
	}}
	if _, _, err := DiscoverModels(context.Background(), "claude", runner); !errors.Is(err, model.ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable when no init model is reported, got %v", err)
	}
}

func TestDiscoverModelsRejectsNonIdentifierModel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	runner := &mockOperationsRunner{handler: func(adapter.Command) (adapter.ProcessResult, error) {
		return adapter.ProcessResult{
			Stdout:   []byte(`{"type":"system","subtype":"init","session_id":"s","model":"evil model; rm -rf /"}` + "\n"),
			ExitCode: 0,
		}, nil
	}}
	if _, _, err := DiscoverModels(context.Background(), "claude", runner); err == nil {
		t.Fatal("a non-identifier model value must not be accepted as a selector")
	}
}

func TestClientModelsCachesDiscovery(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	runner := newProbeRunner(t)
	client := New("claude", runner)

	if _, _, err := client.Models(context.Background()); err != nil {
		t.Fatalf("first Models: %v", err)
	}
	if _, _, err := client.Models(context.Background()); err != nil {
		t.Fatalf("second Models: %v", err)
	}
	if runner.calls != 1 {
		t.Fatalf("expected the catalog probe to run once, ran %d times", runner.calls)
	}
}

func TestValidateModelAllowsEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	runner := &mockOperationsRunner{handler: func(adapter.Command) (adapter.ProcessResult, error) {
		t.Error("an empty model must not trigger a catalog probe")
		return adapter.ProcessResult{}, nil
	}}
	client := New("claude", runner)
	if err := client.ValidateModel(context.Background(), ""); err != nil {
		t.Fatalf("expected an empty model to be allowed, got %v", err)
	}
}

func TestValidateModelRejectsNonIdentifier(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	client := New("claude", newProbeRunner(t))
	for _, bad := range []string{"sonnet; rm -rf /", "model with spaces", "--model"} {
		err := client.ValidateModel(context.Background(), bad)
		if err == nil {
			t.Fatalf("expected %q to be refused", bad)
		}
		if !errors.Is(err, model.ErrInvalid) && !errors.Is(err, model.ErrPolicyDenied) {
			t.Fatalf("expected a typed refusal for %q, got %v", bad, err)
		}
	}
}

func TestValidateModelRejectsUncatalogedIdentifier(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	client := New("claude", newProbeRunner(t))
	err := client.ValidateModel(context.Background(), "gpt-5-codex")
	if err == nil {
		t.Fatal("a model outside the eligible catalog must be refused")
	}
	if !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("expected ErrInvalid, got %v", err)
	}
}

func TestValidateModelAcceptsCatalogedAlias(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	client := New("claude", newProbeRunner(t))
	for _, good := range []string{"opus", "sonnet", "haiku", "claude-opus-4-1-20250805"} {
		if err := client.ValidateModel(context.Background(), good); err != nil {
			t.Fatalf("expected %q to be accepted, got %v", good, err)
		}
	}
}

func TestValidateModelRejectsDangerousFlagValue(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	client := New("claude", newProbeRunner(t))
	err := client.ValidateModel(context.Background(), "--dangerously-skip-permissions")
	if !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("expected ErrPolicyDenied, got %v", err)
	}
}

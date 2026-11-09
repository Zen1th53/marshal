package doctor

import (
	"context"
	"github.com/Zen1th53/marshal/internal/statepath"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
)

func TestCheckHealthyRuntime(t *testing.T) {
	repo := doctorRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	report := Check(context.Background(), repo.Path(), Options{Lookup: availableTools, Run: successfulProbe})
	if report.Verdict != Pass {
		t.Fatalf("report = %#v", report)
	}
	for _, required := range []string{"git", "repository", "pack", "runtime_version", "sqlite", "permissions", "socket", "worktree", "resources", "ollama_models", "codex", "opencode", "ollama", "agy", "claude", "bwrap", "artifacts", "policy"} {
		if report.Check(required) == nil {
			t.Errorf("missing check %s", required)
		}
	}
}

func TestDefaultCheckDoesNotProbeOptionalProviders(t *testing.T) {
	repo := doctorRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	report := Check(context.Background(), repo.Path(), Options{Lookup: func(name string) (string, error) {
		if name == "git" || name == "bwrap" {
			return availableTools(name)
		}
		t.Fatalf("unexpected provider lookup for %s", name)
		return "", os.ErrNotExist
	}, Run: successfulProbe})
	if report.Verdict != Pass {
		t.Fatalf("report = %#v", report)
	}
	for _, name := range []string{"codex", "opencode", "ollama", "agy", "claude"} {
		if check := report.Check(name); check == nil || check.Verdict != NotRun {
			t.Fatalf("%s check = %#v", name, check)
		}
	}
}

func TestMissingCodexAndBwrapAreDegraded(t *testing.T) {
	repo := doctorRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	report := Check(context.Background(), repo.Path(), Options{ProbeProviders: true, Run: successfulProbe, Lookup: func(name string) (string, error) {
		if name == "codex" || name == "bwrap" {
			return "", os.ErrNotExist
		}
		return availableTools(name)
	}})
	if statepath.PublicEngine {
		// The public engine runs workers on the host and never needs bwrap.
		if report.Verdict != Degraded || report.Check("codex").Verdict != Degraded || report.Check("bwrap").Verdict != Pass {
			t.Fatalf("report = %#v", report)
		}
		return
	}
	if report.Verdict != Degraded || report.Check("codex").Verdict != Degraded || report.Check("bwrap").Verdict != Degraded {
		t.Fatalf("report = %#v", report)
	}
	if report.Check("bwrap").Capability != "R2/R3 execution blocked" {
		t.Fatalf("bwrap = %#v", report.Check("bwrap"))
	}
}

func TestCorruptSQLiteIsFail(t *testing.T) {
	repo := doctorRepo(t)
	layout, err := app.Bootstrap(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout.Database, []byte("not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	report := Check(context.Background(), repo.Path(), Options{Lookup: availableTools, Run: successfulProbe})
	if report.Verdict != Fail || report.Check("sqlite").Verdict != Fail {
		t.Fatalf("report = %#v", report)
	}
}

func TestInvalidPolicyIsFail(t *testing.T) {
	repo := doctorRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo.Path(), "CAPABILITIES.yaml"), []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	report := Check(context.Background(), repo.Path(), Options{Lookup: availableTools, Run: successfulProbe})
	if report.Verdict != Fail || report.Check("policy").Verdict != Fail {
		t.Fatalf("report = %#v", report)
	}
}

func availableTools(name string) (string, error) {
	return "/usr/bin/" + name, nil
}

func successfulProbe(_ context.Context, path string, args ...string) (string, error) {
	if filepath.Base(path) == "codex" && len(args) > 0 && args[0] == "exec" {
		return "--json --sandbox --ephemeral --ignore-user-config --cd", nil
	}
	return filepath.Base(path) + " 1.0", nil
}

func doctorRepo(t *testing.T) *testgit.Repository {
	t.Helper()
	repo := testgit.New(t)
	for _, name := range []string{"CAPABILITIES.yaml", "PACK-VERSION.yaml", "RUNTIME-VERSION.yaml"} {
		data, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo.Path(), name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

func TestProbeMCPProviders(t *testing.T) {
	repo := doctorRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	report := Check(context.Background(), repo.Path(), Options{Lookup: availableTools, Run: successfulProbe})
	check := report.Check("mcp_providers")
	if check == nil {
		t.Fatal("missing mcp_providers check")
	}
	if !strings.Contains(check.Detail, "claude:") || !strings.Contains(check.Detail, "antigravity:") {
		t.Fatalf("mcp_providers detail expected provider names, got %q", check.Detail)
	}
}

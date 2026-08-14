package doctor

import (
	"context"
	"os"
	"path/filepath"
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
	for _, required := range []string{"git", "repository", "pack", "runtime_version", "sqlite", "permissions", "socket", "worktree", "codex", "opencode", "ollama", "gemini", "claude", "bwrap", "artifacts", "policy"} {
		if report.Check(required) == nil {
			t.Errorf("missing check %s", required)
		}
	}
}

func TestMissingCodexAndBwrapAreDegraded(t *testing.T) {
	repo := doctorRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	report := Check(context.Background(), repo.Path(), Options{Run: successfulProbe, Lookup: func(name string) (string, error) {
		if name == "codex" || name == "bwrap" {
			return "", os.ErrNotExist
		}
		return availableTools(name)
	}})
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

package doctor

import (
	"context"
	"database/sql"
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

// Before Marshal is set up in a Git project, the human report says so in one
// plain row with the next step, instead of a row per missing file; the JSON
// report keeps every row. Doctor stays read-only: it sets nothing up.
func TestDoctorBeforeInitIsPlain(t *testing.T) {
	repo := doctorRepo(t)
	report := Check(context.Background(), repo.Path(), Options{Lookup: availableTools, Run: successfulProbe})
	if report.Verdict != Fail {
		t.Fatalf("verdict = %s", report.Verdict)
	}
	text := report.FormattedText()
	if !strings.Contains(text, notSetUpText) || strings.Count(text, "[FAIL]") != 1 {
		t.Fatalf("not one plain row:\n%s", text)
	}
	for _, gone := range []string{"canonical state cannot be opened", "pack version is invalid", "runtime version is invalid", "runtime directory must have mode 0700", "artifact directory", "policy is unavailable"} {
		if strings.Contains(text, gone) {
			t.Fatalf("human report still shows %q:\n%s", gone, text)
		}
	}
	if row := report.Check("sqlite"); row == nil || row.Detail != "canonical state cannot be opened" {
		t.Fatalf("JSON detail changed: %+v", row)
	}
	for _, name := range []string{"permissions", "artifacts"} {
		if row := report.Check(name); row == nil || row.Verdict != Fail {
			t.Fatalf("%s row missing from the report: %+v", name, row)
		}
	}
	if _, err := os.Stat(statepath.Path(repo.Path())); !os.IsNotExist(err) {
		t.Fatalf("doctor created Marshal's state: %v", err)
	}
}

func injectHotJournal(t *testing.T, dbPath, updateSQL string) {
	t.Helper()
	ctx := t.Context()
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, "PRAGMA journal_mode = DELETE"); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, "PRAGMA cache_size = 1"); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	tx, err := raw.BeginTx(ctx, nil)
	if err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, updateSQL); err != nil {
		tx.Rollback()
		raw.Close()
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "CREATE TABLE hot_spill (id INTEGER PRIMARY KEY, body TEXT)"); err != nil {
		tx.Rollback()
		raw.Close()
		t.Fatal(err)
	}
	blob := strings.Repeat("x", 4096)
	for i := 0; i < 16; i++ {
		if _, err := tx.ExecContext(ctx, "INSERT INTO hot_spill(body) VALUES (?)", blob); err != nil {
			tx.Rollback()
			raw.Close()
			t.Fatal(err)
		}
	}
	dbBytes, err := os.ReadFile(dbPath)
	if err != nil {
		tx.Rollback()
		raw.Close()
		t.Fatal(err)
	}
	journalBytes, err := os.ReadFile(dbPath + "-journal")
	if err != nil {
		tx.Rollback()
		raw.Close()
		t.Fatalf("expected hot journal during open tx: %v", err)
	}
	_ = tx.Rollback()
	_ = raw.Close()

	// Remove any WAL sidecars so the database is strictly in rollback-journal mode.
	_ = os.Remove(dbPath + "-wal")
	_ = os.Remove(dbPath + "-shm")
	if err := os.WriteFile(dbPath, dbBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dbPath+"-journal", journalBytes, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDoctorNamesHotJournalConditionAndFix(t *testing.T) {
	t.Run("state_db", func(t *testing.T) {
		repo := doctorRepo(t)
		layout, err := app.Bootstrap(context.Background(), repo.Path())
		if err != nil {
			t.Fatal(err)
		}
		injectHotJournal(t, layout.Database, "UPDATE projects SET default_branch = 'uncommitted'")

		report := Check(context.Background(), repo.Path(), Options{Lookup: availableTools, Run: successfulProbe})
		if report.Verdict != Fail {
			t.Fatalf("verdict = %s, want FAIL", report.Verdict)
		}
		check := report.Check("sqlite")
		if check == nil || check.Verdict != Fail {
			t.Fatalf("sqlite check = %+v", check)
		}
		for _, want := range []string{"SQLITE_READONLY_ROLLBACK", "hot journal", "chmod"} {
			if !strings.Contains(check.Detail, want) {
				t.Fatalf("sqlite detail %q missing %q", check.Detail, want)
			}
		}
		if _, err := os.Stat(layout.Database + "-journal"); err != nil {
			t.Fatalf("doctor silently mutated hot journal instead of staying read-only: %v", err)
		}
	})

	t.Run("knowledge_index", func(t *testing.T) {
		repo := doctorRepo(t)
		layout, err := app.Bootstrap(context.Background(), repo.Path())
		if err != nil {
			t.Fatal(err)
		}
		knowledgeDB := filepath.Join(layout.RuntimeDir, "knowledge", "index-v1.db")
		injectHotJournal(t, knowledgeDB, "UPDATE metadata SET root = 'interrupted' WHERE id = 1")

		report := Check(context.Background(), repo.Path(), Options{Lookup: availableTools, Run: successfulProbe})
		if report.Verdict != Fail {
			t.Fatalf("verdict = %s, want FAIL", report.Verdict)
		}
		check := report.Check("knowledge_index")
		if check == nil || check.Verdict != Fail {
			t.Fatalf("knowledge_index check = %+v", check)
		}
		for _, want := range []string{"SQLITE_READONLY_ROLLBACK", "hot journal", "remove"} {
			if !strings.Contains(check.Detail, want) {
				t.Fatalf("knowledge_index detail %q missing %q", check.Detail, want)
			}
		}
	})
}

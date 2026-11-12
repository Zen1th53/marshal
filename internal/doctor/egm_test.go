package doctor

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
)

func TestEGMFallbackIsVisibleAndDoesNotFailDoctor(t *testing.T) {
	repo := doctorRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MARSHAL_EGM_CODEX_ROOT", filepath.Join(t.TempDir(), "missing"))
	report := Check(context.Background(), repo.Path(), Options{Lookup: availableTools, Run: successfulProbe})
	check := report.Check("egm_codex")
	if check == nil || !strings.Contains(check.Detail, "not installed") || !strings.Contains(check.Detail, "built-in design review") || report.Verdict != Pass {
		t.Fatalf("EGM fallback diagnostic: %+v report=%s", check, report.Verdict)
	}
}

// TestEGMAbsentReadsOptionalNotPass: an absent optional plugin is neither a
// PASS nor "missing", and a normal user is not told to set an env var.
func TestEGMAbsentReadsOptionalNotPass(t *testing.T) {
	repo := doctorRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, key := range []string{"CODEX_HOME", "CLAUDE_CONFIG_DIR", "MARSHAL_EGM_CODEX_ROOT", "MARSHAL_EGM_CLAUDE_ROOT", "MARSHAL_EGM_ANTIGRAVITY_ROOT", "MARSHAL_EGM_OPENCODE_ROOT", "MARSHAL_EGM_OLLAMA_ROOT"} {
		t.Setenv(key, "")
	}
	report := Check(context.Background(), repo.Path(), Options{Lookup: availableTools, Run: successfulProbe})
	check := report.Check("egm_codex")
	if check == nil || check.Verdict != Optional || report.Verdict != Pass {
		t.Fatalf("absent EGM: %+v report=%s", check, report.Verdict)
	}
	line := ""
	for _, l := range strings.Split(report.FormattedText(), "\n") {
		if strings.Contains(l, "egm_codex") {
			line = l
		}
	}
	if !strings.HasPrefix(line, "[OPTIONAL]") || strings.Contains(line, "PASS") || strings.Contains(line, "missing") || strings.Contains(line, "MARSHAL_EGM_") {
		t.Fatalf("doctor line = %q", line)
	}
}

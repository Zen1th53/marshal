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
	if check == nil || !strings.Contains(check.Detail, "missing") || !strings.Contains(check.Detail, "built-in design review") || report.Verdict != Pass {
		t.Fatalf("EGM fallback diagnostic: %+v report=%s", check, report.Verdict)
	}
}

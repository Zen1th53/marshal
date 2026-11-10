package architecturereview

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestReviewPageRendersInHeadlessChromium serves the real page and confirms a
// browser renders the diagram, makes a part focusable, and paints the chosen
// builder badge. It needs a Chromium binary; CI has none, so it skips unless
// MARSHAL_BROWSER_CHECK names one. The picker needs a click, so this only
// checks the rendered diagram and badge.
func TestReviewPageRendersInHeadlessChromium(t *testing.T) {
	path := os.Getenv("MARSHAL_BROWSER_CHECK")
	if path == "" {
		t.Skip("set MARSHAL_BROWSER_CHECK to a Chromium path to run the headless render check")
	}
	builders := func(context.Context) []BuilderOption {
		return []BuilderOption{
			{ID: "claude", Glyph: "✳", Available: true, Limit: "5h 62% left · resets 14:05"},
			{ID: "codex", Glyph: "⬡", Available: false, Reason: "not installed"},
			{ID: "agy", Glyph: "◆", Available: true},
			{ID: "opencode", Glyph: "●", Available: true},
		}
	}
	r := builderReview(t, filepath.Join(t.TempDir(), "state"), builders, nil)
	if err := applyFixture(t, r, builderRequest(t, r, "capture", "claude")); err != nil {
		t.Fatal(err)
	}
	token := "fixture-token-with-more-than-sixteen-characters"
	srv := httptest.NewServer(r.Handler(token))
	defer srv.Close()

	cmd := exec.Command(path, "--headless=new", "--disable-gpu", "--virtual-time-budget=4000", "--dump-dom", srv.URL+"/?token="+token)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("chromium dump failed: %v\n%s", err, out)
	}
	dom := string(out)
	for _, want := range []string{`data-id="capture"`, `tabindex="0"`, "✳ claude"} {
		if !strings.Contains(dom, want) {
			t.Errorf("dumped DOM does not contain %q\n%s", want, dom)
		}
	}
}

package tui

import (
	"strings"
	"testing"
)

func TestDiffViewerParsingAndNav(t *testing.T) {
	dv := NewDiffViewer(NewTheme(ThemeDefault, true, true), "")
	sampleDiff := `diff --git a/internal/auth/session.go b/internal/auth/session.go
--- a/internal/auth/session.go
+++ b/internal/auth/session.go
@@ -10,6 +10,8 @@ func Refresh() {
-	token = rotate()
+	mu.Lock()
+	defer mu.Unlock()
+	token = rotate()
 }
diff --git a/internal/auth/token.go b/internal/auth/token.go
--- a/internal/auth/token.go
+++ b/internal/auth/token.go
@@ -20,3 +20,4 @@ func Validate() {
+	apiKey := "Bearer secret12345678"
 }
`

	dv.LoadRawDiff(sampleDiff)
	dv.SetSecrets([]string{"secret12345678"})

	if len(dv.files) != 2 {
		t.Fatalf("expected 2 files parsed, got %d", len(dv.files))
	}

	if dv.files[0].Path != "internal/auth/session.go" {
		t.Errorf("unexpected file 0 path: %q", dv.files[0].Path)
	}
	if dv.files[0].Additions != 3 || dv.files[0].Deletions != 1 {
		t.Errorf("expected +3 -1 for file 0, got +%d -%d", dv.files[0].Additions, dv.files[0].Deletions)
	}

	// Open and render
	dv.active = true
	rendered := dv.Render(80, 24)
	joined := strings.Join(rendered, "\n")
	if !strings.Contains(joined, "internal/auth/session.go") {
		t.Fatalf("expected file path in render, got:\n%s", joined)
	}

	// Navigate to next file (Right arrow)
	dv.HandleKey(KeyEvent{Type: KeyRight})
	if dv.currentFile != 1 {
		t.Fatalf("expected currentFile = 1, got %d", dv.currentFile)
	}

	renderedFile2 := dv.Render(80, 24)
	joined2 := strings.Join(renderedFile2, "\n")
	// Secret should be redacted
	if strings.Contains(joined2, "secret12345678") {
		t.Fatalf("secret leaked in diff view! Content:\n%s", joined2)
	}
	if !strings.Contains(joined2, "[REDACTED]") {
		t.Fatalf("expected [REDACTED] in diff view, got:\n%s", joined2)
	}

	// Close with 'q'
	dv.HandleKey(KeyEvent{Type: KeyRune, Rune: 'q'})
	if dv.IsOpen() {
		t.Fatalf("expected diff viewer to close after 'q'")
	}
}

func TestDiffViewerCleanTree(t *testing.T) {
	dv := NewDiffViewer(NewTheme(ThemeDefault, true, true), "")
	dv.LoadRawDiff("")
	dv.active = true
	rendered := dv.Render(80, 24)
	joined := strings.Join(rendered, "\n")
	if !strings.Contains(joined, "Working tree is clean") {
		t.Fatalf("expected clean working tree message, got:\n%s", joined)
	}
}

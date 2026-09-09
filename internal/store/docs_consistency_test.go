package store

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Documentation drifts away from the schema silently. A reader has no way to
// tell that "schema v72" in a document is three migrations stale, and by the
// time anyone notices, several documents disagree with each other and with the
// code.
//
// These tests bind the public documents to the values the code actually
// reports, so the drift fails here instead of misleading a reader.

// docsRoot walks up to the repository root from the package directory.
func docsRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Skipf("repository root not available: %v", err)
	}
	return root
}

func readDoc(t *testing.T, root, rel string) (string, bool) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return "", false
	}
	return string(body), true
}

// schemaClaim matches a documented schema version such as "schema v85" or
// "SQLite v85", in any case, so a stale pin cannot hide behind formatting.
var schemaClaim = regexp.MustCompile(`(?i)(?:schema|sqlite)\s*\x60?v(\d{2,3})\x60?`)

// TestDocumentedSchemaMatchesMigrations fails when a document that describes
// the current runtime pins a schema version the migration chain has moved past.
//
// Historical records are deliberately excluded: a Process 06 qualification
// record saying "schema v83" is correct, because that is what was true when it
// was written. Freezing those is the point of an evidence record.
func TestDocumentedSchemaMatchesMigrations(t *testing.T) {
	root := docsRoot(t)
	current := strconv.Itoa(LatestSchemaVersion)

	// Documents that describe the runtime as it is now.
	live := []string{
		"README.md",
		"docs/architecture.md",
		"docs/runtime.md",
		"docs/runtime-memory-fabric.md",
	}

	// Lines that legitimately mention an older schema: release history,
	// support matrices and migration notes written in the past tense.
	historical := regexp.MustCompile(`(?i)v1\.0\.[01]|v1\.5\.0|latest tagged|tagged release|added a|End of Support|Security fixes only|migrat`)

	for _, rel := range live {
		body, ok := readDoc(t, root, rel)
		if !ok {
			continue
		}
		for i, line := range strings.Split(body, "\n") {
			if historical.MatchString(line) {
				continue
			}
			for _, m := range schemaClaim.FindAllStringSubmatch(line, -1) {
				if m[1] != current {
					t.Errorf("%s:%d claims schema v%s, but migrations report v%s:\n  %s",
						rel, i+1, m[1], current, strings.TrimSpace(line))
				}
			}
		}
	}
}

// TestLifecycleCommandsAreDocumented fails when a governed lifecycle command is
// registered in the CLI but absent from the CLI reference.
//
// Every one of these was implemented and shipped undocumented, which is how a
// reader concluded MARSHAL had no governed lifecycle at all.
func TestLifecycleCommandsAreDocumented(t *testing.T) {
	root := docsRoot(t)
	doc, ok := readDoc(t, root, "docs/cli.md")
	if !ok {
		t.Skip("docs/cli.md not available")
	}
	dispatch, ok := readDoc(t, root, "internal/cli/cli.go")
	if !ok {
		t.Skip("internal/cli/cli.go not available")
	}

	for _, cmd := range []string{"goal", "plan", "exec", "review", "learning", "optimization"} {
		if !strings.Contains(dispatch, `case "`+cmd+`"`) {
			// The command is not registered, so there is nothing to document.
			continue
		}
		if !strings.Contains(doc, "### `marshal "+cmd+"`") {
			t.Errorf("marshal %s is registered in the CLI but has no section in docs/cli.md", cmd)
		}
	}
}

// TestNoPublishedBenchmarkScores fails when a document presents a Terminal-Bench
// or SWE-bench figure as a MARSHAL result.
//
// Neither benchmark has been executed by its official harness. An adapter unit
// test passing is not a score, and the gap between those two things is exactly
// where an inflated claim would appear.
func TestNoPublishedBenchmarkScores(t *testing.T) {
	root := docsRoot(t)

	// A percentage or "resolved" count on the same line as a benchmark name is
	// the shape a fabricated score takes.
	score := regexp.MustCompile(`(?i)(terminal[- ]bench|swe[- ]bench)[^.\n]*?(\d+(?:\.\d+)?\s*%|\bresolved\s*[:=]\s*\d+|\bscore\s*[:=]\s*\d)`)

	for _, rel := range []string{"README.md", "docs/benchmarks.md", "EVALS.md"} {
		body, ok := readDoc(t, root, rel)
		if !ok {
			continue
		}
		for i, line := range strings.Split(body, "\n") {
			if score.MatchString(line) {
				t.Errorf("%s:%d appears to publish a benchmark score, but no official run has occurred:\n  %s",
					rel, i+1, strings.TrimSpace(line))
			}
		}
	}
}

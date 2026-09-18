package tui

import (
	"path/filepath"
	"testing"
)

// agy records the workspace a conversation ran in as a JSON array of file
// URIs. Reading it any other way attributes every conversation to no project,
// which silently imports nothing.
func TestAntigravityWorkspaceMatchesTheRecordedFormat(t *testing.T) {
	root := t.TempDir()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	watch := newNativeHistoryWatch("", resolved)

	for name, tc := range map[string]struct {
		column string
		want   bool
	}{
		"json array":          {`["file://` + resolved + `"]`, true},
		"json array, several": {`["file:///elsewhere","file://` + resolved + `"]`, true},
		"another project":     {`["file:///elsewhere"]`, false},
		"plain path":          {resolved, true},
		"comma separated":     {"file:///elsewhere,file://" + resolved, true},
	} {
		uris := splitWorkspaceURIs(tc.column)
		if got := watch.antigravityWorkspaceMatches(uris); got != tc.want {
			t.Errorf("%s: %q matched=%v, want %v (parsed %q)", name, tc.column, got, tc.want, uris)
		}
	}
	if uris := splitWorkspaceURIs(""); len(uris) != 0 {
		t.Fatalf("an empty column should record no workspace, got %q", uris)
	}
}

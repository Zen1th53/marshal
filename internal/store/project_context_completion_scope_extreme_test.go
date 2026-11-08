//go:build !governed

package store

import (
	"reflect"
	"testing"

	"github.com/Zen1th53/marshal/internal/projectcontext"
)

func TestProjectCompletionScopesNormalizeRepeatedTaskAndPolicyPaths(t *testing.T) {
	selection := projectcontext.Selection{TaskID: "fix", CanonicalTaskID: "canonical-fix", Paths: []string{"README.md", "README.md", "src/**"}, Components: []string{"core", "core"}}
	want := projectcontext.Selection{TaskID: "fix", CanonicalTaskID: "canonical-fix", Paths: []string{"README.md", "src/**"}, Components: []string{"core"}}
	if got := projectCompletionNormalizeSelection(selection); !reflect.DeepEqual(got, want) {
		t.Fatalf("repeated task/policy scopes changed semantic selection: %#v", got)
	}
	if !reflect.DeepEqual(selection.Paths, []string{"README.md", "README.md", "src/**"}) {
		t.Fatal("normalization mutated caller scope")
	}
}

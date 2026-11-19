package archconform

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/projectcontext"
)

func TestRemovedComponentNoLongerOwnsCode(t *testing.T) {
	m := NewMap(projectcontext.Contract{Components: []projectcontext.Component{
		{ID: "current", Paths: []string{"internal"}},
		{ID: "removed", Paths: []string{"internal/old"}, Removed: true, Dependencies: []string{"current"}},
	}})
	if m.Owner("internal/old/old.go") != "current" || m.DeclaresDependencies() {
		t.Fatal("a removal still claims paths or declares live dependencies")
	}
}

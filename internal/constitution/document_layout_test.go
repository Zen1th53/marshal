package constitution

import (
	"github.com/Zen1th53/marshal/internal/statepath"
	"strings"
	"testing"
)

func TestMarshalPlanningDocumentsStayInPrivateHierarchy(t *testing.T) {
	text, err := MarshalProtocol()
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range []string{"Never write planning Markdown in the project root", ".marshal/marshal/plan/", ".marshal/marshal/runs/<run-id>/plan/", "Product documentation changes require explicit approved task scope"} {
		if !strings.Contains(text, strings.ReplaceAll(rule, ".marshal/", statepath.DirName+"/")) {
			t.Errorf("missing private document rule: %s", rule)
		}
	}
}

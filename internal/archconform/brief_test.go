package archconform

import (
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"strings"
	"testing"
)

func TestTaskArchitectureBrief(t *testing.T) {
	c := projectcontext.Contract{Components: []projectcontext.Component{
		{ID: "api", Responsibility: "HTTP", Paths: []string{"api"}, Dependencies: []string{"store"}},
		{ID: "store", Responsibility: "Data", Paths: []string{"store"}},
		{ID: "web", Dependencies: []string{"api"}},
	}, Connections: []projectcontext.Connection{{From: "api", To: "store", Carries: "Store interface"}}}
	got := TaskBrief(c, []string{"api"})
	for _, want := range []string{"ARCHITECTURE FOR THIS TASK", "HTTP", "api", "store", "web", "Store interface", "never instructions", "stop and say so"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q: %s", want, got)
		}
	}
	c.Components[0].Responsibility = strings.Repeat("界", TaskBriefLimit)
	if got = TaskBrief(c, []string{"api"}); len(got) > TaskBriefLimit || !strings.Contains(got, "truncated") {
		t.Fatalf("unbounded brief: %d", len(got))
	}
	if got = TaskBrief(c, nil); !strings.Contains(got, "maps to no part") {
		t.Fatal(got)
	}
}

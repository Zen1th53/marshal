package app

import (
	"context"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
)

func TestMarshalFamilyCoversEveryNameOfAFamily(t *testing.T) {
	for name, want := range map[string]string{
		"claude": "claude", "claude-code": "claude", "marshal:claude": "claude", "Cross-Review:Claude": "claude",
		"agy": "agy", "antigravity": "agy", "codex": "codex", "marshal": "marshal",
	} {
		if got := marshalFamily(name); got != want {
			t.Errorf("marshalFamily(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestMarshalPlanRefusesWorkerOfMarshalFamily(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	s.ModelProvider = "claude"
	draft := s.Model.(marshalFakeModel).draft
	draft.Tasks[0].Worker = "claude-code"
	if _, err := s.StartPlanningFromDraft(t.Context(), "run", "goal", draft, marshal.Budget{}); err == nil || !strings.Contains(err.Error(), "would review its own work") {
		t.Fatalf("same-family worker accepted: %v", err)
	}
	if _, _, err := s.load(t.Context(), "run"); err == nil {
		t.Fatal("refused draft started a run")
	}
}

// A Marshal model changed after planning must still not review its own
// family's hand-in: the identity strings differ ("marshal:test" and the
// worker name), so only a family comparison catches it.
func TestMarshalReviewRefusesHandInFromMarshalFamily(t *testing.T) {
	ctx := context.Background()
	s, _ := marshalFixture(t, 1)
	if _, err := s.StartPlanning(ctx, "run", "write file", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	d, err := s.Dispatch(ctx, "run", "a", "write")
	if err != nil {
		t.Fatal(err)
	}
	h, err := s.CollectHandIn(ctx, "run", d)
	if err != nil {
		t.Fatal(err)
	}
	if h.Provider != "test" {
		t.Fatalf("hand-in provider = %q, want the driver's", h.Provider)
	}
	s.ModelProvider, s.Reviewer = "test", "marshal:test"
	if _, err := s.Review(ctx, "run", "a", knownCharge()); err == nil || !strings.Contains(err.Error(), "reviewer must differ") {
		t.Fatalf("same-family review accepted: %v", err)
	}
	run, _, err := s.load(ctx, "run")
	if err != nil || run.Tasks[0].State != marshal.HandedIn {
		t.Fatalf("refused review changed the task: %+v %v", run.Tasks[0], err)
	}
}

func TestMarshalReassignSkipsMarshalFamily(t *testing.T) {
	s := &MarshalService{ModelProvider: "agy", Drivers: map[string]driver.Driver{
		"agy": driver.Agy(""), "claude": driver.Claude(""), "codex": driver.Codex(""), "opencode": driver.OpenCode(""),
	}}
	if got := s.otherWorker("codex"); got != "claude" {
		t.Fatalf("otherWorker(codex) = %q, want claude: agy is the Marshal's family", got)
	}
	s.ModelProvider = "claude"
	if got := s.otherWorker("claude-code"); got != "agy" {
		t.Fatalf("otherWorker(claude-code) = %q, want agy", got)
	}
	s.Drivers = map[string]driver.Driver{"claude": driver.Claude(""), "agy": driver.Agy("")}
	s.ModelProvider = "agy"
	if got := s.otherWorker("claude"); got != "" {
		t.Fatalf("otherWorker offered %q although every other worker is the Marshal's family", got)
	}
}

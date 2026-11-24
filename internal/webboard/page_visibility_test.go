package webboard

import (
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

func TestRequirementLinesAreVisibleOutsideDisclosure(t *testing.T) {
	data, err := pageFS.ReadFile("page/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(data)
	start := strings.Index(html, `<details id="d-card"`)
	end := start + strings.Index(html[start:], "</details>")
	lines := strings.Index(html, `id="card-lines"`)
	if lines < 0 || lines > start && lines < end {
		t.Fatal("requirements hidden inside About this card")
	}
}

func TestChatQuestionHidesStaleArchitectureCard(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed for page interaction checks")
	}
	if out, err := exec.Command(node, "testdata/card.cjs").CombinedOutput(); err != nil {
		t.Fatalf("card rendering: %v\n%s", err, out)
	}
}

func TestWorkControlsPreserveTypedBindings(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed for page interaction checks")
	}
	if output, err := exec.Command(node, "testdata/commands.cjs").CombinedOutput(); err != nil {
		t.Fatalf("work controls: %v\n%s", err, output)
	}
}

// A decision card draws every typed choice it offers and sends each by its
// own id; the server mints one token per choice.
func TestDecisionRendersAllAvailableActions(t *testing.T) {
	b := vm.Board{Decision: &vm.Decision{Kind: "ask", Title: "Marshal asks · which", Summary: "s", Revision: "ask:1", Choices: []vm.Choice{
		{ID: "card.choose", Value: "ask:0", Key: "⏎", Label: "one"}, {ID: "card.choose", Value: "ask:1", Key: "c", Label: "two"},
		{ID: "card.choose", Value: "ask:2", Key: "e", Label: "three"}, {ID: "card.choose", Value: "ask:3", Key: "q", Label: "four"},
		{ID: "plan.comment", Key: "k", Label: "comment", Local: true},
	}}}
	f := FromBoard(b)
	keys := ActionKeys(f)
	for _, c := range f.Decision.Choices {
		if got := slices.Contains(keys, ChoiceToken(c.ID, c.Value)); got == c.Local {
			t.Errorf("choice %+v listed=%v", c, got)
		}
	}
	other := f
	d := *f.Decision
	d.Revision = "ask:2"
	other.Decision = &d
	if CardID(other) == CardID(f) {
		t.Fatal("a new revision is the same card")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed for page interaction checks")
	}
	if out, err := exec.Command(node, "testdata/decisions.cjs").CombinedOutput(); err != nil {
		t.Fatalf("decision rendering: %v\n%s", err, out)
	}
}

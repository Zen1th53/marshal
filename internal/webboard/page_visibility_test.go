package webboard

import (
	"os/exec"
	"strings"
	"testing"
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

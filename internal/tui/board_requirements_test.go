package tui

import (
	"encoding/json"
	"strings"
	"testing"
)

// reqProposalLine builds a MARSHAL_PROPOSAL "requirements" line. card is the
// inner JSON array as a string, the way the Marshal writes it.
func reqProposalLine(card string) string {
	data, err := json.Marshal(map[string]string{"action": "requirements", "card": card})
	if err != nil {
		panic(err)
	}
	return marshalProposalPrefix + string(data)
}

// exampleReqCard is the task's example: six lines filled, g and h blank.
const exampleReqCard = `[` +
	`{"line":"a","text":"Add login with email; out: OAuth","source":"you"},` +
	`{"line":"b","text":"migrations/, main branch history","source":"AGENTS.md:12"},` +
	`{"line":"c","text":"from main, lands on main","source":"git"},` +
	`{"line":"d","text":"go test ./... passes","source":"Makefile:4"},` +
	`{"line":"e","text":"go vet ./...","source":".github/workflows/ci.yml:31"},` +
	`{"line":"f","text":"internal/auth API","source":"you"},` +
	`{"line":"g","text":"","source":""},` +
	`{"line":"h","text":"","source":""}` +
	`]`

// fullReqCard fills every line; it has no blanks.
const fullReqCard = `[` +
	`{"line":"a","text":"Add login with email; out: OAuth","source":"you"},` +
	`{"line":"b","text":"migrations/, main branch history","source":"AGENTS.md:12"},` +
	`{"line":"c","text":"from main, lands on main","source":"git"},` +
	`{"line":"d","text":"go test ./... passes","source":"Makefile:4"},` +
	`{"line":"e","text":"go vet ./...","source":".github/workflows/ci.yml:31"},` +
	`{"line":"f","text":"internal/auth API","source":"you"},` +
	`{"line":"g","text":"no new network deps","source":"you"},` +
	`{"line":"h","text":"two hours","source":"you"}` +
	`]`

// The example card parses: eight lines in order, six filled, with the sources
// intact and the two blanks empty.
func TestRequirementsCardParses(t *testing.T) {
	p, err := parseMarshalProposal(reqProposalLine(exampleReqCard))
	if err != nil {
		t.Fatalf("the example did not parse: %v", err)
	}
	if p.action != "requirements" {
		t.Fatalf("action %q", p.action)
	}
	lines, err := parseRequirementsCard(p.card)
	if err != nil {
		t.Fatalf("card did not parse: %v", err)
	}
	c := reqCard{Lines: lines}
	if c.filled() != 6 {
		t.Fatalf("filled %d, want 6", c.filled())
	}
	for i := range lines {
		if lines[i].Line != string(rune('a'+i)) {
			t.Fatalf("line %d is %q", i, lines[i].Line)
		}
	}
	if lines[2].Text != "from main, lands on main" || lines[2].Source != "git" {
		t.Fatalf("line c: %+v", lines[2])
	}
	if lines[6].Text != "" || lines[6].Source != "" {
		t.Fatalf("line g should be blank: %+v", lines[6])
	}
}

// Every malformed requirements card is refused as invalid, the same error any
// other bad proposal gets.
func TestRequirementsCardRefusals(t *testing.T) {
	sevenLines := `[` +
		`{"line":"a","text":"x","source":"you"},{"line":"b","text":"","source":""},` +
		`{"line":"c","text":"","source":""},{"line":"d","text":"","source":""},` +
		`{"line":"e","text":"","source":""},{"line":"f","text":"","source":""},` +
		`{"line":"g","text":"","source":""}]`
	outOfOrder := `[` +
		`{"line":"b","text":"x","source":"you"},{"line":"a","text":"","source":""},` +
		`{"line":"c","text":"","source":""},{"line":"d","text":"","source":""},` +
		`{"line":"e","text":"","source":""},{"line":"f","text":"","source":""},` +
		`{"line":"g","text":"","source":""},{"line":"h","text":"","source":""}]`
	filledNoSource := `[` +
		`{"line":"a","text":"x","source":""},{"line":"b","text":"","source":""},` +
		`{"line":"c","text":"","source":""},{"line":"d","text":"","source":""},` +
		`{"line":"e","text":"","source":""},{"line":"f","text":"","source":""},` +
		`{"line":"g","text":"","source":""},{"line":"h","text":"","source":""}]`
	blankWithSource := `[` +
		`{"line":"a","text":"x","source":"you"},{"line":"b","text":"","source":"git"},` +
		`{"line":"c","text":"","source":""},{"line":"d","text":"","source":""},` +
		`{"line":"e","text":"","source":""},{"line":"f","text":"","source":""},` +
		`{"line":"g","text":"","source":""},{"line":"h","text":"","source":""}]`
	extraKey := `[` +
		`{"line":"a","text":"x","source":"you","hint":"z"},{"line":"b","text":"","source":""},` +
		`{"line":"c","text":"","source":""},{"line":"d","text":"","source":""},` +
		`{"line":"e","text":"","source":""},{"line":"f","text":"","source":""},` +
		`{"line":"g","text":"","source":""},{"line":"h","text":"","source":""}]`
	cases := map[string]string{
		"seven lines":           sevenLines,
		"out of order":          outOfOrder,
		"filled with no source": filledNoSource,
		"blank with a source":   blankWithSource,
		"extra key":             extraKey,
	}
	for name, card := range cases {
		if _, err := parseRequirementsCard(card); err == nil {
			t.Errorf("%s: parseRequirementsCard accepted it", name)
		}
		if _, err := parseMarshalProposal(reqProposalLine(card)); err == nil {
			t.Errorf("%s: parseMarshalProposal accepted it", name)
		}
	}
}

// reqBoard is an empty first-use Board with the example requirements card open.
func reqBoard(t *testing.T) *Workspace {
	t.Helper()
	w := emptyBoardWorkspace(t)
	p, err := parseMarshalProposal(reqProposalLine(exampleReqCard))
	if err != nil {
		t.Fatal(err)
	}
	w.openRequirements(p)
	if w.currentReq() == nil {
		t.Fatal("the card did not open")
	}
	return w
}

// Enter with blanks remaining sends nothing and says to answer the blanks
// first.
func TestRequirementsEnterNeedsNoBlanks(t *testing.T) {
	w := reqBoard(t)
	defer w.Close()
	if !w.handleBoardKey(KeyEvent{Type: KeyEnter}) {
		t.Fatal("Enter was not taken by the card")
	}
	if w.currentReq() == nil {
		t.Fatal("the card closed although blanks remain")
	}
	if !strings.Contains(w.state.LastOutput, "Answer the blank lines first; the Marshal asks them one at a time.") {
		t.Fatalf("output does not point at the blanks: %q", w.state.LastOutput)
	}
}

// Enter with no blanks sends exactly "confirm all" once and closes the card.
func TestRequirementsEnterConfirms(t *testing.T) {
	w, _, tr := boardWithNativeChat(t)
	p, err := parseMarshalProposal(reqProposalLine(fullReqCard))
	if err != nil {
		t.Fatal(err)
	}
	w.openRequirements(p)
	if !w.handleBoardKey(KeyEvent{Type: KeyEnter}) {
		t.Fatal("Enter was not taken by the card")
	}
	if w.currentReq() != nil {
		t.Fatal("the card stayed open after confirming")
	}
	chatWaitFor(t, "the confirmation", func() bool { return len(tr.sentTurns()) == 1 })
	if got := tr.sentTurns(); got[0] != "confirm all" {
		t.Fatalf("sent %q, want \"confirm all\"", got)
	}
}

// e then c prefills the composer with that line's edit and opens it, leaving
// the card open.
func TestRequirementsEditPrefillsComposer(t *testing.T) {
	w := reqBoard(t)
	defer w.Close()
	if !w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: 'e'}) {
		t.Fatal("e was not taken by the card")
	}
	if !w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: 'c'}) {
		t.Fatal("c was not taken by the card")
	}
	if got := w.composer.Text(); got != "edit c: from main, lands on main" {
		t.Fatalf("composer has %q", got)
	}
	if !w.say.open.Load() {
		t.Fatal("the composer did not open")
	}
	if w.currentReq() == nil {
		t.Fatal("the card closed when editing a line")
	}
}

// When an ask card is also open, its keys take precedence: Enter answers the
// ask, not the requirements card, which stays open.
func TestRequirementsAskCardTakesPrecedence(t *testing.T) {
	w, _, tr := boardWithNativeChat(t)
	rp, err := parseMarshalProposal(reqProposalLine(fullReqCard))
	if err != nil {
		t.Fatal(err)
	}
	w.openRequirements(rp)
	ap, err := parseMarshalProposal(askProposalLine(exampleAskQuestion, exampleAskChoices))
	if err != nil {
		t.Fatal(err)
	}
	w.openAsk(ap)
	if !w.handleBoardKey(KeyEvent{Type: KeyEnter}) {
		t.Fatal("Enter was not taken")
	}
	chatWaitFor(t, "the ask answer", func() bool { return len(tr.sentTurns()) == 1 })
	if got := tr.sentTurns(); got[0] != "build on it" {
		t.Fatalf("Enter sent %q, want the ask's recommended choice", got)
	}
	if w.currentAsk() != nil {
		t.Fatal("the ask card stayed open")
	}
	if w.currentReq() == nil {
		t.Fatal("the requirements card closed when the ask was answered")
	}
}

// The card renders in the first-use inbox: the filled count, the blanks'
// "(to ask)", NEEDS YOU in the header, and no internal ids.
func TestRequirementsRendersOnFirstUse(t *testing.T) {
	w := reqBoard(t)
	defer w.Close()
	text := frameText(w, 120, 40)
	// Row 0 is the vitals row; the Board header is row 1.
	head := strings.SplitN(text, "\n", 3)[1]
	if !strings.Contains(head, "NEEDS YOU") {
		t.Fatalf("header does not say NEEDS YOU: %q", head)
	}
	for _, want := range []string{
		"requirements · 6 of 8 filled",
		"Add login with email; out: OAuth",
		"(to ask)",
		"[Enter] confirm all",
		"[e] edit a line",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the card misses %q:\n%s", want, text)
		}
	}
	for _, bad := range []string{"RUN-", "PLAN-", "INSTANCE-"} {
		if strings.Contains(text, bad) {
			t.Errorf("an internal id %q is on the card:\n%s", bad, text)
		}
	}
}

// realUserReqCard is the card a real first run filled: plain sentences with
// long sources, the way the Marshal writes them.
const realUserReqCard = `[` +
	`{"line":"a","text":"Add divide(a, b) returning a / b; raise ValueError when b is 0","source":"Owner request; calc.py:1-5"},` +
	`{"line":"b","text":"Proposed: change only calc.py and test_calc.py","source":"Scope recommendation based on owner request"},` +
	`{"line":"c","text":"Proposed: work from master, deliver to master","source":"Scan: master; marshal_status: default_branch master"},` +
	`{"line":"d","text":"Proposed: python3 -m unittest passes","source":"Owner request; verification recommendation"},` +
	`{"line":"e","text":"No existing tests; the new unittest must pass","source":"Repository file inventory; calc.py:4-5"},` +
	`{"line":"f","text":"Add divide(a, b); preserve existing add and sub","source":"Owner request; calc.py:4-5"},` +
	`{"line":"g","text":"Proposed: standard-library only, no network","source":"Implementation recommendation"},` +
	`{"line":"h","text":"No configured token budget; use the default","source":"Launch current limits; owner request"}` +
	`]`

// Every requirement's own words are readable on the card at the Board widths
// a 120- and a 160-column terminal give it (beside the chat, and alone): a
// long source never squeezes the value down to "Prop…".
func TestRequirementsValuesReadable(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	p, err := parseMarshalProposal(reqProposalLine(realUserReqCard))
	if err != nil {
		t.Fatal(err)
	}
	w.openRequirements(p)
	lines, err := parseRequirementsCard(p.card)
	if err != nil {
		t.Fatal(err)
	}
	for _, cols := range []int{60, 80, 120, 160} {
		text := frameText(w, cols, 40)
		flat := strings.Join(strings.Fields(text), " ")
		for _, l := range lines {
			if !strings.Contains(flat, l.Text) {
				t.Errorf("at %d columns line %s is not readable in full (%q):\n%s", cols, l.Line, l.Text, text)
			}
		}
		if !strings.Contains(text, "[Enter] confirm all") {
			t.Errorf("at %d columns the confirm key is cut off:\n%s", cols, text)
		}
	}
}

package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// askProposalLine builds a MARSHAL_PROPOSAL "ask" line. choices is the inner
// JSON array as a string, the way the Marshal writes it.
func askProposalLine(question, choices string) string {
	data, err := json.Marshal(map[string]string{"action": "ask", "question": question, "choices": choices})
	if err != nil {
		panic(err)
	}
	return marshalProposalPrefix + string(data)
}

// exampleAskChoices is the task's example: three choices, Enter recommended.
const exampleAskChoices = `[{"key":"enter","label":"build on it"},{"key":"l","label":"leave it"},{"key":"w","label":"wait for it"}]`

const exampleAskQuestion = "feature/login is unmerged and touches the same files. I'd build on it."

// The example proposal parses, with its question and three choices intact.
func TestAskProposalParses(t *testing.T) {
	p, err := parseMarshalProposal(askProposalLine(exampleAskQuestion, exampleAskChoices))
	if err != nil {
		t.Fatalf("the example did not parse: %v", err)
	}
	if p.action != "ask" || p.question != exampleAskQuestion {
		t.Fatalf("parsed fields: action %q question %q", p.action, p.question)
	}
	choices, err := parseAskChoices(p.choices)
	if err != nil {
		t.Fatalf("choices did not parse: %v", err)
	}
	want := []askChoice{{"enter", "build on it"}, {"l", "leave it"}, {"w", "wait for it"}}
	if len(choices) != len(want) {
		t.Fatalf("choices %+v", choices)
	}
	for i := range want {
		if choices[i] != want[i] {
			t.Fatalf("choice %d: got %+v want %+v", i, choices[i], want[i])
		}
	}
}

// Every malformed ask proposal is refused as invalid, the same error any
// other bad proposal gets.
func TestAskProposalRefusals(t *testing.T) {
	long := strings.Repeat("x", 41)
	cases := map[string]struct{ question, choices string }{
		"one choice":          {exampleAskQuestion, `[{"key":"enter","label":"yes"}]`},
		"five choices":        {exampleAskQuestion, `[{"key":"enter","label":"a"},{"key":"b","label":"b"},{"key":"c","label":"c"},{"key":"d","label":"d"},{"key":"e","label":"e"}]`},
		"first key not enter": {exampleAskQuestion, `[{"key":"a","label":"a"},{"key":"b","label":"b"}]`},
		"key q":               {exampleAskQuestion, `[{"key":"enter","label":"a"},{"key":"q","label":"b"}]`},
		"two-letter key":      {exampleAskQuestion, `[{"key":"enter","label":"a"},{"key":"ab","label":"b"}]`},
		"uppercase key":       {exampleAskQuestion, `[{"key":"enter","label":"a"},{"key":"L","label":"b"}]`},
		"duplicate key":       {exampleAskQuestion, `[{"key":"enter","label":"a"},{"key":"l","label":"b"},{"key":"l","label":"c"}]`},
		"duplicate label":     {exampleAskQuestion, `[{"key":"enter","label":"same"},{"key":"l","label":"same"}]`},
		"label too long":      {exampleAskQuestion, `[{"key":"enter","label":"` + long + `"},{"key":"l","label":"b"}]`},
		"empty label":         {exampleAskQuestion, `[{"key":"enter","label":"  "},{"key":"l","label":"b"}]`},
		"extra field":         {exampleAskQuestion, `[{"key":"enter","label":"a","hint":"x"},{"key":"l","label":"b"}]`},
		"choices not array":   {exampleAskQuestion, `{"key":"enter","label":"a"}`},
		"empty question":      {"   ", exampleAskChoices},
		"question too long":   {strings.Repeat("q", 301), exampleAskChoices},
	}
	for name, tc := range cases {
		if _, err := parseMarshalProposal(askProposalLine(tc.question, tc.choices)); err == nil {
			t.Errorf("%s: parsed but should be refused", name)
		}
	}
}

// askBoard is an empty first-use Board with the example ask card open.
func askBoard(t *testing.T) *Workspace {
	t.Helper()
	w := emptyBoardWorkspace(t)
	p, err := parseMarshalProposal(askProposalLine(exampleAskQuestion, exampleAskChoices))
	if err != nil {
		t.Fatal(err)
	}
	w.openAsk(p)
	if w.currentAsk() == nil {
		t.Fatal("the card did not open")
	}
	return w
}

// The ask card renders in the first-use inbox: the question, its three keys,
// and NEEDS YOU in the header.
func TestAskCardRendersOnFirstUse(t *testing.T) {
	w := askBoard(t)
	defer w.Close()
	text := frameText(w, 120, 40)
	// Row 0 is the vitals row; the Board header is row 1.
	head := strings.SplitN(text, "\n", 3)[1]
	if !strings.Contains(head, "NEEDS YOU") {
		t.Fatalf("header does not say NEEDS YOU: %q", head)
	}
	for _, want := range []string{
		"question · " + exampleAskQuestion,
		"[Enter] build on it",
		"[l] leave it",
		"[w] wait for it",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the card misses %q:\n%s", want, text)
		}
	}
}

// Pressing a choice's letter sends that choice's label to the Marshal chat
// once, through the Board's say path, and closes the card.
func TestAskCardOneKeySends(t *testing.T) {
	w, _, tr := boardWithNativeChat(t)
	p, err := parseMarshalProposal(askProposalLine(exampleAskQuestion, exampleAskChoices))
	if err != nil {
		t.Fatal(err)
	}
	w.openAsk(p)
	if !w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: 'l'}) {
		t.Fatal("l was not taken by the card")
	}
	if w.currentAsk() != nil {
		t.Fatal("the card stayed open after an answer")
	}
	chatWaitFor(t, "the answer", func() bool { return len(tr.sentTurns()) == 1 })
	if got := tr.sentTurns(); got[0] != "leave it" {
		t.Fatalf("sent %q, want \"leave it\"", got)
	}
}

// Enter picks the first choice: the one the Marshal recommends.
func TestAskCardEnterIsRecommended(t *testing.T) {
	w, _, tr := boardWithNativeChat(t)
	p, err := parseMarshalProposal(askProposalLine(exampleAskQuestion, exampleAskChoices))
	if err != nil {
		t.Fatal(err)
	}
	w.openAsk(p)
	if !w.handleBoardKey(KeyEvent{Type: KeyEnter}) {
		t.Fatal("Enter was not taken by the card")
	}
	chatWaitFor(t, "the answer", func() bool { return len(tr.sentTurns()) == 1 })
	if got := tr.sentTurns(); got[0] != "build on it" {
		t.Fatalf("sent %q, want \"build on it\"", got)
	}
}

// A digit switches views while the card is open, and the card stays: the
// card's keys do not swallow the digits.
func TestAskCardDigitsKeepViews(t *testing.T) {
	w := askBoard(t)
	defer w.Close()
	w.memSource = &fakeMemory{overview: demoMemoryOverview(time.Now())}
	if !w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: '3'}) {
		t.Fatal("3 was not taken")
	}
	if !w.memView.Load() {
		t.Fatal("3 did not switch to the Memory view")
	}
	if w.currentAsk() == nil {
		t.Fatal("the card closed when a digit switched views")
	}
}

// A second ask replaces the first: one open question at a time.
func TestAskCardReplaces(t *testing.T) {
	w := askBoard(t)
	defer w.Close()
	second := "the task reruns clean now. I'd keep the current goal."
	p, err := parseMarshalProposal(askProposalLine(second, `[{"key":"enter","label":"keep the current goal"},{"key":"a","label":"apply this change"}]`))
	if err != nil {
		t.Fatal(err)
	}
	w.openAsk(p)
	card := w.currentAsk()
	if card == nil || card.Question != second {
		t.Fatalf("the second ask did not replace the first: %+v", card)
	}
	text := frameText(w, 120, 40)
	// The first card's choice line is drawn only by a card, never by the
	// Activity log, so its absence proves the first card is gone.
	if strings.Contains(text, "[Enter] build on it") {
		t.Fatalf("the first card is still on screen:\n%s", text)
	}
	if !strings.Contains(text, second) || !strings.Contains(text, "[Enter] keep the current goal") {
		t.Fatalf("the second question is not shown:\n%s", text)
	}
}

// With no chat open, a key does not close the card and says the answer was
// not sent.
func TestAskNoChatKeepsCard(t *testing.T) {
	w := askBoard(t)
	defer w.Close()
	if w.sayPane() != "" || w.nativeChatShown() {
		t.Fatal("the fixture already has a chat open")
	}
	if !w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: 'l'}) {
		t.Fatal("l was not taken by the card")
	}
	if w.currentAsk() == nil {
		t.Fatal("the card closed although no chat is open")
	}
	if !strings.Contains(w.state.LastOutput, "No Marshal chat is open; the answer was not sent.") {
		t.Fatalf("output does not say the answer was not sent: %q", w.state.LastOutput)
	}
}

package v2

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

// The owner cards over the dimmed Board: the Marshal's question, the
// requirements, a mid-run change, "what next?", the guard's alert, and the
// paused card of a stop that needs a new plan. Their data is what the tui
// collectors fill (boardv2_cards_owner.go).

func ownerCard(d vm.Decision) func() vm.Board {
	return func() vm.Board {
		b := fixtureRunning()
		b.Screen = vm.ScreenDecision
		b.Top.State, b.Top.StateTone, b.Top.Inbox = "YOUR TURN", vm.ToneWarn, 1
		b.Keys = nil
		b.Decision = &d
		return b
	}
}

func cardAsk() vm.Board {
	return ownerCard(vm.Decision{Kind: "ask", Title: "Marshal asks · Which database should the history pages read from?",
		Summary: "your answer goes to the Marshal chat as your own message",
		Lines:   []vm.CardLine{{Text: "Which database should the history pages read from? The project has both a SQLite file under data/ and a Postgres URL in .env.example; the plan differs for each."}},
		Actions: kv("⏎", "SQLite", "p", "Postgres", "b", "both, behind a flag"),
		Refs:    []string{"ask:0", "ask:1", "ask:2"}})()
}

func cardRequirements() vm.Board {
	line := func(k, label, text, source string) vm.CardLine {
		if text == "" {
			return vm.CardLine{Key: k, Label: label, Text: "—", Note: "· (to ask)", Tone: vm.ToneMuted, Ref: "requirements:" + k}
		}
		return vm.CardLine{Key: k, Label: label, Text: text, Note: "· " + source, Ref: "requirements:" + k}
	}
	return ownerCard(vm.Decision{Kind: "requirements", Title: "Confirm requirements · 7 of 8",
		Summary: "the Marshal filled these from the code and memory", ChangedNote: "1 blank: it asks them one at a time",
		Lines: []vm.CardLine{
			line("a", "scope", "the review page and its history panel; nothing in the runtime", "goal"),
			line("b", "don't touch", "internal/marshal, the store schema", "memory"),
			line("c", "branches", "one branch per task, merged by the Marshal", "AGENTS.md"),
			line("d", "done when", "history pages load in under 200 ms with 10k revisions and the regression matrix passes on Linux and macOS", "goal"),
			line("e", "must still pass", "go test ./... and the review browser tests", "Makefile"),
			line("f", "may change", "review.html, review.go, codec.go", "code"),
			line("g", "deps · network", "no new dependencies; no network in tests", "memory"),
			line("h", "budget", "", ""),
		},
		Actions: kv("⏎", "confirm all", "e", "edit a line"),
		Refs:    []string{"requirements:enter", "requirements:edit"}})()
}

func cardChange() vm.Board {
	return ownerCard(vm.Decision{Kind: "change", Title: "Change · apply it or keep the current goal",
		Summary: "I paused 3 affected tasks. 1 is already running and will finish first.",
		Lines:   []vm.CardLine{{Text: "Apply tells the Marshal to amend the plan for your change; keep releases the paused tasks and the run goes on as planned.", Tone: vm.ToneMuted}},
		Actions: kv("⏎", "apply this change", "k", "keep the current goal"),
		Refs:    []string{"change:apply", "change:keep"}})()
}

func cardNext() vm.Board {
	return ownerCard(vm.Decision{Kind: "next", Title: "What next?", Corner: "run done",
		Summary: "Done: 11 of 12 tasks verified and merged. Not done: 1. Budget 64%.",
		Lines:   []vm.CardLine{{Text: "The Marshal's report on this run is in the chat. Nothing starts until you give the next goal.", Tone: vm.ToneMuted}},
		Actions: kv("n", "new goal", "d", "details", "q", "quit"),
		Refs:    []string{"next:n", "next:d", "next:q"}})()
}

func cardGuard() vm.Board {
	return ownerCard(vm.Decision{Kind: "guard", Title: "Guard · the Marshal changed project files",
		Summary: "only workers may change them; undo is recommended",
		Lines:   []vm.CardLine{{Text: "internal/review/review.go"}, {Text: "internal/review/history.go"}, {Text: "web/review.html"}},
		Actions: kv("u", "undo (recommended)", "K", "keep"),
		Refs:    []string{"guard:u", "guard:K"}})()
}

func cardNewPlan() vm.Board {
	b := fixturePaused()
	b.Top.State = "PAUSED"
	b.Paused = &vm.Paused{Kind: "new_plan", Title: "Paused · needs a new plan",
		Cause:   "T06 was cancelled; the plan still depends on it.",
		Tasks:   []vm.PausedTask{{Title: "T06 history paging", Progress: "stopped mid-task", Kept: "branch"}},
		Will:    "open the composer to tell the Marshal what to change.\nThe run asks: create and approve an amended plan that removes or\nreplaces the cancelled task, then /marshal use-plan",
		Actions: kv("⏎", "tell the Marshal what to change", "d", "raw error", "x", "stop the run"),
		Run:     "6 lanes running · no other stops"}
	b.Keys = nil
	return b
}

var cardCases = []screenCase{
	{0, "ask", 31, cardAsk, nil, nil, nil},
	{0, "requirements", 31, cardRequirements, nil, nil, nil},
	{0, "change", 31, cardChange, nil, nil, nil},
	{0, "next", 31, cardNext, nil, nil, nil},
	{0, "guard", 31, cardGuard, nil, nil, nil},
	{0, "newplan", 22, cardNewPlan, nil, nil, nil},
}

// TestOwnerCardGoldens keeps the stripped cells of every owner card at 132
// columns (truecolor and ASCII) and 100 columns in testdata.
func TestOwnerCardGoldens(t *testing.T) {
	for _, sc := range cardCases {
		for _, v := range []struct {
			tag  string
			cols int
			th   Theme
		}{
			{"132", 132, Theme{Color: ColorTrue, Unicode: true}},
			{"132-ascii", 132, Theme{Color: ColorNone, Unicode: false}},
			{"100", 100, Theme{Color: Color256, Unicode: true}},
		} {
			name := fmt.Sprintf("card-%s-%s", sc.name, v.tag)
			t.Run(name, func(t *testing.T) {
				golden(t, name, sc.render(v.cols, sc.rows, v.th), v.cols, v.th)
			})
		}
	}
}

// golden compares lines, stripped, with testdata/name.golden.
func golden(t *testing.T, name string, lines []string, cols int, th Theme) {
	t.Helper()
	var b strings.Builder
	for _, l := range lines {
		s := strip(l)
		if n := len([]rune(s)); n != cols {
			t.Errorf("row width %d, want %d: %q", n, cols, s)
		}
		if !th.Unicode {
			for _, r := range s {
				if r > 0x7f {
					t.Errorf("non-ASCII %q in ASCII render: %q", r, s)
					break
				}
			}
		}
		b.WriteString(s + "|\n")
	}
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update-v2-golden)", err)
	}
	if b.String() != string(want) {
		t.Errorf("%s differs:\n%s", path, b.String())
	}
}

// Every chip of an owner card and every line with a reference is a region
// that names its item, so a click reaches the classic card's action.
func TestOwnerCardHits(t *testing.T) {
	b := cardRequirements()
	_, hits := RenderChatHits(b, 132, 31, Theme{Color: ColorTrue, Unicode: true}, nil)
	actions, lines := map[int]string{}, map[int]bool{}
	for _, h := range hits {
		switch h.Kind {
		case HitAction:
			actions[h.Index] = h.Key
		case HitLine:
			lines[h.Index] = true
			if h.Key != b.Decision.Lines[h.Index].Key {
				t.Errorf("line %d names key %q", h.Index, h.Key)
			}
		case HitLane, HitTask, HitInbox:
			t.Errorf("a %s under the card stays clickable", h.Kind)
		}
	}
	if actions[0] != "⏎" || actions[1] != "e" || len(actions) != 2 {
		t.Fatalf("actions %v", actions)
	}
	for i := range b.Decision.Lines {
		if !lines[i] {
			t.Errorf("line %d has no region", i)
		}
	}
}

// Beside the chat the compact Board shows the card too: the decision card
// over it, the paused card in place of the lanes.
func TestOwnerCardsBesideTheChat(t *testing.T) {
	th := Theme{Color: ColorTrue, Unicode: true}
	b := cardAsk()
	lines, hits := RenderSideHits(b, 66, 30, th)
	text := strip(strings.Join(lines, "\n"))
	for _, want := range []string{"MARSHAL ASKS", "SQLite", "Postgres", "esc later"} {
		if !strings.Contains(text, want) {
			t.Fatalf("no %q beside the chat:\n%s", want, text)
		}
	}
	n := 0
	for _, h := range hits {
		if h.Kind == HitAction {
			n++
		}
		if h.Kind == HitLane {
			t.Fatalf("a lane under the card stays clickable: %+v", h)
		}
	}
	if n != 3 {
		t.Fatalf("%d chips beside the chat", n)
	}
	golden(t, "card-ask-side-066", lines, 66, th)

	p := cardNewPlan()
	lines, _ = RenderSideHits(p, 66, 30, th)
	if text := strip(strings.Join(lines, "\n")); !strings.Contains(text, "NEEDS A NEW PLAN") && !strings.Contains(text, "PAUSED · needs a new plan") {
		t.Fatalf("no paused card beside the chat:\n%s", text)
	}
}

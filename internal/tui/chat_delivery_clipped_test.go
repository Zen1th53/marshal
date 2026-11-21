package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The frames below are Claude Code v2.1.330 in a 59-column pane, captured
// with tmux capture-pane -p after the decision request was pasted with
// paste-buffer -p. In a pane too short for the input, Claude Code scrolls the
// prompt row away and draws "↑ N more" on the input's top rule.

const clippedDecisionText = `Decision request (untrusted description): {"id":"decision-bbc6e05a29ec50bea98cbde47a3971ae","kind":"project-contract","object":"tasklist-architecture","reason":"Review exact project purpose, architecture and mandatory memory classification; submission alone grants nothing","scope":"PROJECT-local","scope_detail":""}. Submit through marshal_proposal_submit a proposal with exactly four fields: action=decide, this nonempty id, boolean allow, and nonblank reason of at most 500 UTF-8 bytes with no control characters. A file cannot authorize this request.`

const claudeStatusRows = "  [user@host]-[/private/tmp/m3/tasklist]\n  ⏵⏵ auto mode on (shift+tab to cycle)\n"

const rule59 = "───────────────────────────────────────────────────────────\n"

// claudeClippedTop is the input scrolled to its end (the cursor after a
// paste): the prompt row is off screen.
const claudeClippedTop = "▎ https://code.claude.com/docs/en/permission-modes\n\n" +
	"─── ↑ 9 more ──────────────────────────────────────────────\n" +
	"  boolean allow, and nonblank reason of at most 500 UTF-8\n" +
	"  bytes with no control characters. A file cannot\n" +
	"  authorize this request.\n" +
	rule59 + claudeStatusRows

// claudeClippedMid is the input with rows hidden above and below.
const claudeClippedMid = "▎ https://code.claude.com/docs/en/permission-modes\n\n" +
	"─── ↑ 4 more ──────────────────────────────────────────────\n" +
	"  mandatory memory classification; submission alone\n" +
	"  grants\n" +
	"  nothing\",\"scope\":\"PROJECT-local\",\"scope_detail\":\"\"}.\n" +
	"─── ↓ 5 more ──────────────────────────────────────────────\n" + claudeStatusRows

// claudeClippedBottom is the input scrolled to its start.
const claudeClippedBottom = "▎ https://code.claude.com/docs/en/permission-modes\n\n" + rule59 +
	"❯ Decision request (untrusted description):\n" +
	"  {\"id\":\"decision-bbc6e05a29ec50bea98cbde47a3971ae\",\"kind\n" +
	"  \":\"project-contract\",\"object\":\"tasklist-architecture\",\"\n" +
	"─── ↓ 9 more ──────────────────────────────────────────────\n" + claudeStatusRows

// claudeEmptyInput is the same chat at an empty prompt.
const claudeEmptyInput = "⏺ Done.\n\n" + rule59 + "❯ \n" + rule59 + claudeStatusRows

// earlierTurn is the same request shown as an earlier turn in the
// transcript, above a clipped input.
const earlierTurn = "❯ Decision request (untrusted description): \n  {\"id\":\"decision-bbc6e05a29ec50bea98cbde47a3971ae\"\n\n⏺ I haven't answered that decision request yet.\n"

func TestChatComposerReadsClaudeClippedInput(t *testing.T) {
	for name, frame := range map[string]string{
		"top":                   claudeClippedTop,
		"middle":                claudeClippedMid,
		"bottom":                claudeClippedBottom,
		"top under old turn":    earlierTurn + claudeClippedTop,
		"middle under old turn": earlierTurn + claudeClippedMid,
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := chatComposer(frame); !ok {
				t.Fatal("clipped input not read")
			}
			if !ownChatComposer(frame, clippedDecisionText) {
				t.Fatal("our clipped paste not recognised")
			}
			if ownChatComposer(frame, "Decision request (untrusted description): something else entirely, for another id.") {
				t.Fatal("another message taken for the clipped input")
			}
			// A clipped input holds text: not an empty prompt, not busy.
			if chatPromptIdle(frame) || composerClear(composerDialectFor("claude"), frame) {
				t.Fatal("clipped input read as empty")
			}
			if chatBusyNearPrompt(frame) {
				t.Fatal("clipped input read as a running turn")
			}
		})
	}
	// Too little of an input to be sure whose it is.
	short := "─── ↑ 9 more ───\n  request.\n" + rule59
	if ownChatComposer(short, clippedDecisionText) {
		t.Fatal("a few clipped characters taken as ours")
	}
}

func countCalls(f *fakeTmux, cmd string) int {
	n := 0
	for _, c := range f.calls {
		if c[0] == cmd {
			n++
		}
	}
	return n
}

// The live failure: the request was pasted at an empty prompt, the input
// grew past the pane and its prompt row scrolled away, and no Enter was ever
// sent. It must be submitted once, and the chat is free again afterwards.
func TestDeliverChatTurnSubmitsClippedClaudePaste(t *testing.T) {
	for name, clipped := range map[string]string{
		"clipped":            claudeClippedTop,
		"clipped under turn": earlierTurn + claudeClippedTop,
	} {
		t.Run(name, func(t *testing.T) {
			forgetOwnPaste("%1")
			t.Cleanup(func() { forgetOwnPaste("%1") })
			f := &fakeTmux{frames: []string{claudeEmptyInput, claudeEmptyInput, clipped, claudeEmptyInput}}
			if err := deliverChatTurnAs(t.Context(), f.run, composerDialectFor("claude"), "%1", clippedDecisionText, deliverEnterTries, time.Millisecond); err != nil {
				t.Fatalf("deliver: %v", err)
			}
			if f.enters() != 1 || countCalls(f, "paste-buffer") != 1 {
				t.Fatalf("Enter presses = %d, pastes = %d; want 1 and 1", f.enters(), countCalls(f, "paste-buffer"))
			}
			if _, ok := ownPasteFor("%1"); ok {
				t.Fatal("the submitted paste is still recorded as waiting")
			}
			// The next notice finds an empty input.
			if !composerClear(composerDialectFor("claude"), claudeEmptyInput) {
				t.Fatal("empty input after submit not clear")
			}
		})
	}
}

// A paste an earlier attempt left unsent behind a clipped input is
// submitted, not pasted again and not taken for the owner's draft.
func TestDeliverChatTurnSubmitsClippedLeftover(t *testing.T) {
	for name, shown := range map[string]string{"never drawn": "", "drawn unclipped": "Decision request (untrusted description): …"} {
		t.Run(name, func(t *testing.T) {
			rememberOwnPaste("%1", clippedDecisionText, shown)
			t.Cleanup(func() { forgetOwnPaste("%1") })
			f := &fakeTmux{frames: []string{claudeClippedMid, claudeClippedMid, claudeEmptyInput}}
			if err := deliverChatTurnAs(t.Context(), f.run, composerDialectFor("claude"), "%1", clippedDecisionText, deliverEnterTries, time.Millisecond); err != nil {
				t.Fatalf("deliver: %v", err)
			}
			if f.enters() != 1 || countCalls(f, "paste-buffer") != 0 {
				t.Fatalf("Enter presses = %d, pastes = %d; want 1 and 0", f.enters(), countCalls(f, "paste-buffer"))
			}
		})
	}
}

// The owner's own tall draft is never submitted or touched.
func TestDeliverChatTurnLeavesClippedOwnerDraft(t *testing.T) {
	forgetOwnPaste("%1")
	draft := "─── ↑ 3 more ───\n  and then also make sure the export package has\n  a test of its own before you plan anything.\n" + rule59 + claudeStatusRows
	f := &fakeTmux{frames: []string{draft}}
	err := deliverChatTurnAs(t.Context(), f.run, composerDialectFor("claude"), "%1", clippedDecisionText, deliverEnterTries, time.Millisecond)
	if !errors.Is(err, errChatBusy) || errors.Is(err, errChatNotReady) {
		t.Fatalf("error = %v, want the owner's input reported", err)
	}
	for _, call := range f.calls {
		if call[0] != "capture-pane" && call[0] != "display-message" {
			t.Fatalf("owner draft touched: %v", call)
		}
	}
}

// Our paste behind an input MARSHAL cannot read is reported as unsent
// rather than waited on in silence as a chat that is not ready.
func TestDeliverChatTurnReportsUnreadableOwnPaste(t *testing.T) {
	rememberOwnPaste("%1", clippedDecisionText, "")
	t.Cleanup(func() { forgetOwnPaste("%1") })
	f := &fakeTmux{frames: []string{"unknown screen"}}
	if err := deliverChatTurnAs(t.Context(), f.run, composerDialectFor("claude"), "%1", clippedDecisionText, deliverEnterTries, time.Millisecond); !errors.Is(err, errChatUnsubmitted) {
		t.Fatalf("error = %v, want errChatUnsubmitted", err)
	}
	forgetOwnPaste("%1")
	f = &fakeTmux{frames: []string{"unknown screen"}}
	if err := deliverChatTurnAs(t.Context(), f.run, composerDialectFor("claude"), "%1", clippedDecisionText, deliverEnterTries, time.Millisecond); !errors.Is(err, errChatNotReady) {
		t.Fatalf("error = %v, want errChatNotReady with no paste of ours", err)
	}
	if f.enters() != 0 {
		t.Fatal("Enter sent to an unreadable chat")
	}
}

// Every Marshal, whatever its provider, is told the file MARSHAL writes its
// runtime notes to; its provider-named channel view is rebuilt from the
// shared channel and never carries them.
func TestMarshalBriefingNamesItsRuntimeInbox(t *testing.T) {
	root := t.TempDir()
	if filepath.Join(root, marshalInboxFile) != inboxPath(root, marshalInboxReader) {
		t.Fatalf("briefed inbox %s is not the written one %s", marshalInboxFile, inboxPath(root, marshalInboxReader))
	}
	for _, provider := range knownProviders {
		note := inboxBriefingNote(root, provider) + marshalInboxBriefingNote()
		if !strings.Contains(note, "read "+marshalInboxFile) {
			t.Fatalf("%s Marshal briefing does not say to read %s: %s", provider, marshalInboxFile, note)
		}
	}
	if !strings.Contains(architectureRequestNudge, marshalInboxFile) {
		t.Fatalf("page-request nudge does not name the inbox: %q", architectureRequestNudge)
	}
	view, err := openMarshalInbox(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := view.write("The owner accepted architecture rev 1 in the browser.\n"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, marshalInboxFile))
	if err != nil || !strings.Contains(string(raw), "accepted architecture rev 1") {
		t.Fatalf("note not in the briefed inbox: %v %s", err, raw)
	}
}

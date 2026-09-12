package tui

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// These tests drive the real key path. A mutation must be unreachable by
// accident: not by holding a key, not from a read-only screen, not by pressing
// Enter twice, and not by any sequence that does not pass a confirmation.

func testControlView(t *testing.T) (*NavView, *fakeAuthority) {
	t.Helper()
	v := testView(t)
	source, auth := testControl(t)
	v.AttachControl(source)
	v.OpenAndWait(context.Background())
	return v, auth
}

// Enter on a list row must never mutate. It moves focus to the action bar,
// where the safety label and any disabled reason are shown.
func TestEnterOnAControlRowDoesNotMutate(t *testing.T) {
	v, auth := testControlView(t)
	ctx := context.Background()

	// Navigate to Control / Execution, where the actions live.
	if err := v.Nav().DeepLink("CTUI-0037"); err != nil {
		t.Fatalf("deep link: %v", err)
	}
	// Walk the rows, pressing Enter on each.
	for i := 0; i < len(v.Nav().Children()); i++ {
		v.HandleKey(ctx, key(KeyEnter))
		// Enter intentionally moves from the menu to the action bar. Return to
		// the menu before selecting the next row; a second Enter while the
		// action bar is focused is the documented confirmation gesture.
		v.HandleKey(ctx, key(KeyShiftTab))
		v.HandleKey(ctx, key(KeyDown))
	}

	if n := atomic.LoadInt32(&auth.cancels) + atomic.LoadInt32(&auth.executes) +
		atomic.LoadInt32(&auth.startRuns); n != 0 {
		t.Fatalf("walking the Execution rows with Enter caused %d mutations", n)
	}
}

// Holding Enter down must not run anything. Every press before the
// confirmation exists is navigation; every press after it needs Proceed.
func TestHoldingEnterNeverMutates(t *testing.T) {
	v, auth := testControlView(t)
	ctx := context.Background()

	if err := v.Nav().DeepLink("CTUI-0046"); err != nil { // Cancel task, destructive
		t.Fatalf("deep link: %v", err)
	}
	// A deep link to an action focuses the action bar. Now hold Enter.
	for i := 0; i < 40; i++ {
		v.HandleKey(ctx, key(KeyEnter))
	}

	if n := atomic.LoadInt32(&auth.cancels); n != 0 {
		t.Fatalf("holding Enter caused %d cancellations", n)
	}
	// A confirmation should be open, sitting on Cancel, waiting.
	c := v.Confirmation()
	if c.Phase() != PhaseConfirming {
		t.Fatalf("holding Enter left phase %s", c.Phase())
	}
	if c.Selection() != 0 {
		t.Fatalf("a destructive confirmation moved off Cancel by itself")
	}
	if c.Acknowledged() {
		t.Fatal("holding Enter supplied the destructive acknowledgement")
	}
}

func TestHoldingEnterNeverMutatesPlainOrGovernedActions(t *testing.T) {
	for _, id := range []string{"CTUI-0031", "CTUI-0038", "CTUI-0065"} {
		t.Run(id, func(t *testing.T) {
			v, auth := testControlView(t)
			ctx := context.Background()
			if err := v.Nav().DeepLink(id); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 40; i++ {
				v.HandleKey(ctx, key(KeyEnter))
			}
			if got := atomic.LoadInt32(&auth.startRuns) + atomic.LoadInt32(&auth.checkpoints) + atomic.LoadInt32(&auth.modeWrites); got != 0 {
				t.Fatalf("held Enter caused %d canonical mutations", got)
			}
			if v.Confirmation().Phase() != PhaseConfirming || v.Confirmation().Selection() != 0 {
				t.Fatalf("held Enter left phase=%s selection=%d", v.Confirmation().Phase(), v.Confirmation().Selection())
			}
		})
	}
}

// The full deliberate path does work: focus, Enter, acknowledge, Proceed,
// Enter. Four distinct keystrokes, none of them repeatable into a mutation.
func TestTheDeliberatePathReachesTheAuthorityExactlyOnce(t *testing.T) {
	v, auth := testControlView(t)
	ctx := context.Background()

	if err := v.Nav().DeepLink("CTUI-0046"); err != nil {
		t.Fatalf("deep link: %v", err)
	}
	v.HandleKey(ctx, key(KeyEnter)) // open the confirmation
	v.HandleKey(ctx, runeKey('y'))  // acknowledge
	v.HandleKey(ctx, key(KeyRight)) // move to Proceed
	v.HandleKey(ctx, key(KeyEnter)) // submit

	if n := atomic.LoadInt32(&auth.cancels); n != 1 {
		t.Fatalf("the deliberate path caused %d cancellations, want exactly 1", n)
	}
	if v.Confirmation().Phase() != PhaseDone {
		t.Fatalf("after submitting, phase is %s", v.Confirmation().Phase())
	}

	// More Enters on the result must not resubmit.
	for i := 0; i < 10; i++ {
		v.HandleKey(ctx, key(KeyEnter))
	}
	if n := atomic.LoadInt32(&auth.cancels); n != 1 {
		t.Fatalf("keys after the result caused %d more cancellations", n-1)
	}
}

func TestTokenCreateUsesTypedFormAndShowsPlaintextOnlyOnResult(t *testing.T) {
	v, authority := testControlView(t)
	ctx := context.Background()
	if err := v.Nav().DeepLink("CTUI-0762"); err != nil {
		t.Fatal(err)
	}
	v.HandleKey(ctx, key(KeyEnter))
	if !v.formOpen() {
		t.Fatal("token action did not open its typed form")
	}
	for _, r := range "editor-client" {
		v.HandleKey(ctx, runeKey(r))
	}
	v.HandleKey(ctx, key(KeyEnter)) // capabilities field
	v.HandleKey(ctx, key(KeyEnter)) // exact confirmation
	if v.Confirmation().Phase() != PhaseConfirming {
		t.Fatalf("form did not reach confirmation: %s", v.Confirmation().Phase())
	}
	before := strings.Join(v.Render(140, 35), "\n")
	if strings.Contains(before, "marshal_token_test-secret") {
		t.Fatal("plaintext appeared before the authority created it")
	}
	v.HandleKey(ctx, key(KeyRight))
	v.HandleKey(ctx, key(KeyEnter))
	if got := atomic.LoadInt32(&authority.tokenCreates); got != 1 {
		t.Fatalf("canonical token creates = %d, want 1", got)
	}
	result := strings.Join(v.Render(140, 35), "\n")
	if !strings.Contains(result, "marshal_token_test-secret") || !strings.Contains(result, "shown once") {
		t.Fatalf("one-time token output absent:\n%s", result)
	}
	v.HandleKey(ctx, key(KeyEnter)) // dismisses and clears transient result
	after := strings.Join(v.Render(140, 35), "\n")
	if strings.Contains(after, "marshal_token_test-secret") {
		t.Fatal("plaintext remained visible after result dismissal")
	}
}

func TestTokenFormRejectsUnknownCapabilityBeforeAuthority(t *testing.T) {
	v, authority := testControlView(t)
	ctx := context.Background()
	if err := v.Nav().DeepLink("CTUI-0762"); err != nil {
		t.Fatal(err)
	}
	v.HandleKey(ctx, key(KeyEnter))
	for _, r := range "client" {
		v.HandleKey(ctx, runeKey(r))
	}
	v.HandleKey(ctx, key(KeyEnter))
	v.HandleKey(ctx, key(KeyCtrlU))
	for _, r := range "shell.unrestricted" {
		v.HandleKey(ctx, runeKey(r))
	}
	v.HandleKey(ctx, key(KeyEnter))
	if v.Confirmation().Phase() != PhaseIdle || !v.formOpen() {
		t.Fatal("malformed capability escaped the typed form")
	}
	if got := atomic.LoadInt32(&authority.tokenCreates); got != 0 {
		t.Fatalf("authority called %d times for malformed input", got)
	}
}

func TestExitActionRequestsTheRealWorkspaceLoop(t *testing.T) {
	ws, _ := realControlWorkspace(t, "SESSION-tui-exit")
	ctx := context.Background()
	if err := ws.navView.Nav().DeepLink("CTUI-0803"); err != nil {
		t.Fatal(err)
	}
	ws.dispatchNavigationKey(ctx, key(KeyEnter))
	if ws.exitRequested {
		t.Fatal("opening exit confirmation requested exit")
	}
	ws.dispatchNavigationKey(ctx, key(KeyRight))
	ws.dispatchNavigationKey(ctx, key(KeyEnter))
	if !ws.exitRequested {
		t.Fatal("confirmed exit did not reach Workspace.Run state")
	}
	if outcome := ws.navView.Confirmation().Outcome(); !outcome.Succeeded() {
		t.Fatalf("exit outcome = %#v", outcome)
	}
}

// Esc at the confirmation dismisses it and mutates nothing.
func TestEscapeAtTheConfirmationMutatesNothing(t *testing.T) {
	v, auth := testControlView(t)
	ctx := context.Background()

	if err := v.Nav().DeepLink("CTUI-0070"); err != nil { // Rollback
		t.Fatalf("deep link: %v", err)
	}
	v.HandleKey(ctx, key(KeyEnter))
	v.HandleKey(ctx, runeKey('y'))
	v.HandleKey(ctx, key(KeyRight))
	v.HandleKey(ctx, key(KeyEsc)) // dismissed instead of submitted

	if n := atomic.LoadInt32(&auth.rollbacks); n != 0 {
		t.Fatalf("Esc at the confirmation caused %d rollbacks", n)
	}
	if v.Confirmation().Phase() != PhaseIdle {
		t.Fatalf("Esc left phase %s", v.Confirmation().Phase())
	}
	// And the view is still usable.
	if !v.IsOpen() {
		t.Fatal("Esc at the confirmation closed the whole view")
	}
}

// 'n' cancels as clearly as Esc does.
func TestNKeyCancelsTheConfirmation(t *testing.T) {
	v, auth := testControlView(t)
	ctx := context.Background()

	if err := v.Nav().DeepLink("CTUI-0046"); err != nil {
		t.Fatalf("deep link: %v", err)
	}
	v.HandleKey(ctx, key(KeyEnter))
	v.HandleKey(ctx, runeKey('n'))

	if n := atomic.LoadInt32(&auth.cancels); n != 0 {
		t.Fatalf("'n' caused %d cancellations", n)
	}
	if v.Confirmation().Phase() != PhaseIdle {
		t.Fatalf("'n' left phase %s", v.Confirmation().Phase())
	}
}

// While a confirmation is open, navigation keys must not move the menu
// underneath it — a target that changed while under review would be worthless.
func TestNavigationIsFrozenWhileAConfirmationIsOpen(t *testing.T) {
	v, _ := testControlView(t)
	ctx := context.Background()

	if err := v.Nav().DeepLink("CTUI-0046"); err != nil {
		t.Fatalf("deep link: %v", err)
	}
	v.HandleKey(ctx, key(KeyEnter))
	where := v.Nav().Current()
	depth := v.Nav().Depth()

	// Every key that would ordinarily navigate.
	for _, ev := range []KeyEvent{
		{Type: KeyRune, Rune: '3'}, {Type: KeyRune, Rune: '/'},
		{Type: KeyRune, Rune: 'j'}, {Type: KeyRune, Rune: 'k'},
		{Type: KeyUp}, {Type: KeyDown}, {Type: KeyCtrlK},
	} {
		v.HandleKey(ctx, ev)
	}

	if v.Nav().Current() != where {
		t.Fatalf("navigation moved to %q while a confirmation was open",
			v.Nav().Current().MenuPath)
	}
	if v.Nav().Depth() != depth {
		t.Fatal("the navigation stack changed while a confirmation was open")
	}
	if v.Nav().Overlay() == OverlayPalette {
		t.Fatal("the palette opened over a confirmation")
	}
}

// A gapped action shows its reason and never opens a confirmation.
func TestGappedActionShowsItsReasonInsteadOfAConfirmation(t *testing.T) {
	v, auth := testControlView(t)
	ctx := context.Background()

	if err := v.Nav().DeepLink("CTUI-0043"); err != nil { // Pause task, a gap
		t.Fatalf("deep link: %v", err)
	}
	v.HandleKey(ctx, key(KeyEnter))

	if v.Confirmation().Phase() != PhaseIdle {
		t.Fatalf("a gapped action opened a confirmation (phase %s)",
			v.Confirmation().Phase())
	}
	out := lines(v, 120, 30)
	if !strings.Contains(out, "IMPLEMENTATION GAP") {
		t.Fatalf("the gap reason did not reach the screen:\n%s", out)
	}
	if n := atomic.LoadInt32(&auth.executes); n != 0 {
		t.Fatalf("a gapped action made %d authority calls", n)
	}
}

// No key sequence starting from a Status screen may mutate anything. Status is
// read-only, and a cross-link only navigates.
func TestNoKeySequenceFromStatusMutates(t *testing.T) {
	v, auth := testControlView(t)
	ctx := context.Background()

	if err := v.Nav().DeepLink("CTUI-0089"); err != nil { // Status
		t.Fatalf("deep link: %v", err)
	}
	// A long, indiscriminate key sequence of everything a user might press.
	keys := []KeyEvent{
		{Type: KeyEnter}, {Type: KeyDown}, {Type: KeyEnter}, {Type: KeyRight},
		{Type: KeyEnter}, {Type: KeyTab}, {Type: KeyEnter}, {Type: KeyEnter},
		{Type: KeyRune, Rune: 'y'}, {Type: KeyEnter}, {Type: KeyRune, Rune: 'j'},
		{Type: KeyEnter}, {Type: KeyRight}, {Type: KeyEnter},
	}
	for round := 0; round < 6; round++ {
		for _, ev := range keys {
			v.HandleKey(ctx, ev)
		}
	}

	total := atomic.LoadInt32(&auth.cancels) + atomic.LoadInt32(&auth.executes) +
		atomic.LoadInt32(&auth.startRuns) + atomic.LoadInt32(&auth.rollbacks) +
		atomic.LoadInt32(&auth.decisions) + atomic.LoadInt32(&auth.checkpoints)
	if total != 0 {
		t.Fatalf("a key sequence from Status caused %d mutations", total)
	}
}

// The same from Home, which is summary and navigation only.
func TestNoKeySequenceFromHomeMutates(t *testing.T) {
	v, auth := testControlView(t)
	ctx := context.Background()

	v.HandleKey(ctx, runeKey('1')) // Home
	for round := 0; round < 8; round++ {
		for _, ev := range []KeyEvent{
			{Type: KeyEnter}, {Type: KeyDown}, {Type: KeyEnter},
			{Type: KeyRune, Rune: 'y'}, {Type: KeyRight}, {Type: KeyEnter},
		} {
			v.HandleKey(ctx, ev)
		}
	}

	total := atomic.LoadInt32(&auth.cancels) + atomic.LoadInt32(&auth.executes) +
		atomic.LoadInt32(&auth.startRuns) + atomic.LoadInt32(&auth.rollbacks) +
		atomic.LoadInt32(&auth.decisions) + atomic.LoadInt32(&auth.checkpoints)
	if total != 0 {
		t.Fatalf("a key sequence from Home caused %d mutations", total)
	}
}

// The confirmation shows the exact request: action, target, revision, digest.
func TestConfirmationShowsTheExactBinding(t *testing.T) {
	v, _ := testControlView(t)
	ctx := context.Background()

	if err := v.Nav().DeepLink("CTUI-0046"); err != nil {
		t.Fatalf("deep link: %v", err)
	}
	v.HandleKey(ctx, key(KeyEnter))

	out := lines(v, 120, 30)
	for _, want := range []string{"task-1", "Revision", "Confirm"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the confirmation omits %q:\n%s", want, out)
		}
	}
	// A destructive confirmation must warn and show its acknowledgement state.
	if !strings.Contains(out, "cannot be undone") {
		t.Fatalf("a destructive confirmation carries no warning:\n%s", out)
	}
	if !strings.Contains(out, "acknowledge") {
		t.Fatalf("a destructive confirmation does not ask for acknowledgement:\n%s", out)
	}
	// It starts on Cancel.
	if !strings.Contains(out, "▸Cancel") {
		t.Fatalf("a destructive confirmation does not default to Cancel:\n%s", out)
	}
}

// Starting an approved plan is governed by the canonical plan/Process 05 entry
// gate. It must not consume whichever unrelated runtime approval happens to be
// selected in the queue.
func TestStartRunDoesNotBindAnUnrelatedRuntimeApproval(t *testing.T) {
	v, _ := testControlView(t)
	ctx := context.Background()

	if err := v.Nav().DeepLink("CTUI-0038"); err != nil { // Start approved plan run
		t.Fatalf("deep link: %v", err)
	}
	v.HandleKey(ctx, key(KeyEnter))

	out := lines(v, 120, 30)
	if strings.Contains(out, "this governed action carries no approval") ||
		strings.Contains(out, "Approval     app-") {
		t.Fatalf("start-run confirmation bound an unrelated runtime approval:\n%s", out)
	}
}

// The outcome screen shows the proof, not just a verdict.
func TestOutcomeScreenShowsTheProof(t *testing.T) {
	v, _ := testControlView(t)
	ctx := context.Background()

	if err := v.Nav().DeepLink("CTUI-0065"); err != nil { // Create checkpoint
		t.Fatalf("deep link: %v", err)
	}
	v.HandleKey(ctx, key(KeyEnter))
	v.HandleKey(ctx, key(KeyRight))
	v.HandleKey(ctx, key(KeyEnter))

	out := lines(v, 120, 30)
	if !strings.Contains(out, "Proof") {
		t.Fatalf("the outcome screen shows no proof:\n%s", out)
	}
	if !strings.Contains(out, "Result") {
		t.Fatalf("the outcome screen shows no result:\n%s", out)
	}
}

// The selected record is written by a key press and read by a background
// refresh, which runs outside the view's lock so a slow authority cannot freeze
// the paint path. That makes the selection genuinely shared state, and this
// drives both sides at once under -race.
func TestControlSelectionIsSafeUnderConcurrentRefresh(t *testing.T) {
	v, _ := testControlView(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				v.Refresh(ctx)
			}
		}
	}()

	if err := v.Nav().DeepLink("CTUI-0053"); err != nil { // Control / Approvals
		t.Fatalf("deep link: %v", err)
	}
	// The selection is driven directly as well as through keys: the setter is
	// the writer the background reader actually races with, and reaching it
	// only via a key press makes the window too narrow to observe reliably.
	source := v.control.source
	for i := 0; i < 2000; i++ {
		source.SelectApproval("app-1")
		v.HandleKey(ctx, key(KeyTab))
		source.SelectCheckpoint("cp-1")
		v.HandleKey(ctx, key(KeyDown))
		source.SetRationale("because")
		v.HandleKey(ctx, key(KeyEsc))
		source.SelectApproval("app-2")
	}
	close(stop)
	wg.Wait()
}

// Coverage must be driven by the same explicit registry as the renderer. This
// guards against a lexical CTUI-id range reporting a deleted Control screen as
// implemented.
func TestControlRendererRegistryMatchesTheDispatcher(t *testing.T) {
	v, _ := testControlView(t)
	ctx := context.Background()
	for id := range controlScreens() {
		if err := v.Nav().DeepLink(id); err != nil {
			t.Fatalf("deep link %s: %v", id, err)
		}
		out := lines(v, 160, 60)
		if strings.Contains(out, "declared by the frozen pack but is not implemented") {
			t.Fatalf("registry entry %s fell through to the generic renderer:\n%s", id, out)
		}
		// Restore a predictable navigation origin before the next deep link.
		v.HandleKey(ctx, key(KeyEsc))
	}
}

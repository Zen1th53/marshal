//go:build linux

package tui

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/tui/board/vm"
	"github.com/Zen1th53/marshal/internal/usage"
)

// cardsFrame collects the cards once.
func cardsFrame(w *Workspace) vm.Board {
	b := vm.Board{Now: time.Now()}
	w.collectV2Cards(&b)
	return b
}

// waitCards collects the cards until ok says what the background reads
// filled in is there.
func waitCards(t *testing.T, w *Workspace, what string, ok func(vm.Board) bool) vm.Board {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		b := cardsFrame(w)
		if ok(b) {
			return b
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never showed: %+v %+v", what, b.Decision, b.Paused)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func keyNames(keys []vm.Key) []string {
	var out []string
	for _, k := range keys {
		out = append(out, k.Key+" "+k.Label)
	}
	return out
}

// recordStopCommands replaces the stop commands' handler with one that
// records what Enter ran.
func recordStopCommands(t *testing.T) *[]string {
	t.Helper()
	orig := boardStopHandle
	t.Cleanup(func() { boardStopHandle = orig })
	var ran []string
	boardStopHandle = func(_ *Workspace, _ context.Context, cmd string) (string, error) {
		ran = append(ran, cmd)
		return "ok", nil
	}
	return &ran
}

// displaySeams sets the platform and environment the display check reads.
func displaySeams(t *testing.T, goos string, env map[string]string) {
	t.Helper()
	origOS, origEnv := v2GOOS, v2Getenv
	t.Cleanup(func() { v2GOOS, v2Getenv = origOS, origEnv })
	v2GOOS = goos
	v2Getenv = func(k string) string { return env[k] }
}

// architectureWorkspace is a first-use Board whose Marshal submitted a
// revision of an accepted project: four parts, two of them this goal's.
func architectureWorkspace(t *testing.T) *Workspace {
	t.Helper()
	w := emptyBoardWorkspace(t)
	t.Cleanup(w.Close)
	t.Cleanup(func() { v2Cards.Delete(w) })
	w.gateContractSource = func(context.Context) (*gateContract, error) { return walkContract(), nil }
	w.refreshGates(t.Context())
	accepted := projectcontext.Contract{ID: "hello-uz", Revision: 1, Components: []projectcontext.Component{
		{ID: "cli", Responsibility: "parses the command line", Dependencies: []string{"greeter"}, Covers: []string{"R1"}},
		{ID: "greeter", Responsibility: "builds the greeting", Covers: []string{"R2"}},
	}}
	pending := projectcontext.Contract{ID: "hello-uz", Revision: 2, Components: []projectcontext.Component{
		{ID: "cli", Responsibility: "parses the command line", Dependencies: []string{"greeter", "locale"}, Covers: []string{"R1"}},
		{ID: "greeter", Responsibility: "builds the greeting", Covers: []string{"R2"}},
		{ID: "locale", Responsibility: "picks the language", Covers: []string{"R3"}},
		{ID: "locale-tests", Responsibility: "table tests", Parent: "locale", Covers: []string{"R3"}},
		{ID: "docs", Responsibility: "usage page", Dependencies: []string{"cli"}, Covers: []string{"R4"}},
	}}
	orig := v2ContractSource
	t.Cleanup(func() { v2ContractSource = orig })
	v2ContractSource = func(*Workspace, context.Context) (projectcontext.Contract, projectcontext.Contract, bool, error) {
		return pending, accepted, true, nil
	}
	dir := filepath.Join(w.workDir, filepath.Dir(architectureURLFile))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.workDir, architectureURLFile), []byte("http://127.0.0.1:37667/?token=7964abcd462b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return w
}

func sketched(b vm.Board) bool { return b.Decision != nil && len(b.Decision.Nodes) > 0 }

// The architecture waiting for the owner is the decision card: its title,
// what the project covers, the parts this goal changes marked, and the keys
// the authority rules allow. On a desktop the browser opens, so no link is
// printed; Enter is not offered because only the browser accepts.
func TestDecisionCardArchitectureWithDisplay(t *testing.T) {
	displaySeams(t, "darwin", nil)
	w := architectureWorkspace(t)
	b := waitCards(t, w, "the sketch", sketched)
	d := b.Decision
	if d.Kind != "architecture" || d.Title != "Accept architecture · rev 1" {
		t.Fatalf("decision %q %q", d.Kind, d.Title)
	}
	if d.Summary != "5 parts · 3 links · covers R1–R4" {
		t.Fatalf("summary %q", d.Summary)
	}
	if d.ChangedNote != "3 parts changed by this goal (dashed)" {
		t.Fatalf("changed note %q", d.ChangedNote)
	}
	want := []vm.Node{
		{Name: "cli", Sub: "parses the command line", Changed: false},
		{Name: "greeter", Sub: "builds the greeting", Changed: false},
		{Name: "docs", Sub: "usage page", Changed: true},
		{Name: "locale", Sub: "picks the language", Changed: true},
	}
	if !slices.Equal(d.Nodes, want) {
		t.Fatalf("nodes %+v, want %+v", d.Nodes, want)
	}
	if d.NoDisplay || d.Link != "" {
		t.Fatalf("a desktop got the no-display link: %v %q", d.NoDisplay, d.Link)
	}
	if got := keyNames(d.Actions); !slices.Equal(got, []string{"a open in browser", "c copy link", "k comment"}) {
		t.Fatalf("actions %q", got)
	}
	if d.SelfApproval != "" {
		t.Fatalf("self-approval %q with no refusal", d.SelfApproval)
	}
	if b.Paused != nil {
		t.Fatalf("a paused card with no run: %+v", b.Paused)
	}
}

// U6: on a headless Linux box (no DISPLAY, no WAYLAND_DISPLAY) the page
// "opened" but no browser appeared. The card prints the review link, and c
// copies it with OSC 52.
func TestDecisionCardArchitectureWithoutDisplay(t *testing.T) {
	displaySeams(t, "linux", map[string]string{"TERM": "xterm"})
	w := architectureWorkspace(t)
	d := waitCards(t, w, "the sketch", sketched).Decision
	if !d.NoDisplay || d.Link != "http://127.0.0.1:37667/?token=7964abcd462b" {
		t.Fatalf("no-display card: %v %q", d.NoDisplay, d.Link)
	}

	origClip := v2ClipboardWrite
	t.Cleanup(func() { v2ClipboardWrite = origClip })
	var copied string
	v2ClipboardWrite = func(_ *Workspace, text string) bool { copied = text; return true }
	if !w.handleV2CardKey(KeyEvent{Type: KeyRune, Rune: 'c'}) || copied != d.Link {
		t.Fatalf("c copied %q", copied)
	}

	// With a display, a launcher that failed says the same.
	displaySeams(t, "linux", map[string]string{"DISPLAY": ":0"})
	if d := cardsFrame(w).Decision; d.NoDisplay {
		t.Fatal("a desktop with a working browser got the link")
	}
	w.memoryAnswer(architectureNoBrowser, true)
	if d := cardsFrame(w).Decision; !d.NoDisplay || d.Link == "" {
		t.Fatalf("a failed opener shows no link: %v %q", d.NoDisplay, d.Link)
	}
}

// P8: the Marshal tried to accept the architecture for the owner and policy
// refused it. The card says when, and that it was refused.
func TestDecisionCardSaysTheSelfApprovalWasRefused(t *testing.T) {
	displaySeams(t, "darwin", nil)
	w := architectureWorkspace(t)
	w.noteRefusedArchitecture("hello-uz", 1)
	w.refreshGates(t.Context())
	d := cardsFrame(w).Decision
	at := w.noteList()[0].At.Format("15:04")
	if want := "The Marshal tried to accept this for you at " + at + ". Refused: only you can accept."; d.SelfApproval != want {
		t.Fatalf("self-approval %q, want %q", d.SelfApproval, want)
	}
}

// The architecture card's keys: a opens the browser, Enter never accepts
// (the Board says where to), Esc puts it off until another decision waits.
func TestDecisionCardKeys(t *testing.T) {
	displaySeams(t, "darwin", nil)
	w := architectureWorkspace(t)
	if !w.handleV2CardKey(KeyEvent{Type: KeyEnter}) || !strings.Contains(lastOutput(w), "Only you can accept the architecture, in the browser") {
		t.Fatalf("Enter: %q", lastOutput(w))
	}
	if !w.handleV2CardKey(KeyEvent{Type: KeyRune, Rune: 'a'}) || !strings.Contains(lastOutput(w), "architecture") || strings.Contains(lastOutput(w), "Only you") {
		t.Fatalf("a did not open the architecture: %q", lastOutput(w))
	}
	if len(w.sayEchoList()) != 0 {
		t.Fatal("the card typed into the chat")
	}
	if !w.handleV2CardKey(KeyEvent{Type: KeyEsc}) || cardsFrame(w).Decision != nil {
		t.Fatal("Esc did not put the card off")
	}
}

// A pending inbox decision is the card when nothing else waits; its keys
// are the inbox's own.
func TestDecisionCardForAnInboxItem(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	defer v2Cards.Delete(w)
	w.permissions.mu.Lock()
	w.permissions.inbox = []permission.Request{{Kind: "network", Object: "proxy.golang.org", Reason: "go mod download", RunID: "R-1", TaskID: "T03"}}
	w.permissions.mu.Unlock()
	d := cardsFrame(w).Decision
	if d == nil || d.Kind != "inbox" || d.Title != "Decide · network proxy.golang.org" || !strings.Contains(d.Summary, "go mod download") || d.ChangedNote == "" {
		t.Fatalf("inbox card %+v", d)
	}
	if !w.handleV2CardKey(KeyEvent{Type: KeyRune, Rune: 'a'}) || !w.inboxView.Confirming() {
		t.Fatal("a did not begin the inbox's allow")
	}
}

// quotaWorkspace is screen 5: agy-2 and agy-4 hit the agy Gemini 5-hour
// limit; claude has room (claudeLeft percent).
func quotaWorkspace(t *testing.T, claudeLeft float64) (*Workspace, time.Time) {
	t.Helper()
	w := NewWorkspace(nil, "project", "session")
	w.screen = NewScreen(nil)
	t.Cleanup(w.Close)
	t.Cleanup(func() { v2Cards.Delete(w) })
	reset := time.Now().Add(65 * time.Minute)
	run := marshal.Run{
		State:    marshal.AwaitingUser,
		PlanID:   "PLAN-1",
		Settings: marshal.Settings{ReworkLimit: 2},
		Pause:    &marshal.Pause{Reason: "provider limit: agy reached its usage limit", Detail: "agy: RESOURCE_EXHAUSTED 429 quota", Code: marshal.CodeProviderLimit, Action: marshal.PauseResume},
		Tasks: []marshal.Task{
			{PlanTaskID: "T06", Title: "history paging", Worker: "agy", State: marshal.Escalated, Branch: "marshal/T06", BaseCommit: "base", FailedAttemptCommit: "tip6"},
			{PlanTaskID: "T09", Title: "dashed-change styles", Worker: "agy", State: marshal.Escalated, Branch: "marshal/T09", BaseCommit: "base", FailedAttemptCommit: "tip9"},
			{PlanTaskID: "T01", Title: "store", Worker: "claude", State: marshal.Merged},
		},
	}
	w.setMarshalPanel(newMarshalPanel("R-9", "claude", run, "paused"))
	w.usage.mu.Lock()
	w.usage.quotas = []usage.Quota{
		{Provider: "agy", Known: true, Windows: []usage.Window{{Label: "Gemini 5h", LeftPercent: 0, ResetsAt: reset}}},
		{Provider: "claude", Known: true, Windows: []usage.Window{{Label: "5h", LeftPercent: claudeLeft, ResetsAt: time.Now().Add(3 * time.Hour)}}},
	}
	w.usage.read = true
	w.usage.mu.Unlock()
	orig := v2CommitCount
	t.Cleanup(func() { v2CommitCount = orig })
	v2CommitCount = func(_ context.Context, _, _, tip string) (int, error) {
		return map[string]int{"tip6": 3, "tip9": 1}[tip], nil
	}
	return w, reset
}

// P7: a provider limit stopped two tasks. The card names the provider and
// window, when it resets, each task and what of it is kept, and exactly
// what Enter does: move both to claude, which has room, and resume. Enter
// runs those commands, through the stop entry's own handler.
func TestPausedCardQuotaWithTwoTasks(t *testing.T) {
	ran := recordStopCommands(t)
	w, reset := quotaWorkspace(t, 52)
	b := waitCards(t, w, "the commit counts", func(b vm.Board) bool {
		return b.Paused != nil && len(b.Paused.Tasks) == 2 && strings.Contains(b.Paused.Tasks[1].Kept, "commit")
	})
	z := b.Paused
	if z.Kind != "quota" || z.Title != "Paused · agy Gemini 5h limit" || z.Resets != reset.Format("15:04") {
		t.Fatalf("title %q %q resets %q", z.Kind, z.Title, z.Resets)
	}
	if z.Cause != "T06 and T09 hit the agy Gemini 5h limit. Their work is kept." {
		t.Fatalf("cause %q", z.Cause)
	}
	want := []vm.PausedTask{
		{Title: "T06 history paging", Progress: "edits committed", Kept: "branch + 3 commits"},
		{Title: "T09 dashed-change styles", Progress: "edits committed", Kept: "branch + 1 commit"},
	}
	if !slices.Equal(z.Tasks, want) {
		t.Fatalf("tasks %+v", z.Tasks)
	}
	if z.Will != "move T06 and T09 to claude in fresh sessions, keep commits, and resume the run." {
		t.Fatalf("will %q", z.Will)
	}
	if got := keyNames(z.Actions); !slices.Equal(got, []string{"⏎ move to claude", "w wait until " + reset.Format("15:04"), "d raw error", "x stop the run"}) {
		t.Fatalf("actions %q", got)
	}
	if b.Decision != nil {
		t.Fatalf("a decision card with nothing to decide: %+v", b.Decision)
	}

	if !w.handleV2CardKey(KeyEvent{Type: KeyEnter}) {
		t.Fatal("Enter was not taken")
	}
	if want := []string{"/marshal reassign T06 claude", "/marshal reassign T09 claude", "/marshal resume"}; !slices.Equal(*ran, want) {
		t.Fatalf("Enter ran %q, want %q", *ran, want)
	}
}

// With no provider that has room, Enter (and w) wait for the reset: the
// quota card's auto-resume is armed, nothing is moved.
func TestPausedCardQuotaWithNoRoomWaits(t *testing.T) {
	ran := recordStopCommands(t)
	w, _ := quotaWorkspace(t, 0)
	w.quotaCard.waitFn = func(ctx context.Context, _ time.Time) bool { <-ctx.Done(); return false }
	w.quotaCard.freeFn = func() bool { return false }
	w.quotaCard.resumeFn = func(context.Context) {}
	z := cardsFrame(w).Paused
	if z == nil || !strings.HasPrefix(z.Will, "resume the run at ") || z.Actions[0].Label != "wait and resume" {
		t.Fatalf("no-room card %+v", z)
	}
	if !w.handleV2CardKey(KeyEvent{Type: KeyEnter}) || !w.quotaCard.isArmed() || len(*ran) != 0 {
		t.Fatalf("Enter: armed=%v ran %q", w.quotaCard.isArmed(), *ran)
	}
	w.disarmQuotaResume()
	if !w.handleV2CardKey(KeyEvent{Type: KeyRune, Rune: 'w'}) || !w.quotaCard.isArmed() {
		t.Fatal("w did not wait for the reset")
	}
	w.disarmQuotaResume()
}

// d shows the raw error; x stops the run through /marshal stop and keeps
// its work.
func TestPausedCardRawErrorAndStop(t *testing.T) {
	ran := recordStopCommands(t)
	w, _ := quotaWorkspace(t, 52)
	if !w.handleV2CardKey(KeyEvent{Type: KeyRune, Rune: 'd'}) || lastOutput(w) != "Raw error: agy: RESOURCE_EXHAUSTED 429 quota" {
		t.Fatalf("d: %q", lastOutput(w))
	}
	if !w.handleV2CardKey(KeyEvent{Type: KeyRune, Rune: 'x'}) || !slices.Equal(*ran, []string{"/marshal stop"}) {
		t.Fatalf("x ran %q", *ran)
	}
}

// pausedWorkspace is a run paused on one task by pause.
func pausedWorkspace(t *testing.T, worker string, pause marshal.Pause) *Workspace {
	t.Helper()
	w := NewWorkspace(nil, "project", "session")
	w.screen = NewScreen(nil)
	t.Cleanup(w.Close)
	t.Cleanup(func() { v2Cards.Delete(w) })
	run := marshal.Run{
		State: marshal.AwaitingUser, PlanID: "PLAN-1", Settings: marshal.Settings{ReworkLimit: 2}, Pause: &pause,
		Tasks: []marshal.Task{{PlanTaskID: "T03", Title: "sub-view links", Worker: worker, State: marshal.Escalated, Branch: "marshal/T03"}},
	}
	w.setMarshalPanel(newMarshalPanel("R-9", worker, run, "paused"))
	return w
}

// A worker that is not signed in: the card says what to run to sign in,
// and Enter retries the task in a fresh session and resumes.
func TestPausedCardAuthFailureSaysWhatToRun(t *testing.T) {
	ran := recordStopCommands(t)
	w := pausedWorkspace(t, "codex", marshal.Pause{Reason: "codex is not signed in for workers: run codex login (or set CODEX_HOME). 401", Detail: "401 Unauthorized", Code: marshal.CodeWorkerAuth, Action: marshal.PauseRetryTask, TaskID: "T03"})
	z := cardsFrame(w).Paused
	if z == nil || z.Kind != "auth" || z.Title != "Paused · codex not signed in" {
		t.Fatalf("auth card %+v", z)
	}
	if !strings.Contains(z.Cause, "run codex login") || !strings.Contains(z.Cause, "then press Enter") {
		t.Fatalf("cause does not say what to run: %q", z.Cause)
	}
	if !strings.Contains(z.Will, "retry T03 in a fresh session") {
		t.Fatalf("will %q", z.Will)
	}
	if len(z.Tasks) != 1 || z.Tasks[0].Kept != "branch" {
		t.Fatalf("tasks %+v", z.Tasks)
	}
	w.handleV2CardKey(KeyEvent{Type: KeyEnter})
	if want := []string{"/marshal retry T03 fresh-session", "/marshal resume"}; !slices.Equal(*ran, want) {
		t.Fatalf("Enter ran %q, want %q", *ran, want)
	}
}

// One Enter handler dispatches per cause: a lost lease and an orphan lane
// retry in a fresh session, any other pause runs the run's own commands,
// a stalled run retries its stuck task.
func TestPausedCardEnterDispatchesPerCause(t *testing.T) {
	for _, c := range []struct {
		name, kind string
		pause      marshal.Pause
		want       []string
	}{
		{"lease", "lease", marshal.Pause{Reason: "codex failed 4 times in a row", Detail: "infra retry: task time ran out", Code: marshal.CodeInfraRetry, Action: marshal.PauseRetryTask, TaskID: "T03"}, []string{"/marshal retry T03 fresh-session", "/marshal resume"}},
		{"orphan", "orphan", marshal.Pause{Reason: "A task's earlier attempt was interrupted, and Marshal couldn't close it.", Detail: "lane admission: orphan task lane requires explicit reconciliation", Code: marshal.CodeLaneAdmission, Action: marshal.PauseRetryTask, TaskID: "T03"}, []string{"/marshal retry T03 fresh-session", "/marshal resume"}},
		{"timeout", "timeout", marshal.Pause{Reason: "codex stopped answering.", Detail: "infra retry: worker operation timed out", Code: marshal.CodeInfraRetry, Action: marshal.PauseRetryTask, TaskID: "T03"}, []string{"/marshal retry T03 fresh-session", "/marshal resume"}},
		{"budget", "other", marshal.Pause{Reason: "budget boundary: plan tokens", Code: marshal.CodeBudget, Action: marshal.PauseResume}, []string{"/marshal resume"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			ran := recordStopCommands(t)
			w := pausedWorkspace(t, "codex", c.pause)
			z := cardsFrame(w).Paused
			if z == nil || z.Kind != c.kind || z.Will == "" || !strings.HasPrefix(z.Title, "Paused · ") {
				t.Fatalf("card %+v", z)
			}
			w.handleV2CardKey(KeyEvent{Type: KeyEnter})
			if !slices.Equal(*ran, c.want) {
				t.Fatalf("Enter ran %q, want %q", *ran, c.want)
			}
		})
	}

	t.Run("stalled", func(t *testing.T) {
		ran := recordStopCommands(t)
		w := stalledWorkspace(t, marshal.Returned)
		defer v2Cards.Delete(w)
		z := cardsFrame(w).Paused
		if z == nil || z.Kind != "stalled" || z.Will != "retry browser-fixes in a fresh session, then resume the run." || len(z.Tasks) != 1 {
			t.Fatalf("stalled card %+v", z)
		}
		w.handleV2CardKey(KeyEvent{Type: KeyEnter})
		if want := []string{"/marshal retry browser-fixes fresh-session", "/marshal resume"}; !slices.Equal(*ran, want) {
			t.Fatalf("Enter ran %q, want %q", *ran, want)
		}
	})
}

// On the home screen y does what the paused card's Enter does, once: the
// From-you line names it, Enter itself only opens the chat, and a second y
// within homeYesQuiet runs nothing more.
func TestHomeYesIsThePausedCardsEnter(t *testing.T) {
	ran := recordStopCommands(t)
	w := pausedWorkspace(t, "codex", marshal.Pause{Reason: "codex stopped answering.", Detail: "infra retry: worker operation timed out", Code: marshal.CodeInfraRetry, Action: marshal.PauseRetryTask, TaskID: "T03"})
	if z := cardsFrame(w).Paused; z == nil {
		t.Fatal("no paused card")
	}
	lines, _, _ := w.frameLinesFull(120, 40)
	if text := StripANSI(strings.Join(lines, "\n")); !strings.Contains(text, "— y ") {
		t.Fatalf("From you does not name y:\n%s", text)
	}
	w.handleBoardKey(KeyEvent{Type: KeyEnter})
	if len(*ran) != 0 {
		t.Fatalf("Enter ran %q", *ran)
	}
	w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: 'y'})
	w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: 'y'})
	if want := []string{"/marshal retry T03 fresh-session", "/marshal resume"}; !slices.Equal(*ran, want) {
		t.Fatalf("y ran %q, want %q once", *ran, want)
	}
}

// With no card, y retries the stuck task From you names, through the same
// retry the Web Board's Retry runs.
func TestHomeYesRetriesTheStuckTask(t *testing.T) {
	orig := boardRetry
	t.Cleanup(func() { boardRetry = orig })
	var calls []string
	boardRetry = func(_ *Workspace, _ context.Context, id string) (string, error) {
		calls = append(calls, id)
		return id + " starts again in a fresh session.", nil
	}
	w, _ := boardWorkspace(t)
	defer w.Close()
	w.permissions.inbox = nil
	in := w.homeIn()
	if in.retry == "" {
		t.Skip("the fixture run has no stuck task without a decision")
	}
	lines, _, _ := w.frameLinesFull(120, 40)
	if text := StripANSI(strings.Join(lines, "\n")); !strings.Contains(text, in.retry+" is stuck — y retry "+in.retry) && !strings.Contains(text, "— y ") {
		t.Fatalf("From you does not name the retry:\n%s", text)
	}
	w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: 'y'})
	w.commandWG.Wait()
	if !slices.Equal(calls, []string{in.retry}) {
		t.Fatalf("retried %q, want %q", calls, in.retry)
	}
}

// sketchContract counts what a first draft covers: every part is new.
func TestSketchOfAFirstDraft(t *testing.T) {
	s := sketchContract(projectcontext.Contract{ID: "p", Revision: 1, Components: []projectcontext.Component{
		{ID: "a", Covers: []string{"R10", "R2"}}, {ID: "b", Dependencies: []string{"a"}, Covers: []string{"R1"}},
	}}, projectcontext.Contract{})
	if s.Parts != 2 || s.Links != 1 || s.Changed != 2 || coversText(s.Covers) != "R1, R2, R10" {
		t.Fatalf("sketch %+v covers %q", s, coversText(s.Covers))
	}
}

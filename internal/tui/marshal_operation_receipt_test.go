package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/statepath"
	"github.com/Zen1th53/marshal/internal/testutil/testcloud"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
	"github.com/Zen1th53/marshal/internal/webboard"
)

func receiptTestWorkspace(t *testing.T, sessionID string) (*Workspace, *app.Runtime) {
	t.Helper()
	repo := testgit.New(t)
	for _, name := range []string{"CAPABILITIES.yaml", "PACK-VERSION.yaml", "RUNTIME-VERSION.yaml"} {
		data, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo.Path(), name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	rt, err := app.Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	status, err := rt.Status(t.Context())
	if err != nil {
		_ = rt.Close()
		t.Fatalf("runtime status: %v", err)
	}
	ws := NewWorkspace(rt.Store(), string(status.Project.ID), sessionID)
	ws.workDir = rt.ProjectRoot()
	ws.AttachRuntime(rt, projectid.ID(rt.ProjectIdentity()))
	ws.AttachULTRA(testcloud.EntitledGate(t, testcloud.Options{}), true)
	t.Cleanup(func() {
		ws.Close()
		_ = rt.Close()
	})
	return ws, rt
}

func seedMarshalRunForReceiptTest(t *testing.T, w *Workspace, runID string, run marshal.Run) (*app.MarshalService, int64) {
	t.Helper()
	service := w.runtime.Marshal()
	if service == nil {
		t.Fatal("runtime has no Marshal service")
	}
	if run.PlanID == "" {
		run.PlanID = "PLAN-" + runID
	}
	if run.PlanVersion < 1 {
		run.PlanVersion = 1
	}
	if run.Settings.Validate() != nil {
		run.Settings = marshal.DefaultSettings()
	}
	rev, err := service.Store.SetMarshalRun(t.Context(), service.ProjectID, runID, run, 0)
	if err != nil {
		t.Fatalf("seed run %s: %v", runID, err)
	}
	for _, task := range run.Tasks {
		if _, err := service.Store.SetMarshalTask(t.Context(), runID, task, 0); err != nil {
			t.Fatalf("seed task %s: %v", task.PlanTaskID, err)
		}
	}
	m := w.marshalSession()
	m.mu.Lock()
	m.service = service
	m.runID = runID
	m.provider = "codex"
	m.mu.Unlock()
	w.setMarshalPanel(newMarshalPanel(runID, "codex", run, string(run.State)))
	return service, rev
}

func readMarshalInboxText(t *testing.T, w *Workspace) string {
	t.Helper()
	path := filepath.Join(w.runtime.ProjectRoot(), statepath.DirName, "inbox", "marshal.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func queueAutomaticProposalRequest(t *testing.T, w *Workspace, p marshalProposal) permission.Request {
	t.Helper()
	p.who = "Marshal"
	if p.occurrence == "" {
		p.occurrence = fmt.Sprintf("runtime:%d-%s", time.Now().UnixNano(), p.action)
	}
	if err := w.queueMarshalRunProposal(p); err != nil {
		t.Fatalf("queueMarshalRunProposal(%s): %v", p.action, err)
	}
	batch := w.permissions.queue.Take(false)
	if len(batch) != 1 {
		t.Fatalf("expected 1 queued permission request, got %d", len(batch))
	}
	return batch[0]
}

func TestBusyReceiptNamesHolderWithoutEffects(t *testing.T) {
	w, _ := receiptTestWorkspace(t, "SESSION-busy-holder")
	runID := "RUN-busy-holder"
	service, initialRev := seedMarshalRunForReceiptTest(t, w, runID, marshal.Run{
		State: marshal.AwaitingUser,
		Tasks: []marshal.Task{
			{PlanTaskID: "T-1", Title: "Retryable task", Worker: "codex", State: marshal.Escalated, EscalationReason: "rework limit reached"},
			{PlanTaskID: "T-2", Title: "Reviewing task", Worker: "claude", State: marshal.HandedIn},
		},
	})
	m := w.marshalSession()
	startedAt := time.Now().UTC().Add(-3 * time.Second).Truncate(time.Second)
	_, holderID, cancel, err := m.reserveWithHolder(t.Context(), marshal.OperationHolder{
		ID:        "op-review-42",
		RunID:     runID,
		TaskID:    "T-2",
		Kind:      "review",
		Title:     "Reviewing T-2",
		StartedAt: startedAt,
	})
	if err != nil {
		t.Fatalf("reserveWithHolder: %v", err)
	}
	if holderID != "op-review-42" {
		t.Fatalf("holderID = %q, want op-review-42", holderID)
	}

	// Releasing with a mismatched holder ID must never release the active lock.
	if m.finishHolder("wrong-holder-id", nil, false) {
		t.Fatal("mismatched holder ID released active reservation")
	}
	if !m.busy || m.currentBlocker() == nil || m.currentBlocker().ID != holderID {
		t.Fatalf("active reservation corrupted after wrong holder release: busy=%v blocker=%+v", m.busy, m.currentBlocker())
	}

	h := &CommandHandler{ws: w}
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"retry", []string{"retry", "T-1"}},
		{"reassign", []string{"reassign", "T-1", "claude"}},
		{"return", []string{"return", "T-2", "needs another check"}},
		{"cancel", []string{"cancel", "T-1"}},
		{"resume", []string{"resume"}},
		{"close", []string{"close"}},
		{"use-plan", []string{"use-plan"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := h.handleMarshal(t.Context(), tc.args)
			if err == nil {
				t.Fatalf("%s unexpectedly succeeded while busy", tc.name)
			}
			if !errors.Is(err, errMarshalBusy) {
				t.Fatalf("%s error %v does not wrap errMarshalBusy", tc.name, err)
			}
			receipt, ok := marshal.RefusalOf(err)
			if !ok {
				t.Fatalf("%s missing typed refusal: %v", tc.name, err)
			}
			if receipt.Code != marshal.RefusalCodeBusy {
				t.Fatalf("%s code = %q, want %q", tc.name, receipt.Code, marshal.RefusalCodeBusy)
			}
			if receipt.Blocker == nil || receipt.Blocker.ID != "op-review-42" || receipt.Blocker.RunID != runID || receipt.Blocker.TaskID != "T-2" || receipt.Blocker.Kind != "review" || receipt.Blocker.Title != "Reviewing T-2" || receipt.Blocker.StartedAt.IsZero() {
				t.Fatalf("%s blocker = %+v", tc.name, receipt.Blocker)
			}
			if receipt.Effects != marshal.EffectsNone {
				t.Fatalf("%s effects = %q, want %q", tc.name, receipt.Effects, marshal.EffectsNone)
			}
			if want := []string{"/marshal status", "/marshal stop"}; !slices.Equal(receipt.NextCommands, want) {
				t.Fatalf("%s nextCommands = %q, want %q", tc.name, receipt.NextCommands, want)
			}
			stored, readErr := service.Store.GetMarshalRun(t.Context(), service.ProjectID, runID)
			if readErr != nil || stored.Revision != initialRev || stored.Value.Tasks[0].State != marshal.Escalated {
				t.Fatalf("%s mutated run state: rev=%d (want %d) state=%s err=%v", tc.name, stored.Revision, initialRev, stored.Value.Tasks[0].State, readErr)
			}
		})
	}

	// Releasing with the real holder ID releases cleanly.
	if !m.finishHolder(holderID, cancel, false) {
		t.Fatal("real holder ID failed to release reservation")
	}
	if m.busy || m.currentBlocker() != nil {
		t.Fatalf("reservation still active after holder release: busy=%v blocker=%+v", m.busy, m.currentBlocker())
	}
}

func TestProposalRefusalEscalatesOnceAfterThree(t *testing.T) {
	w, _ := receiptTestWorkspace(t, "SESSION-escalate-three")
	runID := "RUN-escalate"
	service, initialRev := seedMarshalRunForReceiptTest(t, w, runID, marshal.Run{
		State: marshal.AwaitingUser,
		Tasks: []marshal.Task{
			{PlanTaskID: "T-1", Title: "Retryable task", Worker: "codex", State: marshal.Escalated, EscalationReason: "rework limit reached"},
			{PlanTaskID: "T-2", Title: "Reviewing task", Worker: "claude", State: marshal.HandedIn},
			{PlanTaskID: "T-3", Title: "Permanent hold task", Worker: "codex", State: marshal.Escalated, EscalationReason: "security: honeypot triggered"},
		},
	})

	var chatMu sync.Mutex
	var chatNotices []string
	w.draftTell = func(text string) {
		chatMu.Lock()
		chatNotices = append(chatNotices, text)
		chatMu.Unlock()
	}

	m := w.marshalSession()
	_, holderID, cancel, err := m.reserveWithHolder(t.Context(), marshal.OperationHolder{
		ID:     "op-review-esc",
		RunID:  runID,
		TaskID: "T-2",
		Kind:   "review",
		Title:  "Reviewing T-2",
	})
	if err != nil {
		t.Fatal(err)
	}

	applyAutomaticRetry := func(idx int) error {
		req := queueAutomaticProposalRequest(t, w, marshalProposal{
			action:       "retry",
			taskID:       "T-1",
			freshSession: true,
			occurrence:   fmt.Sprintf("runtime:esc-attempt-%d", idx),
		})
		return w.decidePermission(t.Context(), req, true, "marshal")
	}

	// Attempts 1 and 2 return typed busy refusals with incrementing attempt counts and no escalation.
	for attempt := 1; attempt <= 2; attempt++ {
		err := applyAutomaticRetry(attempt)
		if !errors.Is(err, errMarshalBusy) {
			t.Fatalf("attempt %d: expected errMarshalBusy, got %v", attempt, err)
		}
		receipt, ok := marshal.RefusalOf(err)
		if !ok {
			t.Fatalf("attempt %d: missing typed refusal", attempt)
		}
		if receipt.Code != marshal.RefusalCodeBusy || receipt.Attempt != attempt || receipt.EscalationID != "" || receipt.Effects != marshal.EffectsNone {
			t.Fatalf("attempt %d receipt = %+v", attempt, receipt)
		}
		if receipt.Blocker == nil || receipt.Blocker.ID != "op-review-esc" || receipt.Blocker.Title != "Reviewing T-2" {
			t.Fatalf("attempt %d blocker = %+v", attempt, receipt.Blocker)
		}
		chatMu.Lock()
		n := len(chatNotices)
		chatMu.Unlock()
		if n != 0 {
			t.Fatalf("attempt %d emitted premature chat notice: %q", attempt, chatNotices)
		}
	}

	// Attempt 3 emits one stable escalation and one runtime chat notice.
	err3 := applyAutomaticRetry(3)
	receipt3, ok := marshal.RefusalOf(err3)
	if !ok {
		t.Fatalf("attempt 3 missing typed refusal: %v", err3)
	}
	if receipt3.Code != marshal.RefusalCodeRepeatedFailure || receipt3.Attempt != 3 || receipt3.EscalationID == "" || receipt3.Effects != marshal.EffectsNone {
		t.Fatalf("attempt 3 receipt = %+v", receipt3)
	}
	if receipt3.Blocker == nil || receipt3.Blocker.Title != "Reviewing T-2" {
		t.Fatalf("attempt 3 blocker = %+v", receipt3.Blocker)
	}
	if marshal.IsReplayedEscalation(err3) {
		t.Fatal("attempt 3 should emit fresh escalation, not replay")
	}
	chatMu.Lock()
	if len(chatNotices) != 1 || !strings.HasPrefix(chatNotices[0], marshalNotePrefix) || !strings.Contains(chatNotices[0], receipt3.EscalationID) {
		t.Fatalf("attempt 3 chat notices = %q", chatNotices)
	}
	chatMu.Unlock()
	if inbox := readMarshalInboxText(t, w); !strings.Contains(inbox, receipt3.EscalationID) || !strings.Contains(inbox, "Reviewing T-2") || !strings.Contains(inbox, "effects: none") {
		t.Fatalf("marshal inbox missing escalation details:\n%s", inbox)
	}
	if !webNoteHas(w, receipt3.EscalationID) {
		t.Fatalf("Activity missing escalation id %s: %+v", receipt3.EscalationID, w.noteList())
	}

	// Attempt 4 replays the exact same escalation receipt without another chat notice or any effect.
	err4 := applyAutomaticRetry(4)
	receipt4, ok := marshal.RefusalOf(err4)
	if !ok {
		t.Fatalf("attempt 4 missing typed refusal: %v", err4)
	}
	if !marshal.IsReplayedEscalation(err4) {
		t.Fatal("attempt 4 expected replayed escalation flag")
	}
	if receipt4.EscalationID != receipt3.EscalationID || receipt4.Code != receipt3.Code || receipt4.Attempt != 3 || receipt4.Effects != marshal.EffectsNone {
		t.Fatalf("attempt 4 receipt = %+v, want replay of %+v", receipt4, receipt3)
	}
	chatMu.Lock()
	if len(chatNotices) != 1 {
		t.Fatalf("attempt 4 sent duplicate chat notice: %q", chatNotices)
	}
	chatMu.Unlock()

	stored, err := service.Store.GetMarshalRun(t.Context(), service.ProjectID, runID)
	if err != nil || stored.Revision != initialRev || stored.Value.Tasks[0].State != marshal.Escalated {
		t.Fatalf("automatic refusals mutated run: rev=%d (want %d) state=%s err=%v", stored.Revision, initialRev, stored.Value.Tasks[0].State, err)
	}

	// Owner retry remains possible once the blocker clears, while permanent holds stay enforced.
	m.finishHolder(holderID, cancel, false)
	h := &CommandHandler{ws: w}
	if _, err := h.handleMarshal(t.Context(), []string{"retry", "T-1", "fresh-session"}); err != nil {
		t.Fatalf("owner retry failed after blocker cleared: %v", err)
	}
	if _, err := h.handleMarshal(t.Context(), []string{"retry", "T-3", "fresh-session"}); err == nil || !strings.Contains(err.Error(), "security") {
		t.Fatalf("owner retry bypassed permanent security hold: %v", err)
	}
}

func TestBusyAndPreApplyRefusalMatrix(t *testing.T) {
	w, _ := receiptTestWorkspace(t, "SESSION-refusal-matrix")
	w.screen = NewScreen(nil)
	t.Cleanup(func() { v2Cards.Delete(w) })
	runID := "RUN-matrix"
	pause := marshal.Pause{
		Reason: "T-1 hit rework limit",
		Detail: "rework limit reached",
		Code:   marshal.CodeReworkLimit,
		Action: marshal.PauseRetryTask,
		TaskID: "T-1",
	}
	service, rev := seedMarshalRunForReceiptTest(t, w, runID, marshal.Run{
		State: marshal.AwaitingUser,
		Pause: &pause,
		Tasks: []marshal.Task{
			{PlanTaskID: "T-1", Title: "First task", Worker: "codex", State: marshal.Escalated, EscalationReason: "rework limit reached"},
			{PlanTaskID: "T-2", Title: "Review task", Worker: "claude", State: marshal.HandedIn},
		},
	})

	assertNoMutation := func(t *testing.T, wantRev int64) {
		t.Helper()
		rec, err := service.Store.GetMarshalRun(t.Context(), service.ProjectID, runID)
		if err != nil {
			t.Fatal(err)
		}
		if rec.Revision != wantRev {
			t.Fatalf("unexpected mutation: revision %d, want %d", rec.Revision, wantRev)
		}
	}

	// 1 & 2. Reservation failures + direct m.busy commands + owner-paused resume.
	m := w.marshalSession()
	_, holderID, cancel, err := m.reserveWithHolder(t.Context(), marshal.OperationHolder{
		ID:     "op-matrix-1",
		RunID:  runID,
		TaskID: "T-2",
		Kind:   "review",
		Title:  "Reviewing T-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	h := &CommandHandler{ws: w}

	busyCommands := []struct {
		name string
		args []string
	}{
		{"approve", []string{"approve"}},
		{"use-plan", []string{"use-plan"}},
		{"resume", []string{"resume"}},
		{"close", []string{"close"}},
		{"retry", []string{"retry", "T-1"}},
		{"return", []string{"return", "T-2", "fix tests"}},
		{"reassign", []string{"reassign", "T-1", "claude"}},
		{"cancel", []string{"cancel", "T-1"}},
	}
	for _, tc := range busyCommands {
		t.Run("busy/"+tc.name, func(t *testing.T) {
			_, err := h.handleMarshal(t.Context(), tc.args)
			receipt, ok := marshal.RefusalOf(err)
			if !ok {
				t.Fatalf("%s: expected typed refusal, got %v", tc.name, err)
			}
			if receipt.Code != marshal.RefusalCodeBusy || receipt.Effects != marshal.EffectsNone {
				t.Fatalf("%s: receipt = %+v", tc.name, receipt)
			}
			if receipt.Blocker == nil || receipt.Blocker.ID != "op-matrix-1" || receipt.Blocker.Title != "Reviewing T-2" {
				t.Fatalf("%s: blocker = %+v", tc.name, receipt.Blocker)
			}
			if want := []string{"/marshal status", "/marshal stop"}; !slices.Equal(receipt.NextCommands, want) {
				t.Fatalf("%s: nextCommands = %q, want %q", tc.name, receipt.NextCommands, want)
			}
			assertNoMutation(t, rev)
		})
	}

	t.Run("busy/resume_paused_by_owner", func(t *testing.T) {
		m.holdDispatch(runID)
		defer m.releaseDispatch()
		_, err := h.handleMarshal(t.Context(), []string{"resume"})
		if !errors.Is(err, errMarshalPausedByYou) {
			t.Fatalf("expected errMarshalPausedByYou, got %v", err)
		}
		receipt, ok := marshal.RefusalOf(err)
		if !ok || receipt.Code != marshal.RefusalCodePausedByOwner || receipt.Effects != marshal.EffectsNone {
			t.Fatalf("receipt = %+v (ok=%v)", receipt, ok)
		}
		if want := []string{"/resume"}; !slices.Equal(receipt.NextCommands, want) {
			t.Fatalf("nextCommands = %q, want %q", receipt.NextCommands, want)
		}
		assertNoMutation(t, rev)
	})

	// 3. Board Enter and Web legacy actions return the same pre-effect refusal naming the holder.
	t.Run("board_enter_and_web_legacy_busy", func(t *testing.T) {
		origStop := boardStopHandle
		t.Cleanup(func() { boardStopHandle = origStop })
		var ran []string
		boardStopHandle = func(_ *Workspace, _ context.Context, cmd string) (string, error) {
			ran = append(ran, cmd)
			return "ok", nil
		}

		// Web paused-card Enter refuses pre-effect without queuing or running commands.
		f := webCard(t, w)
		res := w.webBoardAct(t.Context(), webboard.Action{Kind: "key", Card: webboard.CardID(f), Key: "⏎"})
		if res.Status != "refused" || res.Code != marshal.RefusalCodeBusy || res.Effects != marshal.EffectsNone {
			t.Fatalf("web paused Enter = %+v", res)
		}
		if res.Blocker == nil || res.Blocker.ID != "op-matrix-1" || res.Blocker.Title != "Reviewing T-2" {
			t.Fatalf("web paused Enter blocker = %+v", res.Blocker)
		}
		if want := []string{"/marshal status", "/marshal stop"}; !slices.Equal(res.NextCommands, want) {
			t.Fatalf("web paused Enter nextCommands = %q, want %q", res.NextCommands, want)
		}
		rawRes, encErr := json.Marshal(res)
		if encErr != nil || !strings.Contains(string(rawRes), `"code":"operation_busy"`) || !strings.Contains(string(rawRes), `"effects":"none"`) || !strings.Contains(string(rawRes), `"op-matrix-1"`) {
			t.Fatalf("web paused Enter json = %s err=%v", rawRes, encErr)
		}
		if len(ran) != 0 || len(m.nextStop) != 0 {
			t.Fatalf("web refusal had effects: ran=%q nextStop=%q", ran, m.nextStop)
		}

		// Board Enter names the blocker and records the same pre-effect refusal receipt with 0 commands executed.
		if !w.handleV2CardKey(KeyEvent{Type: KeyEnter}) {
			t.Fatal("paused card did not handle Enter")
		}
		if len(ran) != 0 {
			t.Fatalf("Board Enter ran commands while busy: %q", ran)
		}
		if !strings.Contains(w.state.LastOutput, "Reviewing T-2") {
			t.Fatalf("Board Enter answer %q does not name holder", w.state.LastOutput)
		}
		boardReceipt, ok := w.lastOperationRefusal()
		if !ok || boardReceipt.Code != marshal.RefusalCodeBusy || boardReceipt.Effects != marshal.EffectsNone || boardReceipt.Blocker == nil || boardReceipt.Blocker.ID != "op-matrix-1" {
			t.Fatalf("Board Enter refusal = %+v (ok=%v)", boardReceipt, ok)
		}
		if want := []string{"/marshal status", "/marshal stop"}; !slices.Equal(boardReceipt.NextCommands, want) {
			t.Fatalf("Board Enter nextCommands = %q, want %q", boardReceipt.NextCommands, want)
		}
		// Clear queued stop before releasing holder so drain doesn't run anything.
		m.mu.Lock()
		m.nextStop = nil
		m.mu.Unlock()
		assertNoMutation(t, rev)
	})

	m.finishHolder(holderID, cancel, false)

	// 4. Pre-apply chat and file proposals: parse, authorization, stale binding.
	t.Run("pre_apply_parse_auth_stale", func(t *testing.T) {
		// Parse refusal via parser + proposal file.
		_, parseErr := parseMarshalProposal(marshalProposalPrefix + `{"action":"retry","task_id":"T-1"}`)
		parseReceipt, ok := marshal.RefusalOf(parseErr)
		if !ok || parseReceipt.Code != marshal.RefusalCodeParseInvalid || parseReceipt.Effects != marshal.EffectsNone {
			t.Fatalf("parse refusal = %+v (ok=%v, err=%v)", parseReceipt, ok, parseErr)
		}
		if want := []string{"/marshal help"}; !slices.Equal(parseReceipt.NextCommands, want) {
			t.Fatalf("parse nextCommands = %q, want %q", parseReceipt.NextCommands, want)
		}

		// Chat transcript parse refusal records typed refusal with 0 queued permissions.
		w.observeMarshalProposalSource(importer.SessionTranscript{
			SessionID: "chat-parse-test",
			Messages:  []importer.Message{{Role: "assistant", Kind: importer.MessageKindText, Content: marshalProposalPrefix + `{"action":"unknown-op"}`}},
		}, "chat-parse-1", "Marshal")
		if batch := w.permissions.queue.Take(false); len(batch) != 0 {
			t.Fatalf("invalid chat proposal queued permissions: %+v", batch)
		}
		if storedReceipt, ok := w.loadProposalRefusalReceipt(t.Context(), "chat-parse-1"); !ok || storedReceipt.Code != marshal.RefusalCodeParseInvalid || storedReceipt.Effects != marshal.EffectsNone {
			t.Fatalf("chat parse stored receipt = %+v (ok=%v)", storedReceipt, ok)
		}

		// File proposal parse refusal writes typed receipt into <name>.error.
		propDir := filepath.Join(w.runtime.ProjectRoot(), statepath.DirName, "proposals")
		if err := os.MkdirAll(propDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(propDir, "bad.json"), []byte(`{"action":"retry"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		w.observeMarshalProposalFiles(w.runtime.ProjectRoot())
		errFile, err := os.ReadFile(filepath.Join(propDir, "bad.json.error"))
		if err != nil {
			t.Fatalf("missing rejected file error: %v", err)
		}
		if !strings.Contains(string(errFile), `"code":"invalid_proposal"`) || !strings.Contains(string(errFile), `"effects":"none"`) || !strings.Contains(string(errFile), `"/marshal help"`) {
			t.Fatalf("rejected proposal file error missing typed refusal:\n%s", string(errFile))
		}

		// Authorization refusal (non-Marshal architecture auto-open).
		w.enqueueValidatedMarshalProposal(marshalProposal{action: "architecture", reason: "open diagram", who: "Local request (unverified)", occurrence: "unauth-arch-1"})
		authReceipt, ok := w.loadProposalRefusalReceipt(t.Context(), "unauth-arch-1")
		if !ok || authReceipt.Code != marshal.RefusalCodeUnauthorized || authReceipt.Effects != marshal.EffectsNone {
			t.Fatalf("auth refusal = %+v (ok=%v)", authReceipt, ok)
		}
		if want := []string{"/marshal architecture"}; !slices.Equal(authReceipt.NextCommands, want) {
			t.Fatalf("auth nextCommands = %q, want %q", authReceipt.NextCommands, want)
		}

		// Stale binding refusal (plan not awaiting approval / task not in plan / expired binding).
		staleErr := w.queueMarshalRunProposal(marshalProposal{action: "approve"})
		staleReceipt, ok := marshal.RefusalOf(staleErr)
		if !ok || staleReceipt.Code != marshal.RefusalCodeStaleBinding || staleReceipt.Effects != marshal.EffectsNone {
			t.Fatalf("stale approve refusal = %+v (ok=%v, err=%v)", staleReceipt, ok, staleErr)
		}
		if want := []string{"/marshal status"}; !slices.Equal(staleReceipt.NextCommands, want) {
			t.Fatalf("stale nextCommands = %q, want %q", staleReceipt.NextCommands, want)
		}

		staleTaskErr := w.queueMarshalRunProposal(marshalProposal{action: "retry", taskID: "T-missing", freshSession: true})
		staleTaskReceipt, ok := marshal.RefusalOf(staleTaskErr)
		if !ok || staleTaskReceipt.Code != marshal.RefusalCodeStaleBinding || staleTaskReceipt.Effects != marshal.EffectsNone {
			t.Fatalf("stale task refusal = %+v (ok=%v, err=%v)", staleTaskReceipt, ok, staleTaskErr)
		}
		assertNoMutation(t, rev)
	})

	// 5. Runtime journal interrupted operation surfaced through TUI command.
	t.Run("runtime_journal_interrupted", func(t *testing.T) {
		rec, err := service.Store.GetMarshalRun(t.Context(), service.ProjectID, runID)
		if err != nil {
			t.Fatal(err)
		}
		run := rec.Value
		run.Operation = &marshal.LifecycleOperation{
			ID:     "marshal-op-journal-77",
			Kind:   "merge",
			TaskID: "T-2",
			Event: events.Event{
				ID:    "marshal-op-journal-77",
				RunID: runID,
				At:    time.Now().UTC(),
			},
		}
		nextRev, err := service.Store.SetMarshalRun(t.Context(), service.ProjectID, runID, run, rec.Revision)
		if err != nil {
			t.Fatal(err)
		}
		_, err = h.handleMarshal(t.Context(), []string{"retry", "T-1"})
		if !errors.Is(err, app.ErrUnfinishedLifecycleOperation) {
			t.Fatalf("expected ErrUnfinishedLifecycleOperation, got %v", err)
		}
		receipt, ok := marshal.RefusalOf(err)
		if !ok {
			t.Fatalf("missing typed refusal for journal op: %v", err)
		}
		if receipt.Code != marshal.RefusalCodeInterruptedOp || receipt.Blocker == nil || receipt.Blocker.ID != "marshal-op-journal-77" || receipt.Blocker.Kind != "merge" || receipt.Blocker.TaskID != "T-2" {
			t.Fatalf("journal receipt = %+v", receipt)
		}
		if receipt.Effects == marshal.EffectsNone {
			t.Fatalf("interrupted journal op must never claim effects=none: %+v", receipt)
		}
		if want := []string{"/marshal resume"}; !slices.Equal(receipt.NextCommands, want) {
			t.Fatalf("journal nextCommands = %q, want %q", receipt.NextCommands, want)
		}
		assertNoMutation(t, nextRev)
	})
}

func TestAutomaticRetryStopsAcrossRestart(t *testing.T) {
	repo := testgit.New(t)
	for _, name := range []string{"CAPABILITIES.yaml", "PACK-VERSION.yaml", "RUNTIME-VERSION.yaml"} {
		data, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo.Path(), name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	runID := "RUN-restart-persistence"
	openSession := func(sessionID string) (*Workspace, *app.Runtime, *[]string, func()) {
		rt, err := app.Open(t.Context(), repo.Path())
		if err != nil {
			t.Fatalf("open runtime: %v", err)
		}
		status, err := rt.Status(t.Context())
		if err != nil {
			_ = rt.Close()
			t.Fatalf("status: %v", err)
		}
		ws := NewWorkspace(rt.Store(), string(status.Project.ID), sessionID)
		ws.workDir = rt.ProjectRoot()
		ws.AttachRuntime(rt, projectid.ID(rt.ProjectIdentity()))
		ws.AttachULTRA(testcloud.EntitledGate(t, testcloud.Options{}), true)
		var notices []string
		ws.draftTell = func(text string) {
			notices = append(notices, text)
		}
		cleanup := func() {
			ws.Close()
			_ = rt.Close()
		}
		return ws, rt, &notices, cleanup
	}

	attachRunAndBlocker := func(ws *Workspace, blockerKind, blockerTask, blockerTitle string) (string, context.CancelFunc) {
		service := ws.runtime.Marshal()
		rec, err := service.Store.GetMarshalRun(t.Context(), service.ProjectID, runID)
		if err != nil {
			t.Fatalf("load run: %v", err)
		}
		m := ws.marshalSession()
		m.mu.Lock()
		m.service = service
		m.runID = runID
		m.provider = "codex"
		m.mu.Unlock()
		ws.setMarshalPanel(newMarshalPanel(runID, "codex", rec.Value, "paused"))
		_, holderID, cancel, err := m.reserveWithHolder(t.Context(), marshal.OperationHolder{
			RunID:  runID,
			TaskID: blockerTask,
			Kind:   blockerKind,
			Title:  blockerTitle,
		})
		if err != nil {
			t.Fatalf("reserve blocker: %v", err)
		}
		return holderID, cancel
	}

	runAutoRetry := func(ws *Workspace, idx int) error {
		req := queueAutomaticProposalRequest(t, ws, marshalProposal{
			action:       "retry",
			taskID:       "T-1",
			freshSession: true,
			occurrence:   fmt.Sprintf("runtime:restart-%d", idx),
		})
		return ws.decidePermission(t.Context(), req, true, "marshal")
	}

	// Session 1: seed run and record 2 equivalent automatic refusals before restart.
	ws1, _, notices1, close1 := openSession("SESSION-restart-1")
	seedMarshalRunForReceiptTest(t, ws1, runID, marshal.Run{
		State: marshal.AwaitingUser,
		Tasks: []marshal.Task{
			{PlanTaskID: "T-1", Title: "Retryable task", Worker: "codex", State: marshal.Escalated, EscalationReason: "rework limit reached"},
			{PlanTaskID: "T-2", Title: "Review task", Worker: "claude", State: marshal.HandedIn},
		},
	})
	h1, c1 := attachRunAndBlocker(ws1, "review", "T-2", "Reviewing T-2")
	for i := 1; i <= 2; i++ {
		err := runAutoRetry(ws1, i)
		r, ok := marshal.RefusalOf(err)
		if !ok || r.Code != marshal.RefusalCodeBusy || r.Attempt != i || r.EscalationID != "" {
			t.Fatalf("session 1 attempt %d receipt = %+v (ok=%v, err=%v)", i, r, ok, err)
		}
	}
	if len(*notices1) != 0 {
		t.Fatalf("session 1 emitted premature chat notices: %q", *notices1)
	}
	ws1.marshalSession().finishHolder(h1, c1, false)
	close1()

	// Session 2 (after restart): 3rd equivalent automatic refusal hits threshold and escalates once.
	ws2, rt2, notices2, close2 := openSession("SESSION-restart-2")
	h2, c2 := attachRunAndBlocker(ws2, "review", "T-2", "Reviewing T-2")
	err3 := runAutoRetry(ws2, 3)
	r3, ok := marshal.RefusalOf(err3)
	if !ok || r3.Code != marshal.RefusalCodeRepeatedFailure || r3.Attempt != 3 || r3.EscalationID == "" {
		t.Fatalf("session 2 attempt 3 receipt = %+v (ok=%v, err=%v)", r3, ok, err3)
	}
	if len(*notices2) != 1 || !strings.Contains((*notices2)[0], r3.EscalationID) {
		t.Fatalf("session 2 chat notices = %q, want 1 notice naming %s", *notices2, r3.EscalationID)
	}
	// Verify durable audit event was written for the refusal journal.
	evts, err := rt2.Store().ListEvents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	audited := false
	for _, e := range evts {
		if e.Type == "MARSHAL_OPERATION_REFUSAL" && e.Data["escalation_id"] == r3.EscalationID {
			audited = true
			break
		}
	}
	if !audited {
		t.Fatalf("durable refusal escalation %s was not audited in events", r3.EscalationID)
	}
	ws2.marshalSession().finishHolder(h2, c2, false)
	close2()

	// Session 3 (after second restart): 4th equivalent automatic retry replays the exact same escalation without another chat notice.
	ws3, _, notices3, close3 := openSession("SESSION-restart-3")
	defer close3()
	h3, c3 := attachRunAndBlocker(ws3, "review", "T-2", "Reviewing T-2")
	err4 := runAutoRetry(ws3, 4)
	r4, ok := marshal.RefusalOf(err4)
	if !ok || !marshal.IsReplayedEscalation(err4) || r4.EscalationID != r3.EscalationID || r4.Attempt != 3 || r4.Code != marshal.RefusalCodeRepeatedFailure {
		t.Fatalf("session 3 attempt 4 receipt = %+v (ok=%v replayed=%v), want replay of %+v", r4, ok, marshal.IsReplayedEscalation(err4), r3)
	}
	if len(*notices3) != 0 {
		t.Fatalf("session 3 replayed escalation emitted another chat notice: %q", *notices3)
	}

	// Changed predicate (blocker changes from review to merge) starts a fresh evaluation at attempt 1.
	ws3.marshalSession().finishHolder(h3, c3, false)
	h4, c4 := attachRunAndBlocker(ws3, "merge", "T-2", "Merging T-2")
	errChanged := runAutoRetry(ws3, 5)
	rChanged, ok := marshal.RefusalOf(errChanged)
	if !ok || rChanged.Code != marshal.RefusalCodeBusy || rChanged.Attempt != 1 || rChanged.EscalationID != "" {
		t.Fatalf("changed predicate receipt = %+v (ok=%v, err=%v)", rChanged, ok, errChanged)
	}
	ws3.marshalSession().finishHolder(h4, c4, false)

	// Explicit owner action resets the automatic suppression so the original predicate evaluates fresh at attempt 1.
	if _, err := (&CommandHandler{ws: ws3}).handleMarshal(t.Context(), []string{"stop"}); err != nil {
		t.Fatalf("owner /marshal stop: %v", err)
	}
	h5, c5 := attachRunAndBlocker(ws3, "review", "T-2", "Reviewing T-2")
	defer ws3.marshalSession().finishHolder(h5, c5, false)
	errFresh := runAutoRetry(ws3, 6)
	rFresh, ok := marshal.RefusalOf(errFresh)
	if !ok || rFresh.Code != marshal.RefusalCodeBusy || rFresh.Attempt != 1 || rFresh.EscalationID != "" {
		t.Fatalf("post-owner-action fresh evaluation receipt = %+v (ok=%v, err=%v)", rFresh, ok, errFresh)
	}
}

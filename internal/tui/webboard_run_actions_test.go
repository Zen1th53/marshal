package tui

import (
	"encoding/json"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/webboard"
)

func runTestAction(t *testing.T, w *Workspace, cap webboard.SettingCapability, requestID string, payload webboard.SettingPayload) webboard.Action {
	t.Helper()
	state := w.webSettingsState()
	raw, _ := json.Marshal(payload)
	i := store.WebIntent{ProjectID: state.ProjectID, ProjectEpoch: webboard.SettingsEpoch(state.Fence), Owner: "owner", Session: "browser", RequestID: requestID, Digest: cap.Op + cap.AttemptID + string(raw), Op: cap.Op, Key: cap.Target.ID, TargetKind: cap.Target.Kind, Value: string(raw), Revision: cap.Revision, RunID: cap.RunID, AttemptID: cap.AttemptID}
	if _, err := w.store.AdmitWebSetting(t.Context(), i); err != nil {
		t.Fatal(err)
	}
	return webboard.Action{Kind: "cmd", Command: &i, Commit: func(effect func() webboard.Result) webboard.Result { return effect() }}
}

func runCapability(w *Workspace, op string) (webboard.SettingCapability, bool) {
	for _, c := range w.webWorkCapabilities(w.workers.context()) {
		if c.Op == op {
			return c, true
		}
	}
	return webboard.SettingCapability{}, false
}

func mustRunCapability(t *testing.T, w *Workspace, op string) webboard.SettingCapability {
	t.Helper()
	c, ok := runCapability(w, op)
	if !ok {
		t.Fatalf("missing capability %s in %+v", op, w.webWorkCapabilities(t.Context()))
	}
	return c
}

func runReceipt(t *testing.T, w *Workspace, a webboard.Action) store.WebReceipt {
	t.Helper()
	r, err := w.store.WebReceipt(t.Context(), a.Command.ProjectID, a.Command.Owner, a.Command.RequestID, a.Command.Digest)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestWebRunControlsRespectApprovalAndHold(t *testing.T) {
	w, _ := receiptTestWorkspace(t, "browser-run-hold")
	service, _ := seedMarshalRunForReceiptTest(t, w, "work", marshal.Run{State: marshal.Dispatching, Tasks: []marshal.Task{{PlanTaskID: "one", Worker: "codex", State: marshal.Queued}}})
	if _, ok := runCapability(w, "run/resume"); ok {
		t.Fatal("resume offered on a running run")
	}
	// The Board's own busy gate does not block a priority signal.
	m := w.marshalSession()
	m.mu.Lock()
	m.busy = true
	m.mu.Unlock()
	pause := runTestAction(t, w, mustRunCapability(t, w, "run/pause"), "10112233445566778899aabbccddeeff", webboard.SettingPayload{Reason: "inspect"})
	if r := w.webWorkCommand(pause); r.Status != "done" {
		t.Fatalf("pause while busy: %+v", r)
	}
	run, _ := service.Snapshot(t.Context(), "work")
	if run.State != marshal.AwaitingUser || run.CommandFence != "pause" || len(run.Commands) != 1 || run.Commands[0].ID != pause.Command.RequestID {
		t.Fatalf("pause: %+v", run)
	}
	if r := runReceipt(t, w, pause); r.Status != "applied" {
		t.Fatalf("pause receipt: %+v", r)
	}
	if _, ok := runCapability(w, "run/pause"); ok {
		t.Fatal("pause offered while fenced")
	}
	m.mu.Lock()
	m.busy = false
	m.mu.Unlock()
	resume := runTestAction(t, w, mustRunCapability(t, w, "run/resume"), "20112233445566778899aabbccddeeff", webboard.SettingPayload{})

	// A pending amendment and the Board's hold both keep the run paused.
	m.mu.Lock()
	m.pending = &marshalAmendment{reason: "scope"}
	m.mu.Unlock()
	if _, ok := runCapability(w, "run/resume"); ok {
		t.Fatal("resume offered with a pending amendment")
	}
	if r := w.webWorkCommand(resume); r.Status != "refused" {
		t.Fatalf("resume with amendment: %+v", r)
	}
	m.mu.Lock()
	m.pending = nil
	m.mu.Unlock()
	m.holdDispatch("work")
	if _, ok := runCapability(w, "run/resume"); ok {
		t.Fatal("resume offered under the Board's hold")
	}
	if r := w.webWorkCommand(resume); r.Status != "refused" {
		t.Fatalf("resume under hold: %+v", r)
	}
	m.releaseDispatch()
	after, _ := service.Snapshot(t.Context(), "work")
	if after.State != marshal.AwaitingUser || after.Pause == nil || len(after.Commands) != 1 {
		t.Fatalf("refused resume changed the run: %+v", after)
	}
	if r := runReceipt(t, w, resume); r.Status != "queued" {
		t.Fatalf("refused resume started: %+v", r)
	}

	_, _ = w.store.RefuseWebSetting(t.Context(), *resume.Command, "writer_refused")
	again := runTestAction(t, w, mustRunCapability(t, w, "run/resume"), "21112233445566778899aabbccddeeff", webboard.SettingPayload{})

	// A pause that needs a new approved plan is not resumed from here.
	record, _ := service.Store.GetMarshalRun(t.Context(), service.ProjectID, "work")
	record.Value.Pause.Action = marshal.PauseUsePlan
	if _, err := service.Store.SetMarshalRun(t.Context(), service.ProjectID, "work", record.Value, record.Revision); err != nil {
		t.Fatal(err)
	}
	if _, ok := runCapability(w, "run/resume"); ok {
		t.Fatal("resume offered for a pause that needs a new plan")
	}
	if r := w.webWorkCommand(again); r.Status != "refused" {
		t.Fatalf("resume past a use-plan pause: %+v", r)
	}
	_, _ = w.store.RefuseWebSetting(t.Context(), *again.Command, "writer_refused")
	record, _ = service.Store.GetMarshalRun(t.Context(), service.ProjectID, "work")
	record.Value.Pause.Action = marshal.PauseResume
	if _, err := service.Store.SetMarshalRun(t.Context(), service.ProjectID, "work", record.Value, record.Revision); err != nil {
		t.Fatal(err)
	}
	last := runTestAction(t, w, mustRunCapability(t, w, "run/resume"), "22112233445566778899aabbccddeeff", webboard.SettingPayload{})
	// A paused card on screen does not make the run control stale.
	paused, _ := service.Snapshot(t.Context(), "work")
	w.setMarshalPanel(newMarshalPanel("work", "codex", paused, "paused"))
	if f := webboard.FromBoard(w.webBoardFrame()); f.Paused == nil && f.Decision == nil {
		t.Fatalf("no card on screen: %+v", f)
	}
	if r := w.webBoardAct(t.Context(), last); r.Status != "done" || runReceipt(t, w, last).Status != "applied" {
		t.Fatalf("resume: %+v", r)
	}
}

func TestLegacyStopAndTypedStopUseBoundWriter(t *testing.T) {
	w, _ := receiptTestWorkspace(t, "browser-run-stop")
	service, _ := seedMarshalRunForReceiptTest(t, w, "work", marshal.Run{State: marshal.Dispatching, Tasks: []marshal.Task{{PlanTaskID: "one", Worker: "codex", State: marshal.Queued}}})

	// Stop-workers binds every worker of the run; a replaced process
	// invalidates the admitted request before any stop.
	agent := &activeTmuxAgent{id: "lane", role: "task", runID: "work", taskID: "one", paneID: "pane", pid: 1, state: "working"}
	agent.supervisor.PID, agent.supervisor.Start = 2, "original"
	w.tmuxActiveWins = map[string]*activeTmuxAgent{"lane": agent}

	// The legacy screen key never stops workers past the bound control.
	f := webboard.FromBoard(w.webBoardFrame())
	legacy := w.webAct(webboard.Action{Kind: "key", Card: webboard.CardID(f), Key: webKeyStopAll, Confirmed: true})
	if legacy.res.Status != "refused" {
		t.Fatalf("legacy stop ran: %+v", legacy.res)
	}
	workers := runTestAction(t, w, mustRunCapability(t, w, "run/stop-workers"), "30112233445566778899aabbccddeeff", webboard.SettingPayload{})
	agent.supervisor.Start = "replacement"
	if r := w.webWorkCommand(workers); r.Status != "refused" {
		t.Fatalf("replaced worker stopped: %+v", r)
	}
	if r := runReceipt(t, w, workers); r.Status != "queued" {
		t.Fatalf("refused stop-workers started: %+v", r)
	}
	w.tmuxActiveWins = nil

	// The typed stop enters the run writer from any route, with the
	// Board's input loop in the way or not.
	stop := runTestAction(t, w, mustRunCapability(t, w, "run/stop"), "40112233445566778899aabbccddeeff", webboard.SettingPayload{Reason: "owner stop"})
	if r := w.webBoardAct(t.Context(), stop); r.Status != "done" {
		t.Fatalf("typed stop: %+v", r)
	}
	run, _ := service.Snapshot(t.Context(), "work")
	if len(run.Commands) != 1 || run.Commands[0].Operation != "stop" || run.Commands[0].ID != stop.Command.RequestID || run.CommandFence != "stop" || run.State != marshal.AwaitingUser {
		t.Fatalf("typed stop writer: %+v", run)
	}
	if r := runReceipt(t, w, stop); r.Status != "applied" || len(r.Effects) != 1 || r.Effects[0] != "run-command" {
		t.Fatalf("stop receipt: %+v", r)
	}
}

func TestWebRunOpReceiptMatrix(t *testing.T) {
	for _, op := range []string{"run/pause", "run/stop"} {
		t.Run(op, func(t *testing.T) {
			w, _ := receiptTestWorkspace(t, "browser-matrix")
			service, _ := seedMarshalRunForReceiptTest(t, w, "work", marshal.Run{State: marshal.Dispatching, Tasks: []marshal.Task{{PlanTaskID: "one", Worker: "codex", State: marshal.Queued}}})
			cap := mustRunCapability(t, w, op)

			// Expired authority: the commit refuses and nothing runs.
			expired := runTestAction(t, w, cap, "50112233445566778899aabbccddeeff", webboard.SettingPayload{})
			expired.Commit = func(func() webboard.Result) webboard.Result {
				return webboard.Result{Status: "refused", Text: "authority changed"}
			}
			if r := w.webWorkCommand(expired); r.Status != "refused" {
				t.Fatalf("expired: %+v", r)
			}
			// The server ends a refused commit with a refused receipt.
			_, _ = w.store.RefuseWebSetting(t.Context(), *expired.Command, "authority_changed")

			// A changed plan refuses the admitted request.
			changed := runTestAction(t, w, cap, "60112233445566778899aabbccddeeff", webboard.SettingPayload{})
			record, _ := service.Store.GetMarshalRun(t.Context(), service.ProjectID, "work")
			run := record.Value
			run.PlanVersion++
			if _, err := service.Store.SetMarshalRun(t.Context(), service.ProjectID, "work", run, record.Revision); err != nil {
				t.Fatal(err)
			}
			if r := w.webWorkCommand(changed); r.Status != "refused" {
				t.Fatalf("changed plan: %+v", r)
			}
			_, _ = w.store.RefuseWebSetting(t.Context(), *changed.Command, "writer_refused")
			if after, _ := service.Snapshot(t.Context(), "work"); len(after.Commands) != 0 {
				t.Fatalf("refused requests reached the writer: %+v", after.Commands)
			}

			// Lost reply before the effect: the receipt stays queued and
			// reconciliation does not run it.
			cap = mustRunCapability(t, w, op)
			a := runTestAction(t, w, cap, "70112233445566778899aabbccddeeff", webboard.SettingPayload{})
			w.reconcileWebWork(t.Context())
			if r := runReceipt(t, w, a); r.Status != "queued" {
				t.Fatalf("before effect: %+v", r)
			}
			if r := w.webWorkCommand(a); r.Status != "done" {
				t.Fatalf("%s: %+v", op, r)
			}
			// Lost reply after the effect: the receipt holds the outcome and
			// the replay adds no second command.
			if r := w.webWorkCommand(a); r.Status != "refused" {
				t.Fatalf("replay: %+v", r)
			}
			w.reconcileWebWork(t.Context())
			if r := runReceipt(t, w, a); r.Status != "applied" {
				t.Fatalf("after effect: %+v", r)
			}
			after, _ := service.Snapshot(t.Context(), "work")
			if len(after.Commands) != 1 || after.Commands[0].ID != a.Command.RequestID || after.PlanVersion != run.PlanVersion || after.ApprovalScopeDigest != run.ApprovalScopeDigest {
				t.Fatalf("writer: %+v", after)
			}
		})
	}
	t.Run("run/amend.deny", func(t *testing.T) {
		w, _ := receiptTestWorkspace(t, "browser-matrix-deny")
		_, _ = seedMarshalRunForReceiptTest(t, w, "work", marshal.Run{State: marshal.Dispatching, Tasks: []marshal.Task{{PlanTaskID: "one", Worker: "codex", State: marshal.Queued}}})
		if _, ok := runCapability(w, "run/amend.deny"); ok {
			t.Fatal("deny offered without an amendment")
		}
		m := w.marshalSession()
		m.mu.Lock()
		m.amended, m.pending = true, &marshalAmendment{reason: "scope"}
		m.mu.Unlock()
		a := runTestAction(t, w, mustRunCapability(t, w, "run/amend.deny"), "80112233445566778899aabbccddeeff", webboard.SettingPayload{})
		if r := w.webWorkCommand(a); r.Status != "done" {
			t.Fatalf("deny: %+v", r)
		}
		m.mu.Lock()
		left := m.amended || m.pending != nil
		m.mu.Unlock()
		if left || runReceipt(t, w, a).Status != "applied" {
			t.Fatal("deny did not use the amendment writer")
		}
		if r := w.webWorkCommand(a); r.Status != "refused" {
			t.Fatalf("deny replay: %+v", r)
		}
	})
	t.Run("payload", func(t *testing.T) {
		for _, op := range webRunOps {
			if !webWorkOp(op) || !webRunOp(op) {
				t.Fatalf("%s is not routed to a run writer", op)
			}
		}
	})
}

package tui

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/statepath"
	"github.com/Zen1th53/marshal/internal/store"
	"modernc.org/sqlite"
)

func recoveryChatWorkspace(t *testing.T) *Workspace {
	isolateRequiredFixtureProviders(t)
	t.Helper()
	setupFakeTmux(t)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\necho 'codex-cli 0.159.2'\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	t.Setenv("TMUX", "/tmp/tmux-test,1,0")
	t.Setenv("CODEX_HOME", t.TempDir())
	_, w, _ := acceptanceWorkspace(t)
	cleanupTmuxWorkspace(t, w)
	w.tmuxSession, w.tmuxMarshalWin, w.tmuxPath = fixtureTmuxNamespace(t, w.providerRoot()), statepath.Name, "tmux"
	w.tmuxActiveWins = make(map[string]*activeTmuxAgent)
	if err := saveDefaultProvider(w.providerRoot(), "codex"); err != nil {
		t.Fatal(err)
	}
	return w
}

func stopRecoveryDraftWatcher(w *Workspace) {
	m := w.marshalSession()
	m.mu.Lock()
	cancel, done := m.draftCancel, m.draftDone
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

func writeRecoveryChatDraft(t *testing.T, root string) {
	t.Helper()
	pack := filepath.Join(root, app.MarshalPackRelativePath)
	if err := os.MkdirAll(filepath.Join(pack, "tasks"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"REQUIREMENTS.md": "# Requirements\n- test\n", "00_INDEX.md": "| task | worker |\n", "tasks/T1.md": "task 1 note\n"} {
		if err := os.WriteFile(filepath.Join(pack, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	draft := filepath.Join(root, marshalDraftRelativePath)
	if err := os.MkdirAll(filepath.Dir(draft), 0700); err != nil {
		t.Fatal(err)
	}
	data := `{"tasks":[{"id":"T1","title":"task 1","criteria":["c1"],"paths":["a.txt"],"depends_on":[],"worker":"codex","mode":"governed","checks":[{"command":"true","criteria":["c1"]}]}]}`
	if err := os.WriteFile(draft, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(draft+".ready", []byte("ready\n"), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestMarshalChatRestartRecoversStoredRunAfterUnsavedPlanningPlaceholder(t *testing.T) {
	w := recoveryChatWorkspace(t)
	service := w.runtime.Marshal()
	run := marshal.Run{PlanID: "approved-native-plan", PlanVersion: 2, State: marshal.AwaitingUser, Settings: marshal.DefaultSettings(), Tasks: []marshal.Task{{PlanTaskID: "codex", State: marshal.Queued}}}
	if _, err := service.Store.SetMarshalRun(t.Context(), service.ProjectID, "RUN-real-process05", run, 0); err != nil {
		t.Fatal(err)
	}
	if err := saveChatBinding(w.providerRoot(), chatBinding{Provider: "codex", RunID: "RUN-unsaved-first-draft"}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.marshalChat(t.Context()); err != nil {
		t.Fatal(err)
	}
	m := w.marshalSession()
	m.mu.Lock()
	id, watcher := m.runID, m.draftCancel
	m.mu.Unlock()
	if id != "RUN-real-process05" || watcher != nil {
		t.Fatalf("restart retained placeholder or started competing draft observer: %s watcher=%v", id, watcher != nil)
	}
	if panel := w.marshalPanel(); panel == nil || panel.RunID != id || panel.State != marshal.AwaitingUser {
		t.Fatalf("recovered panel: %+v", panel)
	}
	if binding := loadChatBinding(w.providerRoot()); binding.RunID != id {
		t.Fatalf("chat binding not corrected by supported restart: %+v", binding)
	}
	record, err := service.Store.GetMarshalRun(t.Context(), service.ProjectID, id)
	if err != nil || record.Revision != 1 {
		t.Fatalf("chat restart changed stored run: %+v %v", record, err)
	}
}

func TestMarshalChatReopenUnimportedDraftKeepsPendingApproval(t *testing.T) {
	for _, existingDraft := range []bool{false, true} {
		t.Run(fmt.Sprint(existingDraft), func(t *testing.T) {
			w := recoveryChatWorkspace(t)
			if _, err := w.marshalChat(t.Context()); err != nil {
				t.Fatal(err)
			}
			// Stop the asynchronous watcher so reopening performs the import deterministically.
			stopRecoveryDraftWatcher(w)
			root := w.providerRoot()
			before := loadChatBinding(root)
			w.observeMarshalProposals(importer.SessionTranscript{SessionID: "chat", Messages: []importer.Message{{Role: "assistant", Content: `MARSHAL_PROPOSAL {"action":"approve"}`}}})
			w.permissions.mu.Lock()
			pending := w.permissions.planApprovalPending && w.permissions.planApprovalRunID == before.RunID
			w.permissions.mu.Unlock()
			if !pending {
				t.Fatal("approval was not pending for the unimported run")
			}
			if requiredLegacyFixtureRefusal {
				if !existingDraft {
					if _, err := w.marshalChat(t.Context()); err != nil {
						t.Fatal(err)
					}
					stopRecoveryDraftWatcher(w)
				}
				assertUnreviewedDraftRefusedAndDiscarded(t, w, before)
				return
			}
			if existingDraft {
				writeRecoveryChatDraft(t, root)
			}
			if _, err := w.marshalChat(t.Context()); err != nil {
				t.Fatalf("reopen: %v", err)
			}
			if got := loadChatBinding(root).RunID; got != before.RunID {
				t.Fatalf("reopen changed unimported run ID: %s -> %s", before.RunID, got)
			}
			if !existingDraft {
				stopRecoveryDraftWatcher(w)
				writeRecoveryChatDraft(t, root)
				if _, err := w.marshalChat(t.Context()); err != nil {
					t.Fatalf("import: %v", err)
				}
			}
			batch := w.permissions.queue.Take(false)
			if len(batch) != 1 || batch[0].RunID != before.RunID || batch[0].Object != "/marshal approve" {
				t.Fatalf("approval after import: %+v", batch)
			}
		})
	}
}

func TestMarshalChatUnreadableRunPreservesBinding(t *testing.T) {
	w := recoveryChatWorkspace(t)
	root := w.providerRoot()
	binding := chatBinding{Namespace: statepath.DirName, Provider: "codex", RunID: "RUN-corrupt"}
	if err := saveChatBinding(root, binding); err != nil {
		t.Fatal(err)
	}
	m := w.marshalSession()
	m.runID = binding.RunID
	layout, err := project.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", layout.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), "INSERT INTO marshal_runs (project_id, run_id, revision, data_json) VALUES (?, ?, 1, ?)", w.projectID, binding.RunID, "{broken"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.marshalChat(t.Context()); err == nil || !strings.Contains(err.Error(), "decode marshal_runs") {
		t.Fatalf("expected corrupt run error, got %v", err)
	}
	if got := loadChatBinding(root); !reflect.DeepEqual(got, binding) {
		t.Fatalf("binding overwritten: %+v", got)
	}
	m.mu.Lock()
	runID := m.runID
	m.mu.Unlock()
	if runID != binding.RunID {
		t.Fatalf("session binding overwritten: %s", runID)
	}
}

func TestMarshalChatRecoversDurableRunInsteadOfUnpublishedChat(t *testing.T) {
	for _, staleID := range []string{"", "RUN-unpublished-chat"} {
		t.Run(staleID, func(t *testing.T) {
			w := recoveryChatWorkspace(t)
			root := w.providerRoot()
			if err := saveChatBinding(root, chatBinding{Provider: "codex", RunID: staleID}); err != nil {
				t.Fatal(err)
			}
			run := marshal.Run{PlanID: "delivered-goal", PlanVersion: 1, State: marshal.AwaitingUser, Settings: marshal.DefaultSettings(), Tasks: []marshal.Task{{PlanTaskID: "fix", State: marshal.Escalated}}}
			if _, err := w.store.SetMarshalRun(t.Context(), w.projectID, "RUN-durable", run, 0); err != nil {
				t.Fatal(err)
			}
			if _, err := w.marshalChat(t.Context()); err != nil {
				t.Fatal(err)
			}
			stopRecoveryDraftWatcher(w)
			if got := loadChatBinding(root).RunID; got != "RUN-durable" {
				t.Fatalf("stored lifecycle hidden by chat ID %q", got)
			}
			_, _, got, _, err := w.marshalActive(t.Context())
			if err != nil || got != "RUN-durable" {
				t.Fatalf("recovered commands select %q: %v", got, err)
			}
			stored, err := w.store.GetMarshalRun(t.Context(), w.projectID, "RUN-durable")
			if err != nil || stored.Revision != 1 {
				t.Fatalf("recovery mutated durable run: %+v %v", stored, err)
			}
		})
	}
}

func TestMarshalOlderRejectionKeepsNewPendingApproval(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	function := fmt.Sprintf("marshal_pause_%d", time.Now().UnixNano())
	if err := sqlite.RegisterScalarFunction(function, 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		close(entered)
		<-release
		return args[0], nil
	}); err != nil {
		t.Fatal(err)
	}
	_, w, _ := acceptanceWorkspace(t)
	// A uses its own connection, allowing B's observer to read while A is paused.
	path := filepath.Join(t.TempDir(), "closed.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	run := marshal.Run{PlanID: "PLAN-A", PlanVersion: 1, State: marshal.Closed, Settings: marshal.DefaultSettings()}
	data, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE paused_runs (project_id TEXT, run_id TEXT, revision INTEGER, data_json TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO paused_runs VALUES (?, 'RUN-A', 1, ?)", w.projectID, string(data)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE VIEW marshal_runs AS SELECT project_id, run_id, revision, " + function + "(data_json) AS data_json FROM paused_runs"); err != nil {
		t.Fatal(err)
	}
	oldStore, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer oldStore.Close()
	defer unblock()
	oldService := *w.runtime.Marshal()
	oldService.Store = oldStore
	m := w.marshalSession()
	m.service, m.runID = &oldService, "RUN-A"
	tr := func(id string) importer.SessionTranscript {
		return importer.SessionTranscript{SessionID: id, Messages: []importer.Message{{Role: "assistant", Content: `MARSHAL_PROPOSAL {"action":"approve"}`}}}
	}
	done := make(chan struct{})
	go func() { defer close(done); w.observeMarshalProposals(tr("chat-A")) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("A did not reach its store lookup")
	}
	m.mu.Lock()
	m.service, m.runID = w.runtime.Marshal(), "RUN-B"
	m.mu.Unlock()
	w.observeMarshalProposals(tr("chat-B"))
	w.permissions.mu.Lock()
	occurrence := w.permissions.planApprovalOccurrence
	w.permissions.mu.Unlock()
	unblock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("A did not finish rejection")
	}
	w.permissions.mu.Lock()
	pending := w.permissions.planApprovalPending && w.permissions.planApprovalRunID == "RUN-B" && w.permissions.planApprovalOccurrence == occurrence
	w.permissions.mu.Unlock()
	if !pending {
		t.Fatal("A's rejection erased B's pending approval")
	}
	retained, err := w.store.PendingMarshalProposals(t.Context(), w.projectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(retained) != 1 || retained[0].ID != occurrence {
		t.Fatalf("expected only B's approval to remain unresolved: %+v", retained)
	}
	// An approval snapshot resolves the stored Process 04 plan, so B needs its
	// own plan identity as well as the synthetic run used by this race test.
	if err := w.runtime.Marshal().Store.SavePlan(t.Context(), plan.ExecutionPlan{
		ID: "PLAN-B", ProjectID: w.runtime.Marshal().CanonicalPlanProjectID(),
		Goal: plan.GoalBinding{GoalID: "GOAL-B", Revision: 1}, Version: 1,
		State: plan.StateDraft, Mode: plan.ModeStandard, ConstitutionVersion: constitution.Current,
	}, 0); err != nil {
		t.Fatal(err)
	}
	run.PlanID, run.State = "PLAN-B", marshal.Drafting
	if _, err := w.runtime.Marshal().Store.SetMarshalRun(t.Context(), w.projectID, "RUN-B", run, 0); err != nil {
		t.Fatal(err)
	}
	w.queueMarshalPlanApproval()
	batch := w.permissions.queue.Take(false)
	if len(batch) != 1 || batch[0].RunID != "RUN-B" || batch[0].ProposalID != occurrence {
		t.Fatalf("B's imported approval: %+v", batch)
	}
}

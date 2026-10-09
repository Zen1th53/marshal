package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/permission"
)

func TestMarshalChatBriefingAndLaunchDecision(t *testing.T) {
	brief, err := marshalRoleBriefing([]string{"codex", "agy"}, marshal.DefaultSettings(), marshal.Standard)
	if err != nil {
		t.Fatal(err)
	}
	for _, phrase := range []string{"You are the Marshal", marshalDraftRelativePath, app.MarshalPackRelativePath, "tasks/<id>.md", "Do not edit project files", "Only the person approves the plan", "/marshal approve", `{"tasks":[`, "codex, agy", "acceptance mode marshal-then-user", "Current control level: free", "This run:\n- Tier: Standard.\n", "write the pack first, then plan-draft.json, then read back", "Do not read a planned path before its write succeeds", "Confirm each write succeeded"} {
		if !strings.Contains(brief, phrase) {
			t.Errorf("briefing lacks %q", phrase)
		}
	}
	// The model is asked for tasks only; plan identity and digests are the
	// runtime's to compute.
	for _, phrase := range []string{"MarshalDraft", "ExecutionPlan", "project_id", "digest"} {
		if strings.Contains(brief, phrase) {
			t.Errorf("briefing asks the model for %q", phrase)
		}
	}
	if marshalChatProvider("agy") != "antigravity" || marshalChatProvider("claude") != "claude" || marshalChatProvider("codex") != "codex" {
		t.Fatal("Marshal provider did not select the native session")
	}
}

func TestConsumeMarshalDraft(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, marshalDraftRelativePath)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	const written = `{"tasks":[]}`
	if err := os.WriteFile(path, []byte(written), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".ready", []byte("ready\n"), 0600); err != nil {
		t.Fatal(err)
	}
	data, found, err := consumeMarshalDraft(root)
	if err != nil || !found || string(data) != written {
		t.Fatalf("data=%q found=%v err=%v", data, found, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("draft not consumed: %v", err)
	}
	if _, err := os.Stat(path + ".consumed"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := consumeMarshalDraft(root); found || err != nil {
		t.Fatalf("consumed draft picked up again: found=%v err=%v", found, err)
	}
}

func TestDraftReadBackBeforePublication(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, marshalDraftRelativePath)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"tasks":[]}`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, found, err := consumeMarshalDraft(root); err != nil || found {
		t.Fatalf("unpublished draft consumed: %v %v", found, err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(data) {
		t.Fatalf("producer read-back: %s %v", got, err)
	}
	if err := os.WriteFile(path+".ready", []byte("ready\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, found, err := consumeMarshalDraft(root); err != nil || !found {
		t.Fatalf("published draft: %v %v", found, err)
	}
	brief, err := marshalRoleBriefing([]string{"codex"}, marshal.DefaultSettings(), marshal.Standard)
	if err != nil || !strings.Contains(brief, marshalDraftRelativePath+".ready") {
		t.Fatalf("completion marker absent: %v", err)
	}
}

func TestWatcherDoesNotMoveDraftDuringProducerReadBack(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, marshalDraftRelativePath)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	poll := make(chan struct{})
	result := make(chan error)
	go func() {
		defer close(result)
		for range poll {
			_, found, err := consumeMarshalDraft(root)
			if err == nil && found {
				err = fmt.Errorf("watcher consumed before completion marker")
			}
			result <- err
		}
	}()
	defer close(poll)
	for i := 1; i <= 100; i++ {
		written := strings.Repeat(" ", i) + `{"tasks":[]}`
		if err := os.WriteFile(path, []byte(written), 0600); err != nil {
			t.Fatal(err)
		}
		poll <- struct{}{}
		data, readErr := os.ReadFile(path)
		pollErr := <-result
		if readErr != nil || pollErr != nil || string(data) != written {
			t.Fatalf("read-back raced watcher: read=%v watcher=%v data=%q", readErr, pollErr, data)
		}
	}
}

func TestMarshalChatReopenClosedRunStartsFreshDraftAndApproval(t *testing.T) {
	_, fakeLog := setupFakeTmux(t)
	_ = fakeLog
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\necho 'codex-cli 0.159.2'\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	t.Setenv("TMUX", "/tmp/tmux-test,1,0")
	t.Setenv("CODEX_HOME", t.TempDir())

	_, ws, ctx := acceptanceWorkspace(t)
	cleanupTmuxWorkspace(t, ws)
	root := ws.providerRoot()
	ws.tmuxSession = "test-session"
	ws.tmuxMarshalWin = "marshal"
	ws.tmuxPath = "tmux"
	ws.tmuxActiveWins = make(map[string]*activeTmuxAgent)

	if err := saveDefaultProvider(root, "codex"); err != nil {
		t.Fatal(err)
	}

	// 1. Setup closed run in store.
	closedRunID := "RUN-closed-123"
	run := marshal.Run{
		PlanID:      "PLAN-1",
		PlanVersion: 1,
		State:       marshal.Closed,
		Settings:    marshal.DefaultSettings(),
		Repository:  root,
		BaseCommit:  "initial",
		TargetRef:   "refs/heads/main",
	}
	if _, err := ws.runtime.Marshal().Store.SetMarshalRun(ctx, ws.projectID, closedRunID, run, 0); err != nil {
		t.Fatal(err)
	}

	// 2. Setup chat binding pointing to the closed run.
	if err := saveChatBinding(root, chatBinding{Provider: "codex", RunID: closedRunID}); err != nil {
		t.Fatal(err)
	}

	// 3. Reopen chat via /marshal chat.
	msg, err := ws.ExecuteCommand(ctx, "/marshal chat")
	if err != nil {
		t.Fatalf("reopening chat failed: %v", err)
	}
	_ = msg

	// Verify that chat started fresh with a new run ID and updated binding.
	m := ws.marshalSession()
	m.mu.Lock()
	newRunID := m.runID
	m.mu.Unlock()
	if newRunID == closedRunID {
		t.Fatalf("reopened chat stayed bound to closed run %s", closedRunID)
	}
	saved := loadChatBinding(root)
	if saved.RunID != newRunID {
		t.Fatalf("chat binding run ID %s != new run ID %s", saved.RunID, newRunID)
	}

	// 4. Codex writes plan pack, draft, ready marker, and emits approve proposal.
	packDir := filepath.Join(root, app.MarshalPackRelativePath)
	if err := os.MkdirAll(filepath.Join(packDir, "tasks"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "REQUIREMENTS.md"), []byte("# Requirements\n- test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "00_INDEX.md"), []byte("| task | worker |\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "tasks", "T1.md"), []byte("task 1 note\n"), 0600); err != nil {
		t.Fatal(err)
	}

	draftPath := filepath.Join(root, marshalDraftRelativePath)
	if err := os.MkdirAll(filepath.Dir(draftPath), 0700); err != nil {
		t.Fatal(err)
	}
	draftJSON := `{"tasks":[{"id":"T1","title":"task 1","criteria":["c1"],"paths":["a.txt"],"depends_on":[],"worker":"codex","mode":"governed","checks":[{"command":"true","criteria":["c1"]}]}]}`
	if err := os.WriteFile(draftPath, []byte(draftJSON), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(draftPath+".ready", []byte("ready\n"), 0600); err != nil {
		t.Fatal(err)
	}

	ws.observeMarshalProposals(importer.SessionTranscript{
		SessionID: "chat",
		Messages: []importer.Message{
			{Role: "assistant", Content: `MARSHAL_PROPOSAL {"action":"approve"}`},
		},
	})

	// 5. Verify the plan approval popup appears.
	deadline := time.Now().Add(5 * time.Second)
	var batch []permission.Request
	for time.Now().Before(deadline) {
		batch = ws.permissions.queue.Take(false)
		if len(batch) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(batch) != 1 {
		t.Fatalf("expected 1 plan approval popup, got %d (queue: %v)", len(batch), batch)
	}
	if batch[0].Object != "/marshal approve" {
		t.Fatalf("unexpected popup object: %s", batch[0].Object)
	}
}

func TestMarshalChatReopenNonTerminalRunKeepsRecovery(t *testing.T) {
	_, fakeLog := setupFakeTmux(t)
	_ = fakeLog
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\necho 'codex-cli 0.159.2'\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	t.Setenv("TMUX", "/tmp/tmux-test,1,0")
	t.Setenv("CODEX_HOME", t.TempDir())

	_, ws, ctx := acceptanceWorkspace(t)
	cleanupTmuxWorkspace(t, ws)
	root := ws.providerRoot()
	ws.tmuxSession = "test-session"
	ws.tmuxMarshalWin = "marshal"
	ws.tmuxPath = "tmux"
	ws.tmuxActiveWins = make(map[string]*activeTmuxAgent)

	if err := saveDefaultProvider(root, "codex"); err != nil {
		t.Fatal(err)
	}

	activeRunID := "RUN-active-456"
	run := marshal.Run{
		PlanID:      "PLAN-active",
		PlanVersion: 1,
		State:       marshal.Reviewing,
		Settings:    marshal.DefaultSettings(),
		Repository:  root,
		BaseCommit:  "initial",
		TargetRef:   "refs/heads/main",
	}
	if _, err := ws.runtime.Marshal().Store.SetMarshalRun(ctx, ws.projectID, activeRunID, run, 0); err != nil {
		t.Fatal(err)
	}

	if err := saveChatBinding(root, chatBinding{Provider: "codex", RunID: activeRunID}); err != nil {
		t.Fatal(err)
	}

	if _, err := ws.ExecuteCommand(ctx, "/marshal chat"); err != nil {
		t.Fatalf("reopening chat failed: %v", err)
	}

	m := ws.marshalSession()
	m.mu.Lock()
	boundRunID := m.runID
	m.mu.Unlock()
	if boundRunID != activeRunID {
		t.Fatalf("expected bound run ID %s, got %s", activeRunID, boundRunID)
	}
	panel := ws.marshalPanel()
	if panel.Note != "stored run recovered" {
		t.Fatalf("expected panel note 'stored run recovered', got %q", panel.Note)
	}
}

func TestRejectedMarshalProposalRecordsActivityLine(t *testing.T) {
	_, ws, ctx := acceptanceWorkspace(t)
	m := ws.marshalSession()
	service := ws.runtime.Marshal()
	m.service = service
	m.runID = "RUN-not-drafting"

	run := marshal.Run{
		PlanID:      "PLAN-1",
		PlanVersion: 1,
		State:       marshal.Closed,
		Settings:    marshal.DefaultSettings(),
		Repository:  ws.providerRoot(),
		BaseCommit:  "initial",
		TargetRef:   "refs/heads/main",
	}
	if _, err := service.Store.SetMarshalRun(ctx, ws.projectID, m.runID, run, 0); err != nil {
		t.Fatal(err)
	}

	ws.observeMarshalProposals(importer.SessionTranscript{
		SessionID: "chat",
		Messages: []importer.Message{
			{Role: "assistant", Content: `MARSHAL_PROPOSAL {"action":"approve"}`},
		},
	})

	ws.mu.RLock()
	lastOut := ws.state.LastOutput
	ws.mu.RUnlock()
	if !strings.Contains(lastOut, "Ignored Marshal proposal: plan is not awaiting approval") {
		t.Fatalf("expected rejection activity with reason 'plan is not awaiting approval', got %q", lastOut)
	}
}

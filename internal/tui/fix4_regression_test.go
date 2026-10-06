package tui

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/tmux"
)

func TestNativeMarshalOpeningKeepsProtocolHidden(t *testing.T) {
	brief, err := marshalRoleBriefing([]string{"worker"}, marshal.DefaultSettings(), marshal.Standard)
	if err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"codex", "claude", "opencode", "antigravity"} {
		args := marshalKickoffArgs(provider)
		prompt := args[len(args)-1]
		if containsMarshalProtocol(prompt) || strings.Contains(prompt, brief) {
			t.Fatalf("%s exposed compiled protocol", provider)
		}
		language := strings.Index(prompt, "Ask the language question")
		earlier := strings.Index(prompt, "ask about earlier work")
		if language < 0 || earlier <= language || !strings.Contains(prompt, "Begin at step 1.") {
			t.Fatalf("%s lost opening order", provider)
		}
	}
}

func TestWorkerBriefExcludesApprovedHistoryProvenance(t *testing.T) {
	for _, rec := range []model.MemoryRecordV2{
		{ID: "MEM-IMPORT-approved", Scope: "project", Source: model.MemorySource{Kind: "external", Reference: "session"}},
		{ID: "old-import", Scope: "project", Source: model.MemorySource{Kind: "external"}},
		{ID: "continued", Scope: "project", Source: model.MemorySource{Reference: "external:session"}},
		{ID: "session", Scope: "session"},
	} {
		rec.Body = "PRIVATE_CONTINUED_HISTORY"
		brief := marshalTaskBrief(briefTask(), app.BriefContext{Memory: []model.MemoryRecordV2{rec}})
		if strings.Contains(brief, rec.Body) {
			t.Fatalf("history leaked: %+v", rec.Source)
		}
	}
}

func TestHeadlessPermissionProposalsDoNotStartTerminalReaders(t *testing.T) {
	w := NewWorkspace(nil, "project", "session")
	w.queuePermission(permission.Request{Kind: "memory", Object: "candidate"})
	time.Sleep(250 * time.Millisecond)
	w.permissions.mu.Lock()
	running := w.permissions.running
	w.permissions.mu.Unlock()
	if running || w.permissions.queue.Empty() {
		t.Fatal("headless proposal started terminal polling or lost request")
	}
	// Terminal identity may be initialized after proposals arrive; no reader
	// exists before initialization, so this reproduces the gate's write order.
	w.tmuxPath, w.tmuxSession = "initialized-later", "session"
}

func TestEndedRunAlertIsEvidenceWithoutPermission(t *testing.T) {
	w, r := realControlWorkspace(t, "SESSION-expired-alert")
	if r.EgressRequestPending("RUN-ended", "example.test:443") {
		t.Fatal("ended run pending")
	}
	if err := w.deliverEgressAlert(app.EgressAlert{RunID: "RUN-ended", TaskID: "TASK-ended", Endpoint: "example.test:443", Message: "Old refusal", State: "expired"}); err != nil {
		t.Fatal(err)
	}
	if !w.permissions.queue.Empty() || !strings.Contains(w.state.LastOutput, "expired") {
		t.Fatal("ended run replayed a permission")
	}
	req := permission.Request{Kind: "network", Object: "example.test:443", RunID: "RUN-ended"}
	if err := w.decidePermission(context.Background(), req, true, "operator popup"); err != nil {
		t.Fatal(err)
	}
	events, err := r.Store().ListEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == "PERMISSION_DECIDED" && event.Data["allow"] != false {
			t.Fatal("ended run received allow evidence")
		}
	}
}

func TestControlPaneIdentityDoesNotFollowNativeChat(t *testing.T) {
	fake, log := setupFakeTmux(t)
	t.Setenv("TMUX_PANE", "%control")
	pane, err := tmux.CurrentPaneID(context.Background())
	if err != nil || pane != "%control" {
		t.Fatalf("pane=%s err=%v", pane, err)
	}
	w := NewWorkspace(nil, "project", "session")
	w.tmuxPath, w.tmuxSession, w.tmuxMarshalPaneID = fake, "test-session", pane
	for _, p := range []string{"%control", "%chat", "%worker"} {
		if err := w.bindWorkspaceKeysLocked(context.Background(), p, t.TempDir()); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := os.ReadFile(log)
	if strings.Count(string(data), "send-keys -t %control C-x") != 6 || strings.Contains(string(data), "set-hook -t $0 after-new-window") || strings.Contains(string(data), "set-hook -t $0 after-split-window") {
		t.Fatalf("wrong control routing or creation hooks: %s", data)
	}
}

func TestSystemRecordsClearlyLabelledInMemoryViewAndBrief(t *testing.T) {
	rec := model.MemoryRecordV2{ID: "MEM-RUN-old", Title: "Run success outcome", Source: model.MemorySource{Kind: "runtime_outcome"}, Scope: "task"}
	if !strings.Contains(memoryProvenance(rec).Text, "System record") {
		t.Fatal("view lacks system label")
	}
	brief := marshalTaskBrief(briefTask(), app.BriefContext{Memory: []model.MemoryRecordV2{rec}})
	if !strings.Contains(brief, "System record · Run success outcome") {
		t.Fatal("brief presents system evidence as a model fact")
	}
}

func TestAutomaticNativeMarshalLaunchUsesCompiledOpening(t *testing.T) {
	setupFakeTmuxWithDeadFile(t)
	t.Setenv("TMUX", "/tmp/fake-marshal,1,0")
	t.Setenv("MARSHAL_TEST_FORCE_TMUX", "1")
	w := NewWorkspace(nil, "project", "session")
	w.workDir = t.TempDir()
	cleanupTmuxWorkspace(t, w)
	w.InitTmux(w.workDir)
	w.tmuxMu.Lock()
	a := copyAgentLocked(w.tmuxActiveWins["marshal-chat"])
	w.tmuxMu.Unlock()
	if a == nil || len(a.args) == 0 {
		t.Fatal("automatic native Marshal has no launch prompt")
	}
	opening := a.args[len(a.args)-1]
	if containsMarshalProtocol(opening) || opening != marshalKickoff {
		t.Fatalf("automatic launch has unsafe opening: %q", opening)
	}
	if !strings.Contains(strings.Join(a.args[:len(a.args)-1], "\n"), "MARSHAL PROTOCOL") {
		t.Fatal("automatic launch lost hidden instructions")
	}
}

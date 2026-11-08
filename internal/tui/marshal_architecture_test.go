package tui

import (
	"context"
	"errors"
	"github.com/Zen1th53/marshal/internal/statepath"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/memory/importer"
)

func TestMarshalArchitectureProposalGrammarIsStrict(t *testing.T) {
	valid := `MARSHAL_PROPOSAL {"action":"architecture","reason":"Owner architecture request"}`
	proposal, err := parseMarshalProposal(valid)
	if err != nil || proposal.command() != "/marshal architecture" {
		t.Fatalf("valid architecture proposal refused: %+v %v", proposal, err)
	}
	for _, invalid := range []string{
		`MARSHAL_PROPOSAL {"action":"architecture"}`,
		`MARSHAL_PROPOSAL {"action":"architecture","reason":""}`,
		`MARSHAL_PROPOSAL {"action":"architecture","reason":true}`,
		`MARSHAL_PROPOSAL {"action":"architecture","reason":"Owner","action":"approve"}`,
		`MARSHAL_PROPOSAL {"action":"architecture","reason":"Owner","path":"/tmp/foreign"}`,
		`MARSHAL_PROPOSAL {"action":"architecture","reason":"Owner"} trailing`,
	} {
		if _, err := parseMarshalProposal(invalid); err == nil {
			t.Fatalf("invalid proposal accepted: %s", invalid)
		}
	}
}

func TestMarshalArchitectureSingletonSurvivesCommandDeadlineAndStopsWithWorkspace(t *testing.T) {
	w, rt := realControlWorkspace(t, "SESSION-architecture-lifecycle", false)
	m := w.marshalSession()
	entered, release, opened := make(chan context.Context, 1), make(chan struct{}), make(chan string, 4)
	var starts atomic.Int32
	m.architectureStart = func(ctx context.Context, provider, goal string) (*app.ArchitectureReviewSession, error) {
		starts.Add(1)
		entered <- ctx
		<-release
		return &app.ArchitectureReviewSession{URL: "http://127.0.0.1:12345/?token=fixture-only", Done: make(chan struct{})}, nil
	}
	m.architectureBrowser = func(ctx context.Context, url string) error { opened <- url; return ctx.Err() }
	commandCtx, cancel := context.WithCancel(context.Background())
	message, err := w.cmd.handleMarshal(commandCtx, []string{"architecture"})
	if err != nil || !strings.Contains(message, "view-only") {
		t.Fatalf("architecture command failed: %s %v", message, err)
	}
	workerCtx := <-entered
	cancel()
	if workerCtx.Err() != nil {
		t.Fatal("command cancellation terminated the managed architecture generation")
	}
	if _, deadline := workerCtx.Deadline(); deadline {
		t.Fatal("architecture worker inherited command's short deadline")
	}
	if _, err = w.marshalArchitecture(context.Background(), "", "runtime:second"); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case <-opened:
	case <-time.After(3 * time.Second):
		t.Fatal("ready architecture was not opened")
	}
	for until := time.Now().Add(3 * time.Second); time.Now().Before(until); {
		m.mu.Lock()
		ready := m.architecture != nil
		m.mu.Unlock()
		if ready {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if _, err = w.marshalArchitecture(context.Background(), "", ""); err != nil {
		t.Fatal(err)
	}
	select {
	case <-opened:
	case <-time.After(3 * time.Second):
		t.Fatal("existing view was not reused")
	}
	if starts.Load() != 1 {
		t.Fatalf("singleton generated %d model sessions", starts.Load())
	}
	if _, err = w.marshalArchitecture(context.Background(), "Silently replace the graph", ""); err == nil {
		t.Fatal("existing graph replaced without selected browser feedback")
	}
	if !w.permissions.queue.Empty() || mustCount(t, rt.Store(), "tasks") != 0 || len(m.approvals) != 0 {
		t.Fatal("architecture view granted authority or dispatched tasks")
	}
	w.Close()
	if !errors.Is(workerCtx.Err(), context.Canceled) {
		t.Fatal("workspace close did not cancel architecture lifetime")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.architecture != nil {
		t.Fatal("closed workspace retained live architecture session")
	}
}

func TestMarshalArchitectureUnboundSourcesCannotOpenBrowser(t *testing.T) {
	w, rt := realControlWorkspace(t, "SESSION-architecture-unbound", false)
	var calls atomic.Int32
	m := w.marshalSession()
	m.architectureStart = func(context.Context, string, string) (*app.ArchitectureReviewSession, error) {
		calls.Add(1)
		return nil, errors.New("fixture should not execute")
	}
	line := `MARSHAL_PROPOSAL {"action":"architecture","reason":"Owner architecture request"}`
	w.observeMarshalProposals(importer.SessionTranscript{SessionID: "unbound", Messages: []importer.Message{{Role: "assistant", Content: line}}})
	directory := filepath.Join(rt.ProjectRoot(), statepath.DirName, "proposals")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "unbound.json"), []byte(strings.TrimPrefix(line, marshalProposalPrefix)), 0600); err != nil {
		t.Fatal(err)
	}
	w.observeMarshalProposalFiles(rt.ProjectRoot())
	if calls.Load() != 0 || !w.permissions.queue.Empty() {
		t.Fatal("unbound text/file opened architecture or raised an approval popup")
	}
	if pending, err := rt.Store().PendingMarshalProposals(t.Context(), w.projectID); err != nil || len(pending) != 0 {
		t.Fatalf("rejected unbound proposal stayed pending: %+v %v", pending, err)
	}
}

func TestMarshalArchitectureBoundNativeFileOpensOnceAndPersistsResolution(t *testing.T) {
	w, rt := realControlWorkspace(t, "SESSION-architecture-native", false)
	opened := make(chan string, 2)
	var starts atomic.Int32
	m := w.marshalSession()
	m.architectureStart = func(ctx context.Context, provider, goal string) (*app.ArchitectureReviewSession, error) {
		starts.Add(1)
		return &app.ArchitectureReviewSession{URL: "http://127.0.0.1:12345/?token=fixture-only", Done: make(chan struct{})}, nil
	}
	m.architectureBrowser = func(ctx context.Context, url string) error { opened <- url; return nil }
	w.tmuxMu.Lock()
	w.tmuxActiveWins["marshal-chat"] = &activeTmuxAgent{role: "marshal-chat", state: "working", sessionID: "fixture-bound-session"}
	w.tmuxMu.Unlock()
	directory := filepath.Join(rt.ProjectRoot(), statepath.DirName, "proposals")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "architecture.json"), []byte(`{"action":"architecture","reason":"Owner architecture request"}`), 0600); err != nil {
		t.Fatal(err)
	}
	w.observeMarshalProposalFiles(rt.ProjectRoot())
	select {
	case <-opened:
	case <-time.After(3 * time.Second):
		t.Fatal("bound native architecture proposal did not open")
	}
	for until := time.Now().Add(3 * time.Second); time.Now().Before(until); {
		pending, err := rt.Store().PendingMarshalProposals(t.Context(), w.projectID)
		if err != nil {
			t.Fatal(err)
		}
		if len(pending) == 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	w.observeMarshalProposalFiles(rt.ProjectRoot())
	if starts.Load() != 1 {
		t.Fatalf("native proposal replay generated %d views", starts.Load())
	}
	if pending, err := rt.Store().PendingMarshalProposals(t.Context(), w.projectID); err != nil || len(pending) != 0 {
		t.Fatalf("opened proposal never resolved: %+v %v", pending, err)
	}
	if !w.permissions.queue.Empty() || mustCount(t, rt.Store(), "tasks") != 0 {
		t.Fatal("architecture native proposal conferred task/permission authority")
	}
}

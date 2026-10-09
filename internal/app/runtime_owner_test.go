package app

import (
	"bufio"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

func pendingOwnerHandIn(t *testing.T, rt *Runtime) {
	t.Helper()
	run := marshal.Run{PlanID: "owner-plan", PlanVersion: 1, Settings: marshal.DefaultSettings(), State: marshal.Dispatching, Tasks: []marshal.Task{{PlanTaskID: "live", State: marshal.Dispatched}},
		Operation: &marshal.LifecycleOperation{ID: "live-hand-in", Kind: "hand-in", TaskID: "live", Dir: rt.layout.Root,
			Event: events.Event{ID: "live-hand-in", At: time.Now(), Subject: rt.Marshal().ProjectID, RunID: "owner-run", TaskID: "live", Type: events.EventTypeMarshalEscalated, Data: map[string]any{"operation_kind": "hand-in"}}}}
	if _, err := rt.Store().SetMarshalRun(t.Context(), rt.Marshal().ProjectID, "owner-run", run, 0); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeOwnerSecondRuntimePreservesLiveHandIn(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	rt, err := Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	pendingOwnerHandIn(t, rt)
	agent := model.Agent{ID: "AGENT-live", ProjectID: rt.Marshal().ProjectID, DisplayName: "live", Role: model.RoleDeveloper, ModelProvider: "codex", Status: model.AgentRegistered}
	if err := rt.Store().RegisterAgent(t.Context(), agent); err != nil {
		t.Fatal(err)
	}
	session, err := rt.Store().StartSession(t.Context(), model.SessionStart{ID: "SESSION-live", AgentID: agent.ID, ProjectID: agent.ProjectID})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatalf("second runtime must open: %v", err)
	}
	defer second.Close()
	if second.ownerLock != nil {
		t.Fatal("second runtime acquired live ownership")
	}
	if _, err := second.Marshal().Snapshot(t.Context(), "owner-run"); err != nil {
		t.Fatal(err)
	}
	for name, call := range map[string]func() error{
		"startup":        func() error { return second.ReconcileStartup(t.Context()) },
		"startup-report": func() error { _, err := second.ReconcileStartupWithReport(t.Context()); return err },
		"recovery":       func() error { return second.Marshal().RecoverPendingOperations(t.Context()) },
		"resume":         func() error { _, err := second.Marshal().Resume(t.Context(), "owner-run"); return err },
		"cancel":         func() error { return second.Marshal().CancelTask(t.Context(), "owner-run", "live") },
		"dispatch": func() error {
			_, err := second.Marshal().Dispatch(t.Context(), "owner-run", "live", "brief")
			return err
		},
		"bind": func() error { _, err := second.Marshal().BindApprovedPlan(t.Context(), "new-run"); return err },
		"planning": func() error {
			_, err := second.Marshal().StartPlanning(t.Context(), "new-run", "goal", marshal.Budget{})
			return err
		},
	} {
		if err := call(); !errors.Is(err, ErrRuntimeOwned) {
			t.Errorf("%s must refuse ownership: %v", name, err)
		}
	}
	storedSession, err := rt.Store().GetSession(t.Context(), session.ID)
	if err != nil || storedSession.Status != session.Status || storedSession.Revision != session.Revision {
		t.Fatalf("live session reconciled: %+v %v", storedSession, err)
	}
	record, err := rt.Store().GetMarshalRun(t.Context(), rt.Marshal().ProjectID, "owner-run")
	if err != nil {
		t.Fatal(err)
	}
	if record.Revision != 1 || record.Value.Operation == nil || record.Value.Tasks[0].State != marshal.Dispatched || record.Value.Pause != nil {
		t.Fatalf("live operation reconciled: %+v", record)
	}
}

func TestRuntimeOwnerCrashRecovery(t *testing.T) {
	if root := os.Getenv("MARSHAL_OWNER_TEST_ROOT"); root != "" {
		rt, err := Open(t.Context(), root)
		if err != nil {
			t.Fatal(err)
		}
		pendingOwnerHandIn(t, rt)
		fmt.Println("owner-ready")
		// Deliberately omit Close; the parent kills this process.
		select {}
	}
	repo := runtimeRepo(t)
	if _, err := Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestRuntimeOwnerCrashRecovery$", "-test.timeout=30s")
	cmd.Env = append(os.Environ(), "MARSHAL_OWNER_TEST_ROOT="+repo.Path())
	cmd.Stderr = os.Stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	if line, err := bufio.NewReader(out).ReadString('\n'); err != nil || line != "owner-ready\n" {
		t.Fatalf("owner startup: %q %v", line, err)
	}
	second, err := Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatalf("second runtime must open while owner is live: %v", err)
	}
	defer second.Close()
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	recovered, err := Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	record, err := recovered.Store().GetMarshalRun(t.Context(), recovered.Marshal().ProjectID, "owner-run")
	if err != nil {
		t.Fatal(err)
	}
	if record.Value.Operation != nil || record.Value.Tasks[0].State != marshal.Escalated || record.Value.Pause == nil {
		t.Fatalf("dead owner was not recovered: %+v", record)
	}
	if err = recovered.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatalf("normal close retained ownership: %v", err)
	}
	_ = reopened.Close()
}

func TestRuntimeOwnerFailedStartupReleasesLock(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	policy := filepath.Join(repo.Path(), "CAPABILITIES.yaml")
	original, err := os.ReadFile(policy)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(policy, []byte("invalid: ["), 0600); err != nil {
		t.Fatal(err)
	}
	if rt, err := Open(t.Context(), repo.Path()); err == nil {
		_ = rt.Close()
		t.Fatal("invalid policy admitted")
	}
	if err = os.WriteFile(policy, original, 0600); err != nil {
		t.Fatal(err)
	}
	rt, err := Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatalf("failed startup retained ownership: %v", err)
	}
	_ = rt.Close()
}

func TestRuntimeOwnerLockOpenFailure(t *testing.T) {
	if file, err := acquireRuntimeOwner(filepath.Join(t.TempDir(), "missing")); err == nil {
		_ = file.Close()
		t.Fatal("missing state directory must refuse ownership")
	}
}

func TestRuntimeOwnerCloseIsIdempotent(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	rt, err := Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	lock := rt.ownerLock
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := rt.Close(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if rt.ownerLock != nil {
		t.Fatal("closed runtime retained owner lock")
	}
	if _, err := lock.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("owner lock still open: %v", err)
	}
	next, err := Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := next.Marshal().Snapshot(t.Context(), "missing"); err == nil {
		t.Fatal("unexpected run")
	}
	third, err := Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer third.Close()
	if third.ownerLock != nil {
		t.Fatal("repeated close released next owner's lock")
	}
}

// SQLite Close is idempotent, so a behavioral error assertion cannot detect
// duplicate cleanup. Check the startup cleanup paths rather than masking it
// with SQLite's tolerance of repeated Close calls.
func TestRuntimeStartupHasSingleDatabaseCleanup(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "runtime.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "OpenWithOptions" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Close" {
				return true
			}
			id, ok := sel.X.(*ast.Ident)
			if ok && id.Name == "database" {
				calls++
			}
			return true
		})
	}
	if calls != 1 {
		t.Fatalf("startup has %d database close sites; require one deferred failure cleanup", calls)
	}
}

func TestRuntimeOwnerExitThenNextRuntimeRecovers(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	owner, err := Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	pendingOwnerHandIn(t, owner)
	observer, err := Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close()
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if err := observer.Marshal().RecoverPendingOperations(t.Context()); !errors.Is(err, ErrRuntimeOwned) {
		t.Fatalf("observer must reopen to take ownership: %v", err)
	}
	next, err := Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	record, err := next.Store().GetMarshalRun(t.Context(), next.Marshal().ProjectID, "owner-run")
	if err != nil {
		t.Fatal(err)
	}
	if next.ownerLock == nil || record.Value.Operation != nil || record.Value.Tasks[0].State != marshal.Escalated || record.Value.Pause == nil {
		t.Fatalf("next owner did not recover: %+v", record)
	}
}

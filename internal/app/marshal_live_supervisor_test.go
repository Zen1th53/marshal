package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/project"
)

// noHeldLaunchHost models a platform that cannot hold a worker launch.
type noHeldLaunchHost struct{}

func (noHeldLaunchHost) BootID() (string, error) {
	return "", errors.New("no startup barrier on this platform")
}
func (noHeldLaunchHost) Find(string) ([]driver.ProcessIdentity, error) {
	return nil, driver.ErrLaunchUnsupported
}
func (noHeldLaunchHost) Alive(driver.ProcessIdentity) (bool, error) {
	return false, driver.ErrLaunchUnsupported
}
func (noHeldLaunchHost) Stop(driver.ProcessIdentity) error { return driver.ErrLaunchUnsupported }

func withLaunchHost(t *testing.T, processes driver.ProcessAdapter) {
	t.Helper()
	previous := launchHostProcesses
	launchHostProcesses = func() driver.ProcessAdapter { return processes }
	t.Cleanup(func() { launchHostProcesses = previous })
}

func runtimeOverFixture(t *testing.T, s *MarshalService) *Runtime {
	t.Helper()
	return &Runtime{store: s.Store, layout: project.Layout{Root: s.Repository, Worktrees: s.Worktrees, RuntimeDir: filepath.Join(resolvedTempDir(t), "state")}}
}

// A host that cannot hold a launch keeps today's direct, batch launches: the
// runtime builds no supervisor and a run dispatches and collects as before.
func TestRuntimeMarshalFallsBackWithoutHeldLaunch(t *testing.T) {
	withLaunchHost(t, noHeldLaunchHost{})
	fixture, _ := marshalFixture(t, 1)
	rt := runtimeOverFixture(t, fixture)
	live := rt.Marshal()
	if live.LaunchSupervisor != nil || live.rollingRefillAvailable() {
		t.Fatal("supervisor set on a host that cannot hold a launch")
	}
	if live.VerifyProcessExit == nil {
		t.Fatal("stale recovery has no exit verifier")
	}
	ok, err := live.VerifyProcessExit(t.Context(), "run", "a")
	if err != nil || !ok {
		t.Fatalf("untracked worker not reported exited: %v %v", ok, err)
	}
	// A governed attempt this runtime still tracks has not exited.
	_, untrack := rt.trackWorker(t.Context(), "run", "a", "worker")
	if ok, err = live.VerifyProcessExit(t.Context(), "run", "a"); err != nil || ok {
		t.Fatalf("tracked worker reported exited: %v %v", ok, err)
	}
	untrack()
	if ok, err = live.VerifyProcessExit(t.Context(), "run", "a"); err != nil || !ok {
		t.Fatalf("released worker not reported exited: %v %v", ok, err)
	}
}

// Where the host can hold launches, the runtime supervises with receipts in
// its state directory, and a driver without a startup barrier still launches
// directly instead of failing the run.
func TestRuntimeMarshalSupervisesWithHeldLaunch(t *testing.T) {
	withLaunchHost(t, &launchHost{boot: "boot-1"})
	fixture, _ := marshalFixture(t, 1)
	rt := runtimeOverFixture(t, fixture)
	live := rt.Marshal()
	if live.LaunchSupervisor == nil {
		t.Fatal("no supervisor on a host that can hold a launch")
	}
	if rt.Marshal().LaunchSupervisor != live.LaunchSupervisor {
		t.Fatal("services of one runtime do not share a launch supervisor")
	}
	journal, ok := live.LaunchSupervisor.Journal.(driver.FileLaunchJournal)
	if !ok || journal.Dir != filepath.Join(rt.layout.RuntimeDir, "launches") {
		t.Fatalf("launch receipts at %+v, want the project state directory", live.LaunchSupervisor.Journal)
	}

	// The fixture worker is governed: it has no startup barrier.
	copyFixtureRoles(live, fixture)
	withoutRequiredContext(live)
	ctx := t.Context()
	if _, err := live.StartPlanning(ctx, "run", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := live.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	d, err := live.Dispatch(ctx, "run", "a", "write file")
	if err != nil {
		t.Fatalf("governed worker refused under supervision: %v", err)
	}
	if _, ok := d.Handle.LaunchReceipt(); ok {
		t.Fatal("governed worker carries a launch receipt")
	}
	run, err := live.Snapshot(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	if run.Tasks[0].LaunchKey != "" || len(run.Slots) != 0 {
		t.Fatalf("direct launch took a key or slot: key=%q slots=%+v", run.Tasks[0].LaunchKey, run.Slots)
	}
	if _, err := live.CollectHandIn(ctx, "run", d); err != nil {
		t.Fatal(err)
	}
}

// withoutRequiredContext drops the runtime's required project-context hooks,
// which need an accepted project contract and a canonical plan that the
// store-only fixture does not have. Launch wiring is left as the runtime
// built it.
func withoutRequiredContext(s *MarshalService) {
	s.requiredContextDispatch = nil
	s.requiredIntegrationSource = nil
	s.requiredCanonicalVerification = nil
	s.requiredChecksOnlyClose = nil
	s.requiredContextCloseEffect = nil
}

// copyFixtureRoles moves the fixture's model, gate and role seams onto a
// runtime-built service, which keeps its own launch wiring.
func copyFixtureRoles(dst, src *MarshalService) {
	dst.Reviewer, dst.Model, dst.ModelProvider = src.Reviewer, src.Model, src.ModelProvider
	dst.CrossReview, dst.VerifierProvider, dst.IndependentVerify = src.CrossReview, src.VerifierProvider, src.IndependentVerify
	dst.GovernedCheck, dst.ApprovalActor, dst.GateState, dst.Verify = src.GovernedCheck, src.ApprovalActor, src.GateState, src.Verify
	dst.Gate, dst.Drivers, dst.GovernedDrivers = src.Gate, src.Drivers, src.GovernedDrivers
	dst.Worktrees = src.Worktrees
}

// A runtime-wired governed worker that cannot be held, because the platform
// has no startup barrier or because its run starts no worker process at
// one, falls back to a direct launch without a key or slot: the run is
// neither failed nor blocked, and the worker runs once.
func TestRuntimeMarshalGovernedLaunchFallsBackWhenUnheld(t *testing.T) {
	withLaunchHost(t, &launchHost{boot: "boot-1"})
	fixture, _ := marshalFixture(t, 1)
	runs := 0
	fixture.Drivers = map[string]driver.Driver{"worker": driver.Governed{Provider: "test", Held: true, Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		runs++
		return nil, os.WriteFile(filepath.Join(req.Worktree, req.Task.PlanTaskID+".txt"), []byte("done\n"), 0o600)
	}}}
	rt := runtimeOverFixture(t, fixture)
	live := rt.Marshal()
	if live.LaunchSupervisor == nil {
		t.Fatal("no supervisor on a host that can hold a launch")
	}
	copyFixtureRoles(live, fixture)
	withoutRequiredContext(live)
	ctx := t.Context()
	if _, err := live.StartPlanning(ctx, "run", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := live.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	d, err := live.Dispatch(ctx, "run", "a", "write file")
	if err != nil {
		t.Fatalf("unheld governed worker refused: %v", err)
	}
	if _, ok := d.Handle.LaunchReceipt(); ok {
		t.Fatal("unheld governed worker carries a launch receipt")
	}
	run, err := live.Snapshot(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	if run.Tasks[0].LaunchKey != "" || len(run.Slots) != 0 {
		t.Fatalf("direct launch took a key or slot: key=%q slots=%+v", run.Tasks[0].LaunchKey, run.Slots)
	}
	if _, err := live.CollectHandIn(ctx, "run", d); err != nil {
		t.Fatal(err)
	}
	if runs != 1 {
		t.Fatalf("governed run started %d times, want once", runs)
	}
	entries, err := os.ReadDir(filepath.Join(rt.layout.RuntimeDir, "launches"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("unheld launch left %d receipts", len(entries))
	}
}

//go:build linux && !governed

package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/worker"
)

// heldGovernedRunner stands in for a governed provider turn: like a real
// adapter it runs a short command of its own, then its beating worker
// process, through the runtime's task launch runner and worker manager,
// where a supervised launch holds the worker. Each worker start appends the
// launch key it saw to <log>/<task>.launches.
func heldGovernedRunner(rt *Runtime, log string, work func(id string) string) driver.GovernedRunner {
	return func(ctx context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		id := req.Task.PlanTaskID
		script := fmt.Sprintf("printf '%%s\\n' \"$%s\" >> '%s'; %s", driver.LaunchKeyEnv, filepath.Join(log, id+".launches"), work(id))
		runner := &taskLaunchRunner{runtime: rt, taskID: "TASK-held-" + id, base: worker.New(60*time.Second, time.Second, 1<<20)}
		turn := context.WithValue(ctx, providerTurnKey{}, true)
		// A model-discovery style command first: it does not beat, so it is
		// never the held worker.
		if res, err := runner.Run(turn, adapter.Command{Path: "/bin/sh", Args: []string{"-c", "true"}}); err != nil || res.ExitCode != 0 {
			return nil, fmt.Errorf("pre-start command: %v %d", err, res.ExitCode)
		}
		res, err := runner.Run(turn, adapter.Command{Path: "/bin/sh", Args: []string{"-c", script}, Dir: req.Worktree, Heartbeat: func() {}, HeartbeatInterval: time.Second})
		if err != nil {
			return nil, err
		}
		if res.ExitCode != 0 {
			return nil, fmt.Errorf("worker exited %d: %s", res.ExitCode, res.Stderr)
		}
		return nil, nil
	}
}

func launchesOf(t *testing.T, log, id string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(log, id+".launches"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(data))
}

// A governed ULTRA run through the runtime-built service holds each worker
// process until its receipt is durable, so every task runs under a launch
// key and slot, and the third task starts in the slot freed by the second
// while the first is still running. Each attempt launches and merges once.
func TestRuntimeMarshalGovernedUltraRunRefillsFreedSlot(t *testing.T) {
	ctx := t.Context()
	fixture, _ := marshalFixture(t, 3)
	gate := &collectorPreferenceGate{}
	gate.enabled.Store(true)
	fixture.Gate = gate
	settings, err := fixture.Store.GetMarshalSettings(ctx, fixture.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	settings.Value.UltraConcurrency = 2
	if _, err := fixture.Store.SetMarshalSettings(ctx, fixture.ProjectID, settings.Value, settings.Revision); err != nil {
		t.Fatal(err)
	}
	fake := fixture.Model.(marshalFakeModel)
	draft := fake.draft
	for i := range draft.Tasks {
		if draft.Tasks[i].Mode != marshal.Governed {
			t.Fatalf("fixture task %s is not governed", draft.Tasks[i].PlanTaskID)
		}
		draft.Tasks[i].DependsOn = nil
		draft.Plan.Tasks[i].DependsOn = nil
	}
	if draft.Plan.Graph, err = plan.BuildGraph(draft.Plan.Tasks); err != nil {
		t.Fatal(err)
	}
	fixture.Model = marshalFakeModel{draft: draft, review: fake.review}

	log := t.TempDir()
	releaseFile := filepath.Join(t.TempDir(), "release-a")
	var once sync.Once
	release := func() { once.Do(func() { _ = os.WriteFile(releaseFile, []byte("go"), 0o600) }) }
	t.Cleanup(release)
	rt := runtimeOverFixture(t, fixture)
	fixture.Drivers = map[string]driver.Driver{"worker": driver.Governed{Provider: "test", Held: true, Run: heldGovernedRunner(rt, log, func(id string) string {
		if id == "a" {
			return "while [ ! -f '" + releaseFile + "' ]; do sleep 0.05; done; printf a > a.txt"
		}
		return "printf " + id + " > " + id + ".txt"
	})}}
	live := rt.Marshal()
	if live.LaunchSupervisor == nil {
		t.Fatal("runtime-built service has no launch supervisor")
	}
	copyFixtureRoles(live, fixture)
	withoutRequiredContext(live)
	if !live.supervisesLaunch(live.Drivers["worker"]) {
		t.Fatal("governed worker is not supervised on Linux")
	}

	var mu sync.Mutex
	aRunningAtC, reviewedC := false, false
	var aKey string
	cross := live.CrossReview
	live.CrossReview = func(ctx context.Context, task marshal.Task, h marshal.HandIn, c marshal.Control) (marshal.Review, string, error) {
		if task.PlanTaskID == "c" {
			run, _, _ := live.load(context.Background(), "run")
			mu.Lock()
			if !reviewedC {
				reviewedC = true
				a := run.Tasks[0]
				aRunningAtC = a.State == marshal.Dispatched && a.LaunchKey != "" && run.SlotIndex(a.LaunchKey) >= 0
				aKey = a.LaunchKey
			}
			mu.Unlock()
		}
		review, provider, err := cross(ctx, task, h, c)
		review.EvidenceRefs = append(review.EvidenceRefs, "check:test -f "+task.PlanTaskID+".txt")
		return review, provider, err
	}

	if _, err := live.StartPlanning(ctx, "run", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := live.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	rec, err := live.Store.GetMarshalRun(ctx, live.ProjectID, "run")
	if err != nil {
		t.Fatal(err)
	}
	rec.Value.Process05Bound = false
	if _, err := live.Store.SetMarshalRun(ctx, live.ProjectID, "run", rec.Value, rec.Revision); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := live.Execute(context.Background(), "run", marshalBrief, nil)
		done <- err
	}()
	deadline := time.After(30 * time.Second)
	for {
		mu.Lock()
		ready := reviewedC
		mu.Unlock()
		if ready {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("execute ended before the freed slot was refilled: %v", err)
		case <-deadline:
			t.Fatal("timed out waiting for the refilled task's review")
		case <-time.After(20 * time.Millisecond):
		}
	}
	if got := launchesOf(t, log, "a"); len(got) != 1 || got[0] != aKey {
		t.Fatalf("running governed worker a saw launch keys %v, want [%s]", got, aKey)
	}
	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("execute timed out after releasing a")
	}
	mu.Lock()
	running := aRunningAtC
	mu.Unlock()
	if !running {
		t.Fatal("third task waited for the whole batch instead of the freed slot")
	}
	run, _, err := live.load(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	for _, task := range run.Tasks {
		if task.State != marshal.Merged {
			t.Fatalf("task %s = %s, want merged", task.PlanTaskID, task.State)
		}
		got := launchesOf(t, log, task.PlanTaskID)
		if len(got) != 1 || !strings.HasPrefix(got[0], "marshal-launch-") || keys[got[0]] {
			t.Fatalf("task %s launched with keys %v, want one fresh supervised key", task.PlanTaskID, got)
		}
		keys[got[0]] = true
	}
	if n := mergedEvents(t, live); n != 3 {
		t.Fatalf("%d merges, want one per task", n)
	}
	if n := run.ActiveSlots(); n != 0 {
		t.Fatalf("%d worker slots still held: %+v", n, run.Slots)
	}
}

// receiptCrashJournal stops the owner as it is about to record a launch's
// receipt, after its intent is durable and its worker is held. It notes
// how many live processes registered the key at that moment.
type receiptCrashJournal struct {
	driver.FileLaunchJournal
	crashed bool
	found   int
}

func (j *receiptCrashJournal) Store(rec driver.LaunchReceipt) error {
	if !j.crashed && rec.Stage == driver.LaunchReceipted {
		j.crashed = true
		found, err := driver.HostProcesses().Find(rec.Key)
		if err != nil {
			return err
		}
		j.found = len(found)
		return context.Canceled
	}
	return j.FileLaunchJournal.Store(rec)
}

// A governed launch interrupted after its intent, with its worker held at
// the barrier, never runs that worker; the next owner proves the launch
// absent and relaunches it once under the same key.
func TestRuntimeMarshalGovernedLaunchInterruptedAfterIntentRelaunchesOnce(t *testing.T) {
	ctx := t.Context()
	fixture, _ := marshalFixture(t, 1)
	log := t.TempDir()
	rt := runtimeOverFixture(t, fixture)
	fixture.Drivers = map[string]driver.Driver{"worker": driver.Governed{Provider: "test", Held: true, Run: heldGovernedRunner(rt, log, func(id string) string {
		return "printf " + id + " > " + id + ".txt"
	})}}
	live := rt.Marshal()
	copyFixtureRoles(live, fixture)
	withoutRequiredContext(live)
	dir := filepath.Join(rt.layout.RuntimeDir, "launches")
	journal := &receiptCrashJournal{FileLaunchJournal: driver.FileLaunchJournal{Dir: dir}}
	live.LaunchSupervisor = &driver.LaunchSupervisor{Journal: journal, Processes: driver.HostProcesses()}
	if _, err := live.StartPlanning(ctx, "run", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := live.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}

	if _, err := live.Dispatch(ctx, "run", "a", "write file"); err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Fatalf("interrupted launch not observed: %v", err)
	}
	if journal.found != 1 {
		t.Fatalf("held worker registered its key in %d processes, want 1", journal.found)
	}
	if got := launchesOf(t, log, "a"); len(got) != 0 {
		t.Fatalf("held worker ran before its receipt: %v", got)
	}
	run, _, err := live.load(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	if run.Operation == nil || run.Operation.Next == nil || run.Operation.Next.Tasks[0].LaunchKey == "" {
		t.Fatalf("interrupted launch retained no keyed operation: %+v", run.Operation)
	}
	key := run.Operation.Next.Tasks[0].LaunchKey

	// A new owner: fresh supervisor memory over the same receipts.
	live.LaunchSupervisor = &driver.LaunchSupervisor{Journal: driver.FileLaunchJournal{Dir: dir}, Processes: driver.HostProcesses()}
	_ = live.RecoverPendingOperations(ctx)
	run, _, err = live.load(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	if run.Operation != nil || run.Tasks[0].State != marshal.Queued || run.Tasks[0].LaunchKey != key {
		t.Fatalf("proven-absent launch not requeued under its key: op=%+v task=%+v", run.Operation, run.Tasks[0])
	}
	d, err := live.Dispatch(ctx, "run", "a", "write file")
	if err != nil {
		t.Fatal(err)
	}
	if rec, ok := d.Handle.LaunchReceipt(); !ok || rec.Key != key {
		t.Fatalf("relaunch used another key: %+v", rec)
	}
	if _, err := live.CollectHandIn(ctx, "run", d); err != nil {
		t.Fatal(err)
	}
	if got := launchesOf(t, log, "a"); len(got) != 1 || got[0] != key {
		t.Fatalf("worker launched with keys %v, want once under %s", got, key)
	}
	if disp, err := live.LaunchSupervisor.Inspect(key); err != nil || disp != driver.LaunchAbsent {
		t.Fatalf("hand-in kept the receipt: %s %v", disp, err)
	}
}

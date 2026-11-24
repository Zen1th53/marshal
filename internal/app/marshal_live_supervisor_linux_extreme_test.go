//go:build linux && !governed

package app

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/plan"
)

// On Linux the runtime holds native workers behind its own supervisor.
func TestRuntimeMarshalHasLaunchSupervisorOnLinux(t *testing.T) {
	fixture, _ := marshalFixture(t, 1)
	live := runtimeOverFixture(t, fixture).Marshal()
	if live.LaunchSupervisor == nil || !live.rollingRefillAvailable() {
		t.Fatal("runtime-built service has no launch supervisor on Linux")
	}
}

// An ULTRA run through the runtime-built service and its own supervisor,
// with real held native workers, starts the third task in the slot freed by
// the second while the first is still running.
func TestRuntimeMarshalUltraRunRefillsFreedSlot(t *testing.T) {
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
		draft.Tasks[i].Mode = marshal.Native
		draft.Tasks[i].DependsOn = nil
		draft.Plan.Tasks[i].DependsOn = nil
	}
	if draft.Plan.Graph, err = plan.BuildGraph(draft.Plan.Tasks); err != nil {
		t.Fatal(err)
	}
	fixture.Model = marshalFakeModel{draft: draft, review: fake.review}

	releaseFile := filepath.Join(t.TempDir(), "release-a")
	var once sync.Once
	release := func() { once.Do(func() { _ = os.WriteFile(releaseFile, []byte("go"), 0o600) }) }
	t.Cleanup(release)
	fixture.Drivers = map[string]driver.Driver{"worker": driver.Native{Provider: "fake", Binary: "sh", Parse: func([]byte) []marshal.CommandRecord { return nil },
		Args: func(r driver.Request) []string {
			id := r.Task.PlanTaskID
			if id == "a" {
				return []string{"-c", "while [ ! -f '" + releaseFile + "' ]; do sleep 0.05; done; printf a > a.txt"}
			}
			return []string{"-c", "printf " + id + " > " + id + ".txt"}
		}}}

	rt := runtimeOverFixture(t, fixture)
	live := rt.Marshal()
	if live.LaunchSupervisor == nil {
		t.Fatal("runtime-built service has no launch supervisor")
	}
	copyFixtureRoles(live, fixture)
	withoutRequiredContext(live)

	var mu sync.Mutex
	aRunningAtC := false
	reviewedC := false
	cross := live.CrossReview
	live.CrossReview = func(ctx context.Context, task marshal.Task, h marshal.HandIn, c marshal.Control) (marshal.Review, string, error) {
		if task.PlanTaskID == "c" {
			run, _, _ := live.load(context.Background(), "run")
			mu.Lock()
			if !reviewedC {
				reviewedC = true
				aRunningAtC = len(run.Tasks) > 0 && run.Tasks[0].State == marshal.Dispatched
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
	deadline := time.After(20 * time.Second)
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
	for _, task := range run.Tasks {
		if task.State != marshal.Merged {
			t.Fatalf("task %s = %s, want merged", task.PlanTaskID, task.State)
		}
	}
	if n := run.ActiveSlots(); n != 0 {
		t.Fatalf("%d worker slots still held: %+v", n, run.Slots)
	}
}

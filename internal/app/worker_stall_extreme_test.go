//go:build !governed

package app

import (
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
)

func TestExtremeWorkerStallClassifiesAsProviderFailure(t *testing.T) {
	reason := "worker stalled: no output or worktree change for 15m"
	limited, got, cooldown := classifyProviderLimit(errors.New("process 05: "+reason), marshal.HandIn{}, nil)
	if !limited || got != reason || cooldown != 0 {
		t.Fatalf("provider classification = %v, %q, %v", limited, got, cooldown)
	}
	command := adapter.Command{Dir: "/tmp/worktree"}
	configureWorkerStall(withWorkerStall(t.Context(), marshal.DefaultSettings()), &command)
	if command.StallWindow != 15*time.Minute || command.StallWorktree != command.Dir {
		t.Fatalf("command = %+v", command)
	}
	settings := marshal.DefaultSettings()
	settings.WorkerStallMinutes = 3
	configureWorkerStall(withWorkerStall(t.Context(), settings), &command)
	if command.StallWindow != 3*time.Minute {
		t.Fatalf("configured stall window = %v", command.StallWindow)
	}
}

func TestExtremeWorkerStallFailsOverWithoutRework(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)
	s, _ := marshalFixture(t, 1)
	startIntegrityRun(t, s, marshal.Budget{})
	run, rev, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	run.Tasks[0].Worker, run.Tasks[0].Mode = "codex", marshal.Governed
	if err := s.save(t.Context(), "run", run, rev); err != nil {
		t.Fatal(err)
	}
	s.GovernedDrivers = map[string]driver.Driver{
		"codex": &extremeMockDriver{mode: marshal.Governed, errMsg: "worker stalled: no output or worktree change for 15m"},
		"agy":   &extremeMockDriver{mode: marshal.Governed},
	}
	s.Drivers = s.GovernedDrivers
	dispatch, err := s.Dispatch(t.Context(), "run", "a", "implement feature")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CollectHandIn(t.Context(), "run", dispatch); err != nil {
		t.Fatal(err)
	}
	after, _, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	task := after.Tasks[0]
	if task.Worker != "agy" || task.State != marshal.Reassigned || task.ProviderFailovers != 1 || task.ReturnsByAgent["codex"] != 0 {
		t.Fatalf("task after stall = %+v", task)
	}
}

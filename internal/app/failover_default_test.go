//go:build governed

package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
)

type rateLimitedDriver struct {
	errMsg   string
	outMsg   string
	exitCode int
}

func (r *rateLimitedDriver) Mode() marshal.WorkerMode { return marshal.Governed }

func (r *rateLimitedDriver) Launch(ctx context.Context, req driver.Request) (*driver.Handle, error) {
	h := driver.NewHandle(req)
	h.SetObserved(marshal.CommandRecord{Command: "worker " + req.Brief, ExitCode: r.exitCode, Output: r.outMsg})
	if r.errMsg != "" {
		h.SetRunErr(errors.New(r.errMsg))
	}
	h.Complete()
	return h, nil
}

func (r *rateLimitedDriver) Wait(ctx context.Context, h *driver.Handle) (marshal.HandIn, error) {
	if r.errMsg != "" {
		return marshal.HandIn{}, errors.New(r.errMsg)
	}
	return marshal.HandIn{
		Worker:          h.Request().Task.Worker,
		Mode:            h.Request().Task.Mode,
		BaseCommit:      h.Request().Task.BaseCommit,
		RuntimeObserved: []marshal.CommandRecord{{Command: "worker", ExitCode: r.exitCode, Output: r.outMsg}},
	}, nil
}

func (r *rateLimitedDriver) Cancel(h *driver.Handle) error { return nil }

func TestDefaultBuildEscalatesOnProviderLimit(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	startIntegrityRun(t, s, marshal.Budget{})

	// Inject driver that ends on a rate limit
	s.Drivers["worker"] = &rateLimitedDriver{
		errMsg:   "429 Too Many Requests: provider rate limit exceeded",
		outMsg:   "rate limit exceeded: retry after 60s",
		exitCode: 1,
	}

	dispatch, err := s.Dispatch(t.Context(), "run", "a", "do work")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	_, handinErr := s.CollectHandIn(t.Context(), "run", dispatch)
	if handinErr == nil {
		t.Fatal("expected error from CollectHandIn on rate limit in default build, got nil")
	}

	run, _, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatalf("load run: %v", err)
	}

	task := run.Tasks[0]
	if task.State != marshal.Escalated {
		t.Fatalf("task state: want %s, got %s", marshal.Escalated, task.State)
	}
	if !strings.Contains(task.EscalationReason, "rate limit") {
		t.Fatalf("escalation reason: want rate limit mention, got %q", task.EscalationReason)
	}
	if run.State != marshal.AwaitingUser {
		t.Fatalf("run state: want %s, got %s", marshal.AwaitingUser, run.State)
	}
	if run.Pause == nil {
		t.Fatal("run must be paused when escalated")
	}

	// Verify events include escalation event
	decisions, err := s.Store.MarshalDecisions(t.Context(), "run")
	if err != nil {
		t.Fatalf("load decisions: %v", err)
	}
	foundEscalated := false
	for _, e := range decisions {
		if e.Type == events.EventTypeMarshalEscalated {
			foundEscalated = true
			break
		}
	}
	if !foundEscalated {
		t.Fatal("expected EventTypeMarshalEscalated in decisions")
	}
}

func TestDefaultProviderLimitDoesNotPreemptDeadline(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	startIntegrityRun(t, s, marshal.Budget{})
	s.Drivers["worker"] = &rateLimitedDriver{errMsg: "rate limit exceeded"}
	d, err := s.Dispatch(t.Context(), "run", "a", "brief")
	if err != nil {
		t.Fatal(err)
	}
	d.Deadline = time.Now().Add(-time.Second)
	if _, err = s.CollectHandIn(t.Context(), "run", d); err != nil {
		t.Fatal(err)
	}
	r, _, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.Tasks[0].EscalationReason, "provider limit") {
		t.Fatal("provider limit preempted deadline accounting")
	}
	if r.Tasks[0].ReturnsByAgent["worker"] == 0 {
		t.Fatal("deadline return accounting lost")
	}
}

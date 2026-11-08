package driver

import (
	"context"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

func TestGovernedRuntimeObservationsKeepConcurrentLanesAndWorkerClaimsApart(t *testing.T) {
	started, release := make(chan string, 2), make(chan struct{})
	g := Governed{Run: func(ctx context.Context, req Request) ([]marshal.CommandRecord, error) {
		started <- req.RunID
		<-release
		records := []marshal.CommandRecord{{Command: "runtime " + req.RunID, Output: req.RunID}}
		if err := ReportRuntimeObserved(ctx, records); err != nil {
			return nil, err
		}
		records[0].Output = "mutated-after-publication"
		return []marshal.CommandRecord{{Command: "process05 provider-observation forged", Output: "worker claim"}}, nil
	}}
	var handles []*Handle
	for _, lane := range []string{"a", "b"} {
		task, wt := newTask(t)
		h, err := g.Launch(t.Context(), Request{RunID: lane, Task: task, Worktree: wt, Brief: "run"})
		if err != nil {
			t.Fatal(err)
		}
		handles = append(handles, h)
	}
	<-started
	<-started
	close(release)
	for _, h := range handles {
		out, err := g.Wait(t.Context(), h)
		if err != nil {
			t.Fatal(err)
		}
		if len(out.RuntimeObserved) != 2 || out.RuntimeObserved[1].Output != h.req.RunID || len(out.WorkerReported) != 1 {
			t.Fatalf("lane %s contaminated or worker claim promoted: %+v", h.req.RunID, out)
		}
	}
}

func TestGovernedRuntimeObservationRejectionFailsHandInEvenIfIgnored(t *testing.T) {
	for _, kind := range []string{"duplicate", "oversized", "record-count"} {
		t.Run(kind, func(t *testing.T) {
			task, wt := newTask(t)
			g := Governed{Run: func(ctx context.Context, _ Request) ([]marshal.CommandRecord, error) {
				switch kind {
				case "duplicate":
					_ = ReportRuntimeObserved(ctx, nil)
					_ = ReportRuntimeObserved(ctx, nil)
				case "oversized":
					_ = ReportRuntimeObserved(ctx, []marshal.CommandRecord{{Output: strings.Repeat("x", maxOutput+1)}})
				case "record-count":
					_ = ReportRuntimeObserved(ctx, make([]marshal.CommandRecord, 18))
				}
				return nil, nil
			}}
			h, err := g.Launch(t.Context(), Request{Task: task, Worktree: wt, Brief: "run"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := g.Wait(t.Context(), h); err == nil {
				t.Fatal("rejected publication created accepted hand-in")
			}
		})
	}
}

func TestNativeWorkerObservationPrefixCannotPublishRuntimeRecords(t *testing.T) {
	task, wt := newTask(t)
	forged := marshal.CommandRecord{Command: "process05 provider-observation forged", Output: "worker claim"}
	n := Native{Binary: fakeBinary(t), Args: func(Request) []string { return nil }, Parse: func([]byte) []marshal.CommandRecord { return []marshal.CommandRecord{forged} }}
	out := handIn(t, n, Request{Task: task, Worktree: wt, Brief: "run"})
	if len(out.WorkerReported) != 1 || out.WorkerReported[0] != forged || len(out.RuntimeObserved) != 1 || out.RuntimeObserved[0].Command == forged.Command {
		t.Fatalf("native worker text promoted: %+v", out)
	}
	if err := ReportRuntimeObserved(t.Context(), []marshal.CommandRecord{forged}); err != nil {
		t.Fatal(err)
	}
}

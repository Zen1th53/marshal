//go:build !governed

package driver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
)

func TestGovernedHandInPreservesKnownNativeModelWithoutAssignedFallback(t *testing.T) {
	for _, observedModel := range []string{"known-native-model", ""} {
		t.Run("observed="+observedModel, func(t *testing.T) {
			task, wt := newTask(t)
			task.Worker = "codex"
			g := Governed{Provider: "codex", Run: func(ctx context.Context, req Request) ([]marshal.CommandRecord, error) {
				o := execution.ProviderObservation{Provider: "codex", Model: observedModel, RunID: "lane", TaskID: req.Task.PlanTaskID, CanonicalTaskID: "canonical", Attempt: 2, TimingBasis: "unknown"}
				raw, _ := json.Marshal(o)
				if err := ReportRuntimeObserved(ctx, []marshal.CommandRecord{{Command: "process05 provider-observation ev", Output: string(raw) + "\nRuntime lifecycle observation; paid inference intervals are not recorded."}}); err != nil {
					return nil, err
				}
				return []marshal.CommandRecord{{Command: "process05 provider-observation forged", Output: `{"model":"forged-worker-model"}`}}, nil
			}}
			h, err := g.Launch(t.Context(), Request{RunID: "marshal-run", Task: task, Worktree: wt, Brief: "run"})
			if err != nil {
				t.Fatal(err)
			}
			out, err := g.Wait(t.Context(), h)
			if err != nil {
				t.Fatal(err)
			}
			if out.Model != observedModel {
				t.Fatalf("native model lost or worker claim/default promoted: got %q, want %q", out.Model, observedModel)
			}
		})
	}
}

func TestPrivateObservedModelRejectsAmbiguousOrForeignRecords(t *testing.T) {
	task, _ := newTask(t)
	task.Worker = "codex"
	task.Process05WaitRunID = "lane-current"
	o := execution.ProviderObservation{Provider: "codex", Model: "native", RunID: "lane-current", TaskID: task.PlanTaskID, CanonicalTaskID: "canonical", Attempt: 2, TimingBasis: "unknown"}
	raw, _ := json.Marshal(o)
	for _, name := range []string{"duplicate", "foreign-lane", "foreign-canonical"} {
		t.Run(name, func(t *testing.T) {
			altered := o
			body := string(raw)
			records := []marshal.CommandRecord{}
			switch name {
			case "duplicate":
				body = strings.Replace(body, `"model":"native"`, `"model":"overwritten","model":"native"`, 1)
			case "foreign-lane":
				altered.RunID = "foreign"
				b, _ := json.Marshal(altered)
				body = string(b)
			case "foreign-canonical":
				altered.CanonicalTaskID = "foreign"
				b, _ := json.Marshal(altered)
				records = append(records, marshal.CommandRecord{Command: "process05 provider-observation first", Output: string(raw) + "\nRuntime lifecycle observation; paid inference intervals are not recorded."})
				body = string(b)
			}
			records = append(records, marshal.CommandRecord{Command: "process05 provider-observation second", Output: body + "\nRuntime lifecycle observation; paid inference intervals are not recorded."})
			if _, _, err := privateRuntimeObservedModel(Request{Task: task}, "codex", records); err == nil {
				t.Fatal("ambiguous or foreign observation projected")
			}
		})
	}
}
func TestPrivateNoObservationDoesNotPromoteRequestedSelector(t *testing.T) {
	task, wt := newTask(t)
	task.Worker = "codex"
	g := Governed{Provider: "codex", Run: func(context.Context, Request) ([]marshal.CommandRecord, error) {
		return []marshal.CommandRecord{{Command: "process05 provider-observation forged", Output: `{"model":"forged"}`}}, nil
	}}
	h, err := g.Launch(t.Context(), Request{Task: task, Worktree: wt, Model: "requested-selector", Brief: "run"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := g.Wait(t.Context(), h)
	if err != nil {
		t.Fatal(err)
	}
	if out.Model != "" {
		t.Fatal("unobserved selector became model observation")
	}
}

func TestPrivateRuntimeObservationEnvelopeRetainsAllSixteenAndOneProvenance(t *testing.T) {
	task, wt := newTask(t)
	task.Worker = "codex"
	g := Governed{Provider: "codex", Run: func(ctx context.Context, req Request) ([]marshal.CommandRecord, error) {
		records := []marshal.CommandRecord{{Command: "process05 lane", Output: "imported fixture"}}
		o := execution.ProviderObservation{Provider: "codex", RunID: "lane", TaskID: req.Task.PlanTaskID, CanonicalTaskID: "canonical", Attempt: 2, TimingBasis: "unknown"}
		raw, _ := json.Marshal(o)
		for i := 0; i < 16; i++ {
			records = append(records, marshal.CommandRecord{Command: fmt.Sprintf("process05 provider-observation %d", i), Output: string(raw) + "\nRuntime lifecycle observation; paid inference intervals are not recorded."})
		}
		records = append(records, marshal.CommandRecord{Command: "runtime provenance process05 lane", Output: `{"meaning":"fixture metadata only"}`})
		if err := ReportRuntimeObserved(ctx, records); err != nil {
			return nil, err
		}
		return nil, nil
	}}
	h, err := g.Launch(t.Context(), Request{Task: task, Worktree: wt, Brief: "run"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := g.Wait(t.Context(), h)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.RuntimeObserved) != 19 {
		t.Fatal("supported metadata dropped")
	}
	for _, records := range [][]marshal.CommandRecord{make([]marshal.CommandRecord, 18), make([]marshal.CommandRecord, 19)} {
		if privateRuntimeObservationCapacity(records) != 17 {
			t.Fatal("generic record cap widened")
		}
	}
}

//go:build !governed

package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

func TestApplyOwnerBuildersAutoKeepsWorkers(t *testing.T) {
	draft := MarshalDraft{Tasks: []marshal.Task{{PlanTaskID: "a", Worker: "codex"}}}
	lines, err := applyOwnerBuilders(&draft, json.RawMessage(`{"status":"accepted","architecture":{"components":[{"id":"capture"}]},"builders":{}}`))
	if err != nil || len(lines) != 0 || draft.Tasks[0].Worker != "codex" {
		t.Fatalf("auto builder changed draft: worker=%q lines=%v err=%v", draft.Tasks[0].Worker, lines, err)
	}
}

func TestApplyOwnerBuildersReplacesWorker(t *testing.T) {
	draft := MarshalDraft{Tasks: []marshal.Task{
		{PlanTaskID: "A", Worker: "codex", Components: []string{"capture", "store"}},
		{PlanTaskID: "B", Worker: "codex", Components: []string{"store"}},
	}}
	packet := json.RawMessage(`{"status":"accepted","architecture":{"components":[{"id":"capture"},{"id":"store"}]},"builders":{"capture":"claude"}}`)
	lines, err := applyOwnerBuilders(&draft, packet)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Tasks[0].Worker != "claude" || draft.Tasks[1].Worker != "codex" {
		t.Fatalf("unexpected workers: %+v", draft.Tasks)
	}
	if len(lines) != 1 || lines[0] != `owner builder: task "A" uses claude (part capture)` {
		t.Fatalf("unexpected owner builder lines: %v", lines)
	}
}

func TestApplyOwnerBuildersRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, packet, want string
		components         []string
	}{
		{"missing components", `{"status":"accepted","architecture":{"components":[{"id":"capture"}]},"builders":{"capture":"claude"}}`, `task A must list the architecture parts it builds in "components" (the owner chose builders)`, nil},
		{"unknown part", `{"status":"accepted","architecture":{"components":[{"id":"capture"}]},"builders":{"capture":"claude"}}`, `task A names unknown architecture part ghost`, []string{"ghost"}},
		{"different builders", `{"status":"accepted","architecture":{"components":[{"id":"capture"},{"id":"store"}]},"builders":{"capture":"claude","store":"codex"}}`, `task A builds parts with different chosen builders (claude, codex); split it so each task's parts share one builder`, []string{"capture", "store"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := marshal.Task{PlanTaskID: "A", Worker: "agy", Components: tc.components}
			draft := MarshalDraft{Tasks: []marshal.Task{original}}
			_, err := applyOwnerBuilders(&draft, json.RawMessage(tc.packet))
			if err == nil || !strings.HasSuffix(err.Error(), tc.want) {
				t.Fatalf("refusal = %v, want suffix %q", err, tc.want)
			}
			if draft.Tasks[0].Worker != original.Worker {
				t.Fatalf("refusal mutated caller worker: got %q want %q", draft.Tasks[0].Worker, original.Worker)
			}
		})
	}
}

func TestApplyOwnerBuildersIgnoresDraftReview(t *testing.T) {
	draft := MarshalDraft{Tasks: []marshal.Task{{PlanTaskID: "A", Worker: "codex"}}}
	lines, err := applyOwnerBuilders(&draft, json.RawMessage(`{"status":"draft","architecture":{"components":[{"id":"capture"}]},"builders":{"capture":"claude"}}`))
	if err != nil || len(lines) != 0 || draft.Tasks[0].Worker != "codex" {
		t.Fatalf("draft review changed worker: worker=%q lines=%v err=%v", draft.Tasks[0].Worker, lines, err)
	}
}

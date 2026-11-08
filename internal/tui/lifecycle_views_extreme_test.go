//go:build !governed

package tui

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
)

func lifecycleFieldsText(fields []Field) string {
	var lines []string
	for _, field := range fields {
		lines = append(lines, field.Label+": "+field.Value.Display())
	}
	return strings.Join(lines, "\n")
}

func TestExtremeLifecycleViewsReadExactRetainedRunWithoutAuthority(t *testing.T) {
	w, runtime := realControlWorkspace(t, "SESSION-lifecycle", false)
	ctx := t.Context()
	service := runtime.Marshal()
	run := marshal.Run{PlanID: "PLAN-retained", PlanVersion: 7, GoalBinding: "GOAL-retained", Settings: marshal.DefaultSettings(), State: marshal.AwaitingUser,
		Pause: &marshal.Pause{ResumeState: marshal.Dispatching, Reason: "private-reason-must-not-be-shown"}, Operation: &marshal.LifecycleOperation{Kind: "integrate"}}
	if _, err := runtime.Store().SetMarshalRun(ctx, runtime.ProjectID(), "RUN-selected", run, 0); err != nil {
		t.Fatal(err)
	}
	other := run
	other.PlanID, other.State = "PLAN-unrelated", marshal.Closed
	if _, err := runtime.Store().SetMarshalRun(ctx, runtime.ProjectID(), "RUN-unrelated", other, 0); err != nil {
		t.Fatal(err)
	}
	w.marshal = &marshalSession{service: service, runID: "RUN-selected"}
	before, _ := runtime.Store().GetMarshalRun(ctx, runtime.ProjectID(), "RUN-selected")
	for i := 0; i < 25; i++ {
		if _, err := runtime.EmitEvent(ctx, events.Event{ID: fmt.Sprintf("lifecycle-%d", i), Type: events.EventTypeMarshalTaskDispatched, Subject: runtime.ProjectID(), RunID: "RUN-selected", TaskID: "TASK-selected", At: time.Now().UTC(), Data: map[string]any{"detail": "hidden-event-payload"}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runtime.EmitEvent(ctx, events.Event{ID: "other-event", Type: events.EventTypeMarshalRunClosed, Subject: runtime.ProjectID(), RunID: "RUN-unrelated", At: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	source := &StatusSource{}
	w.attachLifecycleSource(source)
	v := testView(t)
	v.AttachSource(source, nil)
	v.Refresh(ctx)
	snap := v.snapshot()
	ia, _ := FrozenIA()
	for _, id := range []string{"CTUI-0092", "CTUI-0100"} {
		node, _ := ia.Node(id)
		content := RenderNode(node, snap)
		text := lifecycleFieldsText(content.Fields)
		if !content.ReadOnly || !content.HasNotice || !strings.Contains(content.Notice.Display(), "IMPLEMENTATION GAP") || node.Binding != BindingGap ||
			!strings.Contains(text, "RUN-selected") || !strings.Contains(text, "PLAN-retained") || !strings.Contains(text, "GOAL-retained") ||
			strings.Contains(text, "unrelated") || strings.Contains(text, "hidden-event-payload") || strings.Contains(text, "private-reason") ||
			!strings.Contains(text, "full Process 00–08 stage records are unavailable") {
			t.Fatalf("%s misrepresents retained scope or completion: %+v\n%s", id, content, text)
		}
	}
	state := lifecycleFieldsText(snap.lifecycle.State)
	if !strings.Contains(state, "Retained Marshal phase: awaiting_user") || !strings.Contains(state, "Plan version: 7") || !strings.Contains(state, "Pending operation kind: integrate") {
		t.Fatalf("retained fields missing: %s", state)
	}
	if got := strings.Count(lifecycleFieldsText(snap.lifecycle.Timeline), "Event #"); got != 20 {
		t.Fatalf("unbounded or incomplete event window: %d", got)
	}
	if _, err := runtime.MarshalRunTimeline(ctx, "foreign", "RUN-selected", 20); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("foreign binding admitted: %v", err)
	}
	// Reading grants no control authority, even on this already admitted root.
	if _, err := runtime.CommandTask(ctx, app.CommandEnvelope{ProjectID: runtime.ProjectIdentity(), SessionID: w.sessionID, TargetID: "TASK-no-authority", IdempotencyKey: "not-minted"}, app.TaskCommand{Operation: "pause"}); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("view minted command authority: %v", err)
	}
	after, _ := runtime.Store().GetMarshalRun(ctx, runtime.ProjectID(), "RUN-selected")
	if !reflect.DeepEqual(before, after) || w.marshal.service != service || w.marshal.runID != "RUN-selected" {
		t.Fatal("view changed retained run or selected session")
	}
	w.marshal.runID = "RUN-missing"
	if got := lifecycleFieldsText(w.readRetainedLifecycle(ctx).State); !strings.Contains(got, "exact selected run is unavailable") || strings.Contains(got, "PLAN-unrelated") {
		t.Fatalf("stale selection silently fell back: %s", got)
	}
	w.projectIdentity = "foreign"
	if got := lifecycleFieldsText(w.readRetainedLifecycle(ctx).State); !strings.Contains(got, "no admitted project identity") {
		t.Fatalf("foreign workspace admitted: %s", got)
	}
}

func TestExtremeLifecycleRetainedRecoveryAndEmptyRemainReadOnly(t *testing.T) {
	w, runtime := realControlWorkspace(t, "SESSION-lifecycle-reopen", false)
	if got := w.readRetainedLifecycle(t.Context()); len(got.State) != 1 || got.State[0].Value.Status != TruthEmpty || w.marshal != nil {
		t.Fatalf("empty project created a run/session: %+v", got)
	}
	run := marshal.Run{PlanID: "PLAN-retained", PlanVersion: 1, Settings: marshal.DefaultSettings(), State: marshal.Dispatching}
	if _, err := runtime.Store().SetMarshalRun(t.Context(), runtime.ProjectID(), "RUN-retained", run, 0); err != nil {
		t.Fatal(err)
	}
	if got := lifecycleFieldsText(w.readRetainedLifecycle(t.Context()).State); !strings.Contains(got, "RUN-retained") || w.marshal != nil {
		t.Fatalf("retained lookup did not stay read-only: %s", got)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := app.Open(t.Context(), runtime.ProjectRoot())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	fresh := &Workspace{runtime: reopened, projectIdentity: projectid.ID(reopened.ProjectIdentity())}
	if got := lifecycleFieldsText(fresh.readRetainedLifecycle(t.Context()).State); !strings.Contains(got, "RUN-retained") || fresh.marshal != nil {
		t.Fatalf("fresh runtime lost retained state or created a session: %s", got)
	}
	foreign, otherRuntime := realControlWorkspace(t, "SESSION-foreign-lifecycle", false)
	_ = foreign
	fresh.marshal = &marshalSession{runID: "RUN-retained", service: otherRuntime.Marshal()}
	if got := lifecycleFieldsText(fresh.readRetainedLifecycle(t.Context()).State); !strings.Contains(got, "another runtime") {
		t.Fatalf("foreign selected service admitted: %s", got)
	}
	if _, err := reopened.MarshalRunTimeline(t.Context(), reopened.ProjectIdentity(), "RUN-missing", 20); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("missing run timeline admitted: %v", err)
	}
}

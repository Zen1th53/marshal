//go:build !governed

package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/Zen1th53/marshal/internal/memory/security"
	"github.com/Zen1th53/marshal/internal/model"
)

const lifecycleViewSource = "retained Marshal state and structured run events; read-only"

func (w *Workspace) attachLifecycleSource(source *StatusSource) {
	source.lifecycle = w.readRetainedLifecycle
}

func buildLifecycleGapFields(id string, snap Snapshot) []Field {
	switch id {
	case "CTUI-0092":
		return snap.lifecycle.State
	case "CTUI-0100":
		return snap.lifecycle.Timeline
	}
	return nil
}

// Reading a selected or retained run must not reopen a provider, acquire
// lifecycle ownership, instantiate a Marshal session, or recover an operation.
func (w *Workspace) readRetainedLifecycle(ctx context.Context) lifecycleSnapshot {
	unavailable := func(value Value) lifecycleSnapshot {
		fields := []Field{{Label: "Retained run", Value: value}}
		return lifecycleSnapshot{State: fields, Timeline: fields}
	}
	if w == nil {
		return unavailable(Unknown("no workspace is attached", lifecycleViewSource))
	}
	w.mu.RLock()
	runtime, binding, current := w.runtime, string(w.projectIdentity), w.marshal
	w.mu.RUnlock()
	if runtime == nil || binding == "" || binding != runtime.ProjectIdentity() {
		return unavailable(Refused("the workspace has no admitted project identity", lifecycleViewSource))
	}
	service := runtime.Marshal()
	if service == nil || service.Store != runtime.Store() || service.ProjectID != runtime.ProjectID() {
		return unavailable(Unknown("no admitted Marshal data project is attached", lifecycleViewSource))
	}
	var runID string
	if current != nil {
		current.mu.Lock()
		runID, selectedService := current.runID, current.service
		current.mu.Unlock()
		if runID != "" && (selectedService == nil || selectedService.Store != service.Store || selectedService.ProjectID != service.ProjectID) {
			return unavailable(Refused("the selected run belongs to another runtime", lifecycleViewSource))
		}
		// Keep the selected identifier; never replace a stale selection with a
		// different run and display its state under the original identity.
		if runID != "" {
			return w.readRetainedRun(ctx, binding, runID)
		}
	}
	runID, _, err := service.Store.LatestMarshalRun(ctx, service.ProjectID)
	if errors.Is(err, model.ErrNotFound) {
		return unavailable(Empty(lifecycleViewSource))
	}
	if err != nil {
		return unavailable(Errored("retained run lookup failed", lifecycleViewSource))
	}
	return w.readRetainedRun(ctx, binding, runID)
}

func (w *Workspace) readRetainedRun(ctx context.Context, binding, runID string) lifecycleSnapshot {
	w.mu.RLock()
	runtime := w.runtime
	w.mu.RUnlock()
	failed := func(reason string) lifecycleSnapshot {
		fields := []Field{{Label: "Retained run", Value: Refused(reason, lifecycleViewSource)}}
		return lifecycleSnapshot{State: fields, Timeline: fields}
	}
	if runtime == nil || binding != runtime.ProjectIdentity() {
		return failed("the runtime binding changed during the read")
	}
	record, err := runtime.Store().GetMarshalRun(ctx, runtime.ProjectID(), runID)
	if err != nil {
		return failed("the exact selected run is unavailable")
	}
	run := record.Value
	known := func(text string) Value {
		if text == "" {
			return Empty(lifecycleViewSource)
		}
		if len(text) > 256 || strings.ContainsFunc(text, unicode.IsControl) || security.NewFirewall(security.FirewallConfig{}).ScanText(text) != nil {
			return Unknown("unsafe or oversized metadata omitted", lifecycleViewSource)
		}
		return Known(text, lifecycleViewSource)
	}
	identity := []Field{{Label: "Run ID", Value: known(runID)},
		{Label: "Data project", Value: known(runtime.ProjectID())},
		{Label: "Plan ID", Value: known(run.PlanID)},
		{Label: "Plan version", Value: known(fmt.Sprint(run.PlanVersion))},
		{Label: "Goal binding", Value: known(run.GoalBinding)},
		{Label: "State revision", Value: known(fmt.Sprint(record.Revision))}}
	snap := lifecycleSnapshot{State: append([]Field{}, identity...), Timeline: append([]Field{}, identity...)}
	snap.State = append(snap.State, Field{Label: "Retained Marshal phase", Value: known(string(run.State))})
	if run.Pause != nil {
		snap.State = append(snap.State, Field{Label: "Paused continuation", Value: known(string(run.Pause.ResumeState))})
	}
	if run.Operation != nil {
		snap.State = append(snap.State, Field{Label: "Pending operation kind", Value: known(run.Operation.Kind)})
	}
	partial := NotRun("observed Marshal run only; full Process 00–08 stage records are unavailable", lifecycleViewSource)
	snap.State = append(snap.State, Field{Label: "Lifecycle coverage", Value: partial})
	snap.Timeline = append(snap.Timeline, Field{Label: "Lifecycle coverage", Value: partial})
	rows, err := runtime.MarshalRunTimeline(ctx, binding, runID, 20)
	if err != nil {
		snap.Timeline = append(snap.Timeline, Field{Label: "Recorded events", Value: Errored("exact run timeline could not be read", lifecycleViewSource)})
		return snap
	}
	confirmed, err := runtime.Store().GetMarshalRun(ctx, runtime.ProjectID(), runID)
	if err != nil || confirmed.Revision != record.Revision {
		return failed("the selected run changed during the snapshot; refresh to read its new revision")
	}
	if len(rows) == 0 {
		snap.Timeline = append(snap.Timeline, Field{Label: "Recorded events", Value: Empty(lifecycleViewSource)})
	}
	for _, event := range rows {
		// Event.Data, Subject, ResourceID, IdempotencyKey and free-form pause
		// reasons are never promoted into a display or model instruction.
		snap.Timeline = append(snap.Timeline,
			Field{Label: fmt.Sprintf("Event #%d", event.Sequence), Value: known(string(event.Type))},
			Field{Label: "Recorded at", Value: known(event.At.UTC().Format(time.RFC3339Nano))},
			Field{Label: "Task ID", Value: known(event.TaskID)})
	}
	snap.Timeline = append(snap.Timeline, Field{Label: "Window", Value: known("up to 20 most recent events; ordered by durable sequence")})
	return snap
}

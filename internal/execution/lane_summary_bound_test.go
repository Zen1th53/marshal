//go:build !governed

package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func boundTestLane(failures int) ExecutionRun {
	run := ExecutionRun{
		RunID:         "parent-1-lane-T1",
		ParentRunID:   "parent-1",
		LaneTaskID:    "T1",
		ParentBinding: "binding-digest",
		Version:       1,
		State:         RunRunning,
		Tasks: map[string]TaskExecution{"T1": {
			TaskID: "T1", State: TaskRunning, Attempts: 3,
			NativeTurn: &NativeTurnBinding{Provider: "codex", ThreadID: "th", TurnID: "tu", Worktree: "/w", WorktreeDigest: "wd", ConstraintDigest: "cd", RunRevision: 7, State: "running"},
		}},
		Evidence:  []EvidenceRef{{}},
		StartedAt: time.Unix(1700000000, 0).UTC(),
	}
	for i := 0; i < failures; i++ {
		run.Failures = append(run.Failures, RunFailure{TaskID: "T1", Stage: "EXECUTE", Reason: fmt.Sprintf("attempt %d: %s", i, strings.Repeat("x", 200)), Timestamp: time.Unix(1700000000+int64(i), 0).UTC()})
		run.ProviderCalls = append(run.ProviderCalls, ProviderCallMeta{CallID: fmt.Sprintf("call-%d", i), TaskID: "T1", Harness: "codex", Timestamp: time.Unix(1700000000+int64(i), 0).UTC()})
	}
	return run
}

func reopenExact(t *testing.T, dir string, want ExecutionRun) *FileRunStore {
	t.Helper()
	store, err := NewFileRunStore(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, err := store.GetRun(context.Background(), want.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reopened lane differs: %d/%d failures, %d/%d calls", len(got.Failures), len(want.Failures), len(got.ProviderCalls), len(want.ProviderCalls))
	}
	return store
}

func laneFileSize(t *testing.T, dir, id string) (int, map[string]any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	return len(data), raw
}

// A lane snapshot rewritten on every update stays within its bound, while the
// identity, binding and every recorded attempt and failure survive a reopen
// exactly, for an old oversized record and under concurrent updates.
func TestLaneSummaryBoundKeepsRecoveryEvidence(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "runs")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	// An old record written before the bound, far over it.
	legacy := boundTestLane(2000)
	data, _ := json.MarshalIndent(legacy, "", "  ")
	if len(data) <= laneSummaryLimit*3 {
		t.Fatalf("legacy fixture too small: %d", len(data))
	}
	if err := os.WriteFile(filepath.Join(dir, legacy.RunID+".json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	// A parent run is never compacted.
	parent := boundTestLane(2000)
	parent.RunID, parent.ParentRunID = "parent-1", ""
	parentData, _ := json.MarshalIndent(parent, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "parent-1.json"), parentData, 0644); err != nil {
		t.Fatal(err)
	}
	want, _ := cloneExecutionRun(legacy)
	store := reopenExact(t, dir, want)

	// The next update compacts it; the cancel source it records is kept.
	run, _ := store.GetRun(ctx, legacy.RunID)
	run.Failures = append(run.Failures, RunFailure{TaskID: "T1", Stage: "EXECUTE", Reason: cancellationReason(context.Background(), context.Canceled)})
	if err := store.UpdateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	size, raw := laneFileSize(t, dir, legacy.RunID)
	if size > laneSummaryLimit {
		t.Fatalf("lane snapshot is %d bytes, over the %d bound", size, laneSummaryLimit)
	}
	if raw["parent_binding"] != "binding-digest" || raw["lane_overflow"] == nil || raw["tasks"].(map[string]any)["T1"].(map[string]any)["native_turn"] == nil {
		t.Fatalf("compacted snapshot lost identity, binding or its history references: %v", raw["lane_overflow"])
	}
	want, _ = store.GetRun(ctx, legacy.RunID)
	if last := want.Failures[len(want.Failures)-1].Reason; !strings.Contains(last, "source is unknown") {
		t.Fatalf("cancellation reason %q", last)
	}
	store = reopenExact(t, dir, want)
	if size, raw := laneFileSize(t, dir, "parent-1"); size != len(parentData) || raw["lane_overflow"] != nil {
		t.Fatal("a parent run was compacted")
	}

	// Concurrent writers each append an attempt; CAS keeps them consistent
	// and the history keeps growing past a second segment.
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 60; {
				run, err := store.GetRun(ctx, legacy.RunID)
				if err != nil {
					t.Error(err)
					return
				}
				run.Failures = append(run.Failures, RunFailure{TaskID: "T1", Stage: "EXECUTE", Reason: fmt.Sprintf("writer %d attempt %d %s", w, i, strings.Repeat("y", 1000))})
				run.ProviderCalls = append(run.ProviderCalls, ProviderCallMeta{CallID: fmt.Sprintf("w%d-%d", w, i)})
				if err := store.UpdateRun(ctx, run); errors.Is(err, ErrRunConflict) {
					continue
				} else if err != nil {
					t.Error(err)
					return
				}
				i++
			}
		}(w)
	}
	wg.Wait()
	want, _ = store.GetRun(ctx, legacy.RunID)
	if len(want.Failures) != 2001+4*60 {
		t.Fatalf("failures %d after concurrent updates", len(want.Failures))
	}
	if size, raw := laneFileSize(t, dir, legacy.RunID); size > laneSummaryLimit || len(raw["lane_overflow"].([]any)) < 2 {
		t.Fatalf("lane snapshot %d bytes with %v segments", size, len(raw["lane_overflow"].([]any)))
	}
	reopenExact(t, dir, want)

	// A missing segment fails the load instead of reopening partial evidence.
	segments, _ := os.ReadDir(filepath.Join(dir, laneOverflowDir))
	if len(segments) == 0 {
		t.Fatal("no history segments written")
	}
	if err := os.Remove(filepath.Join(dir, laneOverflowDir, segments[0].Name())); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileRunStore(dir); err == nil {
		t.Fatal("a lane with a missing history segment reopened")
	}
}

// A cancelled task records the source its context names.
func TestCancelledTaskReasonNamesItsSource(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(errors.New("cancelled by the owner's stop"))
	if got := cancellationReason(ctx, context.Canceled); !strings.Contains(got, "owner's stop") {
		t.Fatalf("reason %q", got)
	}
	if got := cancellationReason(context.Background(), errors.New("boom")); got != "boom" {
		t.Fatalf("plain error reason %q", got)
	}
}

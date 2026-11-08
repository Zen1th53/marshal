package execution

import (
	"encoding/json"
	"errors"
	"github.com/Zen1th53/marshal/internal/statepath"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// The child is a fresh executable with no engine caches or oracle records.
func TestProcess06FreshProcess(t *testing.T) {
	if root := os.Getenv("MARSHAL_HANDOFF_TEST_ROOT"); root != "" {
		store, err := NewFileRunStore(filepath.Join(root, statepath.DirName, "durable-runs"))
		if err != nil {
			t.Fatal(err)
		}
		e, err := NewEngine(EngineConfig{ProjectRoot: root, MaxWorkers: 1, DefaultTTL: time.Minute}, store, nil)
		if err != nil {
			t.Fatal(err)
		}
		bundle, err := e.AssembleProcess06Bundle(t.Context(), os.Getenv("MARSHAL_HANDOFF_TEST_RUN"))
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join(root, statepath.DirName, "expected-bundle.json"))
		if err != nil {
			t.Fatal(err)
		}
		var expected Process06HandoffBundle
		if err := json.Unmarshal(raw, &expected); err != nil {
			t.Fatal(err)
		}
		expected.CompletedAt = bundle.CompletedAt
		// Oracle map iteration need not have the same order; compare by identity.
		if len(bundle.EvidenceBundle) == 0 || !reflect.DeepEqual(expected, *bundle) {
			t.Fatalf("fresh process lost durable handoff facts: expected=%+v actual=%+v", expected, bundle)
		}
		return
	}
	root := t.TempDir()
	store, err := NewFileRunStore(filepath.Join(root, statepath.DirName, "durable-runs"))
	if err != nil {
		t.Fatal(err)
	}
	e, err := NewEngine(EngineConfig{ProjectRoot: root, MaxWorkers: 1, DefaultTTL: time.Minute}, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	goal, p := createTestGoalAndPlan(time.Now().UTC())
	handoff := createTestHandoff(t, root, goal, p)
	run, err := e.InitializeRun(t.Context(), handoff, goal, p)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := e.ExecuteRun(t.Context(), run.RunID)
	if err != nil {
		t.Fatalf("execute: %v tasks=%+v", err, completed.Tasks)
	}
	expected, err := e.AssembleProcess06Bundle(t.Context(), completed.RunID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, statepath.DirName, "expected-bundle.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	child := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestProcess06FreshProcess$", "-test.count=1")
	child.Env = append(os.Environ(), "MARSHAL_HANDOFF_TEST_ROOT="+root, "MARSHAL_HANDOFF_TEST_RUN="+run.RunID)
	if out, err := child.CombinedOutput(); err != nil {
		t.Fatalf("fresh CLI process: %v\n%s", err, out)
	}
	// A durable reference without its full record must never become an empty
	// successful handoff, even if this engine still has a populated oracle.
	damaged, err := store.GetRun(t.Context(), run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	damaged.EvidenceRecords = nil
	if err := store.UpdateRun(t.Context(), damaged); err != nil {
		t.Fatal(err)
	}
	if _, err := e.AssembleProcess06Bundle(t.Context(), run.RunID); !errors.Is(err, ErrEvidenceMissing) {
		t.Fatalf("missing durable evidence accepted: %v", err)
	}
}

package app

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

func TestSavedTaskStatesLoadAndResume(t *testing.T) {
	data, err := os.ReadFile("testdata/task_states.json")
	if err != nil {
		t.Fatal(err)
	}
	var states []marshal.TaskState
	if err := json.Unmarshal(data, &states); err != nil {
		t.Fatal(err)
	}
	seen := map[marshal.TaskState]bool{}
	for _, state := range states {
		seen[state] = true
	}
	for state := range marshal.TaskMoves() {
		if !seen[state] {
			t.Fatalf("persisted state %s missing from fixture", state)
		}
	}
	for _, state := range states {
		t.Run(string(state), func(t *testing.T) {
			s, _ := marshalFixture(t, 1)
			if _, err := s.StartPlanning(t.Context(), "original", "write files", marshal.Budget{}); err != nil {
				t.Fatal(err)
			}
			run, err := s.Approve(t.Context(), "original")
			if err != nil {
				t.Fatal(err)
			}
			run.Tasks[0].State = state
			// Seed an initial snapshot as a saved run, without replaying its history.
			if err := s.Store.SaveMarshalState(t.Context(), s.ProjectID, "saved", run, 0, nil, nil, 0); err != nil {
				t.Fatal(err)
			}
			loaded, _, err := s.load(t.Context(), "saved")
			if err != nil {
				t.Fatal(err)
			}
			if loaded.Tasks[0].State != state {
				t.Fatalf("loaded %s as %s", state, loaded.Tasks[0].State)
			}
			resumed, err := s.Resume(t.Context(), "saved")
			if err != nil {
				t.Fatal(err)
			}
			want := state
			// An interrupted worker returns for another attempt.
			if state == marshal.Dispatched {
				want = marshal.Returned
			}
			if resumed.Tasks[0].State != want {
				t.Errorf("resume %s: got %s, want %s", state, resumed.Tasks[0].State, want)
			}
		})
	}
}

// advanceTaskFixture prepares persisted progress through real lifecycle moves,
// so fixture setup exercises the same persistence guard as execution.
func advanceTaskFixture(t *testing.T, s *MarshalService, id string, index int, target marshal.TaskState) (marshal.Run, int64) {
	t.Helper()
	run, rev, err := s.load(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	type route []marshal.TaskState
	queue := []route{{run.Tasks[index].State}}
	seen := map[marshal.TaskState]bool{}
	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]
		last := path[len(path)-1]
		if last == target {
			for _, next := range path[1:] {
				if err := marshal.SetTaskState(&run.Tasks[index], next); err != nil {
					t.Fatal(err)
				}
				if err := s.save(t.Context(), id, run, rev); err != nil {
					t.Fatal(err)
				}
				rev++
			}
			return run, rev
		}
		if seen[last] {
			continue
		}
		seen[last] = true
		for _, next := range marshal.TaskMoves()[last] {
			queue = append(queue, append(append(route{}, path...), next))
		}
	}
	t.Fatalf("no fixture route from %s to %s", run.Tasks[index].State, target)
	return run, rev
}

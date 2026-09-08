package plan_test

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/plan"
)

func task(id string, dependsOn ...string) plan.Task {
	return plan.Task{ID: id, Title: id, DependsOn: dependsOn, Weight: 1}
}

func mutating(id string, paths []string, dependsOn ...string) plan.Task {
	t := task(id, dependsOn...)
	t.Paths = paths
	t.Mutating = true
	return t
}

func buildGraph(t *testing.T, tasks ...plan.Task) plan.Graph {
	t.Helper()
	graph, err := plan.BuildGraph(tasks)
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}
	return graph
}

func TestLinearChainOrdersAndFindsCriticalPath(t *testing.T) {
	graph := buildGraph(t, task("a"), task("b", "a"), task("c", "b"))

	want := []string{"a", "b", "c"}
	for i, id := range want {
		if graph.Order[i] != id {
			t.Fatalf("order was %v, want %v", graph.Order, want)
		}
	}
	if len(graph.Stages) != 3 {
		t.Fatalf("a linear chain produced %d stages, want 3", len(graph.Stages))
	}
	if strings.Join(graph.CriticalPath, ",") != "a,b,c" {
		t.Fatalf("critical path was %v", graph.CriticalPath)
	}
}

// Independent work runs together; dependent work does not.
func TestFanOutAndFanInGroupCorrectly(t *testing.T) {
	graph := buildGraph(t,
		task("setup"),
		task("left", "setup"),
		task("right", "setup"),
		task("merge", "left", "right"),
	)

	if len(graph.Stages) != 3 {
		t.Fatalf("fan-out/fan-in produced %d stages, want 3: %v", len(graph.Stages), graph.Stages)
	}
	if len(graph.Stages[1]) != 2 {
		t.Fatalf("the parallel stage holds %v, want both branches", graph.Stages[1])
	}
	if graph.Stages[0][0] != "setup" || graph.Stages[2][0] != "merge" {
		t.Fatalf("stages are misordered: %v", graph.Stages)
	}
}

// A cycle is refused rather than repaired: it means the caller's model of the
// work is wrong, and dropping an edge to make it resolvable would hide that.
func TestCycleIsRefusedAndNamesTheTasks(t *testing.T) {
	_, err := plan.BuildGraph([]plan.Task{
		task("a", "c"), task("b", "a"), task("c", "b"),
	})
	if err == nil {
		t.Fatal("a dependency cycle was accepted")
	}
	for _, id := range []string{"a", "b", "c"} {
		if !strings.Contains(err.Error(), id) {
			t.Fatalf("the error does not name %q: %v", id, err)
		}
	}
}

func TestMalformedGraphsAreRefused(t *testing.T) {
	cases := map[string][]plan.Task{
		"no tasks":           {},
		"missing dependency": {task("a", "nonexistent")},
		"self dependency":    {task("a", "a")},
		"duplicate id":       {task("a"), task("a")},
		"empty id":           {{ID: "  ", Title: "x"}},
	}
	for name, tasks := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := plan.BuildGraph(tasks); err == nil {
				t.Fatalf("a plan with %s was accepted", name)
			}
		})
	}
}

// Two tasks changing the same file are not scheduled together, because the
// result would depend on an ordering nobody specified.
func TestTasksSharingAFileAreNotParallel(t *testing.T) {
	graph := buildGraph(t,
		mutating("first", []string{"internal/api/handler.go"}),
		mutating("second", []string{"internal/api/handler.go"}),
	)

	if len(graph.Conflicts) != 1 {
		t.Fatalf("the shared file produced %d conflicts: %+v", len(graph.Conflicts), graph.Conflicts)
	}
	if graph.Conflicts[0].Kind != plan.ConflictSharedPath {
		t.Fatalf("conflict kind was %s", graph.Conflicts[0].Kind)
	}
	if !strings.Contains(graph.Conflicts[0].Detail, "handler.go") {
		t.Fatalf("the conflict does not name the file: %q", graph.Conflicts[0].Detail)
	}
	// They are separated rather than dropped: both still run.
	if len(graph.Stages) != 2 {
		t.Fatalf("conflicting tasks were not separated: %v", graph.Stages)
	}
	if len(graph.Order) != 2 {
		t.Fatalf("a conflicting task was dropped: %v", graph.Order)
	}
}

// A directory covers what is beneath it, and a similarly named sibling is not
// inside it.
func TestPathOverlapUsesSegmentsNotPrefixes(t *testing.T) {
	nested := buildGraph(t,
		mutating("dir", []string{"internal/api"}),
		mutating("file", []string{"internal/api/handler.go"}),
	)
	if len(nested.Conflicts) != 1 {
		t.Fatalf("a file inside a changed directory did not conflict: %+v", nested.Conflicts)
	}

	sibling := buildGraph(t,
		mutating("api", []string{"internal/api"}),
		mutating("apiv2", []string{"internal/apiv2"}),
	)
	if len(sibling.Conflicts) != 0 {
		t.Fatalf("a similarly named sibling directory was treated as overlapping: %+v", sibling.Conflicts)
	}
}

// Read-only tasks cannot interfere however much they overlap, so reporting
// them would be noise that trains people to ignore conflicts.
func TestReadOnlyTasksDoNotConflict(t *testing.T) {
	graph := buildGraph(t,
		plan.Task{ID: "inspect", Title: "inspect", Paths: []string{"internal/api"}, Weight: 1},
		plan.Task{ID: "review", Title: "review", Paths: []string{"internal/api"}, Weight: 1},
	)
	if len(graph.Conflicts) != 0 {
		t.Fatalf("two read-only tasks conflicted: %+v", graph.Conflicts)
	}
	if len(graph.Stages) != 1 {
		t.Fatalf("read-only tasks were separated unnecessarily: %v", graph.Stages)
	}
}

// The critical path follows weight, not task count: it is the chain whose
// length sets the plan's length.
func TestCriticalPathFollowsWeight(t *testing.T) {
	heavy := plan.Task{ID: "heavy", Title: "heavy", Weight: 10}
	light1 := plan.Task{ID: "l1", Title: "l1", Weight: 1}
	light2 := plan.Task{ID: "l2", Title: "l2", DependsOn: []string{"l1"}, Weight: 1}
	final := plan.Task{ID: "final", Title: "final", DependsOn: []string{"heavy", "l2"}, Weight: 1}

	graph := buildGraph(t, heavy, light1, light2, final)
	joined := strings.Join(graph.CriticalPath, ",")
	if joined != "heavy,final" {
		t.Fatalf("critical path was %q; the heavy chain should dominate the longer light one", joined)
	}
}

// The graph is deterministic, so two surfaces rendering the same plan show the
// same sequence.
func TestGraphIsDeterministic(t *testing.T) {
	tasks := []plan.Task{
		task("zebra"), task("alpha"), task("middle", "alpha"),
		mutating("mutate", []string{"a.go"}, "zebra"),
	}
	first := buildGraph(t, tasks...)
	for i := 0; i < 10; i++ {
		next := buildGraph(t, tasks...)
		if strings.Join(next.Order, ",") != strings.Join(first.Order, ",") {
			t.Fatal("repeated builds produced different orderings")
		}
		if next.Digest != first.Digest {
			t.Fatal("repeated builds produced different digests")
		}
	}

	// A structural change changes the digest, so a plan cannot silently
	// describe tasks that have since changed.
	changed := append([]plan.Task(nil), tasks...)
	changed[0].Paths = []string{"new.go"}
	if buildGraph(t, changed...).Digest == first.Digest {
		t.Fatal("changing a task did not change the graph digest")
	}
}

// Every task reaches the order exactly once, so nothing is silently dropped.
func TestEveryTaskIsScheduledExactlyOnce(t *testing.T) {
	tasks := []plan.Task{
		task("a"), task("b", "a"), task("c", "a"),
		mutating("d", []string{"x.go"}, "b"),
		mutating("e", []string{"x.go"}, "b"),
		task("f", "c", "d", "e"),
	}
	graph := buildGraph(t, tasks...)

	seen := map[string]int{}
	for _, id := range graph.Order {
		seen[id]++
	}
	if len(seen) != len(tasks) {
		t.Fatalf("the order holds %d distinct tasks, want %d: %v", len(seen), len(tasks), graph.Order)
	}
	for id, count := range seen {
		if count != 1 {
			t.Fatalf("task %s appears %d times in the order", id, count)
		}
	}

	// Dependencies still precede dependents after conflict splitting.
	position := map[string]int{}
	for i, id := range graph.Order {
		position[id] = i
	}
	for _, task := range tasks {
		for _, dependency := range task.DependsOn {
			if position[dependency] >= position[task.ID] {
				t.Fatalf("%s runs before its dependency %s", task.ID, dependency)
			}
		}
	}
}

package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// This file builds the plan-time task graph.
//
// It is deliberately separate from internal/dag, which is the *runtime* graph:
// its nodes carry live status and its mutations are authorized and persisted,
// because it models work that is happening. Planning reasons about work that
// has not started, and creating live nodes to do that would cross the
// execution boundary Process 04 must not cross.
//
// So this graph is in-memory and produces an ordering, a critical path and a
// set of conflicts. Process 05 is what turns it into runtime nodes.

// Task is one unit of planned work.
type Task struct {
	ID string `json:"id"`
	// Title is what a user reads.
	Title string `json:"title"`
	// Criteria are the Goal acceptance criteria this task serves. A task that
	// serves none is either scope the user did not ask for or a criterion
	// nobody is covering, and both are worth surfacing.
	Criteria []string `json:"criteria,omitempty"`
	// DependsOn are tasks that must finish first.
	DependsOn []string `json:"depends_on,omitempty"`
	// Paths are the files or directories this task expects to change. They
	// drive collision detection, so two tasks touching the same file are not
	// scheduled in parallel.
	Paths []string `json:"paths,omitempty"`
	// Role is the role required to carry it out.
	Role string `json:"role,omitempty"`
	// Mutating reports whether the task changes anything. Read-only tasks
	// never collide with each other, however much they overlap.
	Mutating bool `json:"mutating"`
	// Weight is a relative effort estimate used only to find the longest
	// dependency chain. It is not a duration and is not shown as one.
	Weight int `json:"weight"`
	// RequiresApproval marks a task whose effects need a human decision.
	RequiresApproval bool `json:"requires_approval,omitempty"`
	// Checkpoint marks a task after which state should be recoverable.
	Checkpoint bool `json:"checkpoint,omitempty"`
}

// ConflictKind classifies why two tasks cannot run together.
type ConflictKind string

const (
	// ConflictSharedPath means both tasks change the same file. Running them
	// in parallel would make the result depend on ordering nobody specified.
	ConflictSharedPath ConflictKind = "SHARED_PATH"
	// ConflictSelfReview means a task and its verifier share a role, so the
	// work would be checked by whoever produced it.
	ConflictSelfReview ConflictKind = "SELF_REVIEW"
)

// Conflict is a pair of tasks that must not run concurrently.
type Conflict struct {
	Kind ConflictKind `json:"kind"`
	A    string       `json:"a"`
	B    string       `json:"b"`
	// Detail is user-safe and names the actual cause.
	Detail string `json:"detail"`
}

// Graph is the resolved plan-time task graph.
type Graph struct {
	// Order is a deterministic topological ordering. Determinism matters:
	// two surfaces rendering the same plan must show the same sequence.
	Order []string `json:"order"`
	// Stages group tasks that may run at the same time. Membership already
	// accounts for conflicts, so a stage is genuinely parallel-safe.
	Stages [][]string `json:"stages"`
	// CriticalPath is the longest dependency chain by weight. It is the part
	// of the plan whose length sets the whole plan's length.
	CriticalPath []string `json:"critical_path"`
	// Conflicts are pairs that cannot run concurrently.
	Conflicts []Conflict `json:"conflicts,omitempty"`
	// Digest fingerprints the graph, so a plan can be shown to describe the
	// tasks it was built from.
	Digest string `json:"digest"`
}

// BuildGraph resolves tasks into an ordering, stages and a critical path.
//
// It refuses rather than repairs. A cycle, a dangling dependency or a
// duplicate ID means the caller's model of the work is wrong, and silently
// dropping an edge to make the graph resolvable would hide that.
func BuildGraph(tasks []Task) (Graph, error) {
	if len(tasks) == 0 {
		return Graph{}, fmt.Errorf("%w: a plan needs at least one task", ErrPlanInvalid)
	}

	byID := make(map[string]Task, len(tasks))
	var ids []string
	for _, task := range tasks {
		if strings.TrimSpace(task.ID) == "" {
			return Graph{}, fmt.Errorf("%w: a task has no identifier", ErrPlanInvalid)
		}
		if _, duplicate := byID[task.ID]; duplicate {
			return Graph{}, fmt.Errorf("%w: task %s is defined twice", ErrPlanInvalid, task.ID)
		}
		byID[task.ID] = task
		ids = append(ids, task.ID)
	}
	sort.Strings(ids)

	// A dependency on a task that does not exist means the plan references
	// work nobody planned.
	for _, id := range ids {
		for _, dependency := range byID[id].DependsOn {
			if _, exists := byID[dependency]; !exists {
				return Graph{}, fmt.Errorf("%w: task %s depends on %s, which is not in the plan",
					ErrPlanInvalid, id, dependency)
			}
			if dependency == id {
				return Graph{}, fmt.Errorf("%w: task %s depends on itself", ErrPlanInvalid, id)
			}
		}
	}

	graph := Graph{}
	stages, err := layer(ids, byID)
	if err != nil {
		return Graph{}, err
	}
	graph.Conflicts = detectConflicts(ids, byID)
	graph.Stages = splitConflictingStages(stages, graph.Conflicts)
	for _, stage := range graph.Stages {
		graph.Order = append(graph.Order, stage...)
	}
	graph.CriticalPath = criticalPath(ids, byID)
	graph.Digest = graphDigest(ids, byID)
	return graph, nil
}

// layer assigns tasks to dependency levels, which is both the topological
// order and the natural parallel grouping.
//
// Kahn's algorithm detects a cycle by construction: if any task is still
// waiting when nothing can be scheduled, the remainder contains one.
func layer(ids []string, byID map[string]Task) ([][]string, error) {
	remaining := make(map[string]int, len(ids))
	dependents := make(map[string][]string, len(ids))
	for _, id := range ids {
		remaining[id] = len(byID[id].DependsOn)
		for _, dependency := range byID[id].DependsOn {
			dependents[dependency] = append(dependents[dependency], id)
		}
	}

	var stages [][]string
	placed := 0
	for placed < len(ids) {
		var stage []string
		for _, id := range ids {
			if count, waiting := remaining[id]; waiting && count == 0 {
				stage = append(stage, id)
			}
		}
		if len(stage) == 0 {
			// Nothing can be scheduled and work remains: the rest is a cycle.
			var stuck []string
			for id := range remaining {
				stuck = append(stuck, id)
			}
			sort.Strings(stuck)
			return nil, fmt.Errorf("%w: these tasks depend on each other in a loop: %s",
				ErrPlanInvalid, strings.Join(stuck, ", "))
		}
		sort.Strings(stage)
		for _, id := range stage {
			delete(remaining, id)
			placed++
		}
		for _, id := range stage {
			for _, dependent := range dependents[id] {
				if _, waiting := remaining[dependent]; waiting {
					remaining[dependent]--
				}
			}
		}
		stages = append(stages, stage)
	}
	return stages, nil
}

// detectConflicts finds pairs that must not run concurrently.
func detectConflicts(ids []string, byID map[string]Task) []Conflict {
	var conflicts []Conflict
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			a, b := byID[ids[i]], byID[ids[j]]
			// Two read-only tasks cannot interfere however much they overlap,
			// so reporting them would be noise.
			if !a.Mutating && !b.Mutating {
				continue
			}
			if shared := sharedPaths(a.Paths, b.Paths); len(shared) > 0 {
				conflicts = append(conflicts, Conflict{
					Kind: ConflictSharedPath, A: a.ID, B: b.ID,
					Detail: "Both change " + strings.Join(shared, ", ") + ".",
				})
			}
		}
	}
	sort.SliceStable(conflicts, func(x, y int) bool {
		if conflicts[x].A != conflicts[y].A {
			return conflicts[x].A < conflicts[y].A
		}
		return conflicts[x].B < conflicts[y].B
	})
	return conflicts
}

// sharedPaths returns paths both tasks touch, treating a directory as
// covering everything beneath it.
func sharedPaths(left, right []string) []string {
	var shared []string
	for _, a := range left {
		for _, b := range right {
			if pathsOverlap(a, b) {
				shared = append(shared, a)
				break
			}
		}
	}
	sort.Strings(shared)
	return shared
}

// pathsOverlap reports whether two paths can affect each other. It compares
// segments rather than string prefixes, so "internal/apiv2" is not treated as
// being inside "internal/api".
func pathsOverlap(a, b string) bool {
	a, b = strings.Trim(a, "/"), strings.Trim(b, "/")
	if a == b {
		return true
	}
	return strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// splitConflictingStages separates tasks that a dependency layer would have
// run together but which conflict.
//
// The split preserves ordering: conflicting tasks end up in consecutive
// stages rather than being dropped or reordered arbitrarily, so the plan still
// does everything it said it would, just not simultaneously.
func splitConflictingStages(stages [][]string, conflicts []Conflict) [][]string {
	if len(conflicts) == 0 {
		return stages
	}
	conflicting := make(map[string]map[string]bool)
	for _, conflict := range conflicts {
		if conflicting[conflict.A] == nil {
			conflicting[conflict.A] = map[string]bool{}
		}
		if conflicting[conflict.B] == nil {
			conflicting[conflict.B] = map[string]bool{}
		}
		conflicting[conflict.A][conflict.B] = true
		conflicting[conflict.B][conflict.A] = true
	}

	var split [][]string
	for _, stage := range stages {
		remaining := append([]string(nil), stage...)
		for len(remaining) > 0 {
			var group, deferred []string
			for _, candidate := range remaining {
				collides := false
				for _, chosen := range group {
					if conflicting[candidate][chosen] {
						collides = true
						break
					}
				}
				if collides {
					deferred = append(deferred, candidate)
				} else {
					group = append(group, candidate)
				}
			}
			split = append(split, group)
			remaining = deferred
		}
	}
	return split
}

// criticalPath returns the longest dependency chain by weight.
//
// It is the chain that sets the plan's length: shortening anything off it
// changes nothing, which is worth showing a user deciding where to cut scope.
func criticalPath(ids []string, byID map[string]Task) []string {
	longest := make(map[string]int, len(ids))
	previous := make(map[string]string, len(ids))

	// ids is sorted and layer() already proved the graph acyclic, so a
	// fixpoint pass converges; iterating by dependency depth keeps it linear
	// in practice.
	for changed := true; changed; {
		changed = false
		for _, id := range ids {
			task := byID[id]
			weight := task.Weight
			if weight <= 0 {
				weight = 1
			}
			best, from := 0, ""
			for _, dependency := range task.DependsOn {
				if longest[dependency] > best {
					best, from = longest[dependency], dependency
				}
			}
			if total := best + weight; total > longest[id] {
				longest[id] = total
				previous[id] = from
				changed = true
			}
		}
	}

	end, highest := "", 0
	for _, id := range ids {
		if longest[id] > highest {
			end, highest = id, longest[id]
		}
	}
	var path []string
	for current := end; current != ""; current = previous[current] {
		path = append([]string{current}, path...)
	}
	return path
}

// graphDigest fingerprints the graph's structure, so a plan can be shown to
// describe the tasks it was built from rather than tasks that changed since.
func graphDigest(ids []string, byID map[string]Task) string {
	h := sha256.New()
	h.Write([]byte("marshal.plan.graph.v1\x00"))
	for _, id := range ids {
		task := byID[id]
		dependencies := append([]string(nil), task.DependsOn...)
		sort.Strings(dependencies)
		paths := append([]string(nil), task.Paths...)
		sort.Strings(paths)
		fmt.Fprintf(h, "%s|%s|%s|%s|%t|%d\x00",
			task.ID, task.Role, strings.Join(dependencies, ","),
			strings.Join(paths, ","), task.Mutating, task.Weight)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)[:16])
}

// sortStrings is a local helper so validation.go need not import sort.
func sortStrings(values []string) { sort.Strings(values) }

// digest hashes a string for the constraint fingerprint.
func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:16])
}

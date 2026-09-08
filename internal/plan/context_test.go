package plan_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/plan"
)

// secretish builds credential-shaped text at runtime, so the repository never
// contains a literal that a scanner would flag.
func secretish() string {
	return "AKIA" + strings.Repeat("A", 16)
}

func packagedPlan(t *testing.T, tasks []plan.Task) plan.ExecutionPlan {
	t.Helper()
	goal := confirmedGoal()
	goal.DoNotDo = []string{"Do not touch the payments module"}
	return buildPlan(t, plan.BuildRequest{
		Goal: goal, ProjectID: testProject,
		Assessment: routineAssessment(), Tasks: tasks,
		Candidates: governedCandidates(),
	})
}

func packagesFor(t *testing.T, tasks []plan.Task) map[string]plan.ContextPackage {
	t.Helper()
	built := plan.BuildContextPackages(plan.PackageRequest{Plan: packagedPlan(t, tasks)})
	byTask := map[string]plan.ContextPackage{}
	for _, contextPackage := range built {
		byTask[contextPackage.TaskID] = contextPackage
	}
	return byTask
}

// Hard constraints are restated in every package. A constraint that travels by
// reference is one a failed lookup can silently drop.
func TestEveryPackageRestatesHardConstraints(t *testing.T) {
	packages := packagesFor(t, coveringTasks())
	if len(packages) == 0 {
		t.Fatal("no packages were built")
	}
	for id, contextPackage := range packages {
		joined := strings.Join(contextPackage.HardConstraints, " ")
		if !strings.Contains(joined, "database schema") {
			t.Fatalf("package %s does not restate the hard constraint: %v", id, contextPackage.HardConstraints)
		}
		if len(contextPackage.DoNotDo) == 0 {
			t.Fatalf("package %s carries no prohibitions", id)
		}
		// The Goal is referenced by revision, so the worker is bound to the
		// exact revision rather than to a copy of its text.
		if contextPackage.GoalRef.Revision != confirmedGoal().Revision {
			t.Fatalf("package %s is not bound to the goal revision", id)
		}
	}
}

// A task gets the files it names and nothing more.
func TestPackagesCarryOnlyTheFilesTheTaskNames(t *testing.T) {
	tasks := []plan.Task{
		{ID: "api", Title: "change the api", Mutating: true, Weight: 1,
			Paths: []string{"internal/api"}, Criteria: []string{"responses are cached"}},
		{ID: "docs", Title: "update the docs", Mutating: true, Weight: 1,
			Paths: []string{"docs"}, Criteria: []string{"existing tests pass"}},
	}
	packages := packagesFor(t, tasks)

	apiRefs := refStrings(packages["api"].Files)
	if len(apiRefs) != 1 || apiRefs[0] != "internal/api" {
		t.Fatalf("the api task received %v", apiRefs)
	}
	for _, ref := range apiRefs {
		if strings.HasPrefix(ref, "docs") {
			t.Fatal("a task received another task's files")
		}
	}
	// Every reference says why it is there, which is what makes an over-broad
	// package visible on inspection.
	for _, ref := range packages["api"].Files {
		if strings.TrimSpace(ref.Reason) == "" {
			t.Fatalf("file %s was included with no reason", ref.Ref)
		}
	}
}

// Only fresh, project-bound, task-relevant memory travels, and every exclusion
// is recorded so a sparse package reads as a decision.
func TestMemoryIsFilteredToFreshProjectBoundAndRelevant(t *testing.T) {
	tasks := coveringTasks()
	built := plan.BuildContextPackages(plan.PackageRequest{
		Plan: packagedPlan(t, tasks),
		MemoryCandidates: []plan.MemoryCandidate{
			{ID: "M1", Content: "the cache uses redis", Fresh: true, ProjectBound: true,
				Tasks: []string{"implement"}},
			{ID: "M2", Content: "an older design", Fresh: false, ProjectBound: true,
				Tasks: []string{"implement"}},
			{ID: "M3", Content: "another project's note", Fresh: true, ProjectBound: false,
				Tasks: []string{"implement"}},
			{ID: "M4", Content: "unrelated to any task", Fresh: true, ProjectBound: true,
				Tasks: []string{"nonexistent"}},
		},
	})

	var implement plan.ContextPackage
	for _, contextPackage := range built {
		if contextPackage.TaskID == "implement" {
			implement = contextPackage
		}
	}

	included := refStrings(implement.Memory)
	if len(included) != 1 || included[0] != "M1" {
		t.Fatalf("memory included %v, want only the fresh project-bound relevant note", included)
	}
	excluded := strings.Join(implement.Excluded, " ")
	if !strings.Contains(excluded, "M2") || !strings.Contains(excluded, "M3") {
		t.Fatalf("exclusions were not recorded: %v", implement.Excluded)
	}
	// Memory relevant to no task appears nowhere, and is not noise in the
	// exclusion list of every package either.
	if strings.Contains(excluded, "M4") {
		t.Fatal("memory irrelevant to this task was listed as excluded from it")
	}
}

// Credential material never reaches a package, wherever it came from.
func TestSecretsAreFilteredOutOfPackages(t *testing.T) {
	credential := secretish()
	tasks := []plan.Task{
		{ID: "implement", Title: "use the key " + credential, Mutating: true, Weight: 1,
			Criteria: []string{"responses are cached"}},
		{ID: "test", Title: "run the tests", Weight: 1, DependsOn: []string{"implement"},
			Criteria: []string{"existing tests pass"}},
	}
	built := plan.BuildContextPackages(plan.PackageRequest{
		Plan: packagedPlan(t, tasks),
		MemoryCandidates: []plan.MemoryCandidate{
			{ID: "M1", Content: "the key is " + credential, Fresh: true, ProjectBound: true,
				Tasks: []string{"implement"}},
		},
	})

	// The whole package is serialised and swept, so a secret hiding in any
	// field is caught rather than only the ones we thought to check.
	for _, contextPackage := range built {
		encoded, err := json.Marshal(contextPackage)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if strings.Contains(string(encoded), credential) {
			t.Fatalf("package %s carries a credential", contextPackage.TaskID)
		}
		if contextPackage.TaskID == "implement" && contextPackage.Redactions == 0 {
			t.Fatal("credentials were removed without the redaction being counted")
		}
	}
}

// Memory content is redacted on its own, not only via the package sweep.
//
// The sweep covers the fields it walks; memory arrives as separate content and
// is filtered where it is read. Without a count from that path, a credential
// living only in memory content would be invisible — and "how many secrets did
// we find upstream" is the signal that tells an operator something is leaking
// into the memory store.
func TestMemoryContentRedactionIsCountedSeparately(t *testing.T) {
	credential := secretish()
	// The task itself is clean, so any redaction counted must come from memory.
	tasks := []plan.Task{
		{ID: "implement", Title: "add the cache", Mutating: true, Weight: 1,
			Criteria: []string{"responses are cached"}},
		{ID: "test", Title: "run the tests", Weight: 1, DependsOn: []string{"implement"},
			Criteria: []string{"existing tests pass"}},
	}
	built := plan.BuildContextPackages(plan.PackageRequest{
		Plan: packagedPlan(t, tasks),
		MemoryCandidates: []plan.MemoryCandidate{
			{ID: "M1", Content: "the deploy key is " + credential,
				Fresh: true, ProjectBound: true, Tasks: []string{"implement"}},
		},
	})

	for _, contextPackage := range built {
		if contextPackage.TaskID != "implement" {
			continue
		}
		if contextPackage.Redactions == 0 {
			t.Fatal("a credential in memory content was not counted as a redaction")
		}
		return
	}
	t.Fatal("no package was built for the task carrying the memory")
}

// Network access is denied unless the task asked for it: a capability nobody
// asked for is one nobody weighed the risk of.
func TestCapabilitiesAreLeastPrivilege(t *testing.T) {
	tasks := []plan.Task{
		{ID: "read", Title: "inspect the code", Weight: 1,
			Criteria: []string{"responses are cached"}},
		{ID: "fetch", Title: "fetch the upstream schema", Mutating: true, Weight: 1,
			NeedsNetwork: true, Criteria: []string{"existing tests pass"}},
	}
	packages := packagesFor(t, tasks)

	read := packages["read"]
	if contains(read.Capabilities, "write_project") {
		t.Fatal("a read-only task was granted write access")
	}
	if contains(read.Capabilities, "network") {
		t.Fatal("a task that did not ask for network access was granted it")
	}
	// Denial is stated rather than left as the complement of the allow-list.
	denied := strings.Join(read.DeniedScope, " ")
	if !strings.Contains(denied, "network") {
		t.Fatalf("the denial of network access is not stated: %v", read.DeniedScope)
	}
	if !strings.Contains(denied, "credential") && !strings.Contains(denied, "secret") {
		t.Fatalf("reading credentials is not explicitly denied: %v", read.DeniedScope)
	}

	fetch := packages["fetch"]
	if !contains(fetch.Capabilities, "network") {
		t.Fatal("a task that needs the network was not granted it")
	}
	if !contains(fetch.Capabilities, "write_project") {
		t.Fatal("a mutating task was not granted write access")
	}
}

// A worker is told what it depends on, and not the whole chain above it.
func TestPackagesCarryDirectDependencyOutputsOnly(t *testing.T) {
	tasks := []plan.Task{
		{ID: "a", Title: "a", Mutating: true, Weight: 1, Criteria: []string{"responses are cached"}},
		{ID: "b", Title: "b", Mutating: true, Weight: 1, DependsOn: []string{"a"}},
		{ID: "c", Title: "c", Weight: 1, DependsOn: []string{"b"}, Criteria: []string{"existing tests pass"}},
	}
	packages := packagesFor(t, tasks)

	if len(packages["c"].DependencyOutputs) != 1 || packages["c"].DependencyOutputs[0] != "b" {
		t.Fatalf("c received %v, want only its direct dependency", packages["c"].DependencyOutputs)
	}
	if len(packages["a"].DependencyOutputs) != 0 {
		t.Fatalf("a task with no dependencies received %v", packages["a"].DependencyOutputs)
	}
}

// Verification obligations travel, so a worker knows up front what its output
// will be judged against.
func TestPackagesCarryTheirVerificationObligations(t *testing.T) {
	packages := packagesFor(t, coveringTasks())
	implement := packages["implement"]
	if len(implement.Verification) == 0 {
		t.Fatal("a task covering a criterion carries no verification obligation")
	}
	for _, obligation := range implement.Verification {
		if strings.TrimSpace(obligation.Method) == "" {
			t.Fatal("an obligation travelled with no method")
		}
	}
}

// Approvals travel as requirements. Nothing in a package can express that an
// approval was granted.
func TestPackagesCarryApprovalsAsRequirementsOnly(t *testing.T) {
	tasks := []plan.Task{
		{ID: "drop", Title: "delete the old records", Mutating: true, Weight: 1,
			Criteria: []string{"responses are cached", "existing tests pass"}},
	}
	packages := packagesFor(t, tasks)

	drop := packages["drop"]
	if len(drop.Approvals) == 0 {
		t.Fatal("destructive work carried no approval requirement")
	}
	encoded, err := json.Marshal(drop.Approvals)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// The serialised form must not contain a field that could read as a grant.
	for _, forbidden := range []string{"granted", "approved_by", "approved_at", "pre_approved"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("an approval requirement carries %q, which could be read as a grant", forbidden)
		}
	}
}

func refStrings(refs []plan.ContextRef) []string {
	var out []string
	for _, ref := range refs {
		out = append(out, ref.Ref)
	}
	return out
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

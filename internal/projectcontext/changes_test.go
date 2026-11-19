package projectcontext

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestContractChangesWholeProject(t *testing.T) {
	base := Contract{ID: "project", Revision: 3, Components: []Component{
		{ID: "same", Responsibility: "Keep", Paths: []string{"a", "b"}, Dependencies: []string{"edit", "drop"}},
		{ID: "edit", Responsibility: "Before", Paths: []string{"edit"}},
		{ID: "drop", Responsibility: "Remove", Paths: []string{"drop"}},
	}}
	proposal := Contract{ID: base.ID, Revision: 4, Components: []Component{
		{ID: "same", Responsibility: "Keep", Paths: []string{"b", "a"}, Dependencies: []string{"edit", "add"}},
		{ID: "edit", Responsibility: "After", Paths: []string{"edit"}},
		{ID: "add", Responsibility: "New", Paths: []string{"add"}},
	}}
	before, _ := json.Marshal(base)
	changes := ContractChanges(base, proposal)
	if changes.BaseRevision != 3 {
		t.Fatal(changes.BaseRevision)
	}
	parts, edges := map[string]Change{}, map[string]Change{}
	for _, entry := range changes.Components {
		parts[entry.ID] = entry.Change
		if entry.ID == "edit" && (entry.Before == nil || entry.Before.Responsibility != "Before") {
			t.Fatal("changed component has no before value")
		}
	}
	for _, entry := range changes.Connections {
		edges[entry.ID()] = entry.Change
	}
	if !reflect.DeepEqual(parts, map[string]Change{"same": ChangeExisting, "edit": ChangeChanged, "add": ChangeNew, "drop": ChangeRemoved}) ||
		!reflect.DeepEqual(edges, map[string]Change{"same--edit": ChangeExisting, "same--add": ChangeNew, "same--drop": ChangeRemoved}) {
		t.Fatalf("parts %v, connections %v", parts, edges)
	}
	after, _ := json.Marshal(base)
	if string(before) != string(after) {
		t.Fatal("diff mutated the accepted contract")
	}
	for _, entry := range ContractChanges(Contract{}, proposal).Components {
		if entry.Change != ChangeNew {
			t.Fatal("baseline component is not new")
		}
	}
}

func TestContractRevisionRequiresExplicitRemoval(t *testing.T) {
	base := Contract{ID: "project", Revision: 1, Components: []Component{{ID: "core"}, {ID: "old"}}}
	proposal := Contract{ID: base.ID, Revision: 2, Components: []Component{{ID: "core"}}}
	if err := ValidateContractRevision(base, proposal); err == nil || !strings.Contains(err.Error(), "old") || !strings.Contains(err.Error(), "removed:true") {
		t.Fatalf("silent removal: %v", err)
	}
	proposal.Components = append(proposal.Components, Component{ID: "old", Removed: true})
	if err := ValidateContractRevision(base, proposal); err != nil {
		t.Fatal(err)
	}
	changes := ContractChanges(base, proposal)
	if changes.Components[1].Change != ChangeRemoved {
		t.Fatal(changes)
	}
	proposal.ID = "goal-fragment"
	if err := ValidateContractRevision(base, proposal); err == nil {
		t.Fatal("a fresh contract replaced the project")
	}
}

func TestComponentChangesPathsAndParent(t *testing.T) {
	base := Contract{Components: []Component{{ID: "part", Responsibility: "Own it", Paths: []string{"old"}}}}
	for _, part := range []Component{
		{ID: "part", Responsibility: "Own it", Paths: []string{"new"}},
		{ID: "part", Responsibility: "Own it", Paths: []string{"old"}, Parent: "parent"},
	} {
		if got := ContractChanges(base, Contract{Components: []Component{part}}); got.Components[0].Change != ChangeChanged {
			t.Fatal(got)
		}
	}
}

func TestRemovedParentRequiresMovingOrRemovingItsChildren(t *testing.T) {
	base := Contract{ID: "project", Revision: 1, Components: []Component{{ID: "parent"}, {ID: "child", Parent: "parent"}}}
	next := base
	next.Revision++
	next.Components = []Component{{ID: "parent", Removed: true}, {ID: "child", Parent: "parent"}}
	if err := ValidateContractRevision(base, next); err == nil {
		t.Fatal("an active child was left inside a removed parent")
	}
	next.Components[1].Parent = ""
	if err := ValidateContractRevision(base, next); err != nil {
		t.Fatal(err)
	}
}

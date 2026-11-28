//go:build governed

package app

import "testing"

func TestArchitectureWorkspaceReportsEditionAvailability(t *testing.T) {
	var r *Runtime
	snapshot, err := r.ArchitectureWorkspace(t.Context())
	if err != nil || snapshot.Notice == "" || len(snapshot.Layouts) != 0 {
		t.Fatalf("availability: %#v, %v", snapshot, err)
	}
}

//go:build governed

package workerenv

import (
	"slices"
	"testing"
)

// The governed edition's worker environment is left as it was.
func TestHostUnchangedInGovernedEdition(t *testing.T) {
	if HostFiltered {
		t.Fatal("the governed edition must not filter here")
	}
	if got := Host(plantedEnv); !slices.Equal(got, plantedEnv) {
		t.Fatalf("Host changed the governed environment: %v", got)
	}
	if Host(nil) != nil {
		t.Fatal("Host(nil) must stay nil in the governed edition")
	}
}

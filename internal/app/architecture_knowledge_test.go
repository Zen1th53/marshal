package app

import (
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestCommunityArchitectureReceiverColdReadDoesNotMigrateOrEnroll(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	r, err := Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := r.ArchitectureAcceptance(t.Context()); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("cold unregistered architecture was not denied: %v", err)
	}
}

//go:build !governed

package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
)

// marshalDecidesWork routes reads and network through the Marshal decision
// protocol for one test, and keeps an unanswered read waiting for the Marshal
// (re-delivery) instead of applying it. Production applies them automatically;
// the protocol itself (delivery, deadlines, authenticated answers) still
// serves memory and marshal-command requests, and these tests use reads as its
// vehicle.
func marshalDecidesWork(t *testing.T) {
	t.Helper()
	marshalDecidesReads(t)
	saved := extremeUnansweredAppliesKinds
	extremeUnansweredAppliesKinds = map[string]bool{}
	t.Cleanup(func() { extremeUnansweredAppliesKinds = saved })
}

// marshalDecidesReads routes reads and network through a Marshal decision but
// keeps the production handling of an unanswered one.
func marshalDecidesReads(t *testing.T) {
	t.Helper()
	saved := automaticWorkKinds
	automaticWorkKinds = map[string]bool{}
	t.Cleanup(func() { automaticWorkKinds = saved })
}

// explicitReadGrant reports whether a recorded read decision allows folder.
// HasReadGrant also allows undecided folders in this edition.
func explicitReadGrant(rt *app.Runtime, folder string) bool {
	resolved, err := filepath.EvalSymlinks(folder)
	if err != nil {
		return false
	}
	grants, err := rt.OperatorReadGrants(context.Background())
	if err != nil {
		return false
	}
	allowed, specificity := false, -1
	for _, grant := range grants {
		rel, err := filepath.Rel(grant.Folder, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			continue
		}
		if len(grant.Folder) > specificity {
			allowed, specificity = grant.Allowed, len(grant.Folder)
		}
	}
	return allowed
}

package plan_test

import (
	"strings"
	"testing"
)

// Instructions are approved scope: a scoped amendment that rewrites them
// must go back to the person as a revision.
func TestScopedAmendmentMayNotRewriteInstructions(t *testing.T) {
	p := amendFixture(t)
	next := splitFixture(p)
	next.Tasks[0].Instructions = "use a different cache"
	if _, err := p.AmendScoped(p.Version, "change approach", next); err == nil || !strings.Contains(err.Error(), "instructions") {
		t.Fatalf("instruction rewrite kept approval: %v", err)
	}
}

//go:build !governed

package tui

import (
	"github.com/Zen1th53/marshal/internal/permission"
	"testing"
)

func TestGuaranteeConfirmationAlwaysGoesToOwner(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	req := permission.Request{Kind: "marshal-command", Object: "/marshal approve", Who: "Marshal", ProposalID: "runtime:approve", OwnerConfirmation: true}
	if w.routeBuildPermission(t.Context(), req) {
		t.Fatal("guarantee card was routed to the Marshal")
	}
	if marshalSelfApprovedPlan(req, true, nil) {
		t.Fatal("owner confirmation was labelled automatic")
	}
}

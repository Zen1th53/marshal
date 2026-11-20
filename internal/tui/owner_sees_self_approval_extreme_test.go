//go:build !governed

package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/store"
)

// Only the Marshal's own refused architecture acceptance and its own plan
// approval are told to the owner this way; nothing else is.
func TestMarshalSelfDecisionKinds(t *testing.T) {
	contract := permission.Request{Kind: "project-contract", Object: "ARCH-self"}
	refused := errors.Join(store.ErrProjectContractInstructionMissing, errors.New("journal settled"))
	if !marshalSelfAcceptRefused(contract, true, refused) {
		t.Fatal("a refused self-acceptance was not recognised")
	}
	for name, ok := range map[string]bool{
		"denied":      marshalSelfAcceptRefused(contract, false, refused),
		"applied":     marshalSelfAcceptRefused(contract, true, nil),
		"other error": marshalSelfAcceptRefused(contract, true, errors.New("conflict")),
		"other kind":  marshalSelfAcceptRefused(permission.Request{Kind: "memory"}, true, refused),
	} {
		if ok {
			t.Errorf("%s read as a refused self-acceptance", name)
		}
	}
	plan := permission.Request{Kind: "marshal-command", Object: "/marshal approve", RunID: "RUN-1"}
	if !marshalSelfApprovedPlan(plan, true, nil) {
		t.Fatal("the Marshal's plan approval was not recognised")
	}
	for name, ok := range map[string]bool{
		"failed":  marshalSelfApprovedPlan(plan, true, errors.New("expired")),
		"denied":  marshalSelfApprovedPlan(plan, false, nil),
		"task":    marshalSelfApprovedPlan(permission.Request{Kind: "marshal-command", Object: "/marshal approve-task t1"}, true, nil),
		"amend":   marshalSelfApprovedPlan(permission.Request{Kind: "marshal-command", Object: "/marshal amend approve"}, true, nil),
		"command": marshalSelfApprovedPlan(permission.Request{Kind: "read", Object: "/marshal approve"}, true, nil),
	} {
		if ok {
			t.Errorf("%s read as a self-approved plan", name)
		}
	}
}

// The refused attempt shows in the inbox under the architecture gate while
// that revision waits, and in Activity once; a new revision has no warning.
func TestRefusedSelfAcceptanceShowsInTheInbox(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	c := walkContract()
	w.gateContractSource = func(context.Context) (*gateContract, error) { return c, nil }
	w.refreshGates(t.Context())
	w.noteRefusedArchitecture(c.ID, 0)

	text := frameText(w, 200, 40)
	if !strings.Contains(text, "architecture · waits for you in the browser") || !strings.Contains(text, selfAcceptRefusedNote) {
		t.Fatalf("the inbox does not say the Marshal tried to accept:\n%s", text)
	}
	if n := w.noteList(); len(n) == 0 || n[0].Text != selfAcceptRefusedNote || n[0].Err {
		t.Fatalf("Activity misses the refused attempt: %+v", n)
	}

	next := *c
	next.Revision, next.Digest = 2, "sha256:next"
	w.gateContractSource = func(context.Context) (*gateContract, error) { return &next, nil }
	w.refreshGates(t.Context())
	for _, g := range w.ownerGates(nil) {
		if g.warning != "" {
			t.Fatalf("a new revision still carries the old warning: %+v", g)
		}
	}
}

// A plan the Marshal approved without the owner is said once per plan.
func TestSelfApprovedPlanSaidOnce(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	w.noteSelfApprovedPlan("RUN-1/plan:1")
	w.noteSelfApprovedPlan("RUN-1/plan:1")
	count := func() int {
		n := 0
		for _, note := range w.noteList() {
			if note.Text == selfApprovedPlanNote {
				n++
			}
		}
		return n
	}
	if count() != 1 {
		t.Fatalf("the self-approved plan was said %d times: %+v", count(), w.noteList())
	}
	w.RecordActivity("Run RUN-1 finished.")
	w.noteSelfApprovedPlan("RUN-2/plan:1")
	if count() != 2 {
		t.Fatalf("a second plan was not said: %+v", w.noteList())
	}
}

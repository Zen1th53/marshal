//go:build linux

package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
)

// memoryVerifySpecIDs are the memory/verification actions whose typed forms this
// qualification drives. They are B's bindings; I installs them into the
// manifest and ControlSource before these forms can render on the real binary.
func memoryVerifyGaps(t *testing.T) []string {
	t.Helper()
	ia := testView(t).nav.ia
	gaps := map[string]bool{}
	for _, g := range ia.Gaps() {
		gaps[g.SpecID] = true
	}
	var missing []string
	for _, id := range []string{"CTUI-0450", "CTUI-0352", "CTUI-0071"} {
		if gaps[id] {
			missing = append(missing, id)
		}
	}
	return missing
}

// memoryMenuPTYAction drives Ctrl+K → destination palette → the named action →
// open its closed typed form. It mirrors goalMenuPTYAction for B's surfaces.
func memoryMenuPTYAction(t *testing.T, s *ptySession, title string) {
	t.Helper()
	s.send("\x0b") // Ctrl+K opens the navigation destination palette.
	s.send(title)
	s.send("\r") // Navigate to the action bar; this cannot submit.
	s.send("\r") // Open the closed typed form.
}

// TestPTYMemoryVerificationMenuForms proves the memory and verification typed
// forms render over a real terminal through the navigation palette, not just as
// in-process binding functions.
func TestPTYMemoryVerificationMenuForms(t *testing.T) {
	if !editionExtreme {
		t.Skip("candidate menu qualification requires (default build)")
	}
	if gaps := memoryVerifyGaps(t); len(gaps) > 0 {
		t.Skipf("memory/verification actions not yet bound (unmet qualification): %s", strings.Join(gaps, ", "))
	}
	openPTY(t)
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	project := initProject(t, bin)
	const session = "SESSION-memory-verify-menu"
	terminal := startFrozenTUIInProject(t, 40, 160, bin, project, "tui", session)
	terminal.mustSee("Marshal")

	terminal.send("\x0e") // Ctrl+N opens navigation in the Marshal.
	memoryMenuPTYAction(t, terminal, "Evidence-gated promotion")
	terminal.mustSee("Exact memory ID")

	terminal.send("\x1b")
	memoryMenuPTYAction(t, terminal, "Challenge with counter-evidence")
	terminal.mustSee("Exact verification session ID")
}

// TestPTYMemoryRollbackSelectionAndCancel proves the destructive memory rollback
// opens on Cancel, a bare Enter mutates nothing, and only an explicit
// acknowledgement followed by Proceed restores the selected snapshot.
func TestPTYMemoryRollbackSelectionAndCancel(t *testing.T) {
	if !editionExtreme {
		t.Skip("candidate menu qualification requires (default build)")
	}
	if gaps := memoryVerifyGaps(t); len(gaps) > 0 {
		t.Skipf("memory rollback not yet bound (unmet qualification): %s", strings.Join(gaps, ", "))
	}
	openPTY(t)
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	project := initProject(t, bin)
	const session = "SESSION-memory-rollback-menu"

	projectID, snapshotID, recordID := seedMemoryRollback(t, project, session)

	terminal := startFrozenTUIInProject(t, 40, 160, bin, project, "tui", session)
	terminal.mustSee("Marshal")
	terminal.send("\x0e")

	// First attempt: open the form, then a bare Enter on Cancel must not restore.
	memoryMenuPTYAction(t, terminal, "Roll back memory state")
	terminal.mustSee("Exact durable memory snapshot ID")
	terminal.send(snapshotID + "\r")
	terminal.mustSee("Restore rationale")
	terminal.send("restore reviewed historical fact\r")
	terminal.mustSee("Proceed")
	terminal.send("\r") // confirmation opens on Cancel; Enter is a held key, not a decision
	if got := readMemoryRollbackBody(t, project, projectID, recordID); got != "later fact" {
		t.Fatalf("default Cancel mutated the record: body %q", got)
	}

	// Dismiss, re-open, and explicitly acknowledge before Proceed.
	terminal.send("\x1b")
	memoryMenuPTYAction(t, terminal, "Roll back memory state")
	terminal.mustSee("Exact durable memory snapshot ID")
	terminal.send(snapshotID + "\r")
	terminal.mustSee("Restore rationale")
	terminal.send("restore reviewed historical fact\r")
	terminal.mustSee("Proceed")
	terminal.send("y\x1b[C\r")
	terminal.mustSee("Restored 1 records as candidates")

	if got := readMemoryRollbackBody(t, project, projectID, recordID); got != "historical fact" {
		t.Fatalf("acknowledged rollback did not restore: body %q", got)
	}
}

func seedMemoryRollback(t *testing.T, project, session string) (projectID, snapshotID, recordID string) {
	t.Helper()
	ctx := context.Background()
	r, err := app.Open(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	local, err := r.OpenLocalControl(ctx)
	if err != nil {
		r.Close()
		t.Fatal(err)
	}
	opCtx := local.Context(ctx)
	p, ok := auth.LocalFromContext(opCtx)
	if !ok {
		r.Close()
		t.Fatal("local operator unavailable")
	}
	principal := authz.Principal{ID: p.ID(), Role: authz.Role{Name: "orchestrator", Authorities: []authz.Authority{authz.AuthorityPolicyAdmin}}}
	rec, err := r.Memory().Remember(opCtx, principal, app.RememberRequest{
		ProjectID: r.ProjectID(),
		Title:     "before restore",
		Body:      "historical fact",
		ScopeID:   r.ProjectID(),
		Kind:      model.MemoryKindSemantic,
	})
	if err != nil {
		r.Close()
		t.Fatal(err)
	}
	snap, err := r.CreateMemorySnapshot(opCtx, app.CommandEnvelope{
		ProjectID: r.ProjectIdentity(), SessionID: session,
		TargetID: "SNAP-memory-rollback", IdempotencyKey: "snapshot-once",
	}, "before", []string{rec.ID})
	if err != nil {
		r.Close()
		t.Fatal(err)
	}
	if _, err = r.Store().UpdateMemory(opCtx, rec.ProjectID, rec.ID, rec.Revision, func(m *model.MemoryRecordV2) error {
		m.Body = "later fact"
		return nil
	}); err != nil {
		r.Close()
		t.Fatal(err)
	}
	projectID = rec.ProjectID
	snapshotID = snap.ID
	recordID = rec.ID
	if err = r.Close(); err != nil {
		t.Fatal(err)
	}
	return projectID, snapshotID, recordID
}

func readMemoryRollbackBody(t *testing.T, project, projectID, recordID string) string {
	t.Helper()
	r, err := app.Open(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	rec, err := r.Store().GetMemoryV2(context.Background(), projectID, recordID)
	if err != nil {
		t.Fatal(err)
	}
	return rec.Body
}

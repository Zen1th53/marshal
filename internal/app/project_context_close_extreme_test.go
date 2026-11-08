//go:build !governed

package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

func requiredCloseHostFixture(t *testing.T, complete bool) (*Runtime, context.Context, *MarshalService, *requiredIntegrationSource, *marshal.LifecycleOperation) {
	t.Helper()
	r, ctx, cli, envelope, id, _ := completionRuntimeFixture(t, "complete")
	session, err := r.store.GetVerificationSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if complete {
		if _, err = r.Verification().completeRequiredContextAttestationWithCLI(ctx, session, envelope, "host-only-close-fixture", cli); err != nil {
			t.Fatal(err)
		}
	}
	r.requiredIntegrationSources.mu.Lock()
	source := r.requiredIntegrationSources.byRun[session.Binding.RunID]
	r.requiredIntegrationSources.mu.Unlock()
	service := r.Marshal()
	next := source.marshalRun
	next.State = marshal.Closed
	op := &marshal.LifecycleOperation{Kind: "close", Before: source.marshalRun.BaseCommit, After: source.head, Target: source.marshalRun.TargetRef, Next: &next, Event: events.Event{Type: events.EventTypeMarshalRunClosed}}
	revision, err := service.beginOperation(ctx, source.marshalRunID, source.marshalRun, source.marshalRevision, op)
	if err != nil {
		t.Fatal(err)
	}
	op.Authorized = true
	pending := source.marshalRun
	pending.Operation = op
	if err = service.save(ctx, source.marshalRunID, pending, revision); err != nil {
		t.Fatal(err)
	}
	return r, ctx, service, source, op
}

func TestRequiredCloseEffectActualHostTargetMovesOnlyWithLiveCurrentCompletion(t *testing.T) {
	r, ctx, service, source, op := requiredCloseHostFixture(t, true)
	called := 0
	effect := func() error {
		called++
		_, err := gitMarshal(ctx, r.ProjectRoot(), "merge", "--ff-only", "--no-edit", source.head)
		return err
	}
	if err := r.withRequiredContextCloseEffect(ctx, service, source.marshalRunID, source.marshalRun, op, effect); err != nil {
		t.Fatal("actual completed source could not move host fixture target", err)
	}
	after, err := gitMarshal(ctx, r.ProjectRoot(), "rev-parse", op.Target)
	if err != nil || strings.TrimSpace(after) != source.head || called != 1 {
		t.Fatal("target effect mismatch", called, after, err)
	}
	if err = r.withRequiredContextCloseEffect(ctx, service, source.marshalRunID, source.marshalRun, op, effect); err == nil || called != 1 {
		t.Fatal("live close proof replayed", called, err)
	}
	// All changes are to an isolated host fixture repository. No native/model
	// invocation or actual user's original branch/install was performed.
}

func TestRequiredCloseEffectRefusesChangesBeforeAnyTargetMutation(t *testing.T) {
	for _, scenario := range []string{"missing-live-proof", "revoked-memory", "changed-source", "altered-next", "extra-journal-revision", "foreign-context"} {
		t.Run(scenario, func(t *testing.T) {
			r, ctx, service, source, op := requiredCloseHostFixture(t, scenario != "missing-live-proof")
			switch scenario {
			case "revoked-memory":
				if _, err := r.store.UpdateMemory(t.Context(), r.ProjectID(), "MEM-required-resolver", 1, func(record *model.MemoryRecordV2) error { record.ACLScope = "foreign"; return nil }); err != nil {
					t.Fatal(err)
				}
			case "changed-source":
				if err := os.WriteFile(filepath.Join(source.workspace, "README.md"), []byte("late changed source"), 0600); err != nil {
					t.Fatal(err)
				}
			case "altered-next":
				op.Next.State = marshal.Verifying
			case "extra-journal-revision":
				stored, err := r.store.GetMarshalRun(ctx, r.ProjectID(), source.marshalRunID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = r.store.SetMarshalRun(ctx, r.ProjectID(), source.marshalRunID, stored.Value, stored.Revision); err != nil {
					t.Fatal(err)
				}
			case "foreign-context":
				ctx = t.Context()
			}
			called := false
			if err := r.withRequiredContextCloseEffect(ctx, service, source.marshalRunID, source.marshalRun, op, func() error { called = true; return nil }); err == nil || called {
				t.Fatal("changed/missing proof reached target effect", err)
			}
			after, err := gitMarshal(t.Context(), r.ProjectRoot(), "rev-parse", op.Target)
			if err != nil || strings.TrimSpace(after) != op.Before {
				t.Fatal("refusal moved target", after, err)
			}
		})
	}
}

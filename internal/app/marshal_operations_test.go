package app

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/statepath"
)

func TestInterruptedOperationReceiptNamesJournal(t *testing.T) {
	t.Run("launch reports journal id and unknown effects", func(t *testing.T) {
		s, _ := marshalFixture(t, 1)
		if _, err := s.StartPlanning(t.Context(), "run", "write", marshal.Budget{}); err != nil {
			t.Fatal(err)
		}
		run, rev, err := s.load(t.Context(), "run")
		if err != nil {
			t.Fatal(err)
		}
		op := &marshal.LifecycleOperation{
			Kind:   "launch",
			TaskID: "a",
			Event:  events.Event{Type: events.EventTypeMarshalTaskDispatched},
		}
		rev, err = s.beginOperation(t.Context(), "run", run, rev, op)
		if err != nil {
			t.Fatal(err)
		}
		stored, rev, err := s.load(t.Context(), "run")
		if err != nil {
			t.Fatal(err)
		}
		if stored.Operation == nil || stored.Operation.ID == "" {
			t.Fatalf("expected persisted operation journal, got %+v", stored.Operation)
		}
		journalID := stored.Operation.ID

		// Check unfinishedMarshalOperation directly.
		err = unfinishedMarshalOperation(stored)
		if !errors.Is(err, ErrUnfinishedLifecycleOperation) {
			t.Fatalf("expected ErrUnfinishedLifecycleOperation, got %v", err)
		}
		receipt, ok := marshal.RefusalOf(err)
		if !ok {
			t.Fatalf("expected typed refusal, got %T %v", err, err)
		}
		if receipt.Code != marshal.RefusalCodeInterruptedOp {
			t.Fatalf("code = %q, want %q", receipt.Code, marshal.RefusalCodeInterruptedOp)
		}
		if receipt.Blocker == nil || receipt.Blocker.ID != journalID || receipt.Blocker.Kind != "launch" || receipt.Blocker.TaskID != "a" || receipt.Blocker.RunID != "run" || receipt.Blocker.StartedAt.IsZero() {
			t.Fatalf("blocker = %+v, want journal id %q launch/a/run", receipt.Blocker, journalID)
		}
		if receipt.Effects != marshal.EffectsUnknown {
			t.Fatalf("effects = %q, want %q (must never claim none)", receipt.Effects, marshal.EffectsUnknown)
		}
		if want := []string{"/marshal resume"}; !slices.Equal(receipt.NextCommands, want) {
			t.Fatalf("nextCommands = %q, want %q", receipt.NextCommands, want)
		}

		// A second beginOperation refuses with the same journal blocker.
		if _, err := s.beginOperation(t.Context(), "run", stored, rev, &marshal.LifecycleOperation{Kind: "merge", TaskID: "a"}); !errors.Is(err, ErrUnfinishedLifecycleOperation) {
			t.Fatalf("second beginOperation err = %v", err)
		} else if second, ok := marshal.RefusalOf(err); !ok || second.Blocker == nil || second.Blocker.ID != journalID || second.Effects == marshal.EffectsNone {
			t.Fatalf("second beginOperation receipt = %+v (ok=%v)", second, ok)
		}

		// Lifecycle operations like RetryTask surface the same journal refusal without mutating revision.
		if err := s.RetryTask(t.Context(), "run", "a", true); !errors.Is(err, ErrUnfinishedLifecycleOperation) {
			t.Fatalf("RetryTask err = %v", err)
		} else if retryReceipt, ok := marshal.RefusalOf(err); !ok || retryReceipt.Blocker == nil || retryReceipt.Blocker.ID != journalID || retryReceipt.Effects != marshal.EffectsUnknown {
			t.Fatalf("RetryTask receipt = %+v (ok=%v)", retryReceipt, ok)
		}
		after, afterRev, err := s.load(t.Context(), "run")
		if err != nil {
			t.Fatal(err)
		}
		if afterRev != rev || after.Operation == nil || after.Operation.ID != journalID {
			t.Fatalf("run mutated by refused retry: rev %d->%d op=%+v", rev, afterRev, after.Operation)
		}
	})

	t.Run("hand-in with recorded artifact reports partial effects", func(t *testing.T) {
		s, _ := marshalFixture(t, 1)
		if _, err := s.StartPlanning(t.Context(), "run", "write", marshal.Budget{}); err != nil {
			t.Fatal(err)
		}
		run, rev, err := s.load(t.Context(), "run")
		if err != nil {
			t.Fatal(err)
		}
		op := &marshal.LifecycleOperation{
			Kind:   "hand-in",
			TaskID: "a",
			HandIn: &marshal.HandIn{ResultCommit: "deadbeef"},
			Event:  events.Event{Type: events.EventTypeMarshalTaskHandedIn},
		}
		if _, err = s.beginOperation(t.Context(), "run", run, rev, op); err != nil {
			t.Fatal(err)
		}
		stored, _, err := s.load(t.Context(), "run")
		if err != nil {
			t.Fatal(err)
		}
		err = unfinishedMarshalOperation(stored)
		receipt, ok := marshal.RefusalOf(err)
		if !ok || receipt.Blocker == nil || receipt.Blocker.ID != op.ID || receipt.Effects != marshal.EffectsPartial {
			t.Fatalf("hand-in refusal = %+v (ok=%v, err=%v)", receipt, ok, err)
		}
		if want := []string{"/marshal resume"}; !slices.Equal(receipt.NextCommands, want) {
			t.Fatalf("nextCommands = %q, want %q", receipt.NextCommands, want)
		}
	})

	t.Run("moved integration ref during merge recovery reports partial effects and journal id", func(t *testing.T) {
		s, repo := marshalFixture(t, 1)
		if _, err := s.StartPlanning(t.Context(), "run", "write", marshal.Budget{}); err != nil {
			t.Fatal(err)
		}
		run, rev, err := s.load(t.Context(), "run")
		if err != nil {
			t.Fatal(err)
		}
		branch := statepath.RefName + "/run/integration"
		ref := "refs/heads/" + branch
		marshalGit(t, repo, "branch", branch, run.BaseCommit)
		// Move the integration branch with an unrelated commit so recovery sees partial movement.
		wt := filepath.Join(t.TempDir(), "wt-move")
		marshalGit(t, repo, "worktree", "add", wt, branch)
		if err := os.WriteFile(filepath.Join(wt, "moved.txt"), []byte("moved\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		marshalGit(t, wt, "add", "moved.txt")
		marshalGit(t, wt, "commit", "-m", "unrelated move")
		marshalGit(t, repo, "worktree", "remove", "--force", wt)

		next := run
		next.State = marshal.Verifying
		op := &marshal.LifecycleOperation{
			Kind:   "merge",
			TaskID: "a",
			Dir:    filepath.Join(s.Worktrees, "retained"),
			Target: ref,
			Before: run.BaseCommit,
			After:  run.BaseCommit,
			Next:   &next,
			Event:  events.Event{Type: events.EventTypeMarshalTaskMerged},
		}
		rev, err = s.beginOperation(t.Context(), "run", run, rev, op)
		if err != nil {
			t.Fatal(err)
		}
		stored, rev, err := s.load(t.Context(), "run")
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = s.recoverOperation(t.Context(), "run", stored, rev)
		if err == nil || !errors.Is(err, ErrUnfinishedLifecycleOperation) {
			t.Fatalf("recoverOperation err = %v, want ErrUnfinishedLifecycleOperation", err)
		}
		receipt, ok := marshal.RefusalOf(err)
		if !ok || receipt.Blocker == nil || receipt.Blocker.ID != op.ID || receipt.Effects != marshal.EffectsPartial {
			t.Fatalf("recoverOperation receipt = %+v (ok=%v, err=%v)", receipt, ok, err)
		}
		if !strings.Contains(err.Error(), op.ID) {
			t.Fatalf("error text %q does not name journal id %q", err.Error(), op.ID)
		}
		if want := []string{"/marshal status", "/marshal amend"}; !slices.Equal(receipt.NextCommands, want) {
			t.Fatalf("nextCommands = %q, want %q", receipt.NextCommands, want)
		}
	})
}

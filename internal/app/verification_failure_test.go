package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/verification"
)

func TestVerificationErrorPausesDurablyEvenWhenCancelled(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "active", true: "cancelled"}[cancelled], func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "state.db")
			s, repo := marshalFixture(t, 1, dbPath)
			startIntegrityRun(t, s, marshal.Budget{})
			run, rev, err := s.load(t.Context(), "run")
			if err != nil {
				t.Fatal(err)
			}
			run.State = marshal.Verifying
			if err := s.save(t.Context(), "run", run, rev); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(s.Worktrees, integrationTaskID("run", run))
			marshalGit(t, repo, "worktree", "add", "--detach", dir, "HEAD")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			s.Verify = func(context.Context, marshal.Run, string) (verification.Session, verification.Binding, error) {
				if cancelled {
					cancel()
				}
				return verification.Session{}, verification.Binding{}, errors.New("check a#0: output exceeds artifact bound")
			}
			if _, err := s.VerifyMerged(ctx, "run", knownCharge()); err == nil {
				t.Fatal("verification error lost")
			}
			if err := s.Store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := store.Open(t.Context(), dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			stored, err := reopened.GetMarshalRun(t.Context(), s.ProjectID, "run")
			if err != nil {
				t.Fatal(err)
			}
			paused := stored.Value
			if paused.State != marshal.AwaitingUser || paused.Pause == nil || paused.Pause.ResumeState != marshal.Verifying || !strings.Contains(paused.Pause.Reason, "check a#0") || !strings.Contains(strings.Join(paused.Pause.Resolutions, " "), "/marshal resume") {
				t.Fatalf("verification stop lost: %+v", paused)
			}
		})
	}
}

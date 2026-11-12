package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/verification"
)

// autoCloseFixture is a one-task run the Marshal closes on its own once the
// integrated result is verified.
func autoCloseFixture(t *testing.T) (*MarshalService, string) {
	t.Helper()
	s, repo := marshalFixture(t, 1)
	settings := marshal.DefaultSettings()
	settings.AcceptanceMode = marshal.AcceptMarshal
	if _, err := s.Store.SetMarshalSettings(t.Context(), s.ProjectID, settings, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartPlanning(t.Context(), "run", "write file", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(t.Context(), "run"); err != nil {
		t.Fatal(err)
	}
	marshalGit(t, repo, "checkout", "--detach")
	return s, repo
}

// A run that closes on its own verifies the integrated result once, in the
// close itself. Each verification is a fresh verifier model turn plus every
// approved check, so a separate pass right before the close only doubled the
// "final check" wait.
func TestAutoCloseVerifiesIntegratedResultOnce(t *testing.T) {
	s, repo := autoCloseFixture(t)
	const turn = 150 * time.Millisecond // stands in for the verifier's model turn
	var mu sync.Mutex
	var passes []time.Duration
	var merged, closed time.Time
	verify := s.Verify
	s.Verify = func(ctx context.Context, run marshal.Run, head string) (verification.Session, verification.Binding, error) {
		start := time.Now()
		time.Sleep(turn)
		session, binding, err := verify(ctx, run, head)
		mu.Lock()
		passes = append(passes, time.Since(start))
		mu.Unlock()
		return session, binding, err
	}
	observe := func(run marshal.Run) {
		now := time.Now()
		if run.State == marshal.Verifying && merged.IsZero() {
			merged = now
		}
		if run.State == marshal.Closed && closed.IsZero() {
			closed = now
		}
	}
	run, err := s.Execute(t.Context(), "run", marshalBrief, observe)
	if err != nil || run.State != marshal.Closed {
		t.Fatalf("execute: %s %v", run.State, err)
	}
	if marshalGit(t, repo, "rev-parse", "refs/heads/main") != deliveredCommit(t, s, "run") {
		t.Fatal("target was not delivered")
	}
	t.Logf("final check: merged->closed %s, verification passes %v", closed.Sub(merged).Round(time.Millisecond), passes)
	if len(passes) != 1 {
		t.Fatalf("verified the same integrated result %d times before closing, want 1", len(passes))
	}
}

// A failed verification in a self-closing run still stops for the person
// with the same resumable pause, and nothing is delivered.
func TestAutoCloseFailedVerificationPausesForResume(t *testing.T) {
	s, repo := autoCloseFixture(t)
	before := marshalGit(t, repo, "rev-parse", "refs/heads/main")
	verify := s.Verify
	s.Verify = func(ctx context.Context, run marshal.Run, head string) (verification.Session, verification.Binding, error) {
		session, binding, err := verify(ctx, run, head)
		session.RequiredChecks = map[string]verification.Status{"integration": verification.StatusFail}
		return session, binding, err
	}
	run, err := s.Execute(t.Context(), "run", marshalBrief, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if run.State != marshal.AwaitingUser || run.Pause == nil || run.Pause.Code != marshal.CodeVerificationFailed || run.Pause.ResumeState != marshal.Verifying || !strings.HasPrefix(run.Pause.Reason, "verification failed: ") {
		t.Fatalf("failed verification pause: %s %+v", run.State, run.Pause)
	}
	if marshalGit(t, repo, "rev-parse", "refs/heads/main") != before {
		t.Fatal("unverified result was delivered")
	}
}

// A verifier that cannot run in a self-closing run leaves the same durable,
// resumable stop as a separate verification did.
func TestAutoCloseVerifierErrorPausesDurably(t *testing.T) {
	for _, stage := range []string{"verify", "source"} {
		t.Run(stage, func(t *testing.T) {
			s, repo := autoCloseFixture(t)
			before := marshalGit(t, repo, "rev-parse", "refs/heads/main")
			cause := errors.New("runtime proof: check output exceeds admitted bound")
			if stage == "source" {
				s.requiredIntegrationSource = func(context.Context, string, marshal.Run, string) error { return cause }
			} else {
				s.Verify = func(context.Context, marshal.Run, string) (verification.Session, verification.Binding, error) {
					return verification.Session{}, verification.Binding{}, cause
				}
			}
			if _, err := s.Execute(t.Context(), "run", marshalBrief, nil); !errors.Is(err, cause) {
				t.Fatalf("execute: %v", err)
			}
			run, err := (&MarshalService{Store: s.Store, ProjectID: s.ProjectID, Repository: s.Repository}).Snapshot(t.Context(), "run")
			if err != nil {
				t.Fatal(err)
			}
			if run.State != marshal.AwaitingUser || run.Pause == nil || run.Pause.ResumeState != marshal.Verifying || !strings.Contains(run.Pause.Reason, cause.Error()) || !strings.Contains(strings.Join(run.Pause.Resolutions, " "), "/marshal resume") {
				t.Fatalf("lost verification stop: %s %+v", run.State, run.Pause)
			}
			if marshalGit(t, repo, "rev-parse", "refs/heads/main") != before {
				t.Fatal("unverified result was delivered")
			}
		})
	}
}

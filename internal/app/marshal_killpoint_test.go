//go:build !governed

package app

import (
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
)

// These faults return without abandonment, like a process lost after a durable
// write. Every recovery closes and reopens SQLite before inspecting Git.
func injectKillPoint(s *MarshalService, kind, boundary string) error {
	crash := errors.New("kill point: " + kind + "/" + boundary)
	s.lifecycleBoundary = func(k, b string) error {
		if k == kind && b == boundary {
			return crash
		}
		return nil
	}
	s.AfterLifecycleEffect = func(k, _ string) error {
		if k == kind && boundary == "git" {
			return crash
		}
		return nil
	}
	return crash
}

func reopenKillPoint(t *testing.T, s *MarshalService, path string) *MarshalService {
	t.Helper()
	s = restartService(t, s, path)
	s.lifecycleBoundary = nil
	if err := s.RecoverPendingOperations(t.Context()); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestMergeRecoveryKillPoints(t *testing.T) {
	for _, boundary := range []string{"intent", "worktree", "git", "receipt"} {
		for _, task := range []string{"a", "b"} {
			t.Run(task+"/"+boundary, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "state.db")
				s, repo := integrationOrderFixture(t, 2, path, nil, false)
				acceptTask(t, s, "a")
				if task == "b" {
					if err := s.Merge(t.Context(), "run", "a"); err != nil {
						t.Fatal(err)
					}
				}
				if task == "b" {
					acceptTask(t, s, "b")
				}
				crash := injectKillPoint(s, "merge", boundary)
				if err := s.Merge(t.Context(), "run", task); !errors.Is(err, crash) {
					t.Fatalf("fault not reached: %v", err)
				}
				before := marshalGit(t, repo, "rev-list", "--all", "--count")
				s = reopenKillPoint(t, s, path)
				run := loadRun(t, s)
				index := taskIndex(run, task)
				expected := 1
				if task == "b" {
					expected++
				}
				if boundary == "intent" && task == "a" {
					if run.State != marshal.AwaitingUser || run.Operation != nil || run.Pause == nil || !strings.Contains(run.Pause.Reason, "interrupted merge") || len(run.Pause.Resolutions) == 0 {
						t.Fatalf("missing explicit hold: %+v", run)
					}
					if run.Tasks[index].State != marshal.Accepted || mergedEvents(t, s) != 0 {
						t.Fatal("unapplied merge acknowledged")
					}
				} else {
					if run.Operation != nil || run.Tasks[index].State != marshal.Merged || mergedEvents(t, s) != expected {
						t.Fatalf("merge recovery: %+v", run)
					}
					dir := integrationDir(s, run)
					if status := marshalGit(t, dir, "status", "--porcelain"); status != "" {
						t.Fatalf("dirty integration: %s", status)
					}
					if marshalGit(t, dir, "rev-parse", "HEAD") != marshalGit(t, repo, "rev-parse", integrationRef()) {
						t.Fatal("checkout/ref mismatch")
					}
					if got := marshalGit(t, repo, "rev-list", "--count", "--merges", integrationRef()); got != strconv.Itoa(expected) {
						t.Fatalf("merge count = %s, want %d", got, expected)
					}
				}
				s = reopenKillPoint(t, s, path)
				if got := marshalGit(t, repo, "rev-list", "--all", "--count"); got != before {
					t.Fatalf("recovery created commits: %s -> %s", before, got)
				}
			})
		}
	}
}

func TestCloseRecoveryKillPoints(t *testing.T) {
	for _, proof := range []string{"full", "checks-only"} {
		for _, checkout := range []string{"checked-out", "unchecked"} {
			for _, boundary := range []string{"intent", "checkpoint", "authorized", "fence", "git", "receipt"} {
				t.Run(proof+"/"+checkout+"/"+boundary, func(t *testing.T) {
					var s *MarshalService
					var repo, path string
					if proof == "checks-only" {
						s, repo, path, _ = checksOnlyFixture(t, marshal.AcceptMarshalThenUser, finalModelOff)
						if _, err := s.Execute(t.Context(), "run", marshalBrief, nil); err != nil {
							t.Fatal(err)
						}
					} else {
						path = filepath.Join(t.TempDir(), "state.db")
						s, repo = integrationOrderFixture(t, 1, path, nil, false)
						acceptTask(t, s, "a")
						if err := s.Merge(t.Context(), "run", "a"); err != nil {
							t.Fatal(err)
						}
					}
					if checkout == "unchecked" {
						marshalGit(t, repo, "checkout", "--detach")
					}
					base := marshalGit(t, repo, "rev-parse", "refs/heads/main")
					head := marshalGit(t, repo, "rev-parse", integrationRef())
					crash := injectKillPoint(s, "close", boundary)
					if err := s.Close(t.Context(), "run"); !errors.Is(err, crash) {
						t.Fatalf("fault not reached: %v", err)
					}
					commits := marshalGit(t, repo, "rev-list", "--all", "--count")
					applied := boundary == "git" || boundary == "receipt"
					for range 2 {
						s = reopenKillPoint(t, s, path)
						run := loadRun(t, s)
						if run.Operation != nil {
							t.Fatalf("journal retained: %+v", run.Operation)
						}
						want := base
						closed := 0
						if applied {
							want = head
							closed = 1
							if run.State != marshal.Closed {
								t.Fatalf("not closed: %s", run.State)
							}
						} else if run.State != marshal.AwaitingUser || run.Pause == nil || len(run.Pause.Resolutions) == 0 {
							t.Fatalf("missing explicit hold: %+v", run)
						}
						if got := marshalGit(t, repo, "rev-parse", "refs/heads/main"); got != want {
							t.Fatalf("target %s, want %s", got, want)
						}
						if got := countRunEvents(t, s, events.EventTypeMarshalRunClosed); got != closed {
							t.Fatalf("close receipts %d, want %d", got, closed)
						}
						if got := marshalGit(t, repo, "rev-list", "--all", "--count"); got != commits {
							t.Fatal("recovery created commits")
						}
						if status := marshalGit(t, repo, "status", "--porcelain"); status != "" {
							t.Fatalf("dirty target: %s", status)
						}
						if applied && proof == "checks-only" && (run.ChecksOnly == nil || run.ChecksOnly.Head != head || !run.ChecksOnly.Passed()) {
							t.Fatal("receipt does not bind delivered head")
						}
					}
				})
			}
		}
	}
}

// A matching target commit alone must not prove that the close fence ran.
func TestCloseRecoveryAdvancedWithoutFence(t *testing.T) {
	for _, proof := range []string{"full", "checks-only"} {
		for _, checkout := range []string{"checked-out", "unchecked"} {
			t.Run(proof+"/"+checkout, func(t *testing.T) {
				var s *MarshalService
				var repo, path string
				if proof == "checks-only" {
					s, repo, path, _ = checksOnlyFixture(t, marshal.AcceptMarshalThenUser, finalModelOff)
					if _, err := s.Execute(t.Context(), "run", marshalBrief, nil); err != nil {
						t.Fatal(err)
					}
				} else {
					path = filepath.Join(t.TempDir(), "state.db")
					s, repo = integrationOrderFixture(t, 1, path, nil, false)
					acceptTask(t, s, "a")
					if err := s.Merge(t.Context(), "run", "a"); err != nil {
						t.Fatal(err)
					}
				}
				if checkout == "unchecked" {
					marshalGit(t, repo, "checkout", "--detach")
				}
				crash := injectKillPoint(s, "close", "authorized")
				if err := s.Close(t.Context(), "run"); !errors.Is(err, crash) {
					t.Fatalf("fault not reached: %v", err)
				}
				pending := loadRun(t, s)
				op := pending.Operation
				if op == nil || !op.Authorized {
					t.Fatal("authorized intent missing")
				}
				// An external actor advances to the expected commit without the fence.
				// Keep the checkout consistent, so only the missing proof is at issue.
				if checkout == "checked-out" {
					marshalGit(t, repo, "merge", "--ff-only", op.After)
				} else {
					marshalGit(t, repo, "update-ref", op.Target, op.After, op.Before)
				}
				for range 2 {
					s = reopenKillPoint(t, s, path)
					run := loadRun(t, s)
					if run.State != marshal.AwaitingUser || run.Operation == nil || run.Pause == nil || !strings.Contains(run.Pause.Reason, "missing durable close fence proof") {
						t.Fatalf("unproven advancement not explicitly held: %+v", run)
					}
					// Full-proof journals from older versions have this same shape:
					// authorized, with no operation-keyed fence event.
					if proof == "full" && !strings.Contains(run.Pause.Reason, "cannot prove fence passage; inspect") {
						t.Fatalf("legacy journal lacks inspection/confirmation path: %+v", run.Pause)
					}
					if countRunEvents(t, s, events.EventTypeMarshalRunClosed) != 0 {
						t.Fatal("unproven close acknowledged")
					}
					if marshalGit(t, repo, "rev-parse", op.Target) != op.After {
						t.Fatal("recovery moved target")
					}
				}
			})
		}
	}
}

func TestCloseRecoveryPartialTargetCheckout(t *testing.T) {
	for _, proof := range []string{"full", "checks-only"} {
		t.Run(proof, func(t *testing.T) {
			var s *MarshalService
			var repo, path string
			if proof == "checks-only" {
				s, repo, path, _ = checksOnlyFixture(t, marshal.AcceptMarshalThenUser, finalModelOff)
				if _, err := s.Execute(t.Context(), "run", marshalBrief, nil); err != nil {
					t.Fatal(err)
				}
			} else {
				path = filepath.Join(t.TempDir(), "state.db")
				s, repo = integrationOrderFixture(t, 1, path, nil, false)
				acceptTask(t, s, "a")
				if err := s.Merge(t.Context(), "run", "a"); err != nil {
					t.Fatal(err)
				}
			}
			crash := injectKillPoint(s, "close", "git")
			if err := s.Close(t.Context(), "run"); !errors.Is(err, crash) {
				t.Fatalf("fault not reached: %v", err)
			}
			op := loadRun(t, s).Operation
			if op == nil {
				t.Fatal("missing close journal")
			}
			// Model loss during Git's checkout update: ref at after, index/files
			// still at before, with the genuine authorized fence record retained.
			marshalGit(t, repo, "reset", "--hard", op.Before)
			marshalGit(t, repo, "update-ref", op.Target, op.After, op.Before)
			if status := marshalGit(t, repo, "status", "--porcelain"); status == "" {
				t.Fatal("partial checkout not constructed")
			}
			s = reopenKillPoint(t, s, path)
			run := loadRun(t, s)
			if run.State != marshal.AwaitingUser || run.Operation == nil || run.Pause == nil || !strings.Contains(run.Pause.Reason, "target checkout does not match delivered commit") || !strings.Contains(run.Pause.Reason, "reset --hard "+op.After) {
				t.Fatalf("partial checkout not explicitly held: %+v", run)
			}
			if countRunEvents(t, s, events.EventTypeMarshalRunClosed) != 0 {
				t.Fatal("partial checkout acknowledged")
			}
			s = reopenKillPoint(t, s, path)
			if held := loadRun(t, s); held.State != marshal.AwaitingUser || held.Operation == nil {
				t.Fatalf("second recovery lost held operation: %+v", held)
			}
			// Perform the advertised repair and prove recovery can finish once.
			marshalGit(t, repo, "reset", "--hard", op.After)
			for range 2 {
				s = reopenKillPoint(t, s, path)
				if repaired := loadRun(t, s); repaired.State != marshal.Closed || repaired.Operation != nil {
					t.Fatalf("repaired checkout did not recover: %+v", repaired)
				}
				if countRunEvents(t, s, events.EventTypeMarshalRunClosed) != 1 {
					t.Fatal("repair must produce exactly one close receipt")
				}
			}
		})
	}
}

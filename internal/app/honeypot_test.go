package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/worker"
)

func TestMarshalHoneypotHandInNeverMerged(t *testing.T) {
	for _, contaminated := range []bool{false, true} {
		name := "clean"
		if contaminated {
			name = "token"
		}
		t.Run(name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "state.db")
			s, repo := marshalFixture(t, 1, dbPath)
			r := &Runtime{store: s.Store}
			notified := false
			r.SetEgressAlertSink(func(alert EgressAlert) error {
				if alert.TaskID == "a" && alert.Kind == "honeypot" && alert.State == "failed" {
					notified = true
				}
				return nil
			})
			defer func() {
				r.honeypotMu.Lock()
				defer r.honeypotMu.Unlock()
				for _, trap := range r.honeypots {
					_ = trap.Close()
				}
			}()
			s.HandInGuard = r.guardHoneypotHandIn
			s.Drivers["worker"] = driver.Governed{Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
				trap, err := r.armHoneypot(req.Worktree)
				if err != nil {
					return nil, err
				}
				content := []byte("clean\n")
				if contaminated {
					content = []byte(strings.SplitN(trap.Env[0], "=", 2)[1])
				}
				return nil, os.WriteFile(filepath.Join(req.Worktree, "a.txt"), content, 0600)
			}}
			original := marshalGit(t, repo, "rev-parse", "HEAD")
			startIntegrityRun(t, s, marshal.Budget{})
			d, err := s.Dispatch(t.Context(), "run", "a", "write file")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.CollectHandIn(t.Context(), "run", d); err != nil {
				t.Fatal(err)
			}
			status := r.honeypotStatus()
			if status != "armed" {
				t.Fatalf("status: %s", status)
			}
			run, err := s.Snapshot(t.Context(), "run")
			if err != nil {
				t.Fatal(err)
			}
			if contaminated {
				if run.Tasks[0].State == marshal.HandedIn || run.Tasks[0].State == marshal.Accepted {
					t.Fatalf("contaminated task accepted: %s", run.Tasks[0].State)
				}
				if _, err := s.Review(t.Context(), "run", "a", knownCharge()); err == nil {
					t.Fatal("contaminated hand-in reviewed")
				}
				if err := s.Merge(t.Context(), "run", "a"); err == nil {
					t.Fatal("contaminated hand-in merged")
				}
				if got := marshalGit(t, repo, "rev-parse", "HEAD"); got != original {
					t.Fatal("project head changed")
				}
				events, err := s.Store.ListEvents(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, event := range events {
					if event.Type == "HONEYPOT_HIT" {
						found = true
					}
				}
				if !found {
					t.Fatal("operator alert missing")
				}
				if !notified {
					t.Fatal("honeypot incident was not delivered to the workspace/chat sink")
				}
				handin, err := s.Store.GetMarshalHandIn(t.Context(), "run", "a", 1)
				if err != nil || handin.Value.ResultCommit == "" {
					t.Fatalf("missing hand-in evidence: %v", err)
				}
				// Even a later accepted state cannot override durable artifact quarantine.
				saved, revision, err := s.load(t.Context(), "run")
				if err != nil {
					t.Fatal(err)
				}
				saved.Tasks[0].State = marshal.Accepted
				saved.Tasks[0].ResultCommit = handin.Value.ResultCommit
				if err := s.save(t.Context(), "run", saved, revision); err != nil {
					t.Fatal(err)
				}
				if err := s.Store.Close(); err != nil {
					t.Fatal(err)
				}
				reopened, err := store.Open(t.Context(), dbPath)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = reopened.Close() })
				restarted := &MarshalService{Store: reopened, Repository: s.Repository, Worktrees: s.Worktrees, ProjectID: s.ProjectID}
				if err := restarted.Merge(t.Context(), "run", "a"); err == nil || !strings.Contains(err.Error(), "quarantined") {
					t.Fatalf("quarantine lost after restart: %v", err)
				}

			} else {
				if run.Tasks[0].State != marshal.HandedIn {
					t.Fatalf("clean hand-in returned: %s", run.Tasks[0].State)
				}
				if v, err := s.Review(t.Context(), "run", "a", knownCharge()); err != nil || v != marshal.VerdictAccept {
					t.Fatalf("review: %s %v", v, err)
				}
				if err := s.Merge(t.Context(), "run", "a"); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestMarshalHoneypotWorkerFailurePausesRun(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	s.Drivers["worker"] = driver.Governed{Run: func(context.Context, driver.Request) ([]marshal.CommandRecord, error) { return nil, worker.ErrHoneypot }}
	startIntegrityRun(t, s, marshal.Budget{})
	dispatch, err := s.Dispatch(t.Context(), "run", "a", "write file")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CollectHandIn(t.Context(), "run", dispatch); !errors.Is(err, worker.ErrHoneypot) {
		t.Fatalf("missing refusal: %v", err)
	}
	run, err := s.Snapshot(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if run.State != marshal.AwaitingUser || run.Tasks[0].State != marshal.Escalated {
		t.Fatalf("run not stopped: %s %s", run.State, run.Tasks[0].State)
	}
	if err := s.Merge(t.Context(), "run", "a"); err == nil {
		t.Fatal("stopped task merged")
	}
}

func TestHoneypotIncidentRetainsRunIdentity(t *testing.T) {
	s, repo := marshalFixture(t, 1)
	r := &Runtime{store: s.Store}
	trap, err := r.armHoneypot(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer trap.Close()
	var got EgressAlert
	r.SetEgressAlertSink(func(alert EgressAlert) error { got = alert; return nil })
	token := strings.SplitN(trap.Env[0], "=", 2)[1]
	err = r.checkHoneypot(t.Context(), "a", trap, []byte(token), nil, EgressAlert{RunID: "child", ParentRunID: "execution", TaskID: "canonical", Worker: "worker"})
	if !errors.Is(err, worker.ErrHoneypot) {
		t.Fatalf("honeypot not enforced: %v", err)
	}
	if got.RunID != "child" || got.ParentRunID != "execution" || got.TaskID != "canonical" {
		t.Fatalf("live identity = %#v", got)
	}
	events, err := s.Store.ListEvents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == "HONEYPOT_HIT" {
			if event.Data["run_id"] != "child" || event.Data["parent_run_id"] != "execution" || event.Data["task_id"] != "canonical" {
				t.Fatalf("durable identity = %#v", event.Data)
			}
			return
		}
	}
	t.Fatal("durable incident missing")
}

//go:build !governed

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/cloud"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/verification"
)

func TestExtremeRunParallelCrossReviewAndVerifier(t *testing.T) {
	ctx := context.Background()
	extremeProviderCLIs(t)
	s, _ := marshalUserCloseFixture(t, 2)
	s.Gate = cloud.Authorize(ctx, cloud.Config{}, t.TempDir(), "test").Gate
	if marshal.TierPolicy(s.Gate, marshal.DefaultSettings()).Tier != marshal.Ultra {
		t.Fatal("local authorization did not select ULTRA")
	}
	s.ModelProvider = "test"
	var reviews atomic.Int32
	var verified atomic.Bool
	s.CrossReview = func(_ context.Context, t marshal.Task, h marshal.HandIn, _ marshal.Control) (marshal.Review, string, error) {
		reviews.Add(1)
		ref := "check:test -f " + t.PlanTaskID + ".txt"
		return marshal.Review{Verdict: marshal.VerdictAccept, Reviewer: "second", EvidenceRefs: []string{ref}}, "test", nil
	}
	s.VerifierProvider = func(context.Context, marshal.Run) (string, error) { return "test", nil }
	s.IndependentVerify = func(_ context.Context, run marshal.Run, head string, session verification.Session) (marshal.VerifierEvidence, error) {
		verified.Store(true)
		return marshal.VerifierEvidence{Reviewer: "verifier", Provider: "verifier", Commit: head, Verdict: "pass", InputDigest: verifierInputDigest(run, head, session)}, nil
	}

	settings, err := s.Store.GetMarshalSettings(ctx, s.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	settings.Value.UltraConcurrency = 2
	if _, err := s.Store.SetMarshalSettings(ctx, s.ProjectID, settings.Value, settings.Revision); err != nil {
		t.Fatal(err)
	}

	fakeModel := s.Model.(marshalFakeModel)
	draft := fakeModel.draft
	draft.Tasks[1].DependsOn = nil
	draft.Plan.Tasks[1].DependsOn = nil
	g, err := plan.BuildGraph(draft.Plan.Tasks)
	if err != nil {
		t.Fatal(err)
	}
	draft.Plan.Graph = g
	s.Model = marshalFakeModel{draft: draft, review: fakeModel.review}

	if _, err := s.StartPlanning(ctx, "run-ultra", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run-ultra"); err != nil {
		t.Fatal(err)
	}

	runRec, err := s.Store.GetMarshalRun(ctx, s.ProjectID, "run-ultra")
	if err != nil {
		t.Fatal(err)
	}
	runRec.Value.Process05Bound = false
	if _, err := s.Store.SetMarshalRun(ctx, s.ProjectID, "run-ultra", runRec.Value, runRec.Revision); err != nil {
		t.Fatal(err)
	}

	releaseA := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseA) }) }
	t.Cleanup(release)
	bHandedIn := make(chan struct{})

	// Task a waits for releaseA
	// Task b completes immediately and signals bHandedIn
	s.Drivers["worker"] = driver.Governed{
		Provider: "worker",
		Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
			if req.Task.PlanTaskID == "a" {
				<-releaseA
				return nil, os.WriteFile(filepath.Join(req.Worktree, "a.txt"), []byte("a\n"), 0600)
			} else {
				if err := os.WriteFile(filepath.Join(req.Worktree, "b.txt"), []byte("b\n"), 0600); err != nil {
					return nil, err
				}
				close(bHandedIn)
			}
			return nil, nil
		},
	}

	done := make(chan error, 1)
	go func() {
		_, execErr := s.Execute(ctx, "run-ultra", marshalBrief, nil)
		done <- execErr
	}()

	// Wait for b to finish
	select {
	case <-bHandedIn:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for task b to finish")
	}

	// Wait for task b to be collected and reviewed while task a is still blocked
	var run marshal.Run
	for range 50 {
		time.Sleep(50 * time.Millisecond)
		r, _, err := s.load(ctx, "run-ultra")
		if err == nil {
			run = r
			if len(r.Tasks) == 2 && r.Tasks[1].State == marshal.Accepted {
				break
			}
		}
	}
	if len(run.Tasks) < 2 {
		t.Fatalf("expected 2 tasks in run, got: %+v", run)
	}
	if run.Tasks[1].State != marshal.Accepted {
		t.Fatalf("task b should be collected and reviewed as soon as ready; got state: %s, task 0 state: %s, run state: %s", run.Tasks[1].State, run.Tasks[0].State, run.State)
	}
	// Verify task a is still dispatched
	if run.Tasks[0].State != marshal.Dispatched {
		t.Fatalf("task a should still be dispatched, got: %s", run.Tasks[0].State)
	}

	// Release task a so it can complete
	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("execute failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("execute timed out after releasing task a")
	}

	// Both tasks should now be merged in plan order
	finalRun, _, err := s.load(ctx, "run-ultra")
	if err != nil {
		t.Fatal(err)
	}
	if finalRun.Tasks[0].State != marshal.Merged || finalRun.Tasks[1].State != marshal.Merged {
		t.Fatalf("both tasks should be merged: %+v", finalRun.Tasks)
	}

	if reviews.Load() != 2 {
		t.Fatalf("cross-reviews = %d", reviews.Load())
	}
	if _, err := s.VerifyMerged(ctx, "run-ultra", knownCharge()); err != nil {
		t.Fatal(err)
	}
	if !verified.Load() {
		t.Fatal("independent verifier was skipped")
	}
}
func TestExtremeDecideCannotSkipReviewAndVerifier(t *testing.T) {
	ctx := context.Background()
	extremeProviderCLIs(t)
	s, _ := marshalFixture(t, 1)
	s.Gate = cloud.Authorize(ctx, cloud.Config{}, t.TempDir(), "test").Gate
	s.ModelProvider = "model"
	s.ApprovalActor = func(context.Context, string, string) (string, error) { return "marshal-extreme", nil }
	if _, err := s.StartPlanning(ctx, "run", "write", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	d, err := s.Dispatch(ctx, "run", "a", "write")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CollectHandIn(ctx, "run", d); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Review(ctx, "run", "a", knownCharge()); err == nil {
		t.Fatal("ULTRA review without cross-review passed")
	}
	s.CrossReview = func(context.Context, marshal.Task, marshal.HandIn, marshal.Control) (marshal.Review, string, error) {
		return marshal.Review{Verdict: marshal.VerdictAccept, Reviewer: "second", EvidenceRefs: []string{"check:test -f a.txt"}}, "other", nil
	}
	if v, err := s.Review(ctx, "run", "a", knownCharge()); err != nil || v != marshal.VerdictAccept {
		t.Fatalf("review %s %v", v, err)
	}
	if err = s.Merge(ctx, "run", "a"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.VerifyMerged(ctx, "run", knownCharge()); err == nil {
		t.Fatal("ULTRA verified without independent verifier")
	}
	s.VerifierProvider = func(context.Context, marshal.Run) (string, error) { return "verifier", nil }
	s.IndependentVerify = func(_ context.Context, run marshal.Run, head string, session verification.Session) (marshal.VerifierEvidence, error) {
		if session.Binding.TreeDigest != head {
			return marshal.VerifierEvidence{}, errors.New("verifier saw a different commit")
		}
		return marshal.VerifierEvidence{Reviewer: "verifier", Provider: "verifier", Commit: head, Verdict: "pass", InputDigest: verifierInputDigest(run, head, session)}, nil
	}
	if v, err := s.VerifyMerged(ctx, "run", knownCharge()); err != nil || v != verification.VerifiedComplete {
		t.Fatalf("verify %s %v", v, err)
	}
}

// Provider discovery must never reach installed CLIs in these regressions.
func extremeProviderCLIs(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	for _, name := range []string{"codex", "claude", "opencode", "agy"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n[ \"$1\" = --version ] && { echo 'test 1.0'; exit 0; }\nexit 1\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func setupExtremeService(t *testing.T, n int) (*MarshalService, string) {
	t.Helper()
	ctx := context.Background()
	extremeProviderCLIs(t)
	s, repo := marshalFixture(t, n)
	s.Gate = cloud.Authorize(ctx, cloud.Config{}, t.TempDir(), "test").Gate
	if marshal.TierPolicy(s.Gate, marshal.DefaultSettings()).Tier != marshal.Ultra {
		t.Fatal("local authorization did not select ULTRA")
	}
	s.ModelProvider = "test"
	s.CrossReview = func(_ context.Context, task marshal.Task, h marshal.HandIn, _ marshal.Control) (marshal.Review, string, error) {
		ref := "check:test -f " + task.PlanTaskID + ".txt"
		return marshal.Review{Verdict: marshal.VerdictAccept, Reviewer: "second", EvidenceRefs: []string{ref}}, "test", nil
	}
	s.VerifierProvider = func(context.Context, marshal.Run) (string, error) { return "test", nil }
	s.IndependentVerify = func(_ context.Context, run marshal.Run, head string, session verification.Session) (marshal.VerifierEvidence, error) {
		return marshal.VerifierEvidence{Reviewer: "verifier", Provider: "verifier", Commit: head, Verdict: "pass", InputDigest: verifierInputDigest(run, head, session)}, nil
	}
	return s, repo
}

func TestExtremePerProviderCaps(t *testing.T) {
	ctx := context.Background()
	// 5 codex tasks (a..e) and 3 claude tasks (f..h) = 8 tasks
	s, _ := setupExtremeService(t, 8)

	fakeModel := s.Model.(marshalFakeModel)
	draft := fakeModel.draft
	for i := range draft.Tasks {
		draft.Tasks[i].DependsOn = nil
		draft.Plan.Tasks[i].DependsOn = nil
		if i < 5 {
			draft.Tasks[i].Worker = "codex"
		} else {
			draft.Tasks[i].Worker = "claude"
		}
	}
	g, err := plan.BuildGraph(draft.Plan.Tasks)
	if err != nil {
		t.Fatal(err)
	}
	draft.Plan.Graph = g
	s.Model = marshalFakeModel{draft: draft, review: fakeModel.review}

	var activeCodex, maxCodex atomic.Int32
	var activeClaude, maxClaude atomic.Int32
	codexGate := make(chan struct{})
	var codexOnce sync.Once
	claudeGate := make(chan struct{})
	var claudeOnce sync.Once

	s.Drivers["codex"] = driver.Governed{
		Provider: "codex",
		Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
			cur := activeCodex.Add(1)
			for {
				old := maxCodex.Load()
				if cur <= old || maxCodex.CompareAndSwap(old, cur) {
					break
				}
			}
			if cur == 4 {
				codexOnce.Do(func() { close(codexGate) })
			} else {
				select {
				case <-codexGate:
				case <-time.After(5 * time.Second):
				}
			}
			activeCodex.Add(-1)
			return nil, os.WriteFile(filepath.Join(req.Worktree, req.Task.PlanTaskID+".txt"), []byte("done\n"), 0600)
		},
	}
	s.Drivers["claude"] = driver.Governed{
		Provider: "claude",
		Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
			cur := activeClaude.Add(1)
			for {
				old := maxClaude.Load()
				if cur <= old || maxClaude.CompareAndSwap(old, cur) {
					break
				}
			}
			if cur == 2 {
				claudeOnce.Do(func() { close(claudeGate) })
			} else {
				select {
				case <-claudeGate:
				case <-time.After(5 * time.Second):
				}
			}
			activeClaude.Add(-1)
			return nil, os.WriteFile(filepath.Join(req.Worktree, req.Task.PlanTaskID+".txt"), []byte("done\n"), 0600)
		},
	}

	if _, err := s.StartPlanning(ctx, "run-provider-caps", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run-provider-caps"); err != nil {
		t.Fatal(err)
	}

	finalRun, err := s.Execute(ctx, "run-provider-caps", marshalBrief, nil)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	if maxCodex.Load() > 4 {
		t.Fatalf("expected codex peak concurrency <= 4, got %d", maxCodex.Load())
	}
	if maxClaude.Load() > 2 {
		t.Fatalf("expected claude peak concurrency <= 2, got %d", maxClaude.Load())
	}
	if maxCodex.Load() != 4 {
		t.Fatalf("expected codex peak concurrency to reach default cap of 4, got %d", maxCodex.Load())
	}
	if maxClaude.Load() != 2 {
		t.Fatalf("expected claude peak concurrency to reach default cap of 2, got %d", maxClaude.Load())
	}

	for _, task := range finalRun.Tasks {
		if task.State != marshal.Merged {
			t.Fatalf("task %s not merged: %s", task.PlanTaskID, task.State)
		}
	}
}

func TestExtremeGlobalCap(t *testing.T) {
	ctx := context.Background()
	// 14 tasks: 4 codex (a..d), 2 claude (e..f), 4 agy (g..j), 4 opencode (k..n)
	s, _ := setupExtremeService(t, 14)

	fakeModel := s.Model.(marshalFakeModel)
	draft := fakeModel.draft
	for i := range draft.Tasks {
		draft.Tasks[i].DependsOn = nil
		draft.Plan.Tasks[i].DependsOn = nil
		switch {
		case i < 4:
			draft.Tasks[i].Worker = "codex"
		case i < 6:
			draft.Tasks[i].Worker = "claude"
		case i < 10:
			draft.Tasks[i].Worker = "agy"
		default:
			draft.Tasks[i].Worker = "opencode"
		}
	}
	g, err := plan.BuildGraph(draft.Plan.Tasks)
	if err != nil {
		t.Fatal(err)
	}
	draft.Plan.Graph = g
	s.Model = marshalFakeModel{draft: draft, review: fakeModel.review}

	var activeGlobal, maxGlobal atomic.Int32
	globalGate := make(chan struct{})
	var globalOnce sync.Once
	makeDriver := func(provider string) driver.Governed {
		return driver.Governed{
			Provider: provider,
			Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
				cur := activeGlobal.Add(1)
				for {
					old := maxGlobal.Load()
					if cur <= old || maxGlobal.CompareAndSwap(old, cur) {
						break
					}
				}
				if cur == 12 {
					globalOnce.Do(func() { close(globalGate) })
				} else {
					select {
					case <-globalGate:
					case <-time.After(5 * time.Second):
					}
				}
				activeGlobal.Add(-1)
				return nil, os.WriteFile(filepath.Join(req.Worktree, req.Task.PlanTaskID+".txt"), []byte("done\n"), 0600)
			},
		}
	}

	for _, p := range []string{"codex", "claude", "agy", "opencode"} {
		s.Drivers[p] = makeDriver(p)
	}

	if _, err := s.StartPlanning(ctx, "run-global-cap", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run-global-cap"); err != nil {
		t.Fatal(err)
	}

	finalRun, err := s.Execute(ctx, "run-global-cap", marshalBrief, nil)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	if maxGlobal.Load() > 12 {
		t.Fatalf("expected global cap <= 12, got peak concurrency %d", maxGlobal.Load())
	}
	if maxGlobal.Load() != 12 {
		t.Fatalf("expected peak concurrency to reach global cap 12, got %d", maxGlobal.Load())
	}
	for _, task := range finalRun.Tasks {
		if task.State != marshal.Merged {
			t.Fatalf("task %s not merged: %s", task.PlanTaskID, task.State)
		}
	}
}

func TestExtremeOverlappingFileScopesSerialized(t *testing.T) {
	ctx := context.Background()
	// 3 tasks: 'a' and 'b' share "shared.txt", 'c' has "c.txt"
	s, _ := setupExtremeService(t, 3)

	fakeModel := s.Model.(marshalFakeModel)
	draft := fakeModel.draft
	for i := range draft.Tasks {
		draft.Tasks[i].DependsOn = nil
		draft.Plan.Tasks[i].DependsOn = nil
	}
	draft.Tasks[0].Worker = "codex"
	draft.Tasks[0].Files = []string{"a.txt", "shared.txt"}
	draft.Plan.Tasks[0].Paths = []string{"a.txt", "shared.txt"}

	draft.Tasks[1].Worker = "claude"
	draft.Tasks[1].Files = []string{"b.txt", "shared.txt"}
	draft.Plan.Tasks[1].Paths = []string{"b.txt", "shared.txt"}

	draft.Tasks[2].Worker = "agy"
	draft.Tasks[2].Files = []string{"c.txt"}
	draft.Plan.Tasks[2].Paths = []string{"c.txt"}

	g, err := plan.BuildGraph(draft.Plan.Tasks)
	if err != nil {
		t.Fatal(err)
	}
	draft.Plan.Graph = g
	s.Model = marshalFakeModel{draft: draft, review: fakeModel.review}

	var aRunning, bRunning atomic.Bool
	var overlapDetected atomic.Bool
	var aAndCConcurrent atomic.Bool
	aStarted := make(chan struct{})
	cStarted := make(chan struct{})

	s.Drivers["codex"] = driver.Governed{
		Provider: "codex",
		Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
			aRunning.Store(true)
			close(aStarted)
			if bRunning.Load() {
				overlapDetected.Store(true)
			}
			select {
			case <-cStarted:
				aAndCConcurrent.Store(true)
			case <-time.After(2 * time.Second):
			}
			if bRunning.Load() {
				overlapDetected.Store(true)
			}
			aRunning.Store(false)
			return nil, os.WriteFile(filepath.Join(req.Worktree, "a.txt"), []byte("done\n"), 0600)
		},
	}
	s.Drivers["claude"] = driver.Governed{
		Provider: "claude",
		Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
			bRunning.Store(true)
			if aRunning.Load() {
				overlapDetected.Store(true)
			}
			time.Sleep(30 * time.Millisecond)
			if aRunning.Load() {
				overlapDetected.Store(true)
			}
			bRunning.Store(false)
			return nil, os.WriteFile(filepath.Join(req.Worktree, "b.txt"), []byte("done\n"), 0600)
		},
	}
	s.Drivers["agy"] = driver.Governed{
		Provider: "agy",
		Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
			close(cStarted)
			select {
			case <-aStarted:
				aAndCConcurrent.Store(true)
			case <-time.After(2 * time.Second):
			}
			return nil, os.WriteFile(filepath.Join(req.Worktree, "c.txt"), []byte("done\n"), 0600)
		},
	}

	if _, err := s.StartPlanning(ctx, "run-overlapping", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run-overlapping"); err != nil {
		t.Fatal(err)
	}

	finalRun, err := s.Execute(ctx, "run-overlapping", marshalBrief, nil)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	if overlapDetected.Load() {
		t.Fatal("tasks with overlapping file scopes ran concurrently")
	}
	if !aAndCConcurrent.Load() {
		t.Fatal("independent ready tasks a and c should have run concurrently")
	}
	for _, task := range finalRun.Tasks {
		if task.State != marshal.Merged {
			t.Fatalf("task %s not merged: %s", task.PlanTaskID, task.State)
		}
	}
}

func TestExtremeDependenciesRespected(t *testing.T) {
	ctx := context.Background()
	// 3 tasks: Task a (no deps), Task b (depends on a), Task c (no deps)
	s, _ := setupExtremeService(t, 3)

	fakeModel := s.Model.(marshalFakeModel)
	draft := fakeModel.draft

	draft.Tasks[0].Worker = "codex"
	draft.Tasks[0].Files = []string{"a.txt"}
	draft.Tasks[0].DependsOn = nil
	draft.Plan.Tasks[0].Paths = []string{"a.txt"}
	draft.Plan.Tasks[0].DependsOn = nil

	draft.Tasks[1].Worker = "codex"
	draft.Tasks[1].Files = []string{"b.txt"}
	draft.Tasks[1].DependsOn = []string{"a"}
	draft.Plan.Tasks[1].Paths = []string{"b.txt"}
	draft.Plan.Tasks[1].DependsOn = []string{"a"}

	draft.Tasks[2].Worker = "claude"
	draft.Tasks[2].Files = []string{"c.txt"}
	draft.Tasks[2].DependsOn = nil
	draft.Plan.Tasks[2].Paths = []string{"c.txt"}
	draft.Plan.Tasks[2].DependsOn = nil

	g, err := plan.BuildGraph(draft.Plan.Tasks)
	if err != nil {
		t.Fatal(err)
	}
	draft.Plan.Graph = g
	s.Model = marshalFakeModel{draft: draft, review: fakeModel.review}

	var bStartedBeforeAMerged atomic.Bool
	var aRunning, cRunning atomic.Bool
	var aAndCConcurrent atomic.Bool
	aStarted := make(chan struct{})
	cStarted := make(chan struct{})

	s.Drivers["codex"] = driver.Governed{
		Provider: "codex",
		Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
			if req.Task.PlanTaskID == "a" {
				aRunning.Store(true)
				close(aStarted)
				select {
				case <-cStarted:
					aAndCConcurrent.Store(true)
				case <-time.After(2 * time.Second):
				}
				aRunning.Store(false)
			} else if req.Task.PlanTaskID == "b" {
				rec, err := s.Store.GetMarshalRun(ctx, s.ProjectID, "run-deps")
				if err != nil || rec.Value.Tasks[0].State != marshal.Merged {
					bStartedBeforeAMerged.Store(true)
				}
			}
			return nil, os.WriteFile(filepath.Join(req.Worktree, req.Task.PlanTaskID+".txt"), []byte("done\n"), 0600)
		},
	}
	s.Drivers["claude"] = driver.Governed{
		Provider: "claude",
		Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
			cRunning.Store(true)
			close(cStarted)
			select {
			case <-aStarted:
				aAndCConcurrent.Store(true)
			case <-time.After(2 * time.Second):
			}
			cRunning.Store(false)
			return nil, os.WriteFile(filepath.Join(req.Worktree, req.Task.PlanTaskID+".txt"), []byte("done\n"), 0600)
		},
	}

	if _, err := s.StartPlanning(ctx, "run-deps", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run-deps"); err != nil {
		t.Fatal(err)
	}

	finalRun, err := s.Execute(ctx, "run-deps", marshalBrief, nil)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	if bStartedBeforeAMerged.Load() {
		t.Fatal("task b started before its dependency task a was merged")
	}
	if !aAndCConcurrent.Load() {
		t.Fatal("independent ready tasks a and c should have run concurrently")
	}
	for _, task := range finalRun.Tasks {
		if task.State != marshal.Merged {
			t.Fatalf("task %s not merged: %s", task.PlanTaskID, task.State)
		}
	}
}

func TestExtremeConfigurableLimitsViaSettings(t *testing.T) {
	ctx := context.Background()
	s, _ := setupExtremeService(t, 4)

	settings, err := s.Store.GetMarshalSettings(ctx, s.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	settings.Value.ProviderLimits.Codex = 1
	settings.Value.GlobalCap = 2
	if _, err := s.Store.SetMarshalSettings(ctx, s.ProjectID, settings.Value, settings.Revision); err != nil {
		t.Fatal(err)
	}

	fakeModel := s.Model.(marshalFakeModel)
	draft := fakeModel.draft
	for i := range draft.Tasks {
		draft.Tasks[i].DependsOn = nil
		draft.Plan.Tasks[i].DependsOn = nil
		draft.Tasks[i].Worker = "codex"
	}
	g, err := plan.BuildGraph(draft.Plan.Tasks)
	if err != nil {
		t.Fatal(err)
	}
	draft.Plan.Graph = g
	s.Model = marshalFakeModel{draft: draft, review: fakeModel.review}

	var activeCodex, maxCodex atomic.Int32
	s.Drivers["codex"] = driver.Governed{
		Provider: "codex",
		Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
			cur := activeCodex.Add(1)
			for {
				old := maxCodex.Load()
				if cur <= old || maxCodex.CompareAndSwap(old, cur) {
					break
				}
			}
			time.Sleep(30 * time.Millisecond)
			activeCodex.Add(-1)
			return nil, os.WriteFile(filepath.Join(req.Worktree, req.Task.PlanTaskID+".txt"), []byte("done\n"), 0600)
		},
	}

	if _, err := s.StartPlanning(ctx, "run-config", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run-config"); err != nil {
		t.Fatal(err)
	}

	finalRun, err := s.Execute(ctx, "run-config", marshalBrief, nil)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	if maxCodex.Load() != 1 {
		t.Fatalf("expected codex peak concurrency to be limited to configured 1, got %d", maxCodex.Load())
	}
	for _, task := range finalRun.Tasks {
		if task.State != marshal.Merged {
			t.Fatalf("task %s not merged: %s", task.PlanTaskID, task.State)
		}
	}
}

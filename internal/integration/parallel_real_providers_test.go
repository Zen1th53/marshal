package integration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/claude"
	"github.com/Zen1th53/marshal/internal/adapter/codex"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/memory/working"
	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/worker"
)

// TestRealParallelProviderAgentsSharedMemory is opt-in because it invokes two
// authenticated external providers. The providers start concurrently, then
// exchange verified findings through the canonical task cursor at an explicit
// turn boundary. MARSHAL never attempts to mutate an opaque token stream.
func TestRealParallelProviderAgentsSharedMemory(t *testing.T) {
	useClaude := os.Getenv("MARSHAL_TEST_REAL_PARALLEL_CODEX_CLAUDE") == "1"
	useTwoCodexAgents := os.Getenv("MARSHAL_TEST_REAL_PARALLEL_CODEX_AGENTS") == "1"
	if !useClaude && !useTwoCodexAgents {
		t.Skip("set MARSHAL_TEST_REAL_PARALLEL_CODEX_CLAUDE=1 or MARSHAL_TEST_REAL_PARALLEL_CODEX_AGENTS=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	repo := runtimeIntegrationRepo(t)
	if err := os.WriteFile(filepath.Join(repo.Path(), "codex-fact.txt"), []byte("CODEx fact: configuration lives in config/foo.yaml\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo.Path(), "claude-fact.txt"), []byte("Claude fact: validation command is go test ./internal/app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	rt, err := app.Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	codexBinary, err := project.FindBinary("codex")
	if err != nil {
		t.Fatal(err)
	}
	runner := worker.New(4*time.Minute, 2*time.Second, 8<<20)
	providers := []struct {
		name      string
		principal string
		client    adapter.Adapter
		factFile  string
		needle    string
		slot      working.SlotType
	}{
		{"codex-a", "agent-real-codex-a", codex.New(codexBinary, runner), "codex-fact.txt", "config/foo.yaml", working.SlotFinding},
	}
	if useClaude {
		claudeBinary, findErr := project.FindBinary("claude")
		if findErr != nil {
			t.Fatal(findErr)
		}
		providers = append(providers, struct {
			name      string
			principal string
			client    adapter.Adapter
			factFile  string
			needle    string
			slot      working.SlotType
		}{"claude", "agent-real-claude", claude.New(claudeBinary, runner), "claude-fact.txt", "go test ./internal/app", working.SlotToolResults})
	} else {
		providers = append(providers, struct {
			name      string
			principal string
			client    adapter.Adapter
			factFile  string
			needle    string
			slot      working.SlotType
		}{"codex-b", "agent-real-codex-b", codex.New(codexBinary, runner), "claude-fact.txt", "go test ./internal/app", working.SlotToolResults})
	}
	for _, provider := range providers {
		if _, err := provider.client.Probe(ctx); err != nil {
			t.Fatalf("probe %s: %v", provider.name, err)
		}
	}
	const taskID = "TASK-REAL-PARALLEL-MEMORY"
	head := repo.HEAD(t)
	principals := []authz.Principal{memoryReader(providers[0].principal), memoryReader(providers[1].principal)}
	grantMemoryTaskAccess(t, rt, taskID, principals...)

	type providerResult struct {
		index  int
		result adapter.Result
		err    error
	}
	type refreshResult struct {
		index int
		err   error
	}
	start := make(chan struct{})
	results := make(chan providerResult, len(providers))
	readyToRefresh := make(chan int, len(providers))
	refreshStart := make(chan struct{})
	refreshResults := make(chan refreshResult, len(providers))
	var wg sync.WaitGroup
	for i := range providers {
		provider := providers[i]
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			result, runErr := provider.client.Run(ctx, adapter.Request{
				TaskID: taskID, Worktree: repo.Path(), BaseCommit: head, HeadCommit: head,
				Title:             "Read " + provider.factFile + " and report its exact factual value. Do not modify files.",
				AllowedOperations: []string{"filesystem.read"},
			})
			results <- providerResult{index: index, result: result, err: runErr}
			if runErr != nil || result.Status != adapter.StatusSuccess || !strings.Contains(result.FinalText, provider.needle) {
				return
			}
			if _, publishErr := rt.Memory().SetTaskSlotWithProvenance(ctx, principals[index], "PROJECT-local", taskID, provider.slot, result.FinalText, false,
				app.WorkingProvenance{Provider: provider.name, RunID: "REAL-" + strings.ToUpper(provider.name)}); publishErr != nil {
				refreshResults <- refreshResult{index: index, err: publishErr}
				readyToRefresh <- index
				return
			}
			// The agent workflow remains active at an explicit turn boundary.
			// Once both peers have published, each refreshes canonical task state.
			readyToRefresh <- index
			<-refreshStart
			page, refreshErr := rt.Memory().RefreshTaskMemory(ctx, principals[index], "PROJECT-local", taskID, 0, 20)
			if refreshErr == nil {
				combined := ""
				for _, change := range page.Changes {
					if change.Slot != nil {
						combined += change.Slot.Value + "\n"
					}
				}
				if !strings.Contains(combined, providers[1-index].needle) {
					refreshErr = errors.New("peer finding was not visible at the live refresh boundary")
				}
			}
			refreshResults <- refreshResult{index: index, err: refreshErr}
		}(i)
	}
	close(start)
	got := make([]adapter.Result, len(providers))
	for range providers {
		outcome := <-results
		if outcome.err != nil || outcome.result.Status != adapter.StatusSuccess {
			t.Fatalf("%s run: status=%s exit=%d err=%v stdout=%s stderr=%s final=%s", providers[outcome.index].name, outcome.result.Status, outcome.result.ExitCode, outcome.err, outcome.result.Stdout, outcome.result.Stderr, outcome.result.FinalText)
		}
		if !strings.Contains(outcome.result.FinalText, providers[outcome.index].needle) {
			t.Fatalf("%s did not verify fixture fact: %q", providers[outcome.index].name, outcome.result.FinalText)
		}
		got[outcome.index] = outcome.result
	}
	for range providers {
		<-readyToRefresh
	}
	close(refreshStart)
	wg.Wait()
	for range providers {
		outcome := <-refreshResults
		if outcome.err != nil {
			t.Fatalf("%s live refresh: %v", providers[outcome.index].name, outcome.err)
		}
	}
	if !intervalsOverlap(got[0].StartedAt, got[0].EndedAt, got[1].StartedAt, got[1].EndedAt) {
		t.Fatalf("provider calls did not overlap: codex=%s..%s claude=%s..%s", got[0].StartedAt, got[0].EndedAt, got[1].StartedAt, got[1].EndedAt)
	}

	plan, err := rt.Memory().SetTaskSlot(ctx, principals[0], "PROJECT-local", taskID, working.SlotPlanState, "revision one", false)
	if err != nil {
		t.Fatal(err)
	}
	casStart := make(chan struct{})
	casErrors := make(chan error, 2)
	for i := range principals {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-casStart
			_, updateErr := rt.Memory().UpdateTaskSlotCAS(ctx, principals[index], "PROJECT-local", taskID, working.SlotPlanState, plan.Revision, providers[index].name+" proposal")
			casErrors <- updateErr
		}(i)
	}
	close(casStart)
	wg.Wait()
	close(casErrors)
	winners, conflicts := 0, 0
	for casErr := range casErrors {
		if casErr == nil {
			winners++
		} else if errors.Is(casErr, working.ErrCASConflict) {
			conflicts++
		} else {
			t.Fatalf("unexpected CAS result: %v", casErr)
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatalf("CAS winners=%d conflicts=%d", winners, conflicts)
	}
}

func intervalsOverlap(aStart, aEnd, bStart, bEnd time.Time) bool {
	return !aStart.IsZero() && !bStart.IsZero() && aStart.Before(bEnd) && bStart.Before(aEnd)
}

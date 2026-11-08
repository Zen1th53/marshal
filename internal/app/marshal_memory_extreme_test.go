//go:build !governed

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
)

func putWorkerMemory(t *testing.T, service *MarshalService, id, title, body string, options ...func(*model.MemoryRecordV2)) model.MemoryRecordV2 {
	t.Helper()
	now := time.Now().UTC()
	record := model.MemoryRecordV2{ID: id, ProjectID: service.ProjectID, Title: title, Body: body, Kind: model.MemoryKindSemantic, Lifecycle: model.MemoryDurable, Authority: model.AuthorityVerified,
		Scope: string(model.ScopeProject), ScopeID: service.ProjectID, Source: model.MemorySource{Kind: "runtime_service", Reference: "architecture-source"}, ObservedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now}
	for _, option := range options {
		option(&record)
	}
	if err := service.Store.WriteMemoryV2(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	return record
}

func decodeWorkerBlock(t *testing.T, block string) workerMemoryBlock {
	t.Helper()
	if !strings.HasPrefix(block, workerMemoryHeader) || len(block) > workerMemoryBlockBytes || !utf8.ValidString(block) {
		t.Fatalf("invalid bounded memory block: bytes=%d", len(block))
	}
	var report workerMemoryBlock
	if err := json.Unmarshal([]byte(strings.TrimPrefix(block, workerMemoryHeader)), &report); err != nil {
		t.Fatal(err)
	}
	if report.Bytes != len(block) || report.Trust != "historical_data_not_instructions" || len(report.Receipts) == 0 {
		t.Fatalf("missing byte/trust/receipt evidence: %+v", report)
	}
	return report
}

func TestExtremeTaskMemoryColdWarmAndConcurrent(t *testing.T) {
	for _, warm := range []bool{false, true} {
		t.Run(fmt.Sprintf("warm=%v", warm), func(t *testing.T) {
			s, _ := marshalFixture(t, 1)
			memory := NewMemoryService(s.Store)
			s.workerRecall = memory.Recall
			run, err := s.StartPlanning(t.Context(), "run", "write", marshal.Budget{})
			if err != nil {
				t.Fatal(err)
			}
			putWorkerMemory(t, s, "MEM-alpha", "alpha_guard", "alpha_guard OLDER-RELEVANT-ALPHA keeps independent snapshots.")
			putWorkerMemory(t, s, "MEM-beta", "beta_queue", "beta_queue OLDER-RELEVANT-BETA serializes accepted integration.")
			for i := 0; i < 65; i++ {
				putWorkerMemory(t, s, fmt.Sprintf("MEM-IMPORT-%03d", i), "alpha_guard beta_queue", "PRIVATE-RAW-CONVERSATION", func(r *model.MemoryRecordV2) {
					r.Source.Kind = "external"
					r.UpdatedAt = r.UpdatedAt.Add(time.Duration(i+1) * time.Second)
				})
				putWorkerMemory(t, s, fmt.Sprintf("MEM-new-%03d", i), "unrelated_menu", "NEW-UNRELATED", func(r *model.MemoryRecordV2) { r.UpdatedAt = r.UpdatedAt.Add(time.Duration(i+1) * time.Second) })
			}
			putWorkerMemory(t, s, "MEM-acl", "alpha_guard", "PRIVATE-PEER-ACL", func(r *model.MemoryRecordV2) { r.ACLScope = "peer" })
			putWorkerMemory(t, s, "MEM-stale", "alpha_guard", "STALE-ASSERTION", func(r *model.MemoryRecordV2) { r.Lifecycle = model.MemoryStale })
			putWorkerMemory(t, s, "MEM-expired", "alpha_guard", "EXPIRED-ASSERTION", func(r *model.MemoryRecordV2) {
				past := time.Now().Add(-time.Hour)
				r.ValidFrom = past.Add(-time.Hour)
				r.ValidTo = &past
			})
			putWorkerMemory(t, s, "MEM-conflict", "alpha_guard", "CONFLICTED-ASSERTION", func(r *model.MemoryRecordV2) { r.Lifecycle = model.MemoryConflicted })
			putWorkerMemory(t, s, "MEM-session", "alpha_guard", "PRIVATE-SESSION", func(r *model.MemoryRecordV2) { r.Scope = string(model.ScopeSession); r.ScopeID = "session-peer" })
			if warm {
				if err := memory.RebuildProjections(t.Context(), s.ProjectID); err != nil {
					t.Fatal(err)
				}
			}
			// Populate the shared cache with Marshal-only history before workers read.
			if _, err := memory.Recall(t.Context(), testPrincipal("marshal"), RecallRequest{ProjectID: s.ProjectID, Query: "MEM-IMPORT-000"}); err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			for _, cue := range []string{"alpha_guard", "beta_queue"} {
				wg.Add(1)
				go func(cue string) {
					defer wg.Done()
					task := run.Tasks[0]
					task.Files = []string{"internal/" + cue + ".go"}
					task.Title = cue
					task.Criteria = nil
					bc, err := s.briefContext(t.Context(), "run", run, task)
					if err != nil {
						t.Error(err)
						return
					}
					report := decodeWorkerBlock(t, bc.MemoryContext)
					if len(bc.Memory) != 0 || len(report.Records) != 1 || report.Records[0].ID != "MEM-"+strings.Split(cue, "_")[0] || report.Records[0].SourceReference != "architecture-source" {
						t.Errorf("wrong task-relevant snapshot: %+v", report)
					}
					for _, forbidden := range []string{"PRIVATE-", "NEW-UNRELATED", "STALE-ASSERTION", "EXPIRED-ASSERTION", "CONFLICTED-ASSERTION"} {
						if strings.Contains(bc.MemoryContext, forbidden) {
							t.Errorf("worker received %s", forbidden)
						}
					}
				}(cue)
			}
			wg.Wait()
		})
	}
}

func TestExtremeTaskMemoryProductionDispatchFreshness(t *testing.T) {
	s, _ := marshalFixture(t, 2)
	memory := NewMemoryService(s.Store)
	var mu sync.Mutex
	requests := map[string]RecallRequest{}
	s.workerRecall = func(ctx context.Context, p authz.Principal, r RecallRequest) (RecallResponse, error) {
		if p.ID != "worker" || !r.ExcludeSessionHistory || len(r.AllowedScopeIDs) != 1 || r.AllowedScopeIDs[0] != s.ProjectID {
			t.Error("worker principal/scope binding changed")
		}
		mu.Lock()
		requests[r.TaskID] = r
		mu.Unlock()
		return memory.Recall(ctx, p, r)
	}
	putWorkerMemory(t, s, "MEM-a", "a", "a PRODUCTION-FIRST-NOTE")
	putWorkerMemory(t, s, "MEM-b", "b", "b PRODUCTION-DEPENDENT-NOTE")
	putWorkerMemory(t, s, "MEM-old-head", "b", "b HISTORICAL-OLD-RUNTIME", func(r *model.MemoryRecordV2) { r.Kind = model.MemoryKindEpisodic; r.HeadCommit = "older-source-commit" })
	putWorkerMemory(t, s, "MEM-long", "b", strings.Repeat("b 東京 <proof>&\"quoted\" \\ \r\n", 600))
	baseDriver := s.Drivers["worker"].(driver.Governed)
	original := baseDriver.Run
	baseDriver.Run = func(ctx context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		mu.Lock()
		recall := requests[req.Task.PlanTaskID]
		mu.Unlock()
		actual := marshalGit(t, req.Worktree, "rev-parse", "HEAD")
		if recall.CurrentHead != actual || recall.RunID != "run" || recall.Provider != "worker" {
			t.Errorf("brief recall head=%q actual=%q", recall.CurrentHead, actual)
		}
		report := decodeWorkerBlock(t, req.Brief)
		if strings.Contains(req.Brief, "HISTORICAL-OLD-RUNTIME") {
			t.Error("historical runtime outcome became current task fact")
		}
		if req.Task.PlanTaskID == "b" && !report.Truncated {
			t.Error("long Unicode memory truncation was hidden")
		}
		return original(ctx, req)
	}
	s.Drivers["worker"] = baseDriver
	if _, err := s.StartPlanning(t.Context(), "run", "write", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(t.Context(), "run"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Execute(t.Context(), "run", func(_ marshal.Task, bc BriefContext) string { return bc.MemoryContext }, nil); err != nil {
		t.Fatal(err)
	}
	if requests["a"].CurrentHead == requests["b"].CurrentHead {
		t.Fatal("dependent task retained the original run base after integration advanced")
	}
}

func TestExtremeTaskMemoryOptionalFailureAndRuntimeReuse(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	run, err := s.StartPlanning(t.Context(), "run", "write", marshal.Budget{})
	if err != nil {
		t.Fatal(err)
	}
	s.workerRecall = func(context.Context, authz.Principal, RecallRequest) (RecallResponse, error) {
		return RecallResponse{}, authz.ErrUnauthorized
	}
	activity := ""
	s.RecordActivity = func(value string) { activity = value }
	bc, err := s.briefContext(t.Context(), "run", run, run.Tasks[0])
	if err != nil || bc.MemoryContext != "" || len(bc.Memory) != 0 || bc.MemoryStatus != "unavailable" || activity == "" {
		t.Fatalf("optional recall blocked or fell back to raw records: %+v %v", bc, err)
	}
	if _, err := s.Approve(t.Context(), "run"); err != nil {
		t.Fatal(err)
	}
	completed, err := s.Execute(t.Context(), "run", func(_ marshal.Task, bc BriefContext) string {
		if bc.MemoryContext != "" || bc.MemoryStatus != "unavailable" {
			t.Error("unauthorized optional memory reached a worker")
		}
		return "complete approved task"
	}, nil)
	if err != nil || completed.Tasks[0].State != marshal.Merged {
		t.Fatal("optional recall failure blocked authorized task progress")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.briefContext(ctx, "run", run, run.Tasks[0]); !errors.Is(err, context.Canceled) {
		t.Fatal("owner cancellation was swallowed")
	}
	repo := runtimeRepo(t)
	if _, err := Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if runtime.Marshal().workerRecall == nil {
		t.Fatal("runtime did not inject existing memory service")
	}
	providerCalls := 0
	provider := sourceChangingCandidate{change: func() error { providerCalls++; return nil }}
	runtime.Memory().RegisterCandidateProvider(provider)
	// Observe a provider registered on the runtime through a separately obtained Marshal service.
	project := runtime.ProjectID()
	_, err = runtime.Marshal().workerRecall(t.Context(), testPrincipal("worker"), RecallRequest{ProjectID: project, Query: "marker"})
	if err != nil {
		t.Fatal(err)
	}
	// The scoped callback is a method value of this runtime service, not a fresh projection.
	if providerCalls != 1 {
		t.Fatal("shared runtime providers were lost")
	}
}

func TestExtremeTaskMemoryOptionalDeadline(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	run, err := s.StartPlanning(t.Context(), "run", "write", marshal.Budget{})
	if err != nil {
		t.Fatal(err)
	}
	s.workerRecall = func(ctx context.Context, _ authz.Principal, _ RecallRequest) (RecallResponse, error) {
		<-ctx.Done()
		return RecallResponse{}, ctx.Err()
	}
	started := time.Now()
	bc, err := s.briefContext(t.Context(), "run", run, run.Tasks[0])
	if err != nil || bc.MemoryStatus != "unavailable" || bc.MemoryContext != "" || time.Since(started) > 2500*time.Millisecond {
		t.Fatal("optional recall timeout leaked data or blocked task context")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := s.briefContext(ctx, "run", run, run.Tasks[0]); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("owner deadline was swallowed as optional memory failure")
	}
}

func TestExtremeTaskMemoryReturnAndReassignmentBase(t *testing.T) {
	s, repo := marshalFixture(t, 1)
	run, err := s.StartPlanning(t.Context(), "run", "write", marshal.Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "returned.txt"), []byte("returned\n"), 0600); err != nil {
		t.Fatal(err)
	}
	marshalGit(t, repo, "add", "returned.txt")
	marshalGit(t, repo, "commit", "-m", "returned")
	result := marshalGit(t, repo, "rev-parse", "HEAD")
	task := run.Tasks[0]
	task.ResultCommit = result
	task.State = marshal.Returned
	var observed string
	s.workerRecall = func(_ context.Context, _ authz.Principal, req RecallRequest) (RecallResponse, error) {
		observed = req.CurrentHead
		return RecallResponse{Context: "<marshal_memory_context/>"}, nil
	}
	if _, err := s.briefContext(t.Context(), "run", run, task); err != nil {
		t.Fatal(err)
	}
	if observed != result {
		t.Fatal("returned worker context ignored resumed result commit")
	}
	task.State = marshal.Reassigned
	if _, err := s.briefContext(t.Context(), "run", run, task); err != nil {
		t.Fatal(err)
	}
	if observed != task.BaseCommit {
		t.Fatal("reassigned worker inherited the earlier worker result as fresh context")
	}
}

func TestExtremeTaskMemoryPlanNoteAndCompleteEscapedBudget(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	s.workerRecall = NewMemoryService(s.Store).Recall
	run, err := s.StartPlanning(t.Context(), "run", "write", marshal.Budget{})
	if err != nil {
		t.Fatal(err)
	}
	run.Tasks[0].Files, run.Tasks[0].Title, run.Tasks[0].Criteria = nil, "write", nil
	run.Pack = &marshal.PlanPack{Tasks: map[string]string{run.Tasks[0].PlanTaskID: "sqlite snapshot isolation"}}
	for i := 0; i < 3; i++ {
		putWorkerMemory(t, s, fmt.Sprintf("MEM-BUDGET-%d", i), "sqlite snapshot isolation", strings.Repeat("sqlite snapshot isolation <&>東京\r\n", 300), func(record *model.MemoryRecordV2) { record.Source.Reference = strings.Repeat("<&>東京", 100) })
	}
	bc, err := s.briefContext(t.Context(), "run", run, run.Tasks[0])
	if err != nil {
		t.Fatal(err)
	}
	report := decodeWorkerBlock(t, bc.MemoryContext)
	if !report.Truncated || len(report.Records) == 0 || !strings.Contains(bc.MemoryContext, "sqlite snapshot isolation") {
		t.Fatalf("approved note query lost useful bounded data: %+v", report)
	}
	for _, item := range report.Records {
		if item.Text == "" || !utf8.ValidString(item.Text) {
			t.Fatal("budget returned an empty/useless or split UTF-8 snippet")
		}
	}
	if len(report.Records) == 3 && report.Bytes < 3500 {
		t.Fatal("escaping-heavy fixture did not exercise the complete serialized budget")
	}
}

func TestExtremeTaskMemoryFinalAdmittedWorkerAndFrozenBase(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)
	s, _ := marshalFixture(t, 1)
	memory := NewMemoryService(s.Store)
	putWorkerMemory(t, s, "MEM-CODEX-ACL", "a", "a OLD-ACTOR-PRIVATE-CODEX", func(record *model.MemoryRecordV2) { record.ACLScope = "codex" })
	putWorkerMemory(t, s, "MEM-CLAUDE-ACL", "a", "a FINAL-ACTOR-CLAUDE", func(record *model.MemoryRecordV2) { record.ACLScope = "claude" })
	if _, err := s.StartPlanning(t.Context(), "run", "write", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(t.Context(), "run"); err != nil {
		t.Fatal(err)
	}
	run, revision, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	run.Tasks[0].Worker = "codex"
	if err := s.save(t.Context(), "run", run, revision); err != nil {
		t.Fatal(err)
	}
	markProviderUnavailable("codex", s.clock().Add(time.Hour))
	baseDriver := s.Drivers["worker"].(driver.Governed)
	original := baseDriver.Run
	captured := false
	baseDriver.Run = func(ctx context.Context, request driver.Request) ([]marshal.CommandRecord, error) {
		captured = true
		if request.Task.Worker != "claude" || strings.Contains(request.Brief, "OLD-ACTOR-PRIVATE-CODEX") || !strings.Contains(request.Brief, "FINAL-ACTOR-CLAUDE") {
			t.Error("provider admission reused the old actor-authorized memory")
		}
		return original(ctx, request)
	}
	s.Drivers = map[string]driver.Driver{"codex": baseDriver, "claude": baseDriver}
	queries := 0
	s.workerRecall = func(ctx context.Context, principal authz.Principal, request RecallRequest) (RecallResponse, error) {
		queries++
		if principal.ID != "claude" || request.Provider != "claude" || request.Query == "" || len(request.Query) > 128 || queries > 3 {
			t.Error("recall bound to pre-admission actor or unbounded queries")
		}
		return memory.Recall(ctx, principal, request)
	}
	if _, err := s.Execute(t.Context(), "run", func(_ marshal.Task, bc BriefContext) string { return bc.MemoryContext }, nil); err != nil {
		t.Fatal(err)
	}
	if !captured || queries == 0 {
		t.Fatal("final admitted driver/recall was not exercised")
	}

	// Once Dispatch resolves the task, changing integration during recall must
	// not cause a second base resolution inside the memory helper.
	s2, repo := marshalFixture(t, 2)
	run2, err := s2.StartPlanning(t.Context(), "run", "write", marshal.Budget{})
	if err != nil {
		t.Fatal(err)
	}
	integration := filepath.Join(s2.Worktrees, integrationTaskID("run", run2))
	if err := os.MkdirAll(filepath.Dir(integration), 0700); err != nil {
		t.Fatal(err)
	}
	marshalGit(t, repo, "worktree", "add", "--detach", integration, run2.BaseCommit)
	task := run2.Tasks[1]
	resolved, err := s2.resolveDispatchTaskBase(t.Context(), "run", run2, task)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(integration, "advanced.txt"), []byte("later integration\n"), 0600); err != nil {
		t.Fatal(err)
	}
	marshalGit(t, integration, "add", "advanced.txt")
	marshalGit(t, integration, "commit", "-m", "later integration")
	observed := ""
	s2.workerRecall = func(_ context.Context, _ authz.Principal, request RecallRequest) (RecallResponse, error) {
		observed = request.CurrentHead
		return RecallResponse{Context: "<marshal_memory_context/>"}, nil
	}
	if _, err := s2.briefContextFromTask(t.Context(), "run", run2, resolved, true); err != nil {
		t.Fatal(err)
	}
	if observed != resolved.BaseCommit || observed == marshalGit(t, integration, "rev-parse", "HEAD") {
		t.Fatal("authorized context drifted from the frozen dispatch base")
	}
}

func TestExtremeTaskMemoryQueryChannelsAndEmptyInput(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	run, err := s.StartPlanning(t.Context(), "run", "write", marshal.Budget{})
	if err != nil {
		t.Fatal(err)
	}
	task := run.Tasks[0]
	task.Files = []string{"internal/alpha_guard.go", "internal/beta_queue.go"}
	task.Title = "Fix exact title cue"
	run.Pack = &marshal.PlanPack{Tasks: map[string]string{task.PlanTaskID: "task_note_cue"}}
	var queries []string
	s.workerRecall = func(_ context.Context, _ authz.Principal, request RecallRequest) (RecallResponse, error) {
		queries = append(queries, request.Query)
		return RecallResponse{Context: "<marshal_memory_context/>"}, nil
	}
	if _, err := s.briefContext(t.Context(), "run", run, task); err != nil {
		t.Fatal(err)
	}
	if len(queries) != 3 || queries[0] != "alpha_guard" || queries[1] != "exact title cue" || queries[2] != "task_note_cue" {
		t.Fatalf("file/title/approved note channels lost: %v", queries)
	}
	queries = nil
	task.Files, task.Criteria, task.Title, task.Instructions = nil, nil, "write", ""
	run.Pack = nil
	bc, err := s.briefContext(t.Context(), "run", run, task)
	if err != nil || len(queries) != 0 || bc.MemoryStatus != "no_query" || bc.MemoryContext != "" {
		t.Fatal("empty task cues caused recent-record retrieval")
	}
}

func TestExtremeWorkerMemoryExcludesUnusableLifecycles(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	s.workerRecall = NewMemoryService(s.Store).Recall
	putWorkerMemory(t, s, "MEM-usable", "usable", "lifecycletoken usable")
	for _, lc := range []model.MemoryLifecycle{model.MemoryObserved, model.MemoryCandidate, model.MemoryStale, model.MemoryConflicted, model.MemorySuperseded, model.MemoryTombstoned, model.MemoryRejected} {
		lc := lc
		putWorkerMemory(t, s, "MEM-"+string(lc), string(lc), "lifecycletoken "+string(lc), func(r *model.MemoryRecordV2) { r.Lifecycle = lc })
	}
	run, err := s.StartPlanning(t.Context(), "run", "write", marshal.Budget{})
	if err != nil {
		t.Fatal(err)
	}
	task := run.Tasks[0]
	task.Title = "lifecycletoken"
	bc := BriefContext{Note: "lifecycletoken"}
	if err := s.addWorkerMemory(t.Context(), "run", run, task, false, &bc); err != nil {
		t.Fatal(err)
	}
	report := decodeWorkerBlock(t, bc.MemoryContext)
	if len(report.Records) != 1 || report.Records[0].ID != "MEM-usable" {
		t.Fatalf("unusable lifecycle reached worker brief: %+v", report.Records)
	}
}

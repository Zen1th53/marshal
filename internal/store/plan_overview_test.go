package store

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestPlanOverview(t *testing.T) {
	s := openTestStore(t)
	marshalTestProject(t, s)
	ctx := t.Context()

	if _, err := s.PlanOverview(ctx, "run-x"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("missing run: %v", err)
	}
	if _, err := s.PlanOverview(ctx, ""); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("no run: %v", err)
	}
	run := marshal.Run{PlanID: "plan-1", PlanVersion: 2, ApprovalScopeDigest: "d", Tier: marshal.Ultra, Settings: marshal.DefaultSettings(), State: marshal.Dispatching, Tasks: []marshal.Task{
		{PlanTaskID: "T-1", Title: "events list", Worker: "codex", State: marshal.Merged, Files: []string{"internal/mcp/events_list.go"}},
		{PlanTaskID: "T-2", Title: "recall", Worker: "claude", State: marshal.Dispatched, DependsOn: []string{"T-1"}, Files: []string{"internal/mcp/memory_recall.go"}},
		{PlanTaskID: "T-3", Title: "bridge", Worker: "codex", State: marshal.Queued, DependsOn: []string{"T-1", "T-2"}},
	}}
	if err := s.SaveMarshalState(ctx, "p", "run-1", run, 0, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	got, err := s.PlanOverview(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.PlanID != "plan-1" || got.PlanVersion != 2 || got.RunState != marshal.Dispatching || len(got.Tasks) != 3 || len(got.Versions) != 0 {
		t.Fatalf("overview before versions: %+v", got)
	}
	if t3 := got.Tasks[2]; t3.ID != "T-3" || !slices.Equal(t3.DependsOn, []string{"T-1", "T-2"}) || t3.State != marshal.Queued {
		t.Fatalf("task T-3 %+v", t3)
	}
	if t2 := got.Tasks[1]; t2.Worker != "claude" || !slices.Equal(t2.Scope, []string{"internal/mcp/memory_recall.go"}) {
		t.Fatalf("task T-2 %+v", t2)
	}

	at := time.Date(2026, 11, 3, 13, 20, 0, 0, time.UTC)
	version := func(plan string, v, supersedes int64, state, reason string, at time.Time) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, `INSERT INTO execution_plans(plan_id,version,project_id,goal_id,goal_revision,state,mode,constitution_version,supersedes,revision_reason,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			plan, v, "p", "goal", 1, state, "ultra", "v1", supersedes, reason, at.Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	version("plan-1", 1, 0, "STALE", "", at)
	version("plan-1", 2, 1, "APPROVED", "added T-3 bridge", at.Add(21*time.Minute))
	version("plan-other", 5, 4, "APPROVED", "not this run's plan", at)
	got, err = s.PlanOverview(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	want := []PlanVersion{
		{Version: 2, State: "APPROVED", Supersedes: 1, Reason: "added T-3 bridge", At: at.Add(21 * time.Minute), Current: true},
		{Version: 1, State: "STALE", At: at},
	}
	if len(got.Versions) != len(want) {
		t.Fatalf("versions %+v", got.Versions)
	}
	for i, w := range want {
		if g := got.Versions[i]; g != w {
			t.Errorf("version %d = %+v, want %+v", i, g, w)
		}
	}

	// The versions read is bounded: the newest planVersionRows.
	for v := int64(3); v <= 14; v++ {
		version("plan-1", v, v-1, "STALE", "", at)
	}
	got, err = s.PlanOverview(ctx, "run-1")
	if err != nil || len(got.Versions) != planVersionRows || got.Versions[0].Version != 14 {
		t.Fatalf("bounded versions %d, newest %+v, %v", len(got.Versions), got.Versions[0], err)
	}
}

// storeQueryPlan is SQLite's query plan for q, its details joined.
func storeQueryPlan(t *testing.T, s *Store, q string) string {
	t.Helper()
	rows, err := s.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	return strings.Join(plan, "; ")
}

// The Plan overview reads the run by its key and the plan's versions by
// the (plan_id, version) key, newest first without a sort.
func TestPlanOverviewQueryPlans(t *testing.T) {
	s := openTestStore(t)
	for _, q := range []string{
		`SELECT data_json FROM marshal_runs WHERE run_id = 'r'`,
		`SELECT version, state, supersedes, revision_reason, created_at FROM execution_plans WHERE plan_id = 'p' ORDER BY version DESC LIMIT 10`,
	} {
		if text := storeQueryPlan(t, s, q); !strings.Contains(text, "USING") || strings.Contains(text, "TEMP B-TREE") || strings.Contains(text, "json_extract") {
			t.Errorf("%s: plan %q does not use an index", q, text)
		}
	}
}

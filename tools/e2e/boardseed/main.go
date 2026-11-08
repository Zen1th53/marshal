// Command boardseed writes a demo Marshal run into a throwaway project's
// store, so an end-to-end walk of the real TUI has a Board to look at
// without spending provider quota: tasks in every state, decisions for the
// activity pane and the header, and the owner's accepted architecture
// revision for the header chip. It mirrors the run the Board demo goldens
// paint (internal/tui/board_demo_test.go). Lanes and the inbox come from
// live workers and permission requests, which a store seed cannot fake.
//
// Usage: boardseed [-awaiting] PROJECT_ROOT
//
// -awaiting leaves the run waiting for the operator (awaiting_user with a
// verification pause), for a walk of the Board's run-stop notice.
//
// Never point it at a real project: it appends decisions to the latest run.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/store"
)

const runID = "R-7f3a"

var awaiting bool

func main() {
	args := os.Args[1:]
	if len(args) == 2 && args[0] == "-awaiting" {
		awaiting, args = true, args[1:]
	}
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: boardseed [-awaiting] PROJECT_ROOT")
		os.Exit(2)
	}
	if err := seed(context.Background(), args[0]); err != nil {
		fmt.Fprintln(os.Stderr, "boardseed:", err)
		os.Exit(1)
	}
}

func seed(ctx context.Context, root string) error {
	layout, err := project.Discover(root)
	if err != nil {
		return err
	}
	st, err := store.Open(ctx, layout.Database)
	if err != nil {
		return err
	}
	defer st.Close()
	p, err := st.Project(ctx)
	if err != nil {
		return err
	}
	type row struct {
		id, worker, file string
		state            marshal.TaskState
	}
	rows := []row{
		{"T-009", "opencode", "internal/mcp/events_list.go", marshal.Merged},
		{"T-011", "codex", "internal/mcp/run_digest.go", marshal.Accepted},
		{"T-017", "codex", "internal/mcp/memory_recall.go", marshal.Dispatched},
		{"T-018", "claude", "internal/app/memory_candidates.go", marshal.Dispatched},
		{"T-019", "agy", "internal/store/memory_restore.go", marshal.Dispatched},
		{"T-020", "codex", "internal/app/memory_bridge.go", marshal.HandedIn},
		{"T-023", "codex", "internal/netpolicy/broker_npm.go", marshal.Escalated},
		{"T-024", "claude", "internal/app/credential_lease.go", marshal.Returned},
		{"T-027", "opencode", "internal/tui/memory_review_extreme.go", marshal.Dispatched},
		{"T-031", "agy", "scripts/release/push_tags.sh", marshal.Escalated},
		{"T-033", "", "internal/store/migrations_canonical.go", marshal.Queued},
		{"T-034", "", "docs/mcp-memory.md", marshal.Queued},
	}
	run := marshal.Run{PlanID: "PLAN-demo", PlanVersion: 2, ApprovalScopeDigest: "d", BaseCommit: "base", Tier: marshal.Ultra, Settings: marshal.DefaultSettings(), State: marshal.Dispatching}
	for _, r := range rows {
		t := marshal.Task{PlanTaskID: r.id, Title: r.file, Worker: r.worker, State: r.state, Files: []string{r.file}}
		if r.id == "T-024" {
			t.ReturnsByAgent = map[string]int{"claude": 1}
		}
		run.Tasks = append(run.Tasks, t)
	}
	if awaiting {
		run.State = marshal.AwaitingUser
		run.Pause = &marshal.Pause{Reason: "verification stopped: go test ./internal/netpolicy/... failed (exit 1)", Resolutions: []string{"inspect the retained verification error and artifacts; resolve the reported cause, then /marshal resume to rerun verification"}, ResumeState: marshal.Verifying}
	}
	if err := st.SaveMarshalState(ctx, p.ID, runID, run, 0, nil, nil, 0); err != nil {
		return fmt.Errorf("save run: %w", err)
	}
	start := time.Now().UTC().Add(-13 * time.Minute)
	n := 0
	decide := func(kind events.EventType, task string, at time.Time, data map[string]any) error {
		n++
		_, err := st.AppendMarshalDecision(ctx, events.Event{ID: fmt.Sprintf("EVENT-SEED-%03d", n), Type: kind, Subject: p.ID, RunID: runID, TaskID: task, At: at, Data: data})
		return err
	}
	if err := decide(events.EventTypeMarshalPlanApproved, "", start, nil); err != nil {
		return err
	}
	for i, r := range rows {
		if r.worker == "" {
			continue
		}
		if err := decide(events.EventTypeMarshalTaskDispatched, r.id, start.Add(time.Duration(10+i)*time.Second), map[string]any{"worker": r.worker}); err != nil {
			return err
		}
	}
	for _, d := range []struct {
		kind events.EventType
		task string
		ago  time.Duration
		data map[string]any
	}{
		{events.EventTypeMarshalUsageCharged, "T-017", 5 * time.Minute, map[string]any{"charge_phase": "worker", "usage_known": true, "usage_units": float64(41000)}},
		{events.EventTypeMarshalTaskHandedIn, "T-020", 6 * time.Minute, map[string]any{"reason": "4 claims · 3 SUPPORTED 1 UNSUPPORTED"}},
		{events.EventTypeMarshalTaskAccepted, "T-011", 90 * time.Second, map[string]any{"reason": "fresh codex session · pre_delivery"}},
		{events.EventTypeMarshalTaskReturned, "T-024", 41 * time.Second, map[string]any{"reason": "review: lease ended mid-invocation → rework 1/2"}},
		{events.EventTypeMarshalEscalated, "T-031", 30 * time.Second, map[string]any{"reason": "hard gate: git push --force origin main"}},
		{events.EventTypeMarshalEscalated, "T-023", 17 * time.Second, map[string]any{"reason": "egress registry.npmjs.org outside manifest (taint U)"}},
		{events.EventTypeMarshalTaskMerged, "T-009", 2 * time.Second, map[string]any{"reason": "→ integration a91f2c · 7 evidence refs"}},
	} {
		if err := decide(d.kind, d.task, time.Now().UTC().Add(-d.ago), d.data); err != nil {
			return err
		}
	}
	return acceptArchitecture(ctx, layout.Database, p.ID, start)
}

// acceptArchitecture records the owner's signed acceptance of architecture
// revision 7, as the Board e2e test does, so the header shows its chip.
func acceptArchitecture(ctx context.Context, path, projectID string, at time.Time) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	var receipt store.SignedArchitectureReceipt
	receipt.Challenge.Binding.ArchitectureID, receipt.Challenge.Binding.Revision, receipt.Challenge.Binding.Digest = "ARCH-1", 7, "a3f1c9e2"
	receipt.Challenge.Message = "accept architecture"
	raw, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT OR IGNORE INTO architecture_target_generations(project_id,generation,enrollment_id,enrollment) VALUES(?,1,'enrol-seed',x'7b7d')`, []any{projectID}},
		{`INSERT OR IGNORE INTO architecture_owner_nonces(nonce,project_id,generation,payload_digest,expires_unix) VALUES('nonce-seed',?,1,'pd',0)`, []any{projectID}},
		{`INSERT OR IGNORE INTO architecture_accepted_revisions(receipt_id,receipt_digest,project_id,generation,nonce,receipt) VALUES('rcpt-seed','d-rcpt-seed',?,1,'nonce-seed',?)`, []any{projectID, raw}},
		{`INSERT OR IGNORE INTO architecture_owner_audit(receipt_id,project_id,generation,message_id,session_id,origin,accepted_at) VALUES('rcpt-seed',?,1,'m','s','owner_command',?)`, []any{projectID, at.Format(time.RFC3339Nano)}},
	} {
		if _, err := db.ExecContext(ctx, q.sql, q.args...); err != nil {
			return fmt.Errorf("%s: %w", q.sql, err)
		}
	}
	return nil
}

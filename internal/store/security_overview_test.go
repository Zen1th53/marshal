package store

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestSecurityOverview(t *testing.T) {
	s := openTestStore(t)
	marshalTestProject(t, s)
	ctx := t.Context()

	if _, err := s.SecurityOverview(ctx, "p", "run-x"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("missing run: %v", err)
	}
	run := marshal.Run{PlanID: "plan-1", PlanVersion: 1, ApprovalScopeDigest: "d", Tier: marshal.Ultra, Settings: marshal.DefaultSettings(), State: marshal.Dispatching, Tasks: []marshal.Task{{PlanTaskID: "T-1", Worker: "codex", State: marshal.Dispatched}}}
	if err := s.SaveMarshalState(ctx, "p", "run-1", run, 0, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SecurityOverview(ctx, "other", "run-1"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("another project's run: %v", err)
	}
	got, err := s.SecurityOverview(ctx, "p", "run-1")
	if err != nil || len(got.Egress) != 0 || len(got.Leases) != 0 || got.HoneypotTrips != 0 {
		t.Fatalf("empty run: %+v %v", got, err)
	}

	at := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	n := 0
	egress := func(kind events.EventType, run, parent, worker, task string, data map[string]any) {
		t.Helper()
		n++
		data["parent_run_id"] = parent
		if _, err := s.Append(ctx, events.Event{ID: fmt.Sprintf("EVENT-EGRESS-%02d", n), Type: kind, Subject: worker, RunID: run, TaskID: task, At: at.Add(time.Duration(n) * time.Second), Data: data}); err != nil {
			t.Fatal(err)
		}
	}
	attempt := events.EventTypeNetworkEgressAttempt
	notify := events.EventTypeNetworkEgressNotification
	// codex-1 reaches github through the allowlist.
	egress(attempt, "RUN-EGRESS-a", "run-1", "codex-1", "TASK-1", map[string]any{"endpoint": "github.com:443", "allowed": true, "rule_id": "manifest"})
	// codex-3 is refused npm and asks the owner, who has not answered.
	egress(attempt, "RUN-EGRESS-b", "run-1", "codex-3", "TASK-3", map[string]any{"endpoint": "registry.npmjs.org:443", "allowed": false, "reason": "outside manifest"})
	egress(notify, "RUN-EGRESS-b", "run-1", "codex-3", "TASK-3", map[string]any{"endpoint": "registry.npmjs.org:443", "requestable": true, "message": "codex-3 wants to reach registry.npmjs.org:443. Allow?"})
	// agy-2 is refused pypi and asks; the owner grants it for the run.
	egress(attempt, "RUN-EGRESS-c", "run-1", "agy-2", "TASK-4", map[string]any{"endpoint": "pypi.org:443", "allowed": false})
	egress(notify, "RUN-EGRESS-c", "run-1", "agy-2", "TASK-4", map[string]any{"endpoint": "pypi.org:443", "requestable": true})
	egress(events.EventTypeNetworkEgressGranted, "RUN-EGRESS-c", "run-1", "agy-2", "TASK-4", map[string]any{"endpoint": "pypi.org:443", "source": "operator command"})
	// A harness probe's refusal is not requestable: it stays denied.
	egress(attempt, "RUN-EGRESS-d", "run-1", "runtime", "TASK-5", map[string]any{"endpoint": "example.com:443", "allowed": false})
	egress(notify, "RUN-EGRESS-d", "run-1", "runtime", "TASK-5", map[string]any{"endpoint": "example.com:443", "requestable": false})
	// Another run's egress is not this run's.
	egress(attempt, "RUN-EGRESS-z", "run-2", "claude-9", "TASK-9", map[string]any{"endpoint": "evil.example:443", "allowed": false})

	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO tasks(task_id,project_id,title,status,risk,revision,created_at,updated_at) VALUES('TASK-1','p','t','proposed','R1',0,'2026-11-03T00:00:00Z','2026-11-03T00:00:00Z')`)
	now := time.Now().UTC()
	lease := func(id, state string, expires time.Time, revoked bool) {
		t.Helper()
		var rev any
		if revoked {
			rev = now.Format(time.RFC3339Nano)
		}
		exec(`INSERT INTO secret_leases(lease_id,subject,task_id,provider,secret_name,secret_version,purpose,issued_at,expires_at,revoked_at,state) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			id, "codex-1", "TASK-1", "env", "GITHUB_TOKEN", "1", "github read token", now.Add(-time.Hour).Format(time.RFC3339Nano), expires.Format(time.RFC3339Nano), rev, state)
	}
	lease("L-late", "leased", now.Add(20*time.Minute), false)
	lease("L-soon", "used", now.Add(5*time.Minute), false)
	lease("L-ended", "leased", now.Add(-time.Minute), false)
	lease("L-revoked", "revoked", now.Add(time.Hour), true)
	lease("L-expired", "expired", now.Add(time.Hour), false)

	audit := func(id, kind string, data string) {
		t.Helper()
		exec(`INSERT INTO audit_events(event_id,event_type,project_id,aggregate_revision,timestamp,data_json) VALUES(?,?,?,0,?,?)`, id, kind, "p", at.Format(time.RFC3339Nano), data)
	}
	audit("A-1", "HONEYPOT_SCAN", `{"task_id":"TASK-1","outcome":"clean"}`)
	audit("A-2", "HONEYPOT_HIT", `{"task_id":"TASK-3","reason":"canary read"}`)
	audit("A-3", "PERMISSION_DECIDED", `{"kind":"network","object":"pypi.org:443","allow":true}`)

	got, err = s.SecurityOverview(ctx, "p", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	want := []SecurityEgress{
		{Decision: EgressDenied, Worker: "runtime", Task: "TASK-5", RunID: "RUN-EGRESS-d", Endpoint: "example.com:443", Reason: "not in the run's allowlist"},
		{Decision: EgressAllowed, Worker: "agy-2", Task: "TASK-4", RunID: "RUN-EGRESS-c", Endpoint: "pypi.org:443", Reason: "owner: allow for run"},
		{Decision: EgressWaiting, Worker: "codex-3", Task: "TASK-3", RunID: "RUN-EGRESS-b", Endpoint: "registry.npmjs.org:443", Reason: "outside the run's allowlist: outside manifest"},
		{Decision: EgressAllowed, Worker: "codex-1", Task: "TASK-1", RunID: "RUN-EGRESS-a", Endpoint: "github.com:443", Reason: "manifest"},
	}
	if len(got.Egress) != len(want) {
		t.Fatalf("egress rows %+v", got.Egress)
	}
	for i, w := range want {
		g := got.Egress[i]
		g.Sequence, g.At = 0, time.Time{}
		if g != w {
			t.Errorf("egress %d = %+v, want %+v", i, g, w)
		}
	}
	if len(got.Leases) != 2 || got.Leases[0].ID != "L-soon" || got.Leases[1].ID != "L-late" || got.Leases[0].Secret != "env/GITHUB_TOKEN" || got.Leases[0].Purpose != "github read token" {
		t.Fatalf("leases %+v", got.Leases)
	}
	if got.HoneypotTrips != 1 || got.HoneypotPartial {
		t.Fatalf("honeypot %d partial %v", got.HoneypotTrips, got.HoneypotPartial)
	}
	if len(got.Audit) != 3 || got.Audit[0].Type != "PERMISSION_DECIDED" || got.Audit[0].Subject != "network pypi.org:443 · allowed" || got.Audit[1].Subject != "TASK-3" {
		t.Fatalf("audit %+v", got.Audit)
	}
}

// The overview's rows are bounded: the newest securityEgressRows decisions.
func TestSecurityOverviewEgressBound(t *testing.T) {
	s := openTestStore(t)
	marshalTestProject(t, s)
	ctx := t.Context()
	run := marshal.Run{PlanID: "plan-1", PlanVersion: 1, ApprovalScopeDigest: "d", Tier: marshal.Ultra, Settings: marshal.DefaultSettings(), State: marshal.Dispatching}
	if err := s.SaveMarshalState(ctx, "p", "run-1", run, 0, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	for i := range 30 {
		if _, err := s.Append(ctx, events.Event{ID: fmt.Sprintf("E-%02d", i), Type: events.EventTypeNetworkEgressAttempt, Subject: "codex-1", RunID: "run-1", At: time.Now().UTC(), Data: map[string]any{"endpoint": fmt.Sprintf("h%02d.example:443", i), "allowed": true}}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.SecurityOverview(ctx, "p", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Egress) != securityEgressRows || got.Egress[0].Endpoint != "h29.example:443" {
		t.Fatalf("%d rows, newest %+v", len(got.Egress), got.Egress[0])
	}
}

// The overview reads through keys and indexes, never by scanning a table
// with json_extract; the audit read walks the rowid newest first and stops
// at its limit, with no sort.
func TestSecurityOverviewQueryPlans(t *testing.T) {
	s := openTestStore(t)
	plan := func(q string) string {
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
	for _, q := range []string{
		`SELECT project_id FROM marshal_runs WHERE run_id = 'r'`,
		`SELECT sequence, subject, task_id, run_id, at, data_json FROM structured_events INDEXED BY structured_events_by_type_sequence WHERE event_type = 'x' ORDER BY sequence DESC LIMIT 200`,
		`SELECT lease_id, subject, task_id, provider, secret_name, purpose, state, expires_at FROM secret_leases INDEXED BY secret_leases_by_expiry WHERE expires_at > 'now' AND revoked_at IS NULL ORDER BY expires_at LIMIT 20`,
	} {
		if text := plan(q); !strings.Contains(text, "USING") || strings.Contains(text, "TEMP B-TREE") || strings.Contains(text, "json_extract") {
			t.Errorf("%s: plan %q does not use an index", q, text)
		}
	}
	q := `SELECT event_type, COALESCE(project_id, ''), COALESCE(task_id, ''), timestamp, data_json FROM audit_events ORDER BY rowid DESC LIMIT 200`
	if text := plan(q); strings.Contains(text, "TEMP B-TREE") || strings.Contains(text, "json_extract") {
		t.Errorf("audit read sorts or filters by JSON: %q", text)
	}
}

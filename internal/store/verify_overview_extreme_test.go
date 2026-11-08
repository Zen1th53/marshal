//go:build !governed

package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// The Extreme overview carries the run's architecture compatibility review,
// found through the project's canonical id and the (project_id, stage) index.
func TestVerifyOverviewArchitecture(t *testing.T) {
	s := openTestStore(t)
	marshalTestProject(t, s)
	ctx := t.Context()
	run := marshal.Run{PlanID: "plan-1", PlanVersion: 1, ApprovalScopeDigest: "d", Tier: marshal.Ultra, Settings: marshal.DefaultSettings(), State: marshal.Verifying, Tasks: []marshal.Task{{PlanTaskID: "T-1", Worker: "codex", State: marshal.Merged}}}
	if err := s.SaveMarshalState(ctx, "p", "run-1", run, 0, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	got, err := s.VerifyOverview(ctx, "run-1")
	if err != nil || got.Architecture != nil {
		t.Fatalf("before a review: %+v %v", got.Architecture, err)
	}
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO extreme_project_contract_revisions(project_id,contract_id,revision,digest,contract_json,acceptance_occurrence,created_at) VALUES('canon','c',1,'dg','{}','o',?)`, now.Format(timeLayout))
	exec(`INSERT INTO extreme_project_contract_active(project_id,storage_project_id,contract_id,revision,digest,cas_version,bootstrap_state) VALUES('canon','p','c',1,'dg',1,'accepted')`)
	receipt := func(id, plan, outcome string) {
		r := projectcontext.Receipt{ID: id, Stage: "architecture_compatibility", Outcome: outcome, ReasonCode: "CANONICAL_INDEPENDENT_ARCHITECTURE_OBSERVATION", CheckedAt: now, Binding: projectcontext.Binding{PlanID: plan}}
		raw, _ := json.Marshal(r)
		exec(`INSERT INTO extreme_project_context_receipts(receipt_id,project_id,contract_id,contract_revision,stage,manifest_digest,packet_digest,binding_json,receipt_json,outcome,required_count,included_count,rendered_bytes,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,0,0,0,?)`, id, "canon", "c", 1, r.Stage, "m", "pd", "{}", string(raw), outcome, now.Format(timeLayout))
	}
	receipt("CTX-ARCH-1", "plan-1", "COMPATIBLE")
	receipt("CTX-ARCH-2", "plan-other", "INCOMPATIBLE")
	obs := ProjectArchitectureObservation{ID: "CTX-ARCH-1", Invocation: ProjectArchitectureInvocation{Provider: "claude"}, Assessments: []ProjectArchitectureAssessment{{Status: "PASS"}, {Status: "PASS"}, {Status: "UNKNOWN"}}}
	data, _ := json.Marshal(map[string]any{"receipt_id": "CTX-ARCH-1", "observation": obs})
	exec(`INSERT INTO audit_events(event_id,event_type,project_id,aggregate_revision,timestamp,data_json) VALUES(?,?,?,0,?,?)`, "architecture-receipt-CTX-ARCH-1", "PROJECT_ARCHITECTURE_COMPATIBILITY_RECORDED", "p", now.Format(timeLayout), string(data))

	got, err = s.VerifyOverview(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	a := got.Architecture
	if a == nil || a.ReceiptID != "CTX-ARCH-1" || a.Outcome != "COMPATIBLE" || a.Provider != "claude" || a.Pass != 2 || a.Unknown != 1 || !a.At.Equal(now) {
		t.Fatalf("architecture %+v", a)
	}

	rows, err := s.db.QueryContext(ctx, `EXPLAIN QUERY PLAN SELECT r.receipt_id, r.receipt_json FROM extreme_project_contract_active a
		JOIN extreme_project_context_receipts r INDEXED BY extreme_project_context_by_stage
		ON r.project_id = a.project_id AND r.stage = 'architecture_compatibility'
		WHERE a.storage_project_id = 'p' ORDER BY r.rowid DESC LIMIT 20`)
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
	if text := strings.Join(plan, "; "); !strings.Contains(text, "extreme_project_context_by_stage") {
		t.Fatalf("receipts are not read through the stage index: %s", text)
	}
}

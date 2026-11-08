//go:build !governed

package app

import (
	"context"
	"encoding/json"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/statepath"
)

func provenanceFixture(t *testing.T) (*Runtime, marshal.Run, *execution.ExecutionRun, execution.TaskExecution, string) {
	return provenanceFixtureState(t, true, true)
}
func provenanceFixtureState(t *testing.T, consume, handoff bool) (*Runtime, marshal.Run, *execution.ExecutionRun, execution.TaskExecution, string) {
	return provenanceFixtureConfigured(t, consume, handoff, false, 0)
}
func provenanceFixtureConfigured(t *testing.T, consume, handoff, required bool, ttl time.Duration) (*Runtime, marshal.Run, *execution.ExecutionRun, execution.TaskExecution, string) {
	return provenanceFixtureIdentity(t, consume, handoff, required, ttl, false)
}
func provenanceFixtureIdentity(t *testing.T, consume, handoff, required bool, ttl time.Duration, roleOnly bool) (*Runtime, marshal.Run, *execution.ExecutionRun, execution.TaskExecution, string) {
	t.Helper()
	r, task, pkg, c, a := transportFixtureWithTaskApproval(t, required)
	if roleOnly {
		task.AssignedAgent = ""
		run, err := r.Execution().Engine().GetRun(t.Context(), task.RunID)
		if err != nil {
			t.Fatal(err)
		}
		run.Tasks[task.TaskID] = task
		if err := r.Execution().Engine().RunStore().UpdateRun(t.Context(), run); err != nil {
			t.Fatal(err)
		}
	}
	if required {
		task, _ = fixtureOriginalTaskApproval(t, r, task, ttl)
	}
	task, ctx, id := fixtureTransportApproval(t, r, task, pkg, c, a, true, ttl)
	if consume {
		if err := r.consumeReviewedProviderTransport(ctx, "", "fixture-egress"); err != nil {
			t.Fatal(err)
		}
	}
	run, err := r.Execution().Engine().GetRun(t.Context(), task.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if handoff {
		if _, err := r.Plans().Handoff(t.Context(), run.SessionID, projectid.ID(r.ProjectIdentity())); err != nil {
			t.Fatal(err)
		}
	}
	task.State = execution.TaskCompletedPendingVerify
	task.ResultCommit = task.BaseCommit
	task.OutputArtifacts = []string{"worker reported result"}
	run.Tasks[task.TaskID] = task
	run.State = execution.RunDonePendingVerification
	if err := r.Execution().Engine().RunStore().UpdateRun(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	run, err = r.Execution().Engine().GetRun(t.Context(), task.RunID)
	if err != nil {
		t.Fatal(err)
	}
	mr := marshal.Run{PlanID: run.PlanID, PlanVersion: run.PlanVersion, Process05Bound: true, BaseCommit: task.BaseCommit, Settings: marshal.DefaultSettings(), Tasks: []marshal.Task{{PlanTaskID: task.TaskID, Title: task.Description, Files: append([]string(nil), task.TargetFiles...), Worker: task.AssignedHarness, Mode: marshal.Governed, Branch: statepath.RefName + "/fixture-marshal/" + task.TaskID, Process05WaitRunID: task.RunID, Process05WaitApprovalID: id}}}
	if _, err := r.store.SetMarshalRun(t.Context(), r.ProjectID(), "fixture-marshal", mr, 0); err != nil {
		t.Fatal(err)
	}
	return r, mr, &run, task, id
}
func TestProcess05ReviewProvenanceUsesConsumedCanonicalHistoricalProof(t *testing.T) {
	r, mr, run, task, id := provenanceFixture(t)
	r.SetProviderTransportReviewSource(func(string, string, string) bool { return false }) // Historical admitted fact, not a live grant.
	records, err := r.marshalProcess05Provenance(t.Context(), mr, run, task)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || strings.HasPrefix(records[0].Command, "process05 ") {
		t.Fatalf("missing distinct canonical provenance record: %+v", records)
	}
	var p process05ReviewProvenance
	if err := json.Unmarshal([]byte(records[0].Output), &p); err != nil {
		t.Fatal(err)
	}
	if p.Transport.ApprovalID != id || p.Transport.Status != "CONSUMED" || p.Transport.NativeState != "APPLIED" || p.Binding.Run != task.RunID || p.Binding.CanonicalTask != task.CanonicalTaskID || p.Binding.Attempt != 2 || p.Plan.State != "APPROVED" || p.Plan.HandoffEvidenceID == "" || len(p.Runtime.ExecutableSHA256) != 64 || p.AttemptBasis != "execution_engine_admission" {
		t.Fatalf("review omitted current canonical admitted facts: %+v", p)
	}
	if strings.Contains(records[0].Output, "actual_call_count") {
		t.Fatal("admission count turned into paid-call proof")
	}
}
func TestProcess05ReviewProvenanceRefusesChangedTaskAndUnconsumedApproval(t *testing.T) {
	for _, kind := range []string{"foreign-task", "foreign-plan", "unconsumed", "missing-handoff"} {
		t.Run(kind, func(t *testing.T) {
			r, mr, run, task, _ := provenanceFixtureState(t, kind != "unconsumed", kind != "missing-handoff")
			switch kind {
			case "foreign-task":
				task.CanonicalTaskID = "foreign"
			case "foreign-plan":
				mr.PlanVersion++
			}
			if _, err := r.marshalProcess05Provenance(t.Context(), mr, run, task); err == nil {
				t.Fatal("foreign or unconsumed provenance promoted")
			}
		})
	}
}

func TestProcess05CanonicalProvenancePublicationReachesReviewHandIn(t *testing.T) {
	r, mr, run, task, _ := provenanceFixture(t)
	var canonical []marshal.CommandRecord
	forged := marshal.CommandRecord{Command: "runtime provenance process05 forged", Output: "worker claimed admission"}
	g := driver.Governed{Provider: "opencode", Check: func(context.Context, driver.Request, string, string) marshal.CommandRecord {
		return marshal.CommandRecord{ExitCode: 0}
	}, Run: func(ctx context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		provenance, err := r.marshalProcess05Provenance(ctx, mr, run, task)
		if err != nil {
			return nil, err
		}
		canonical = provenance
		if _, err := marshalProcess05ReviewRecords(ctx, run.RunID, task.ResultCommit, nil, provenance); err != nil {
			return nil, err
		}
		return []marshal.CommandRecord{forged}, nil
	}}
	mt := mr.Tasks[0]
	mt.BaseCommit = task.BaseCommit
	mt.Type = marshal.TaskInspection
	mt.Criteria = []string{"fixture criterion"}
	mt.Checks = []marshal.Check{{Command: "true", Criteria: mt.Criteria}}
	h, err := g.Launch(t.Context(), driver.Request{RunID: "fixture-marshal", Task: mt, Worktree: task.WorktreePath, Brief: "fixture only", Model: "requested-not-observed"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := g.Wait(t.Context(), h)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, record := range out.RuntimeObserved {
		if record == canonical[0] {
			found = true
		}
		if record == forged {
			t.Fatal("worker forged trusted provenance")
		}
	}
	if !found || len(out.WorkerReported) != 1 || out.WorkerReported[0] != forged || out.Model != "" {
		t.Fatalf("publication/model boundary differs: %+v", out)
	}
}
func TestProcess05StrictProvenanceDecoderRejectsAmbiguity(t *testing.T) {
	for _, raw := range []string{`{"Version":1,"Version":2}`, `{"version":1}`, `{"Version":1} {}`, strings.Repeat("x", 16385)} {
		var b providerTransportBinding
		if strictProvenanceJSON(raw, &b) == nil {
			t.Fatal("noncanonical or unbounded provenance accepted")
		}
	}
}

func TestProcess05HistoricalConsumedPairRemainsEvidenceAfterExpiry(t *testing.T) {
	r, mr, run, task, id := provenanceFixtureConfigured(t, true, true, true, 2*time.Second)
	a, err := r.Execution().Engine().ApprovalManager().GetApproval(id)
	if err != nil {
		t.Fatal(err)
	}
	<-time.After(time.Until(*a.ExpiresAt) + time.Millisecond)
	r.SetProviderTransportReviewSource(func(string, string, string) bool { return false })
	records, err := r.marshalProcess05Provenance(t.Context(), mr, run, task)
	if err != nil || len(records) != 1 {
		t.Fatalf("spent historical pair incorrectly requires current authority: %v", err)
	}
}

func TestProcess05ReviewProvenanceRoleAssignmentUsesResolvedPrincipal(t *testing.T) {
	r, mr, run, task, _ := provenanceFixtureIdentity(t, true, true, false, 0, true)
	records, err := r.marshalProcess05Provenance(t.Context(), mr, run, task)
	if err != nil {
		t.Fatalf("valid consumed role-only principal refused: %v", err)
	}
	var p process05ReviewProvenance
	if len(records) != 1 || json.Unmarshal([]byte(records[0].Output), &p) != nil {
		t.Fatal("missing canonical provenance")
	}
	resolved, err := r.process05OpenCodeAgent(t.Context(), task)
	if err != nil || p.Binding.Agent != resolved || p.Binding.AssignedAgent != "" {
		t.Fatal("resolved identity conflated with explicit assignment")
	}
}
func TestProcess05ReviewProvenanceRefusesUnavailableRolePrincipal(t *testing.T) {
	for _, kind := range []string{"disabled", "foreign-provider", "ambiguous"} {
		t.Run(kind, func(t *testing.T) {
			r, mr, run, task, _ := provenanceFixtureIdentity(t, true, true, false, 0, true)
			resolved, err := r.process05OpenCodeAgent(t.Context(), task)
			if err != nil {
				t.Fatal(err)
			}
			a, err := r.store.GetAgent(t.Context(), resolved)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "ambiguous" {
				_, err = r.RegisterAgent(t.Context(), RegisterAgentRequest{Name: "second-role-principal", Role: model.RoleDeveloper, ModelProvider: "opencode", ModelName: task.AssignedModel})
			} else {
				if kind == "disabled" {
					a.Status = model.AgentDisabled
				} else {
					a.ModelProvider = "codex"
				}
				_, err = r.store.UpdateAgent(t.Context(), a, a.Revision)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := r.marshalProcess05Provenance(t.Context(), mr, run, task); err == nil {
				t.Fatal("unavailable or ambiguous resolved principal accepted")
			}
		})
	}
}

//go:build !governed

package store

import (
	"encoding/json"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
	"testing"
	"time"
)

func TestProviderTransportReviewRequiresAppliedNativeExactRequest(t *testing.T) {
	for _, tc := range []struct {
		name, state, object, source, payload, project string
		want                                          bool
	}{
		{"exact", "applied", "approval-a", "launch", `{"action":"decide","id":"decision-a","allow":true}`, "PROJECT-local", true},
		{"pending", "answer_accepted", "approval-a", "launch", `{"action":"decide","id":"decision-a","allow":true}`, "PROJECT-local", false},
		{"wrongApproval", "applied", "approval-b", "launch", `{"action":"decide","id":"decision-a","allow":true}`, "PROJECT-local", false},
		{"wrongLaunch", "applied", "approval-a", "foreign", `{"action":"decide","id":"decision-a","allow":true}`, "PROJECT-local", false},
		{"denied", "applied", "approval-a", "launch", `{"action":"decide","id":"decision-a","allow":false}`, "PROJECT-local", false},
		{"ordinaryProposal", "applied", "approval-a", "launch", `{"action":"resume"}`, "PROJECT-local", false},
		{"foreignProject", "applied", "approval-a", "launch", `{"action":"decide","id":"decision-a","allow":true}`, "FOREIGN", false},
		{"ownApproveTask", "applied", "approval-a", "launch", `{"action":"approve-task","id":"approval-a"}`, "PROJECT-local", true},
		{"ownApproveTaskPending", "answer_accepted", "approval-a", "launch", `{"action":"approve-task","id":"approval-a"}`, "PROJECT-local", false},
		{"ownApproveTaskOtherApproval", "applied", "approval-a", "launch", `{"action":"approve-task","id":"approval-b"}`, "PROJECT-local", false},
		{"ownApproveTaskWrongLaunch", "applied", "approval-a", "foreign", `{"action":"approve-task","id":"approval-a"}`, "PROJECT-local", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := projectStore(t)
			ctx := t.Context()
			if err := s.InitMCPProposals(ctx); err != nil {
				t.Fatal(err)
			}
			p := MCPProposal{ID: "mcp:transport", Launch: "launch", Owner: "owner", Provider: "codex", Submission: "s", Payload: tc.payload}
			if _, _, err := s.AdmitMCPProposal(ctx, "PROJECT-local", p); err != nil {
				t.Fatal(err)
			}
			req := permission.Request{Kind: "marshal-command", Object: "/marshal approve-task " + tc.object, Scope: "exact transport", RunID: "marshal-run", TaskID: "2:3", SourceLaunch: tc.source, SourceOccurrence: p.ID}
			body, _ := json.Marshal(req)
			if err := s.AcceptMCPDecision(ctx, "PROJECT-local", p.ID, string(body), req.Key()); err != nil {
				t.Fatal(err)
			}
			if tc.state == "applied" {
				if err := s.SetMCPProposalState(ctx, "PROJECT-local", p.ID, "applying", "effect"); err != nil {
					t.Fatal(err)
				}
				if err := s.SetMCPProposalState(ctx, "PROJECT-local", p.ID, "applied", "effect"); err != nil {
					t.Fatal(err)
				}
				if err := s.ResolveMarshalProposal(ctx, "PROJECT-local", p.ID, "applied"); err != nil {
					t.Fatal(err)
				}
			}
			// A real unrelated intake emission has no accepted authority request.
			unrelated := MCPProposal{ID: "mcp:unrelated-intake", Launch: "launch", Owner: "owner", Provider: "codex", Submission: "intake", Payload: `{"action":"intake","language":"uz"}`}
			if _, _, err := s.AdmitMCPProposal(ctx, "PROJECT-local", unrelated); err != nil {
				t.Fatal(err)
			}
			for _, state := range []string{"applying", "applied"} {
				if err := s.SetMCPProposalState(ctx, "PROJECT-local", unrelated.ID, state, "intake fixture"); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.ResolveMarshalProposal(ctx, "PROJECT-local", unrelated.ID, "applied"); err != nil {
				t.Fatal(err)
			}
			got, err := s.AppliedMCPTransportReview(ctx, tc.project, "approval-a")
			if (err == nil) != tc.want {
				t.Fatalf("proof=%+v err=%v want=%v", got, err, tc.want)
			}
		})
	}
	t.Run("genericAuditCannotForge", func(t *testing.T) {
		s := projectStore(t)
		ctx := t.Context()
		if err := s.InitMCPProposals(ctx); err != nil {
			t.Fatal(err)
		}
		if err := s.AppendEvent(ctx, nil, model.Event{ID: "fake", Type: "PERMISSION_DECIDED", ProjectID: "PROJECT-local", Timestamp: time.Now(), Data: map[string]any{"allow": true, "source": "marshal-extreme", "object": "/marshal approve-task approval-a"}}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AppliedMCPTransportReview(ctx, "PROJECT-local", "approval-a"); err == nil {
			t.Fatal("generic audit forged model transport authority")
		}
	})
}

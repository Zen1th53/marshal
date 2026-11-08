package tui

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
)

type recoveryMenuAuthority struct {
	fakeAuthority
	row     model.SessionRecovery
	command app.CommandEnvelope
	calls   int
	deny    bool
	noProof bool
}

func (a *recoveryMenuAuthority) RecoveryContext(_ context.Context, id string, generation int64) (model.SessionRecovery, error) {
	if id != a.row.ID {
		return model.SessionRecovery{}, model.ErrNotFound
	}
	if a.noProof && a.calls > 0 {
		return model.SessionRecovery{}, model.ErrUnavailable
	}
	return a.row, nil
}
func (a *recoveryMenuAuthority) AdoptRecoveryContext(_ context.Context, e app.CommandEnvelope, digest, destination string) (model.SessionRecovery, error) {
	a.calls++
	a.command = e
	if a.deny {
		return model.SessionRecovery{}, model.ErrConflict
	}
	if digest != a.row.Digest || e.ExpectedVersion != a.row.Generation {
		return model.SessionRecovery{}, model.ErrConflict
	}
	a.row.Generation++
	a.row.State = "adopted"
	a.row.AdoptedSessionID = destination
	a.row.Digest = a.row.ContentDigest()
	return a.row, nil
}

func TestSessionRecoveryMenuBindsExactContextAndReadsAdoption(t *testing.T) {
	a := &recoveryMenuAuthority{row: model.SessionRecovery{ID: "exact-context", Generation: 7, SourceSessionID: "interrupted-session", State: "recoverable", Binding: model.SessionRecoveryBinding{ProjectID: "project", GoalID: "goal", GoalRevision: 3}, UpdatedAt: time.Now().UTC()}}
	a.row.Digest = a.row.ContentDigest()
	s := &ControlSource{Authority: a, ProjectID: "project", SessionID: "new-session"}
	b := recoveryActionBindings(s)["CTUI-0073"]
	req := ActionRequest{ProjectID: s.ProjectID, SessionID: s.SessionID, IdempotencyKey: "exact-key", Inputs: map[string]string{"recovery_id": a.row.ID}}
	target, err := b.PrepareRequest(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	req.Target = target
	out, err := b.Execute(context.Background(), req)
	if err != nil || !out.Succeeded() {
		t.Fatalf("adopt: %+v %v", out, err)
	}
	if a.command.TargetID != "exact-context" || a.command.ExpectedVersion != 7 || a.command.SessionID != "interrupted-session" || a.command.IdempotencyKey != "exact-key" || a.row.AdoptedSessionID != "new-session" {
		t.Fatalf("rebound adoption: %+v %+v", a.command, a.row)
	}
}

func TestSessionRecoveryMenuRejectsStaleContextAndRequiresDurableProof(t *testing.T) {
	for _, kind := range []string{"digest", "form-selection", "project", "backend", "proof"} {
		t.Run(kind, func(t *testing.T) {
			a := &recoveryMenuAuthority{row: model.SessionRecovery{ID: "exact", Generation: 1, SourceSessionID: "old", State: "recoverable", Binding: model.SessionRecoveryBinding{ProjectID: "project"}}}
			a.row.Digest = a.row.ContentDigest()
			s := &ControlSource{Authority: a, ProjectID: "project", SessionID: "new"}
			b := recoveryActionBindings(s)["CTUI-0073"]
			req := ActionRequest{ProjectID: s.ProjectID, SessionID: s.SessionID, Inputs: map[string]string{"recovery_id": "exact"}, IdempotencyKey: "adopt", Target: recoveryTarget(a.row)}
			switch kind {
			case "digest":
				req.Target.Digest = "stale"
			case "form-selection":
				req.Inputs["recovery_id"] = "other"
			case "project":
				req.ProjectID = "other"
			case "backend":
				a.deny = true
			case "proof":
				a.noProof = true
			}
			out, err := b.Execute(context.Background(), req)
			if out.Succeeded() {
				t.Fatal("unproved adoption reported success")
			}
			if kind == "proof" {
				if out.Verdict != VerdictUnknown || err != nil {
					t.Fatalf("unproved result: %+v %v", out, err)
				}
			} else if err == nil {
				t.Fatal("stale/backend refusal hidden")
			}
		})
	}
	if _, err := recoveryActionBindings(&ControlSource{Authority: &fakeAuthority{}})["CTUI-0073"].PrepareRequest(context.Background(), ActionRequest{}); !errors.Is(err, ErrNoBinding) {
		t.Fatalf("missing extension: %v", err)
	}
}

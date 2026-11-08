//go:build !governed

package app

import (
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

func TestMemorySnapshotCommandsRequireExactAuthenticatedProjectSessionAndPreview(t *testing.T) {
	r := runtimeForPlan(t)
	projectID := r.ProjectIdentity()
	storedProject, err := r.Store().Project(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	goal := planGoal()
	goal.ID = "GOAL-snapshot"
	goal.SessionID = "SESSION-snapshot"
	goal.ProjectID = projectID
	if err := r.Store().SaveGoalContract(t.Context(), goal, 0); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rec := model.MemoryRecordV2{ID: "MEM-snapshot", ProjectID: storedProject.ID, Kind: model.MemoryKindDecision, Lifecycle: model.MemoryCandidate, Authority: model.AuthorityAgent, Title: "before", Body: "before body", Scope: "project", ScopeID: storedProject.ID, ObservedAt: now, ValidFrom: now, CreatedAt: now, Source: model.MemorySource{Kind: "test", Reference: "fixture"}}
	if err := r.Store().WriteMemoryV2(t.Context(), rec); err != nil {
		t.Fatal(err)
	}
	head := marshalGit(t, r.ProjectRoot(), "rev-parse", "HEAD")
	envelope := CommandEnvelope{ProjectID: projectID, SessionID: goal.SessionID, TargetID: "memory-snapshot:SNAP-auth", ExpectedVersion: 0, IdempotencyKey: "create-snapshot"}
	if _, err := r.CommandCreateMemorySnapshot(t.Context(), envelope, head, []string{rec.ID}); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("anonymous snapshot: %v", err)
	}
	local, err := r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx := local.Context(t.Context())
	for _, kind := range []string{"project", "session", "head"} {
		wrong, wrongHead := envelope, head
		switch kind {
		case "project":
			wrong.ProjectID = "another-project"
		case "session":
			wrong.SessionID = "another-session"
		case "head":
			wrongHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		}
		if _, err := r.CommandCreateMemorySnapshot(ctx, wrong, wrongHead, []string{rec.ID}); err == nil {
			t.Fatalf("foreign %s snapshot accepted", kind)
		}
	}
	snapshot, err := r.CommandCreateMemorySnapshot(ctx, envelope, head, []string{rec.ID})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.RecordCount != 1 || len(snapshot.Records) != 0 {
		t.Fatalf("create must expose bounded metadata: %+v", snapshot)
	}
	replayedSnapshot, err := r.CommandCreateMemorySnapshot(ctx, envelope, head, []string{rec.ID})
	if err != nil || replayedSnapshot.Digest != snapshot.Digest {
		t.Fatalf("snapshot command replay: %+v %v", replayedSnapshot, err)
	}
	if _, err := r.CommandCreateMemorySnapshot(ctx, envelope, head, []string{"MEM-different"}); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("reused snapshot key changed selection: %v", err)
	}
	if _, err := r.RetainedMemorySnapshots(t.Context(), projectID, goal.SessionID, 10); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("anonymous snapshot discovery: %v", err)
	}
	if _, err := r.RetainedMemorySnapshots(ctx, projectID, "foreign-session", 10); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("foreign snapshot discovery: %v", err)
	}
	if _, err := r.RetainedMemorySnapshots(ctx, projectID, goal.SessionID, 51); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("unbounded discovery: %v", err)
	}
	if _, err := r.Store().UpdateMemory(ctx, storedProject.ID, rec.ID, 0, func(m *model.MemoryRecordV2) error { m.Body = "later body"; return nil }); err != nil {
		t.Fatal(err)
	}
	envelope.ExpectedVersion = 1
	envelope.IdempotencyKey = "preview-snapshot"
	preview, err := r.CommandPreviewMemorySnapshot(ctx, envelope, snapshot.Digest, head)
	if err != nil {
		t.Fatal(err)
	}
	replayedPreview, err := r.CommandPreviewMemorySnapshot(ctx, envelope, snapshot.Digest, head)
	if err != nil || replayedPreview.ID != preview.ID || replayedPreview.CurrentDigest != preview.CurrentDigest {
		t.Fatalf("preview receipt replay: %+v %v", replayedPreview, err)
	}
	root := r.ProjectRoot()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r, err = Open(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	if _, err := r.CommandRestoreMemorySnapshot(ctx, envelope, preview); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("old runtime authority survived reopen: %v", err)
	}
	local, err = r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx = local.Context(t.Context())
	retained, err := r.RetainedMemorySnapshots(ctx, projectID, goal.SessionID, 10)
	if err != nil || len(retained) != 1 || retained[0].ID != snapshot.ID || retained[0].Digest != snapshot.Digest || retained[0].RecordCount != 1 || len(retained[0].Records) != 0 {
		t.Fatalf("reopened metadata discovery=%+v %v", retained, err)
	}
	envelope.IdempotencyKey = "restore-snapshot"
	tampered := preview
	tampered.CurrentDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := r.CommandRestoreMemorySnapshot(ctx, envelope, tampered); err == nil {
		t.Fatal("altered preview accepted")
	}
	foreign := envelope
	foreign.SessionID = "another-session"
	if _, err := r.CommandRestoreMemorySnapshot(ctx, foreign, preview); err == nil {
		t.Fatal("foreign-session restore accepted")
	}
	result, err := r.CommandRestoreMemorySnapshot(ctx, envelope, preview)
	if err != nil || len(result.Revisions) != 1 {
		t.Fatalf("canonical restore=%+v %v", result, err)
	}
	replay, err := r.CommandRestoreMemorySnapshot(ctx, envelope, preview)
	if err != nil || replay.Revisions[rec.ID] != result.Revisions[rec.ID] {
		t.Fatalf("exact restore receipt did not replay: %+v %v", replay, err)
	}
	stored, err := r.Store().GetMemoryV2(ctx, storedProject.ID, rec.ID)
	if err != nil || stored.Body != "before body" || stored.Revision != 2 || stored.Lifecycle != model.MemoryCandidate || stored.Confidence != model.ConfidenceUnverified {
		t.Fatalf("no persistent candidate restore: %+v %v", stored, err)
	}
}

// Exercise the actual authorization/transaction boundary without adding a
// production timing hook. Revocation occurs after Runtime derived the trusted
// command and before Store inserts the transactional command receipt.
func TestMemorySnapshotGrantRevokedAfterAuthorizationRollsBackEveryOperation(t *testing.T) {
	for _, operation := range []string{"create", "preview", "restore"} {
		t.Run(operation, func(t *testing.T) {
			r := runtimeForPlan(t)
			project, err := r.Store().Project(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			goal := planGoal()
			goal.ID, goal.SessionID, goal.ProjectID = "GOAL-revoke", "SESSION-revoke", r.ProjectIdentity()
			if err := r.Store().SaveGoalContract(t.Context(), goal, 0); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			rec := model.MemoryRecordV2{ID: "MEM-revoke", ProjectID: project.ID, Kind: model.MemoryKindDecision, Lifecycle: model.MemoryCandidate, Authority: model.AuthorityAgent, Title: "saved", Body: "saved body", Scope: "project", ScopeID: project.ID, ObservedAt: now, ValidFrom: now, CreatedAt: now, Source: model.MemorySource{Kind: "test", Reference: "fixture"}}
			if err := r.Store().WriteMemoryV2(t.Context(), rec); err != nil {
				t.Fatal(err)
			}
			local, err := r.OpenLocalControl(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			ctx := local.Context(t.Context())
			head := marshalGit(t, r.ProjectRoot(), "rev-parse", "HEAD")
			e := CommandEnvelope{ProjectID: goal.ProjectID, SessionID: goal.SessionID, TargetID: "memory-snapshot:SNAP-revoke", IdempotencyKey: "setup"}
			var snapshot store.MemorySnapshot
			var preview store.MemorySnapshotPreview
			if operation != "create" {
				snapshot, err = r.CommandCreateMemorySnapshot(ctx, e, head, []string{rec.ID})
				if err != nil {
					t.Fatal(err)
				}
				e.ExpectedVersion = 1
				if _, err := r.Store().UpdateMemory(ctx, project.ID, rec.ID, 0, func(m *model.MemoryRecordV2) error { m.Body = "later body"; return nil }); err != nil {
					t.Fatal(err)
				}
			}
			if operation == "restore" {
				e.IdempotencyKey = "preview"
				preview, err = r.CommandPreviewMemorySnapshot(ctx, e, snapshot.Digest, head)
				if err != nil {
					t.Fatal(err)
				}
			}
			e.IdempotencyKey = "revoked-before-commit"
			action := "state.restore"
			var input any = preview
			if operation == "create" {
				action = "checkpoint.create"
				input = struct {
					Head string
					IDs  []string
				}{head, []string{rec.ID}}
			}
			if operation == "preview" {
				input = struct{ Digest, Head string }{snapshot.Digest, head}
			}
			command, found, err := r.memorySnapshotCommand(ctx, e, action, input)
			if err != nil || found {
				t.Fatalf("authorization=%v found=%v", err, found)
			}
			binding, err := r.memorySnapshotBinding(ctx, e, command.Actor, head)
			if err != nil {
				t.Fatal(err)
			}
			if err := r.Store().RevokeCapabilityGrant(ctx, capability.GrantID(command.CapabilityGrantID), time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			if operation != "create" {
				if _, err := r.RetainedMemorySnapshots(ctx, goal.ProjectID, goal.SessionID, 10); !errors.Is(err, authz.ErrDenied) {
					t.Fatalf("revoked discovery grant disclosed metadata: %v", err)
				}
			}
			commitCtx := store.WithCommand(ctx, command)
			switch operation {
			case "create":
				_, err = r.Store().CreateMemorySnapshot(commitCtx, binding, "SNAP-revoke", []string{rec.ID})
			case "preview":
				_, err = r.Store().PreviewMemorySnapshot(commitCtx, binding, snapshot.ID, 1, snapshot.Digest, "PREVIEW-revoked")
			case "restore":
				_, err = r.Store().RestoreMemorySnapshot(commitCtx, binding, preview)
			}
			if !errors.Is(err, authz.ErrDenied) {
				t.Fatalf("revoked %s committed: %v", operation, err)
			}
			if _, found, err := r.Store().CommandResult(ctx, command); err != nil || found {
				t.Fatalf("denied receipt persisted: found=%v %v", found, err)
			}
			switch operation {
			case "create":
				if _, err := r.Store().GetMemorySnapshot(ctx, binding, "SNAP-revoke", 1); !errors.Is(err, model.ErrNotFound) {
					t.Fatalf("denied snapshot persisted: %v", err)
				}
			case "preview":
				if _, err := r.Store().GetMemoryPreviewByCommand(ctx, binding, command.Key); !errors.Is(err, model.ErrNotFound) {
					t.Fatalf("denied preview persisted: %v", err)
				}
			case "restore":
				stored, err := r.Store().GetMemoryV2(ctx, project.ID, rec.ID)
				if err != nil || stored.Body != "later body" || stored.Revision != 1 {
					t.Fatalf("denied restore mutated live record: %+v %v", stored, err)
				}
				if history, err := r.Store().GetMemoryRestoreHistory(ctx, binding, preview.ID); err == nil || len(history) != 0 {
					t.Fatalf("denied restore retained committed history: %+v %v", history, err)
				}
			}
		})
	}
}

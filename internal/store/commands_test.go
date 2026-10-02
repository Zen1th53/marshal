package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestCommandAuditFailureRollsBackGoalAndReceipt(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.InitProject(ctx, model.Project{ID: "PROJECT-command", Repository: "/command-test", DefaultBranch: "main", PackVersion: "test"}); err != nil {
		t.Fatal(err)
	}
	project, err := s.Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	goal := model.GoalContract{ID: "GOAL-command", SessionID: "SESSION-command", ProjectID: project.ID, DesiredOutcome: "fix typo", ExpectedArtifact: "README.md", SuccessCriteria: []string{"fixed"}, Risk: model.R1, AuthoritySource: "owner", Revision: 1}
	if err := s.SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	record := CommandRecord{ProjectID: project.ID, Actor: "local-uid:1000", Key: "command-proof", Operation: "goal.revise", SessionID: goal.SessionID, TargetID: goal.ID, ExpectedVersion: 1, Digest: strings.Repeat("a", 64)}
	now := time.Now().UTC()
	grant := capability.Grant{ID: "cap-command-proof", Subject: capability.SubjectID(record.Actor), TaskID: capability.TaskID(project.ID), Kind: capability.KindFilesystemWrite, Scope: capability.Scope{Resource: "/command-test/state.db", Actions: []string{"goal.revise"}}, IssuedAt: now, ExpiresAt: now.Add(time.Hour), Issuer: capability.SubjectID(record.Actor)}
	if err := s.PutCapabilityGrant(ctx, grant); err != nil {
		t.Fatal(err)
	}
	record.CapabilityGrantID = string(grant.ID)

	if _, err := s.db.Exec(`CREATE TRIGGER command_audit_fail BEFORE INSERT ON command_audit BEGIN SELECT RAISE(ABORT,'injected audit write failure'); END;`); err != nil {
		t.Fatal(err)
	}
	goal.Revision = 2
	goal.DesiredOutcome = "revised typo"
	if err := s.SaveGoalContract(WithCommand(ctx, record), goal, 1); err == nil || !strings.Contains(err.Error(), "injected audit") {
		t.Fatalf("failure: %v", err)
	}
	active, err := s.GetActiveGoalContract(ctx, goal.SessionID)
	if err != nil || active.Revision != 1 || active.DesiredOutcome != "fix typo" {
		t.Fatalf("mutation not rolled back: %+v %v", active, err)
	}
	if _, found, err := s.CommandResult(ctx, record); err != nil || found {
		t.Fatalf("receipt survived: %v %v", found, err)
	}
	if _, err := s.db.Exec(`DROP TRIGGER command_audit_fail`); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveGoalContract(WithCommand(ctx, record), goal, 1); err != nil {
		t.Fatal(err)
	}
	var actor, op, target, result string
	var revision int64
	if err := s.db.QueryRow(`SELECT actor,operation,target_id,result,result_version FROM command_audit`).Scan(&actor, &op, &target, &result, &revision); err != nil {
		t.Fatal(err)
	}
	if actor != record.Actor || op != record.Operation || target != goal.ID || result != "applied" || revision != 2 {
		t.Fatalf("audit mismatch: %s %s %s %s %d", actor, op, target, result, revision)
	}
	if version, found, err := s.CommandResult(ctx, record); err != nil || !found || version != 2 {
		t.Fatalf("durable command result: %d %v %v", version, found, err)
	}
	other := record
	other.Digest = strings.Repeat("b", 64)
	if _, _, err := s.CommandResult(ctx, other); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("key reused for different payload: %v", err)
	}
	other = record
	other.Key = "wrong-target"
	other.TargetID = "GOAL-other"
	other.ExpectedVersion = 2
	goal.Revision = 3
	if err := s.SaveGoalContract(WithCommand(ctx, other), goal, 2); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("receipt binding mismatch: %v", err)
	}
	other = record
	other.Key = "revoked-before-commit"
	other.ExpectedVersion = 2
	if err := s.RevokeCapabilityGrant(ctx, grant.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveGoalContract(WithCommand(ctx, other), goal, 2); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("revocation race: %v", err)
	}
	grant.ID = "cap-command-expired"
	grant.IssuedAt = now.Add(-2 * time.Hour)
	grant.ExpiresAt = now.Add(-time.Hour)
	if err := s.PutCapabilityGrant(ctx, grant); err != nil {
		t.Fatal(err)
	}
	other.CapabilityGrantID = string(grant.ID)
	if err := s.SaveGoalContract(WithCommand(ctx, other), goal, 2); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("expiry before commit: %v", err)
	}
	active, err = s.GetActiveGoalContract(ctx, goal.SessionID)
	if err != nil || active.Revision != 2 {
		t.Fatalf("denied commit advanced goal: %+v %v", active, err)
	}

	for _, query := range []string{`UPDATE command_audit SET result='forged'`, `DELETE FROM command_audit`, `UPDATE command_results SET result_version=99`, `DELETE FROM command_results`} {
		if _, err := s.db.Exec(query); err == nil {
			t.Fatalf("immutable records accepted %s", query)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CommandResult(ctx, record); err == nil {
		t.Fatal("closed store fabricated a result")
	}

}

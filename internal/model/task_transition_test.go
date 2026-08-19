package model

import (
	"errors"
	"testing"
)

func TestStandardLowRiskLifecycle(t *testing.T) {
	// Standard lifecycle for R0 / R1: review -> qa -> ready_to_merge -> merged
	commit := "abc1234"

	// review -> qa (Reviewer approved)
	err := ValidateTaskTransition(TaskReview, TaskQA, R1, RoleReviewer, commit, commit)
	if err != nil {
		t.Fatalf("expected review -> qa to pass: %v", err)
	}

	// qa -> ready_to_merge (QA approved)
	err = ValidateTaskTransition(TaskQA, TaskReadyToMerge, R1, RoleQA, commit, commit)
	if err != nil {
		t.Fatalf("expected qa -> ready_to_merge to pass: %v", err)
	}

	// ready_to_merge -> merged (Orchestrator merged)
	err = ValidateTaskTransition(TaskReadyToMerge, TaskMerged, R1, RoleOrchestrator, commit, commit)
	if err != nil {
		t.Fatalf("expected ready_to_merge -> merged to pass: %v", err)
	}
}

func TestHighRiskSecurityReviewLifecycle(t *testing.T) {
	commit := "def5678"

	// For R2 / R3 task, direct qa -> ready_to_merge MUST fail
	err := ValidateTaskTransition(TaskQA, TaskReadyToMerge, R2, RoleQA, commit, commit)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected R2 task skipping security_review to fail with ErrConflict, got: %v", err)
	}

	// qa -> security_review (QA approved)
	err = ValidateTaskTransition(TaskQA, TaskSecurityReview, R2, RoleQA, commit, commit)
	if err != nil {
		t.Fatalf("expected qa -> security_review to pass: %v", err)
	}

	// security_review -> ready_to_merge (AppSec approved)
	err = ValidateTaskTransition(TaskSecurityReview, TaskReadyToMerge, R2, RoleAppSec, commit, commit)
	if err != nil {
		t.Fatalf("expected security_review -> ready_to_merge to pass: %v", err)
	}

	// ready_to_merge -> merged (Admin merged)
	err = ValidateTaskTransition(TaskReadyToMerge, TaskMerged, R2, RoleAdmin, commit, commit)
	if err != nil {
		t.Fatalf("expected ready_to_merge -> merged to pass: %v", err)
	}
}

func TestForbiddenTransitionsAndRoleEnforcement(t *testing.T) {
	commit := "ghi9012"

	// 1. Direct jump review -> merged is forbidden
	err := ValidateTaskTransition(TaskReview, TaskMerged, R1, RoleAdmin, commit, commit)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected direct review -> merged to fail with ErrConflict, got: %v", err)
	}

	// 2. Developer cannot approve QA
	err = ValidateTaskTransition(TaskReview, TaskQA, R1, RoleDeveloper, commit, commit)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected developer review approval to fail with ErrUnauthorized, got: %v", err)
	}

	// 3. Developer cannot execute merge
	err = ValidateTaskTransition(TaskReadyToMerge, TaskMerged, R1, RoleDeveloper, commit, commit)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected developer merge to fail with ErrUnauthorized, got: %v", err)
	}

	// 4. Stale approval: current commit changed from reqCommit
	err = ValidateTaskTransition(TaskReview, TaskQA, R1, RoleReviewer, "newcommit999", "oldcommit111")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected stale commit approval to fail with ErrConflict, got: %v", err)
	}

	// 5. Terminal state cannot transition out
	err = ValidateTaskTransition(TaskMerged, TaskWorking, R1, RoleAdmin, commit, commit)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected transition from merged state to fail, got: %v", err)
	}

	err = ValidateTaskTransition(TaskCancelled, TaskReady, R1, RoleAdmin, commit, commit)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected transition from cancelled state to fail, got: %v", err)
	}
}

func TestRejectionPathways(t *testing.T) {
	commit := "jkl3456"

	// QA rejects back to working
	err := ValidateTaskTransition(TaskQA, TaskWorking, R1, RoleQA, commit, commit)
	if err != nil {
		t.Fatalf("expected QA rejection to working to pass: %v", err)
	}

	// AppSec rejects back to working
	err = ValidateTaskTransition(TaskSecurityReview, TaskWorking, R2, RoleAppSec, commit, commit)
	if err != nil {
		t.Fatalf("expected security rejection to working to pass: %v", err)
	}
}

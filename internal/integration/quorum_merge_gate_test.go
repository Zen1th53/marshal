package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/verify/quorum"
)

func TestQuorumMergeGateLowRiskTask(t *testing.T) {
	repo := runtimeIntegrationRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}

	rt, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rt.Close()

	commit := "commit111111"
	task := model.Task{
		ID:         "TASK-QR-LOW",
		Title:      "Low Risk Feature",
		Status:     model.TaskReview,
		Risk:       model.R1,
		HeadCommit: &commit,
	}

	if _, err := rt.ImportTasks(context.Background(), []model.Task{task}); err != nil {
		t.Fatal(err)
	}

	// 1. Evaluate quorum with QA attestation -> Satisfied
	attestations := []quorum.Attestation{
		{
			Subject:       "agent-qa-1",
			Provider:      "claude",
			Role:          "qa",
			ChangeID:      "TASK-QR-LOW",
			EvidenceID:    "EVID-001",
			Kind:          "qa",
			Result:        quorum.ResultPass,
			ContentDigest: commit,
			CreatedAt:     time.Now().UTC().Add(-time.Minute),
		},
	}

	eval, err := rt.VerifyQuorum(context.Background(), app.QuorumVerifyRequest{
		TaskID:        "TASK-QR-LOW",
		ContentDigest: commit,
		Attestations:  attestations,
	})
	if err != nil {
		t.Fatalf("VerifyQuorum: %v", err)
	}
	if !eval.Satisfied || eval.State != quorum.StateSatisfied {
		t.Fatalf("expected satisfied quorum for R1 task, got %+v", eval)
	}
}

func TestQuorumMergeGateHighRiskTaskRequiresSecurity(t *testing.T) {
	ctx := context.Background()
	commit := "commit222222"

	reqs := app.DeriveQuorumRequirements(model.R2)
	if len(reqs) != 2 {
		t.Fatalf("expected 2 requirements (qa and security) for R2 task, got %d", len(reqs))
	}

	qEngine := quorum.NewEngine(nil)

	// 1. Only QA attestation provided -> Unmet
	qaOnly := []quorum.Attestation{
		{
			Subject:       "agent-qa-1",
			Provider:      "claude",
			Role:          "qa",
			ChangeID:      "TASK-QR-HIGH",
			EvidenceID:    "EVID-001",
			Kind:          "qa",
			Result:        quorum.ResultPass,
			ContentDigest: commit,
			CreatedAt:     time.Now().UTC().Add(-time.Minute),
		},
	}

	eval1, err := qEngine.Evaluate(ctx, reqs, qaOnly, quorum.Provenance{
		ChangeID:      "TASK-QR-HIGH",
		ContentDigest: commit,
	})
	if err != nil {
		t.Fatal(err)
	}
	if eval1.Satisfied || eval1.State == quorum.StateSatisfied {
		t.Fatalf("expected unmet quorum when security attestation is missing, got: %+v", eval1)
	}
	if len(eval1.Missing) != 1 || eval1.Missing[0].Kind != "security" {
		t.Fatalf("expected missing security requirement, got: %+v", eval1.Missing)
	}

	// 2. Both QA and Security attestations provided -> Satisfied
	both := append(qaOnly, quorum.Attestation{
		Subject:       "agent-sec-1",
		Provider:      "codex",
		Role:          "appsec",
		ChangeID:      "TASK-QR-HIGH",
		EvidenceID:    "EVID-002",
		Kind:          "security",
		Result:        quorum.ResultPass,
		ContentDigest: commit,
		CreatedAt:     time.Now().UTC().Add(-time.Minute),
	})

	eval2, err := qEngine.Evaluate(ctx, reqs, both, quorum.Provenance{
		ChangeID:      "TASK-QR-HIGH",
		ContentDigest: commit,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !eval2.Satisfied || eval2.State != quorum.StateSatisfied {
		t.Fatalf("expected satisfied quorum with QA and AppSec, got: %+v", eval2)
	}
}

func TestQuorumMergeGateRejectsStaleAndVetoAttestation(t *testing.T) {
	ctx := context.Background()
	currentCommit := "commit-head-999"
	oldCommit := "commit-old-000"

	reqs := app.DeriveQuorumRequirements(model.R1)
	qEngine := quorum.NewEngine(nil)

	// 1. Stale attestation for old commit is rejected
	stale := []quorum.Attestation{
		{
			Subject:       "agent-qa-1",
			Provider:      "claude",
			Role:          "qa",
			ChangeID:      "TASK-QR-STALE",
			EvidenceID:    "EVID-001",
			Kind:          "qa",
			Result:        quorum.ResultPass,
			ContentDigest: oldCommit, // Does not match currentCommit
			CreatedAt:     time.Now().UTC().Add(-time.Minute),
		},
	}

	_, err := qEngine.Evaluate(ctx, reqs, stale, quorum.Provenance{
		ChangeID:      "TASK-QR-STALE",
		ContentDigest: currentCommit,
	})
	if !errors.Is(err, quorum.ErrStaleAttestation) {
		t.Fatalf("expected ErrStaleAttestation for mismatched digest, got: %v", err)
	}

	// 2. Veto blocks merge
	veto := []quorum.Attestation{
		{
			Subject:       "agent-qa-1",
			Provider:      "claude",
			Role:          "qa",
			ChangeID:      "TASK-QR-VETO",
			EvidenceID:    "EVID-001",
			Kind:          "qa",
			Result:        quorum.ResultVeto,
			ContentDigest: currentCommit,
			CreatedAt:     time.Now().UTC().Add(-time.Minute),
		},
	}

	evalVeto, err := qEngine.Evaluate(ctx, reqs, veto, quorum.Provenance{
		ChangeID:      "TASK-QR-VETO",
		ContentDigest: currentCommit,
	})
	if !errors.Is(err, quorum.ErrVeto) {
		t.Fatalf("expected ErrVeto, got: %v", err)
	}
	if evalVeto.State != quorum.StateBlocked {
		t.Fatalf("expected StateBlocked on veto, got: %s", evalVeto.State)
	}
}

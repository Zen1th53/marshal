package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestM11_ReceiptPersistenceAcrossRestart(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}

	const projectID = "PROJECT-local"
	p := testPrincipal("user-auditor")
	var receiptID string

	// 1. Initial run: remember a record and recall it
	func() {
		rt, err := Open(ctx, repo.Path())
		if err != nil {
			t.Fatal(err)
		}
		defer rt.Close()

		_, err = rt.Memory().Remember(ctx, p, RememberRequest{
			ProjectID: projectID,
			Title:     "Audit Trail Verification Policy",
			Body:      "All state mutations and retrievals must produce immutable cryptographic evidence",
			Kind:      model.MemoryKindDecision,
			Scope:     model.ScopeProject,
		})
		if err != nil {
			t.Fatalf("remember: %v", err)
		}

		res, err := rt.Memory().Recall(ctx, p, RecallRequest{
			ProjectID: projectID,
			Query:     "cryptographic evidence",
		})
		if err != nil {
			t.Fatalf("recall: %v", err)
		}
		if res.Receipt.ReceiptID == "" {
			t.Fatalf("expected receipt ID to be populated in recall receipt: %+v", res.Receipt)
		}
		receiptID = res.Receipt.ReceiptID
	}()

	// 2. Restart runtime and retrieve persisted receipt by ID
	func() {
		rt, err := Open(ctx, repo.Path())
		if err != nil {
			t.Fatal(err)
		}
		defer rt.Close()

		receipt, err := rt.Memory().GetReceipt(ctx, p, projectID, receiptID)
		if err != nil {
			t.Fatalf("GetReceipt after restart: %v", err)
		}
		if receipt.ReceiptID != receiptID || receipt.Query != "" || receipt.QueryDigest != queryDigest("cryptographic evidence") {
			t.Fatalf("unexpected receipt recovered after restart: %+v", receipt)
		}
		if len(receipt.Decisions) == 0 || !receipt.Decisions[0].Included {
			t.Fatalf("expected positive inclusion decision in recovered receipt: %+v", receipt.Decisions)
		}
	}()
}

func TestM11_ReceiptOwnerIsolationAndRawQueryRedaction(t *testing.T) {
	ctx := context.Background()
	_, svc := openTestMemoryService(t)
	owner := testPrincipal("receipt-owner")
	other := testPrincipal("receipt-other")
	response, err := svc.Recall(ctx, owner, RecallRequest{ProjectID: "PROJECT-local", Query: "password=do-not-persist-this"})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if _, err := svc.GetReceipt(ctx, other, "PROJECT-local", response.Receipt.ReceiptID); !errors.Is(err, authz.ErrUnauthorized) {
		t.Fatalf("other caller read receipt: %v", err)
	}
	stored, err := svc.GetReceipt(ctx, owner, "PROJECT-local", response.Receipt.ReceiptID)
	if err != nil {
		t.Fatalf("owner get receipt: %v", err)
	}
	if stored.Query != "" || stored.QueryDigest == "" {
		t.Fatalf("raw query persisted: %+v", stored)
	}
}

func TestM11_ReceiptNonDisclosureOfUnauthorizedScopes(t *testing.T) {
	ctx := context.Background()
	rt, svc := openTestMemoryService(t)

	const projectID = "PROJECT-local"
	pOperator := testPrincipal("operator-1")
	pGuest := testPrincipal("guest-agent")

	// Operator writes an operator-private record
	now := time.Now().UTC()
	privRec := model.MemoryRecordV2{
		ID:         "MEM-PRIV-OPERATOR-99",
		ProjectID:  projectID,
		Kind:       model.MemoryKindDecision,
		Lifecycle:  model.MemoryDurable,
		Confidence: model.ConfidenceVerified,
		Authority:  model.AuthorityOperator,
		Title:      "Super Secret Root Key",
		Body:       "Root key material strictly confidential",
		Scope:      string(model.ScopeOperatorPrivate),
		ScopeID:    "operator-1",
		ACLScope:   "operator-1",
		Source:     model.MemorySource{Kind: "operator", Reference: "operator-1"},
		ObservedAt: now,
		IngestedAt: now,
		ValidFrom:  now,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := rt.Store().WriteMemoryV2(ctx, privRec); err != nil {
		t.Fatalf("write operator private: %v", err)
	}
	svc.IndexRecord(ctx, privRec)

	// Task-scoped record belonging to another task
	otherTaskRec := model.MemoryRecordV2{
		ID:         "MEM-TASK-OTHER-99",
		ProjectID:  projectID,
		Kind:       model.MemoryKindDecision,
		Lifecycle:  model.MemoryDurable,
		Confidence: model.ConfidenceVerified,
		Authority:  model.AuthorityVerified,
		Title:      "Secret Root Key Architecture",
		Body:       "Root key blueprint for other task only",
		Scope:      string(model.ScopeTask),
		ScopeID:    "TASK-OTHER-99",
		Source:     model.MemorySource{Kind: "task", Reference: "TASK-OTHER-99"},
		ObservedAt: now,
		IngestedAt: now,
		ValidFrom:  now,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := rt.Store().WriteMemoryV2(ctx, otherTaskRec); err != nil {
		t.Fatalf("write other task record: %v", err)
	}
	svc.IndexRecord(ctx, otherTaskRec)

	// Guest agent recalls with matching query without allowed scope for TASK-OTHER-99
	res, err := svc.Recall(ctx, pGuest, RecallRequest{
		ProjectID: projectID,
		Query:     "Secret Root Key",
	})
	if err != nil {
		t.Fatalf("guest recall: %v", err)
	}

	// Verify receipt decisions contain ZERO reference to private or foreign task memory IDs
	for _, dec := range res.Receipt.Decisions {
		if dec.MemoryID == privRec.ID || dec.MemoryID == otherTaskRec.ID {
			t.Fatalf("unauthorized memory ID disclosed in receipt decision to guest caller: %+v", dec)
		}
	}
	// Verify aggregate denied count is tracked without leaking identities
	if res.Receipt.DeniedCount == 0 {
		t.Fatalf("expected non-zero DeniedCount in receipt, got %d", res.Receipt.DeniedCount)
	}

	// Operator can recall its record
	opRes, err := svc.Recall(ctx, pOperator, RecallRequest{
		ProjectID: projectID,
		Query:     "Secret Root Key",
	})
	if err != nil {
		t.Fatalf("operator recall: %v", err)
	}
	if len(opRes.Results) != 1 || opRes.Results[0].ID != privRec.ID {
		t.Fatalf("operator could not recall own private record: %+v", opRes)
	}
}

func TestM11_ReceiptLinksOutcomeAndUsesExplicitRetention(t *testing.T) {
	ctx := context.Background()
	rt, svc := openTestMemoryService(t)
	owner := testPrincipal("receipt-run-owner")
	response, err := svc.Recall(ctx, owner, RecallRequest{
		ProjectID: "PROJECT-local", Query: "run context", RunID: "RUN-RECEIPT-LINK",
		TaskID: "TASK-RECEIPT-LINK", Provider: "local-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := svc.CaptureOutcome(ctx, OutcomeCaptureRequest{
		ProjectID: "PROJECT-local", TaskID: "TASK-RECEIPT-LINK", TaskTitle: "receipt linkage",
		RunID: "RUN-RECEIPT-LINK", AgentID: owner.ID, Provider: "local-test", Status: "failed",
		ExitStatus: 7, HeadCommit: "commit-receipt", EvidenceIDs: []string{"EVID-RECEIPT-LINK"},
	})
	if err != nil {
		t.Fatal(err)
	}
	linked, err := svc.GetReceipt(ctx, owner, "PROJECT-local", response.Receipt.ReceiptID)
	if err != nil {
		t.Fatal(err)
	}
	if linked.OutcomeMemoryID != outcome.ID || linked.OutcomeStatus != "failed" || len(linked.EvidenceIDs) != 1 {
		t.Fatalf("receipt was not linked to outcome: %+v", linked)
	}
	if _, err := rt.Store().TombstoneMemory(ctx, "PROJECT-local", outcome.ID, outcome.Revision, "revoked outcome"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetReceipt(ctx, owner, "PROJECT-local", response.Receipt.ReceiptID); err != nil {
		t.Fatalf("memory tombstone rewrote immutable receipt history: %v", err)
	}
	if _, err := svc.PruneReceipts(ctx, owner, "PROJECT-local", time.Now().UTC().Add(time.Hour)); !errors.Is(err, authz.ErrUnauthorized) {
		t.Fatalf("non-admin pruned receipts: %v", err)
	}
	admin := authz.Principal{ID: "receipt-admin", Role: authz.Role{Name: "admin", Authorities: []authz.Authority{authz.AuthorityPolicyAdmin}}}
	count, err := svc.PruneReceipts(ctx, admin, "PROJECT-local", time.Now().UTC().Add(time.Hour))
	if err != nil || count == 0 {
		t.Fatalf("admin retention prune: count=%d err=%v", count, err)
	}
	if _, err := svc.GetReceipt(ctx, owner, "PROJECT-local", response.Receipt.ReceiptID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("pruned receipt remained readable: %v", err)
	}
}

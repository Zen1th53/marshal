package budget

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestBudgetUnknownUsageBlocksDispatch(t *testing.T) {
	limit := int64(100)
	zero := int64(0)
	limits := model.BudgetLimit{MaxTotalTokens: &limit}
	if err := CheckDispatch(limits, model.ConsumedBudget{}, CallRequest{Key: "x", Process: ProcessBuild}); err == nil {
		t.Fatal("unknown tokens allowed")
	}
	if err := CheckDispatch(limits, model.ConsumedBudget{TotalTokens: &zero, HasReportedTokens: true}, CallRequest{Key: "x", Process: ProcessBuild, MaxTokens: &zero}); err != nil {
		t.Fatal(err)
	}
}

func TestVerificationReserveProtectsProcess06Calls(t *testing.T) {
	limits := model.BudgetLimit{MaxModelCalls: 3, VerificationReserveCalls: 1}
	used := model.ConsumedBudget{ModelCalls: 2}
	if err := CheckDispatch(limits, used, CallRequest{Key: "build", Process: ProcessBuild}); err == nil {
		t.Fatal("build consumed reserve")
	}
	if err := CheckDispatch(limits, used, CallRequest{Key: "verify", Process: ProcessVerification}); err != nil {
		t.Fatal(err)
	}
	used.ModelCalls = 3
	used.VerificationCalls = 1
	if err := CheckDispatch(limits, used, CallRequest{Key: "verify2", Process: ProcessVerification}); err == nil {
		t.Fatal("verification exceeded ceiling")
	}
}

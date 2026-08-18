package compiler

import "testing"

func TestCompiledContext(t *testing.T) {
	ctx := CompiledContext{ID: "ctx-1", TaskID: "t-1", TokenCount: 500, BudgetLimit: 1000}
	if ctx.TokenCount > ctx.BudgetLimit {
		t.Fatal("token count exceeds budget")
	}
}

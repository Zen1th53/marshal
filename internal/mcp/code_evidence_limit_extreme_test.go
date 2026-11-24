//go:build !governed && unix

package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Zen1th53/marshal/internal/codeevidence"
	"github.com/Zen1th53/marshal/internal/model"
)

// codeEvidenceBudgetAtCap checks that a budget at the tool's page bound
// passes the adapter to the runtime and one over is a typed refusal that
// names the receipt-bound way on.
func codeEvidenceBudgetAtCap(t *testing.T, tool string, args map[string]any, maximum int) {
	t.Helper()
	relay, caller := relayWorker()
	args["budget"] = float64(maximum)
	if _, err := relay.callTool(context.Background(), caller, tool, args); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("%s at bound did not reach the runtime: %v", tool, err)
	}
	args["budget"] = float64(maximum + 1)
	_, err := relay.callTool(context.Background(), caller, tool, args)
	limit, ok := model.LimitOf(err)
	if !errors.Is(err, model.ErrInvalid) || !ok || limit.CapBytes != int64(maximum) || limit.ActualBytes != int64(maximum+1) || !strings.Contains(limit.Action, "cursor") {
		t.Fatalf("%s one over: %+v %v", tool, limit, err)
	}
	if wire := toolError(tool, err); wire.Data == nil || wire.Data.Code != "invalid_input" || wire.Data.Limit == nil {
		t.Fatalf("%s tool error %+v", tool, wire)
	}
}

func TestCodeContextBudgetAtCap(t *testing.T) {
	codeEvidenceBudgetAtCap(t, "code_context", map[string]any{"task": "Balance"}, codeevidence.MaxContextBytes)
}

func TestCodeExpandBudgetAtCap(t *testing.T) {
	codeEvidenceBudgetAtCap(t, "code_expand", map[string]any{"receipt": "R-1", "cursor": "C-1"}, codeevidence.MaxExpandBytes)
}

// Validation text at its page bound is kept whole; one byte over is cut on
// a UTF-8 boundary and marked truncated.
func TestCodeValidateTextAtCap(t *testing.T) {
	exact := strings.Repeat("a", codeevidence.MaxValidateBytes)
	if text, truncated := boundCodeValidateText(exact); truncated || text != exact {
		t.Fatal("text at the bound was cut")
	}
	over := strings.Repeat("a", codeevidence.MaxValidateBytes-1) + "東"
	text, truncated := boundCodeValidateText(over)
	if !truncated || len(text) > codeevidence.MaxValidateBytes || !utf8.ValidString(text) || !strings.HasPrefix(over, text) {
		t.Fatalf("one over: %d %v", len(text), truncated)
	}
}

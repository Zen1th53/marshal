//go:build governed

package app

import (
	"context"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/execution"
)

// Community's Process 05 fixture runs a mock harness without a provider
// request; the request path is covered directly below.
func captureProcess05ProviderContext(*testing.T, *Runtime, string) func() (string, bool) {
	return func() (string, bool) { return "", false }
}

func TestCommunityProcess05RequestCarriesGovernedBrief(t *testing.T) {
	ctx := withProcess05Brief(context.Background(), "fix", "NOTE\n")
	request := adapter.Request{TrustedContext: "HEADER\n"}
	if err := (&Runtime{}).prepareRequiredProcess05Request(ctx, execution.TaskExecution{TaskID: "fix"}, "", "", "", &request, "KNOWLEDGE"); err != nil {
		t.Fatal(err)
	}
	if request.TrustedContext != "HEADER\nNOTE\nKNOWLEDGE" {
		t.Fatalf("trusted context = %q", request.TrustedContext)
	}
	other := adapter.Request{TrustedContext: "HEADER\n"}
	if err := (&Runtime{}).prepareRequiredProcess05Request(ctx, execution.TaskExecution{TaskID: "other"}, "", "", "", &other, ""); err != nil || strings.Contains(other.TrustedContext, "NOTE") {
		t.Fatalf("another task received the brief: %q %v", other.TrustedContext, err)
	}
}

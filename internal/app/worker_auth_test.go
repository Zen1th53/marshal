package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
)

func TestWorkerAuthIsNeverAQuotaLimit(t *testing.T) {
	for _, message := range []string{
		"HTTP 401 Unauthorized; quota lookup failed",
		"codex is not logged in",
		"missing auth.json",
		"open /home/owner/.codex/auth.json: no such file or directory",
		"authentication failed: usage limit unavailable",
		"credential broker unavailable; governed provider work refused",
	} {
		t.Run(message, func(t *testing.T) {
			if kind, _ := classifyInfraFailure(message); kind != providerAuthFailure {
				t.Fatalf("classified as %q", kind)
			}
			if limited, _, cooldown := classifyProviderLimit(errors.New(message), marshal.HandIn{}, nil); limited || cooldown != 0 {
				t.Fatalf("auth became a quota limit: %v %v", limited, cooldown)
			}
			h := driver.NewHandle(driver.Request{})
			h.SetObserved(marshal.CommandRecord{ExitCode: 1, Output: message})
			h.Complete()
			text, failed := workerFailureText(nil, marshal.HandIn{}, h)
			if !failed || !isProviderAuthFailure(text) {
				t.Fatal("auth in worker output was lost")
			}
		})
	}
	if limited, _, _ := classifyProviderLimit(errors.New("429 quota exhausted"), marshal.HandIn{}, nil); !limited {
		t.Fatal("real quota no longer classified")
	}
	if isProviderAuthFailure(`{"type":"assistant","message":"not logged in"}`) {
		t.Fatal("model content classified as provider auth")
	}
}

func TestWorkerDiagnosticKeepsFirstAndLastLines(t *testing.T) {
	stderr := "first: starting Codex\n" + strings.Repeat("retrying connection\n", 100) + "last: TLS certificate rejected"
	message := workerFailureMessage("Codex execution failed", []byte(stderr), nil)
	_, reason := classifyInfraFailure(message)
	for _, want := range []string{"first: starting Codex", "last: TLS certificate rejected"} {
		if !strings.Contains(reason, want) {
			t.Fatalf("diagnostic lost %q: %s", want, reason)
		}
	}
}

func TestWorkerAuthInStdoutSurvivesDiagnosticSummary(t *testing.T) {
	stdout := []byte(`{"type":"error","message":"401 not logged in"}`)
	message := workerFailureMessage("Codex execution failed", nil, stdout)
	if !isProviderAuthFailure(message) || !strings.Contains(message, "401 not logged in") {
		t.Fatalf("Codex JSON auth error lost: %s", message)
	}
}

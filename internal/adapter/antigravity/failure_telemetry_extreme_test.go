//go:build !governed && unix

package antigravity

import (
	"context"
	"github.com/Zen1th53/marshal/internal/adapter"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestGovernedNativeHostFaultRetainsFiniteOutcome(t *testing.T) {
	for _, tc := range []struct {
		name, script, envelope string
		exit                   int
	}{
		{"native_error", `printf '%s' '{"status":"ERROR","response":"PRIVATE_RAW_SENTINEL"}'`, "non_success_status", 0},
		{"is_error", `printf '%s' '{"status":"SUCCESS","is_error":true,"response":"PRIVATE_RAW_SENTINEL"}'`, "is_error", 0},
		{"missing_status", `printf '%s' '{"response":"PRIVATE_RAW_SENTINEL"}'`, "missing_status", 0},
		{"invalid_json", `printf '%s' 'PRIVATE_RAW_SENTINEL'`, "invalid_json", 0},
		{"host_exit", `printf '%s' '{"status":"SUCCESS","response":"PRIVATE_RAW_SENTINEL"}'; exit 7`, "success", 7},
		{"denied", `printf '%s' '{"status":"SUCCESS","response":"","denied_actions":[{"secret":"PRIVATE_RAW_SENTINEL"}]}'`, "denied_actions", 0},
		{"timeout", `exec /bin/sleep 2`, "invalid_json", -1},
		{"cancelled", `exec /bin/sleep 2`, "invalid_json", -1},
		{"empty", `printf '%s' '{"status":"SUCCESS","response":""}'`, "empty_response", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			binary := filepath.Join(root, "fake-native")
			if err := os.WriteFile(binary, []byte("#!/bin/sh\n"+tc.script+"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			cancel := func() {}
			if tc.name == "timeout" {
				ctx, cancel = context.WithTimeout(ctx, 30*time.Millisecond)
			}
			if tc.name == "cancelled" {
				var stop context.CancelFunc
				ctx, stop = context.WithCancel(ctx)
				timer := time.AfterFunc(30*time.Millisecond, stop)
				cancel = func() { timer.Stop(); stop() }
			}
			defer cancel()
			cmd := exec.CommandContext(ctx, binary)
			cmd.Dir = root
			cmd.Env = []string{"PATH=/usr/bin:/bin"}
			start := time.Now()
			output, err := cmd.Output()
			end := time.Now()
			exit := 0
			if err != nil {
				if e, ok := err.(*exec.ExitError); ok {
					exit = e.ExitCode()
				} else {
					t.Fatal(err)
				}
			}
			c := &Client{governed: true}
			result := c.processResult(adapter.ProcessResult{Stdout: output, ExitCode: exit, StartedAt: start, EndedAt: end, TimedOut: ctx.Err() == context.DeadlineExceeded, Cancelled: ctx.Err() == context.Canceled})
			evidence := c.CollectEvidence(result)
			outcome, ok := evidence["process_outcome"].(map[string]any)
			if !ok || outcome["native_envelope"] != tc.envelope || outcome["exit_code"] != tc.exit {
				t.Fatal("finite actual parser/process outcome missing")
			}
			if outcome["timed_out"] != (tc.name == "timeout") || outcome["cancelled"] != (tc.name == "cancelled") {
				t.Fatal("actual context outcome lost")
			}
			for _, v := range outcome {
				if text, ok := v.(string); ok && text == "PRIVATE_RAW_SENTINEL" {
					t.Fatal("raw native output entered typed outcome")
				}
			}
			c.governed = false
			if _, present := c.CollectEvidence(c.processResult(adapter.ProcessResult{Stdout: output, ExitCode: exit}))["process_outcome"]; present {
				t.Fatal("default native telemetry changed")
			}
		})
	}
}

func TestGovernedOutcomeMetadataRefusesCallerLookalikes(t *testing.T) {
	c := &Client{governed: true}
	for _, r := range []adapter.Result{
		{Status: adapter.StatusFailure, NativeEnvelope: "PRIVATE_RAW_SENTINEL"},
		{Status: adapter.Status("PRIVATE_RAW_SENTINEL"), NativeEnvelope: "invalid_json"},
		{Status: adapter.StatusFailure, NativeEnvelope: "invalid_json", ExitCode: 256},
		{Status: adapter.StatusSuccess, NativeEnvelope: "success", TimedOut: true},
	} {
		if _, ok := c.CollectEvidence(r)["process_outcome"]; ok {
			t.Fatal("invented caller metadata published")
		}
	}
}

//go:build !governed

package app

import (
	"encoding/json"
	"github.com/Zen1th53/marshal/internal/execution"
	"strings"
	"testing"
)

func TestProviderTransportCounterGuidancePreservesBoundValuesAndDigests(t *testing.T) {
	b := providerTransportBinding{Version: 1, Project: "PROJECT-test", Run: "lane-test", Task: "task", CanonicalTask: "TASK-test", Provider: "codex", Model: "model", Endpoint: "chatgpt.com:443", Attempt: 2}
	req := transportApprovalRequest(b)
	for _, literal := range []string{"binding Attempt counts execution-engine admissions, including preflight/resume", "Runtime task_get attempt is a separate canonical-task counter", "neither counter proves prior model calls", "this transport gate precedes the current adapter.Run"} {
		if !strings.Contains(req.Scope, literal) {
			t.Fatalf("transport scope lacks truthful counter guidance %q", literal)
		}
	}
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if req.Parameters != string(raw) || req.CurrentState != string(raw) || !strings.HasSuffix(req.Scope, "exact binding "+string(raw)) {
		t.Fatal("guidance relabeled or changed exact binding")
	}
	_, encoded, ok := strings.Cut(req.Scope, "exact binding ")
	var recovered providerTransportBinding
	if !ok || json.Unmarshal([]byte(encoded), &recovered) != nil || recovered.Attempt != 2 {
		t.Fatal("engine admission attempt was not preserved")
	}
	if transportApprovalRequest(recovered).Scope != req.Scope {
		t.Fatal("scope guidance is not deterministically frozen with binding")
	}
	legacy := req
	legacy.Scope = "brokered provider API only; no worker network or LAN; credential consent is separate; exact binding " + string(raw)
	action := func(r execution.ApprovalRequest) string {
		return execution.ComputeActionDigest(r.OperationType, r.TargetResource, r.DiffPreview, r.Parameters)
	}
	if action(req) != action(legacy) || execution.ComputeStateDigest(req.CurrentState) != execution.ComputeStateDigest(legacy.CurrentState) {
		t.Fatal("presentation changed action/state authority")
	}
	b.Attempt = 1
	changed := transportApprovalRequest(b)
	if action(changed) == action(req) || execution.ComputeStateDigest(changed.CurrentState) == execution.ComputeStateDigest(req.CurrentState) {
		t.Fatal("engine attempt tampering escaped immutable authority")
	}
	if strings.Contains(req.Scope, "zero prior") || strings.Contains(req.Scope, "no prior model calls") {
		t.Fatal("guidance manufactured absence of prior model execution")
	}
}

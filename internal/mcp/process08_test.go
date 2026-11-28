package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/learning"
	"github.com/Zen1th53/marshal/internal/optimization"
)

func p08TestCandidate(id string, dim optimization.Dimension) optimization.Candidate {
	return optimization.Candidate{
		ID:         id,
		Dimension:  dim,
		Hypothesis: "switch route to claude-3-5-sonnet",
		TaskScope:  []string{"code"},
		Evidence: []learning.EvidenceRef{
			{ID: "e1", ClusterID: "cluster-1", Digest: "d1", Kind: "benchmark", Observed: time.Now().UTC()},
		},
		RollbackPlan:     "revert model to baseline",
		VerificationPlan: "run test suite",
		Provenance:       "p08-test",
		ClusterID:        "cluster-1",
		Effects: optimization.Effects{
			WeakensApprovals: false,
		},
	}
}

func seedTestOptimizationCycle(t *testing.T, runtime *app.Runtime, cycleID string) {
	t.Helper()
	st := runtime.Store()
	ctx := context.Background()
	now := time.Now().UTC()

	cand := p08TestCandidate("cand-wire-1", optimization.DimRouting)
	baseline := optimization.Baseline{
		ID:              "base-wire-1",
		MarshalSHA:      "sha-base-1",
		RoutingConfig:   "cfg-base-1",
		VerifierPolicy:  "policy-base-1",
		Toolchain:       "toolchain-base-1",
		EnvironmentHash: "env-base-1",
		RecordedAt:      now,
	}

	cycle, err := optimization.NewCycle(optimization.Cycle{
		ID: cycleID,
		Binding: optimization.Entry{
			ProjectID:       "proj-p08",
			MemoryCommitID:  "mc-p08",
			MemoryVersion:   1,
			MemoryDigest:    "digest-mc-p08",
			SourceSHA:       "sha-run-1",
			TreeDigest:      "tree-digest-1",
			EnvironmentHash: "env-hash-1",
			Outcome:         learning.OutcomeVerifiedComplete,
		},
		Objectives:  []optimization.Objective{{Name: "verified_success", HigherIsBetter: true, Weight: 1.0}},
		Candidates:  []optimization.Candidate{cand},
		Baselines:   []optimization.Baseline{baseline},
		Provenance:  "p08-test",
	}, now)
	if err != nil {
		t.Fatalf("NewCycle: %v", err)
	}
	if err := st.AppendOptimizationCycle(ctx, cycle); err != nil {
		t.Fatalf("AppendOptimizationCycle: %v", err)
	}

	factualRoute := optimization.Route{
		TaskClass:       "code",
		Provider:        "codex",
		ProviderVersion: "1.0",
		Model:           "davinci",
		Harness:         "custom",
		HarnessVersion:  "1.0",
		VerifierPolicy:  "standard",
	}
	altRoute := optimization.Route{
		TaskClass:       "code",
		Provider:        "claude",
		ProviderVersion: "3.5",
		Model:           "sonnet",
		Harness:         "custom",
		HarnessVersion:  "1.0",
		VerifierPolicy:  "standard",
	}
	factual := optimization.FactualRun{
		TaskID:            "task-factual-1",
		Route:             factualRoute,
		Outcome:           learning.OutcomeVerifiedComplete,
		VerifierResult:    optimization.StatusPass,
		ReplayClass:       learning.ReplayExact,
		TreeDigest:        "tree-digest-1",
		EnvironmentDigest: "env-digest-1",
		ObservedAt:        now,
	}
	gov := optimization.Governance{
		GovernableProviders: map[string]bool{"codex": true, "claude": true},
		MaxCanaryExposure:   1.0,
	}
	cf := optimization.Counterfactual{
		ID:                "cf-wire-1",
		Method:            optimization.MethodReplay,
		Factual:           factual,
		Alternate:         altRoute,
		AlternateOutcome:  learning.OutcomeVerifiedComplete,
		AlternateVerifier: optimization.StatusPass,
		Sandbox: optimization.SandboxPolicy{
			WritableRoot:   t.TempDir(),
			MaxWallMillis:  5000,
			MaxMemoryBytes: 1 << 20,
		},
		ClusterID:   "cluster-1",
		Provenance:  "p08-test",
		EvaluatedAt: now,
	}
	cfBuilt, err := optimization.NewCounterfactual(cf, gov, now)
	if err != nil {
		t.Fatalf("NewCounterfactual: %v", err)
	}
	if err := st.AppendCounterfactual(ctx, cycleID, cfBuilt); err != nil {
		t.Fatalf("AppendCounterfactual: %v", err)
	}

	manifest, err := optimization.NewManifest(optimization.BenchmarkManifest{
		ID:               "manifest-wire-1",
		Kind:             optimization.BenchmarkTerminal,
		Version:          "v1.0",
		EvaluatorVersion: "eval-1.0",
		DatasetSnapshot:  "snap-1",
		TaskIDs:          []string{"task-1", "task-2"},
		MarshalSHA:       "sha-marshal-1",
		ConfigDigest:     "cfg-digest-1",
		EnvironmentImage: "env-img-1",
		StartedAt:        now,
		FinishedAt:       now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("NewManifest: %v", err)
	}
	if err := st.AppendBenchmarkManifest(ctx, cycleID, manifest); err != nil {
		t.Fatalf("AppendBenchmarkManifest: %v", err)
	}

	canary := optimization.Canary{
		ID:                  "canary-wire-1",
		CandidateID:         cand.ID,
		BaselineID:          baseline.ID,
		Exposure:            0.1,
		EligibleTaskClasses: []string{"code"},
		MaxTasks:            10,
		Deadline:            now.Add(time.Hour),
		RollbackTriggers: []optimization.Trigger{
			{Metric: "error_rate", Threshold: 0.05, HigherIsWorse: true, Reason: "regression error spike"},
		},
		StartedAt: now,
		State:     optimization.CanaryPending,
	}
	if err := st.AppendCanary(ctx, cycleID, "", canary); err != nil {
		t.Fatalf("AppendCanary: %v", err)
	}
}

func sendMCPWire(t *testing.T, url, token string, reqBody any) (int, map[string]any) {
	t.Helper()
	body, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var res map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatalf("decode response status=%d: %v", resp.StatusCode, err)
	}
	return resp.StatusCode, res
}

// TestProcess08ToolsAreReadOnlyAndCapabilityScoped verifies that all Process 08
// tools exist, are capability-scoped to CapEvidenceRead, and require the
// optimization_id input argument.
func TestProcess08ToolsAreReadOnlyAndCapabilityScoped(t *testing.T) {
	want := map[string]bool{
		"process08_cycle":           false,
		"process08_counterfactuals": false,
		"process08_manifests":       false,
		"process08_canaries":        false,
	}
	srv := NewServer(nil)
	tools := srv.listTools()
	for _, tool := range tools {
		if _, ok := want[tool.Name]; ok {
			want[tool.Name] = true
			if tool.Description == "" {
				t.Errorf("tool %q has empty description", tool.Name)
			}
			req, ok := tool.InputSchema["required"].([]string)
			if !ok || len(req) == 0 || req[0] != "optimization_id" {
				t.Errorf("tool %q missing required optimization_id parameter", tool.Name)
			}
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("Process 08 tool %q absent from MCP tools", name)
			continue
		}
		if got := requiredCapabilityForTool(name); got != auth.CapEvidenceRead {
			t.Errorf("tool %q capability = %q, want %q", name, got, auth.CapEvidenceRead)
		}
	}
}

// TestProcess08ExposesNoMutationTool ensures no mutation tools exist for
// Process 08 in listTools. Process 08 lifecycle actions (start, promote,
// rollback, canary, replay) must remain behind the governed runtime service.
func TestProcess08ExposesNoMutationTool(t *testing.T) {
	forbidden := []string{
		"process08_start_cycle",
		"process08_cycle_start",
		"process08_promote",
		"process08_rollback",
		"process08_canary",
		"process08_manifest",
		"process08_replay",
		"process08_experiment",
		"process08_mutate",
		"process08_commit",
		"process08_invalidate",
		"process08_revise",
	}
	allowedProcess08 := map[string]bool{
		"process08_cycle":           true,
		"process08_counterfactuals": true,
		"process08_manifests":       true,
		"process08_canaries":        true,
	}

	for _, tool := range NewServer(nil).listTools() {
		for _, bad := range forbidden {
			if tool.Name == bad {
				t.Fatalf("MCP exposes a forbidden Process 08 mutation tool: %s", bad)
			}
		}
		if strings.HasPrefix(tool.Name, "process08_") && !allowedProcess08[tool.Name] {
			t.Fatalf("MCP exposes unexpected Process 08 tool: %s", tool.Name)
		}
	}
}

// TestProcess08WireListDiscovery tests that an authenticated client discovers
// all four Process 08 tools over the wire via tools/list, and that no mutation
// tools are exposed.
func TestProcess08WireListDiscovery(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	authMgr := auth.NewManager(t.TempDir())
	token, _, err := authMgr.CreateToken("mcp-client", auth.KindMCPClient, []string{string(auth.CapEvidenceRead)})
	if err != nil {
		t.Fatal(err)
	}

	srv := NewServerWithAuth(runtime, authMgr)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	status, res := sendMCPWire(t, ts.URL, token, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	})
	if status != http.StatusOK {
		t.Fatalf("tools/list status = %d, want 200", status)
	}
	if res["error"] != nil {
		t.Fatalf("unexpected error in tools/list response: %v", res["error"])
	}

	resultMap, _ := res["result"].(map[string]any)
	tools, _ := resultMap["tools"].([]any)
	if len(tools) == 0 {
		t.Fatal("no tools returned in tools/list")
	}

	wantTools := map[string]bool{
		"process08_cycle":           false,
		"process08_counterfactuals": false,
		"process08_manifests":       false,
		"process08_canaries":        false,
	}

	for _, raw := range tools {
		toolMap, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name, _ := toolMap["name"].(string)
		if _, ok := wantTools[name]; ok {
			wantTools[name] = true
			desc, _ := toolMap["description"].(string)
			if desc == "" {
				t.Errorf("wire tool %s has empty description", name)
			}
			schema, _ := toolMap["inputSchema"].(map[string]any)
			if schema["type"] != "object" {
				t.Errorf("wire tool %s schema type = %v, want object", name, schema["type"])
			}
			props, _ := schema["properties"].(map[string]any)
			if props["optimization_id"] == nil {
				t.Errorf("wire tool %s missing optimization_id property", name)
			}
		}
		// Verify no mutation tools on the wire
		if strings.HasPrefix(name, "process08_") && !wantTools[name] && name != "process08_cycle" && name != "process08_counterfactuals" && name != "process08_manifests" && name != "process08_canaries" {
			t.Fatalf("tools/list exposes unexpected Process 08 tool on wire: %s", name)
		}
	}

	for name, found := range wantTools {
		if !found {
			t.Errorf("Process 08 tool %q absent from wire tools/list", name)
		}
	}
}

// TestProcess08WireAuthorizedInvocation tests authenticated and authorized
// invocation over the wire for process08_cycle, process08_counterfactuals,
// process08_manifests, and process08_canaries.
func TestProcess08WireAuthorizedInvocation(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	const cycleID = "opt-wire-p08-test"
	seedTestOptimizationCycle(t, runtime, cycleID)

	authMgr := auth.NewManager(t.TempDir())
	token, _, err := authMgr.CreateToken("mcp-authorized-client", auth.KindMCPClient, []string{string(auth.CapEvidenceRead)})
	if err != nil {
		t.Fatal(err)
	}

	srv := NewServerWithAuth(runtime, authMgr)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Helper to call a tool and unmarshal content text
	callSuccess := func(toolName string, args map[string]any) string {
		t.Helper()
		status, res := sendMCPWire(t, ts.URL, token, map[string]any{
			"jsonrpc": "2.0",
			"id":      time.Now().UnixNano(),
			"method":  "tools/call",
			"params": map[string]any{
				"name":      toolName,
				"arguments": args,
			},
		})
		if status != http.StatusOK {
			t.Fatalf("call %s status = %d, want 200", toolName, status)
		}
		if res["error"] != nil {
			t.Fatalf("call %s returned error: %v", toolName, res["error"])
		}
		resultMap, ok := res["result"].(map[string]any)
		if !ok {
			t.Fatalf("call %s result missing", toolName)
		}
		contentList, ok := resultMap["content"].([]any)
		if !ok || len(contentList) == 0 {
			t.Fatalf("call %s returned empty content", toolName)
		}
		first, ok := contentList[0].(map[string]any)
		if !ok {
			t.Fatalf("call %s content[0] invalid", toolName)
		}
		text, _ := first["text"].(string)
		return text
	}

	// 1. process08_cycle
	cycleText := callSuccess("process08_cycle", map[string]any{"optimization_id": cycleID})
	var cycle optimization.Cycle
	if err := json.Unmarshal([]byte(cycleText), &cycle); err != nil {
		t.Fatalf("unmarshal cycle: %v", err)
	}
	if cycle.ID != cycleID {
		t.Errorf("cycle.ID = %q, want %q", cycle.ID, cycleID)
	}
	if cycle.Digest == "" {
		t.Error("cycle.Digest is empty")
	}
	if cycle.Binding.MemoryCommitID != "mc-p08" {
		t.Errorf("cycle.Binding.MemoryCommitID = %q, want mc-p08", cycle.Binding.MemoryCommitID)
	}
	if len(cycle.Candidates) != 1 || cycle.Candidates[0].ID != "cand-wire-1" {
		t.Errorf("unexpected candidates: %+v", cycle.Candidates)
	}

	// 2. process08_counterfactuals
	cfText := callSuccess("process08_counterfactuals", map[string]any{"optimization_id": cycleID})
	var cfs []optimization.Counterfactual
	if err := json.Unmarshal([]byte(cfText), &cfs); err != nil {
		t.Fatalf("unmarshal counterfactuals: %v", err)
	}
	if len(cfs) != 1 {
		t.Fatalf("len(cfs) = %d, want 1", len(cfs))
	}
	if cfs[0].ID != "cf-wire-1" {
		t.Errorf("cf.ID = %q, want cf-wire-1", cfs[0].ID)
	}
	if cfs[0].Method != optimization.MethodReplay {
		t.Errorf("cf.Method = %q, want %q", cfs[0].Method, optimization.MethodReplay)
	}
	if cfs[0].Digest == "" {
		t.Error("cf.Digest is empty")
	}

	// 3. process08_manifests
	manifestText := callSuccess("process08_manifests", map[string]any{"optimization_id": cycleID})
	var manifests []optimization.BenchmarkManifest
	if err := json.Unmarshal([]byte(manifestText), &manifests); err != nil {
		t.Fatalf("unmarshal manifests: %v", err)
	}
	if len(manifests) != 1 {
		t.Fatalf("len(manifests) = %d, want 1", len(manifests))
	}
	if manifests[0].ID != "manifest-wire-1" {
		t.Errorf("manifest.ID = %q, want manifest-wire-1", manifests[0].ID)
	}
	if manifests[0].Kind != optimization.BenchmarkTerminal {
		t.Errorf("manifest.Kind = %q, want %q", manifests[0].Kind, optimization.BenchmarkTerminal)
	}
	if manifests[0].Digest == "" {
		t.Error("manifest.Digest is empty")
	}

	// 4. process08_canaries
	canaryText := callSuccess("process08_canaries", map[string]any{"optimization_id": cycleID})
	var canaries []optimization.Canary
	if err := json.Unmarshal([]byte(canaryText), &canaries); err != nil {
		t.Fatalf("unmarshal canaries: %v", err)
	}
	if len(canaries) != 1 {
		t.Fatalf("len(canaries) = %d, want 1", len(canaries))
	}
	if canaries[0].ID != "canary-wire-1" {
		t.Errorf("canary.ID = %q, want canary-wire-1", canaries[0].ID)
	}
	if canaries[0].Exposure != 0.1 {
		t.Errorf("canary.Exposure = %f, want 0.1", canaries[0].Exposure)
	}
	if canaries[0].State != optimization.CanaryPending {
		t.Errorf("canary.State = %q, want %q", canaries[0].State, optimization.CanaryPending)
	}

	// 5. Missing optimization_id argument fails gracefully with error
	for _, tool := range []string{"process08_cycle", "process08_counterfactuals", "process08_manifests", "process08_canaries"} {
		status, res := sendMCPWire(t, ts.URL, token, map[string]any{
			"jsonrpc": "2.0",
			"id":      100,
			"method":  "tools/call",
			"params": map[string]any{
				"name":      tool,
				"arguments": map[string]any{},
			},
		})
		if status != http.StatusOK {
			t.Errorf("tool %s with missing arg status = %d, want 200", tool, status)
		}
		errObj, ok := res["error"].(map[string]any)
		if !ok || errObj == nil {
			t.Errorf("tool %s with missing arg did not return JSON-RPC error", tool)
			continue
		}
		errMsg, _ := errObj["message"].(string)
		if !strings.Contains(errMsg, "optimization_id is required") {
			t.Errorf("tool %s error message = %q, want 'optimization_id is required'", tool, errMsg)
		}
	}

	// 6. Nonexistent optimization_id returns error for cycle
	status, res := sendMCPWire(t, ts.URL, token, map[string]any{
		"jsonrpc": "2.0",
		"id":      101,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "process08_cycle",
			"arguments": map[string]any{"optimization_id": "nonexistent-cycle"},
		},
	})
	if status != http.StatusOK {
		t.Errorf("nonexistent cycle status = %d, want 200", status)
	}
	if res["error"] == nil {
		t.Error("expected error for nonexistent cycle")
	}
}

// TestProcess08WireMissingCapabilityRejection tests that tokens lacking
// CapEvidenceRead or unauthenticated requests are rejected on the wire with
// appropriate HTTP status and error codes.
func TestProcess08WireMissingCapabilityRejection(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	authMgr := auth.NewManager(t.TempDir())
	// Token with only status.read capability, lacking evidence.read
	limitedToken, _, err := authMgr.CreateToken("limited-client", auth.KindMCPClient, []string{string(auth.CapStatusRead)})
	if err != nil {
		t.Fatal(err)
	}

	srv := NewServerWithAuth(runtime, authMgr)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	tools := []string{
		"process08_cycle",
		"process08_counterfactuals",
		"process08_manifests",
		"process08_canaries",
	}

	for _, toolName := range tools {
		// 1. Missing capability -> 403 Forbidden with code -32003
		status, res := sendMCPWire(t, ts.URL, limitedToken, map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "tools/call",
			"params": map[string]any{
				"name":      toolName,
				"arguments": map[string]any{"optimization_id": "opt-1"},
			},
		})
		if status != http.StatusForbidden {
			t.Errorf("tool %s with limited token status = %d, want 403", toolName, status)
		}
		errObj, _ := res["error"].(map[string]any)
		if errObj == nil {
			t.Errorf("tool %s missing error object in 403 response", toolName)
		} else {
			code, _ := errObj["code"].(float64)
			if int(code) != -32003 {
				t.Errorf("tool %s error code = %v, want -32003", toolName, code)
			}
			msg, _ := errObj["message"].(string)
			if !strings.Contains(msg, "lacks required capability") || !strings.Contains(msg, string(auth.CapEvidenceRead)) {
				t.Errorf("tool %s error message = %q, want capability notice", toolName, msg)
			}
		}

		// 2. Anonymous request (no token) -> 401 Unauthorized
		status, res = sendMCPWire(t, ts.URL, "", map[string]any{
			"jsonrpc": "2.0",
			"id":      2,
			"method":  "tools/call",
			"params": map[string]any{
				"name":      toolName,
				"arguments": map[string]any{"optimization_id": "opt-1"},
			},
		})
		if status != http.StatusUnauthorized {
			t.Errorf("tool %s anonymous status = %d, want 401", toolName, status)
		}
		errObj, _ = res["error"].(map[string]any)
		if errObj == nil {
			t.Errorf("tool %s anonymous missing error object", toolName)
		} else {
			code, _ := errObj["code"].(float64)
			if int(code) != -32001 {
				t.Errorf("tool %s anonymous error code = %v, want -32001", toolName, code)
			}
		}
	}
}

// TestProcess08WireMutationRejection verifies that attempting to invoke Process
// 08 mutation tools over the wire is rejected.
func TestProcess08WireMutationRejection(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	authMgr := auth.NewManager(t.TempDir())
	evidenceToken, _, err := authMgr.CreateToken("evidence-client", auth.KindMCPClient, []string{string(auth.CapEvidenceRead)})
	if err != nil {
		t.Fatal(err)
	}
	allToken, _, err := authMgr.CreateToken("admin-client", auth.KindLocalUser, []string{string(auth.CapAll)})
	if err != nil {
		t.Fatal(err)
	}

	srv := NewServerWithAuth(runtime, authMgr)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	mutationTools := []string{
		"process08_promote",
		"process08_rollback",
		"process08_start_cycle",
		"process08_canary",
	}

	for _, toolName := range mutationTools {
		// 1. Evidence-read client cannot call mutation tools (rejected with 403 Forbidden)
		status, res := sendMCPWire(t, ts.URL, evidenceToken, map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "tools/call",
			"params": map[string]any{
				"name":      toolName,
				"arguments": map[string]any{"cycle_id": "opt-1"},
			},
		})
		if status != http.StatusForbidden {
			t.Errorf("mutation tool %s status = %d, want 403", toolName, status)
		}
		if res["error"] == nil {
			t.Errorf("mutation tool %s expected error in 403 response", toolName)
		}

		// 2. Even an all-capability admin token receives an unknown tool error,
		// because mutation tools are never registered in MCP.
		status, res = sendMCPWire(t, ts.URL, allToken, map[string]any{
			"jsonrpc": "2.0",
			"id":      2,
			"method":  "tools/call",
			"params": map[string]any{
				"name":      toolName,
				"arguments": map[string]any{"cycle_id": "opt-1"},
			},
		})
		if status != http.StatusOK {
			t.Errorf("admin calling %s status = %d, want 200 with JSON-RPC error", toolName, status)
		}
		errObj, ok := res["error"].(map[string]any)
		if !ok || errObj == nil {
			t.Fatalf("admin calling %s did not receive error: %v", toolName, res)
		}
		errMsg, _ := errObj["message"].(string)
		if !strings.Contains(errMsg, fmt.Sprintf("unknown tool %s", toolName)) {
			t.Errorf("expected unknown tool error, got: %s", errMsg)
		}
	}
}

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/policy"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
)

func TestInitAndJSONDoctor(t *testing.T) {
	repo := cliRepo(t)
	var stdout, stderr bytes.Buffer
	if code := Execute(context.Background(), repo.Path(), []string{"init"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("init code=%d stderr=%s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code := Execute(context.Background(), repo.Path(), []string{"--json", "doctor"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 && code != 2 {
		t.Fatalf("doctor code=%d stderr=%s", code, stderr.String())
	}
	var output map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("doctor output=%q err=%v", stdout.String(), err)
	}
	if output["verdict"] == nil {
		t.Fatalf("doctor output=%#v", output)
	}
}

func TestMutatingCommandFailsUnavailableWithoutDaemon(t *testing.T) {
	repo := cliRepo(t)
	var stdout, stderr bytes.Buffer
	if code := Execute(context.Background(), repo.Path(), []string{"init"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatal(stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code := Execute(context.Background(), repo.Path(), []string{"agent", "register", "--name", "codex", "--role", "developer"}, strings.NewReader(""), &stdout, &stderr)
	if code != 6 || !strings.Contains(stderr.String(), "unavailable") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestTaskImportDryRunDoesNotNeedDaemon(t *testing.T) {
	repo := cliRepo(t)
	tasks := filepath.Join(repo.Path(), "tasks.json")
	if err := os.WriteFile(tasks, []byte(`[{"id":"TASK-001","title":"runtime","status":"ready","risk":"R1"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), repo.Path(), []string{"task", "import", tasks, "--dry-run"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "TASK-001") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRequiredCommandsHaveUsage(t *testing.T) {
	for _, command := range []string{"init", "doctor", "status", "agents", "tasks", "task", "run", "adapters", "adapter", "mcp", "a2a", "events", "artifacts", "verify", "reconcile", "policy", "legal", "daemon"} {
		var stdout, stderr bytes.Buffer
		code := Execute(context.Background(), ".", []string{command, "--help"}, strings.NewReader(""), &stdout, &stderr)
		if code != 0 || !strings.Contains(stdout.String()+stderr.String(), "Usage: marshal ") {
			t.Errorf("%s: code=%d output=%q", command, code, stdout.String()+stderr.String())
		}
	}
}

func TestPolicyTestSubcommandHasUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), ".", []string{"policy", "test", "--help"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String()+stderr.String(), "Usage: marshal policy test SUITE-FILE") {
		t.Fatalf("policy test help code=%d output=%q", code, stdout.String()+stderr.String())
	}
}

func TestAdaptersAndProbeCLI(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), ".", []string{"--json", "adapters"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("adapters code=%d stderr=%s", code, stderr.String())
	}
	var adapters []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &adapters); err != nil {
		t.Fatalf("unmarshal adapters: %v", err)
	}
	if len(adapters) != 4 {
		t.Fatalf("expected 4 adapters, got %d", len(adapters))
	}

	stdout.Reset()
	stderr.Reset()
	code = Execute(context.Background(), ".", []string{"--json", "adapter", "probe", "gemini"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("adapter probe code=%d stderr=%s", code, stderr.String())
	}
}

func TestMCPAndA2AStatusCLI(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), ".", []string{"--json", "mcp", "status"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "2026-07-28") {
		t.Fatalf("mcp status code=%d stdout=%s", code, stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Execute(context.Background(), ".", []string{"--json", "a2a", "status"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "1.0.0") {
		t.Fatalf("a2a status code=%d stdout=%s", code, stdout.String())
	}
}

func cliRepo(t *testing.T) *testgit.Repository {
	t.Helper()
	repo := testgit.New(t)
	for _, name := range []string{"CAPABILITIES.yaml", "PACK-VERSION.yaml", "RUNTIME-VERSION.yaml"} {
		data, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo.Path(), name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

func TestLegalCLI(t *testing.T) {
	repo := cliRepo(t)
	var stdout, stderr bytes.Buffer

	code := Execute(context.Background(), repo.Path(), []string{"legal", "audit"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "MARSHAL Acquisition Due-Diligence Audit") {
		t.Fatalf("legal audit code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Execute(context.Background(), repo.Path(), []string{"legal", "audit", "--json"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("legal audit --json code=%d stderr=%q", code, stderr.String())
	}
	var auditReport map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &auditReport); err != nil {
		t.Fatalf("unmarshal legal audit report: %v", err)
	}
	if auditReport["schema"] != "marshal.acquisition-evidence.v1" {
		t.Fatalf("unexpected audit report schema: %v", auditReport["schema"])
	}

	exportTar := filepath.Join(t.TempDir(), "test-export.tar.gz")
	stdout.Reset()
	stderr.Reset()
	code = Execute(context.Background(), repo.Path(), []string{"legal", "export", "--output", exportTar}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "Evidence pack:") {
		t.Fatalf("legal export code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(exportTar); err != nil {
		t.Fatalf("expected export tar file to exist: %v", err)
	}
}

func TestPolicyTestCLIPassAndFailExitCodes(t *testing.T) {
	repo := cliRepo(t)
	passFile := filepath.Join(repo.Path(), "pass.json")
	if err := os.WriteFile(passFile, policyTestFixture(t, "deny"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := Execute(context.Background(), repo.Path(), []string{"policy", "test", passFile}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("pass code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	failFile := filepath.Join(repo.Path(), "fail.json")
	if err := os.WriteFile(failFile, policyTestFixture(t, "allow"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := Execute(context.Background(), repo.Path(), []string{"policy", "test", failFile}, strings.NewReader(""), &stdout, &stderr); code == 0 {
		t.Fatalf("fail unexpectedly returned zero stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(repo.Path(), ".marshal")); !os.IsNotExist(err) {
		t.Fatalf("read-only policy test created runtime state: err=%v", err)
	}
}

func TestPolicyTestCLIRejectsMalformedAndUnknownFields(t *testing.T) {
	repo := cliRepo(t)
	secret := "MARSHAL_TEST_SECRET_T49_A08_CLI_9f31"
	unknown := filepath.Join(repo.Path(), "unknown.json")
	var document map[string]any
	if err := json.Unmarshal(policyTestFixture(t, "deny"), &document); err != nil {
		t.Fatal(err)
	}
	document["unexpected"] = secret
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unknown, data, 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := Execute(context.Background(), repo.Path(), []string{"policy", "test", unknown}, strings.NewReader(""), &stdout, &stderr); code == 0 {
		t.Fatalf("unknown field returned zero stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String()+stderr.String(), secret) {
		t.Fatalf("secret leaked in CLI output: %q", stdout.String()+stderr.String())
	}
	malformed := filepath.Join(repo.Path(), "malformed.json")
	if err := os.WriteFile(malformed, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := Execute(context.Background(), repo.Path(), []string{"policy", "test", malformed}, strings.NewReader(""), &stdout, &stderr); code == 0 {
		t.Fatalf("malformed input returned zero stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestPolicyTestCLIEvaluatorErrorIsNonzero(t *testing.T) {
	repo := cliRepo(t)
	file := filepath.Join(repo.Path(), "error.json")
	if err := os.WriteFile(file, policyTestFixtureWithDefault(t, "require", "deny"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := Execute(context.Background(), repo.Path(), []string{"--json", "policy", "test", file}, strings.NewReader(""), &stdout, &stderr); code == 0 {
		t.Fatalf("evaluator error returned zero stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), `"status": "ERROR"`) {
		t.Fatalf("missing typed error status: %q", stdout.String())
	}
}

func policyTestFixture(t *testing.T, expected string) []byte {
	return policyTestFixtureWithDefault(t, "deny", expected)
}

func policyTestFixtureWithDefault(t *testing.T, defaultEffect, expected string) []byte {
	t.Helper()
	p := policy.Policy{ID: "cli-policy", Version: 1, Default: policy.Effect(defaultEffect)}
	digest, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	fixture := map[string]any{
		"id": "cli-suite",
		"cases": []any{map[string]any{
			"id":   "case-1",
			"name": "default decision",
			"given": map[string]any{
				"policy":  map[string]any{"id": string(p.ID), "version": p.Version, "default": string(p.Default), "rules": []any{}},
				"binding": map[string]any{"version": p.Version, "digest": string(digest), "generation": 1},
			},
			"when":   map[string]any{"subject_id": "subject-1", "task_id": "task-1", "change_id": "change-1", "action": "read", "resource": "repo"},
			"expect": map[string]any{"decision": expected},
		}},
	}
	data, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(fmt.Errorf("marshal fixture: %w", err))
	}
	return data
}

func TestCLIVersionCommand(t *testing.T) {
	ctx := context.Background()
	var stdout, stderr bytes.Buffer

	code := Run(ctx, []string{"version"}, nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0, got %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "MARSHAL v1.0.1") {
		t.Fatalf("unexpected stdout: %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run(ctx, []string{"--json", "version"}, nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0, got %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"version": "v1.0.1"`) {
		t.Fatalf("unexpected json stdout: %s", stdout.String())
	}
}

func TestMCPA2AServerInsecureRemoteBindRejection(t *testing.T) {
	repo := cliRepo(t)
	dir := repo.Path()
	var stdout, stderr bytes.Buffer
	c := command{
		root:   dir,
		stdout: &stdout,
		stderr: &stderr,
	}

	// 1. MCP serve with --insecure on 0.0.0.0:8080 should fail immediately
	err := c.mcp(context.Background(), []string{"serve", "--listen", "0.0.0.0:8080", "--insecure"})
	if err == nil {
		t.Fatal("expected error for mcp serve with --insecure on non-loopback 0.0.0.0, got nil")
	}
	if !strings.Contains(err.Error(), "--insecure mode is forbidden on non-loopback") {
		t.Fatalf("unexpected error message: %v", err)
	}

	// 2. A2A serve with --insecure on 0.0.0.0:8081 should fail immediately
	err = c.a2a(context.Background(), []string{"serve", "--listen", "0.0.0.0:8081", "--insecure"})
	if err == nil {
		t.Fatal("expected error for a2a serve with --insecure on non-loopback 0.0.0.0, got nil")
	}
	if !strings.Contains(err.Error(), "--insecure mode is forbidden on non-loopback") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestCLITokenCreationCapabilitiesValidation(t *testing.T) {
	repo := cliRepo(t)
	dir := repo.Path()
	var stdout, stderr bytes.Buffer
	c := command{
		root:   dir,
		stdout: &stdout,
		stderr: &stderr,
	}

	// 1. Unknown capability should fail closed
	err := c.auth(context.Background(), []string{"token", "create", "--name", "bad-token", "--capabilities", "task.read,invalid.superpower"})
	if err == nil {
		t.Fatal("expected error for unknown capability, got nil")
	}
	if !strings.Contains(err.Error(), "unknown capability") {
		t.Fatalf("unexpected error message: %v", err)
	}

	// 2. Valid explicit capabilities should succeed
	stdout.Reset()
	err = c.auth(context.Background(), []string{"token", "create", "--name", "good-token", "--kind", "mcp_client", "--capabilities", "status.read,task.read"})
	if err != nil {
		t.Fatalf("unexpected error for valid capabilities: %v", err)
	}
	if !strings.Contains(stdout.String(), "TOKEN-") {
		t.Fatalf("expected token ID in output, got: %s", stdout.String())
	}
}

func TestCLIGCWorktrees(t *testing.T) {
	repo := cliRepo(t)
	var stdout, stderr bytes.Buffer

	if code := Execute(context.Background(), repo.Path(), []string{"init"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatal(stderr.String())
	}

	// 1. gc without args fails with usage
	code := Execute(context.Background(), repo.Path(), []string{"gc"}, strings.NewReader(""), &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected failure for gc without args, got code %d", code)
	}

	// 2. gc worktrees --dry-run
	stdout.Reset()
	stderr.Reset()
	code = Execute(context.Background(), repo.Path(), []string{"gc", "worktrees", "--dry-run"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("gc worktrees --dry-run failed with code %d: stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Inspected:") {
		t.Fatalf("unexpected gc output: %s", stdout.String())
	}
}

func TestCLIGCArtifacts(t *testing.T) {
	repo := cliRepo(t)
	var stdout, stderr bytes.Buffer

	if code := Execute(context.Background(), repo.Path(), []string{"init"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatal(stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code := Execute(context.Background(), repo.Path(), []string{"gc", "artifacts", "--dry-run"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("gc artifacts --dry-run failed with code %d: stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Total:") {
		t.Fatalf("unexpected gc artifacts output: %s", stdout.String())
	}
}

func TestCLIStateBackupVerifyRestore(t *testing.T) {
	repo := cliRepo(t)
	var stdout, stderr bytes.Buffer

	if code := Execute(context.Background(), repo.Path(), []string{"init"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatal(stderr.String())
	}

	backupFile := filepath.Join(t.TempDir(), "test_backup.db")

	// 1. marshal state backup --output ...
	stdout.Reset()
	stderr.Reset()
	code := Execute(context.Background(), repo.Path(), []string{"state", "backup", "--output", backupFile}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("state backup failed with code %d: stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Backup created:") {
		t.Fatalf("unexpected state backup output: %s", stdout.String())
	}

	// 2. marshal state verify-backup ...
	stdout.Reset()
	stderr.Reset()
	code = Execute(context.Background(), repo.Path(), []string{"state", "verify-backup", backupFile}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("state verify-backup failed with code %d: stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Backup verified:") {
		t.Fatalf("unexpected state verify-backup output: %s", stdout.String())
	}

	// 3. marshal state restore ...
	stdout.Reset()
	stderr.Reset()
	code = Execute(context.Background(), repo.Path(), []string{"state", "restore", backupFile}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("state restore failed with code %d: stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "successfully restored") {
		t.Fatalf("unexpected state restore output: %s", stdout.String())
	}
}

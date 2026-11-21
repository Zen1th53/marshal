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

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
	"github.com/Zen1th53/marshal/internal/tui"
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
	if code != 6 || !strings.Contains(stderr.String(), "background service isn't running") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

// TestCLIRuntimeNotRunningMessage: a command that needs the daemon prints the
// plain background-service sentence in text mode and keeps the unavailable exit
// code.
func TestCLIRuntimeNotRunningMessage(t *testing.T) {
	repo := cliRepo(t)
	var stdout, stderr bytes.Buffer
	if code := Execute(context.Background(), repo.Path(), []string{"init"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatal(stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	// status and tasks read the store without the service; agents needs it.
	code := Execute(context.Background(), repo.Path(), []string{"agents"}, strings.NewReader(""), &stdout, &stderr)
	want := "Marshal's background service isn't running in this project. Start it with `marshal daemon` in another terminal, then try again.\n"
	if stderr.String() != want {
		t.Fatalf("stderr = %q, want %q", stderr.String(), want)
	}
	if code != exitCode(model.ErrUnavailable) {
		t.Fatalf("code = %d, want %d", code, exitCode(model.ErrUnavailable))
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
	for _, command := range []string{"init", "doctor", "status", "agents", "tasks", "task", "run", "adapter", "mcp", "events", "artifacts", "verify", "reconcile", "legal", "daemon"} {
		var stdout, stderr bytes.Buffer
		code := Execute(context.Background(), ".", []string{command, "--help"}, strings.NewReader(""), &stdout, &stderr)
		if code != 0 || !strings.Contains(stdout.String()+stderr.String(), "Usage: marshal ") {
			t.Errorf("%s: code=%d output=%q", command, code, stdout.String()+stderr.String())
		}
	}
}

func TestAdapterProbeCLI(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), ".", []string{"--json", "adapter", "probe", "agy"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("adapter probe code=%d stderr=%s", code, stderr.String())
	}
}

func TestMCPStatusCLI(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), ".", []string{"--json", "mcp", "status"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "2026-07-28") {
		t.Fatalf("mcp status code=%d stdout=%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "providers") {
		t.Fatalf("mcp status JSON should include providers: %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Execute(context.Background(), ".", []string{"mcp", "status"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "Provider connections:") {
		t.Fatalf("mcp status text should include Provider connections: code=%d stdout=%s", code, stdout.String())
	}
}

func TestRetiredGeminiCLIIsNotProbed(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), ".", []string{"--json", "adapter", "probe", "gemini"}, strings.NewReader(""), &stdout, &stderr)
	if code == 0 || !strings.Contains(stdout.String(), "unsupported adapter") {
		t.Fatalf("retired adapter accepted: code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestCommunityRejectsRemovedEnterpriseWebCommand(t *testing.T) {
	t.Setenv("MARSHAL_ENTERPRISE", "1")
	t.Setenv("MARSHAL_ENTERPRISE_TIER", "enterprise")
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), ".", []string{"web", "serve"}, strings.NewReader(""), &stdout, &stderr)
	if code == 0 || !strings.Contains(stderr.String(), "unknown command web") {
		t.Fatalf("enterprise entitlement exposed removed web command: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
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

func TestCLIDevelopmentVersion(t *testing.T) {
	if Version != "v0.2.0" {
		t.Fatalf("local build version = %q, want v0.2.0", Version)
	}
}

func TestCLIVersionCommand(t *testing.T) {
	ctx := context.Background()
	var stdout, stderr bytes.Buffer

	code := Run(ctx, []string{"version"}, nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0, got %d, stderr: %s", code, stderr.String())
	}
	if tui.Edition() == "Marshal" {
		if !strings.Contains(stdout.String(), "Marshal") {
			t.Fatalf("Marshal version output omits its edition: %s", stdout.String())
		}
	}
	if !strings.Contains(stdout.String(), "Marshal "+Version+" (") {
		t.Fatalf("unexpected stdout: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), fmt.Sprintf("schema: v%d, constitution: %s", store.LatestSchemaVersion, constitutionVersionString())) {
		t.Fatalf("missing separately labelled runtime versions: %s", stdout.String())
	}
	if tui.BuildVersion != Version {
		t.Fatalf("TUI version %q differs from CLI version %q", tui.BuildVersion, Version)
	}
	screen := tui.RenderStyledScreen(tui.UIState{}, tui.NewTheme(tui.ThemeNoColor, false, false), 160)
	if !strings.Contains(screen, "Marshal "+Version+" CONTROL PLANE") {
		t.Fatalf("TUI banner does not report CLI version %q: %s", Version, screen)
	}
	stdout.Reset()
	stderr.Reset()
	code = Run(ctx, []string{"--json", "version"}, nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0, got %d, stderr: %s", code, stderr.String())
	}
	var result struct {
		Version             string `json:"version"`
		Edition             string `json:"edition"`
		SchemaVersion       int    `json:"schema_version"`
		ConstitutionVersion string `json:"constitution_version"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Version != Version || result.SchemaVersion != store.LatestSchemaVersion || result.ConstitutionVersion != constitutionVersionString() {
		t.Fatalf("unexpected json stdout: %s", stdout.String())
	}
	if tui.Edition() == "Marshal" && result.Edition != "Marshal" {
		t.Fatalf("JSON version output edition = %q, want Marshal", result.Edition)
	}
	if tui.Edition() != "Marshal" && result.Edition != "" {
		t.Fatalf("default JSON version output changed: %s", stdout.String())
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

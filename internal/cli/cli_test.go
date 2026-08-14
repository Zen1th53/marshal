package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	for _, command := range []string{"init", "doctor", "status", "agents", "tasks", "task", "run", "adapters", "adapter", "mcp", "a2a", "events", "artifacts", "verify", "reconcile", "legal", "daemon"} {
		var stdout, stderr bytes.Buffer
		code := Execute(context.Background(), ".", []string{command, "--help"}, strings.NewReader(""), &stdout, &stderr)
		if code != 0 || !strings.Contains(stdout.String()+stderr.String(), "Usage: marshal ") {
			t.Errorf("%s: code=%d output=%q", command, code, stdout.String()+stderr.String())
		}
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

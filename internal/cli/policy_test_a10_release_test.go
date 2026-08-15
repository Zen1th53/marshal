package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestA10PolicyTestCLIJSONIncludesDigestAndRemainsReadOnly(t *testing.T) {
	repo := cliRepo(t)
	suiteFile := filepath.Join(repo.Path(), "a10-pass.json")
	if err := os.WriteFile(suiteFile, policyTestFixture(t, "deny"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), repo.Path(), []string{"--json", "policy", "test", suiteFile}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("JSON policy test code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var report struct {
		Status       string `json:"status"`
		PolicyDigest string `json:"policy_digest"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode JSON report: %v; output=%q", err, stdout.String())
	}
	if report.Status != "PASS" || !strings.HasPrefix(report.PolicyDigest, "sha256:") {
		t.Fatalf("report=%#v, want PASS with exact policy digest", report)
	}
	if _, err := os.Stat(filepath.Join(repo.Path(), ".marshal")); !os.IsNotExist(err) {
		t.Fatalf("read-only policy test created runtime state: err=%v", err)
	}
}

package policy

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestCapabilityPolicySemanticDecisions(t *testing.T) {
	engine := loadRepositoryPolicy(t)
	base := model.PolicyInput{
		AgentID: "AGENT-dev", SessionID: "SESSION-dev", Role: model.RoleDeveloper,
		TaskID: "TASK-001", Risk: model.R1, TaskOwned: true, TargetInScope: true,
		Environment: "local",
	}
	tests := []struct {
		name      string
		operation model.Operation
		mutate    func(*model.PolicyInput)
		want      model.Decision
	}{
		{name: "read", operation: model.FilesystemRead, want: model.Allow},
		{name: "task write", operation: model.FilesystemWrite, want: model.Allow},
		{name: "out of scope write", operation: model.FilesystemWrite, mutate: func(in *model.PolicyInput) { in.TargetInScope = false }, want: model.Deny},
		{name: "shell", operation: model.ShellExecute, want: model.Allow},
		{name: "required network", operation: model.NetworkAccess, mutate: func(in *model.PolicyInput) { in.Required = true }, want: model.Allow},
		{name: "unrequired network", operation: model.NetworkAccess, want: model.Deny},
		{name: "commit", operation: model.GitCommit, want: model.Allow},
		{name: "push", operation: model.GitPush, want: model.Deny},
		{name: "history rewrite", operation: model.GitHistoryRewrite, want: model.Deny},
		{name: "secret", operation: model.SecretRead, want: model.Deny},
		{name: "explicit secret", operation: model.SecretRead, mutate: func(in *model.PolicyInput) { in.ExplicitPermission = true }, want: model.Allow},
		{name: "upload", operation: model.ExternalUpload, want: model.Deny},
		{name: "deploy", operation: model.Deploy, want: model.RequireApproval},
		{name: "destructive", operation: model.DestructiveOperation, want: model.RequireApproval},
		{name: "production write", operation: model.FilesystemWrite, mutate: func(in *model.PolicyInput) { in.Production = true }, want: model.Deny},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := base
			input.Operation = tt.operation
			if tt.mutate != nil {
				tt.mutate(&input)
			}
			got := engine.Decide(input)
			if got.Decision != tt.want {
				t.Fatalf("decision = %s (%s), want %s", got.Decision, got.Reason, tt.want)
			}
			if got.Rule == "" || got.Reason == "" {
				t.Fatalf("decision lacks evidence: %#v", got)
			}
		})
	}
}

func TestDeniedOperationDoesNotExecute(t *testing.T) {
	engine := loadRepositoryPolicy(t)
	called := false
	input := model.PolicyInput{
		AgentID: "AGENT-dev", SessionID: "SESSION-dev", Role: model.RoleDeveloper,
		TaskID: "TASK-001", Risk: model.R1, TaskOwned: true, TargetInScope: true,
		Operation: model.GitHistoryRewrite, Environment: "local",
	}
	err := Enforce(engine, input, func() error {
		called = true
		return nil
	})
	if !errors.Is(err, model.ErrPolicyDenied) || called {
		t.Fatalf("err=%v called=%v", err, called)
	}
}

func TestLoadRejectsUnknownOrIncompletePolicy(t *testing.T) {
	dir := t.TempDir()
	unknown := filepath.Join(dir, "unknown.yaml")
	writeFile(t, unknown, "version: 1\nprinciple: test\ndefault: {}\nroles: {}\ndangerous_operations: []\nunknown: true\n")
	if _, err := Load(unknown); err == nil {
		t.Fatal("unknown policy field accepted")
	}
	incomplete := filepath.Join(dir, "incomplete.yaml")
	writeFile(t, incomplete, "version: 1\nprinciple: test\ndefault: {}\nroles: {}\ndangerous_operations: []\n")
	if _, err := Load(incomplete); err == nil {
		t.Fatal("incomplete default policy accepted")
	}
}

func TestLoadRejectsUnknownCapabilityKey(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "CAPABILITIES.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	modified := strings.Replace(string(source), "  filesystem_read: allow\n", "  filesystem_read: allow\n  invented_capability: allow\n", 1)
	path := filepath.Join(t.TempDir(), "unknown-capability.yaml")
	writeFile(t, path, modified)
	if _, err := Load(path); err == nil {
		t.Fatal("unknown default capability accepted")
	}
}

func loadRepositoryPolicy(t *testing.T) *Engine {
	t.Helper()
	engine, err := Load(filepath.Join("..", "..", "CAPABILITIES.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return engine
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

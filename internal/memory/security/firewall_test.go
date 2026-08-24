package security_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/security"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT86FirewallDetectsSecretsInBodyAndMetadata(t *testing.T) {
	fw := security.NewFirewall(security.FirewallConfig{
		CanarySecrets: []string{"canary-super-secret-token-xyz"},
	})
	ctx := context.Background()

	// 1. Private key in body
	recPrivateKey := model.MemoryRecordV2{
		ID:        "MEM-SEC-01",
		ProjectID: "PROJ-1",
		Kind:      model.MemoryKindSemantic,
		Lifecycle: model.MemoryCandidate,
		Title:     "Deployment Notes",
		Body:      "Here is the key: -----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA0...",
		Scope:     string(model.ScopeProject),
		ScopeID:   "PROJ-1",
	}

	err := fw.ScanRecord(ctx, recPrivateKey)
	if !errors.Is(err, security.ErrSecretDetected) {
		t.Fatalf("expected ErrSecretDetected for private key, got: %v", err)
	}

	// Verify error does NOT contain raw secret text
	if strings.Contains(err.Error(), "MIIEowIBAAKCAQEA0") {
		t.Fatal("security firewall echoed secret text in error string")
	}

	// 2. GitHub Token in Title
	recGithubToken := model.MemoryRecordV2{
		ID:        "MEM-SEC-02",
		ProjectID: "PROJ-1",
		Kind:      model.MemoryKindSemantic,
		Lifecycle: model.MemoryCandidate,
		Title:     "Run with token ghp_1234567890abcdefghijklmnopqrstuvwxyzAB",
		Body:      "Clean body",
		Scope:     string(model.ScopeProject),
		ScopeID:   "PROJ-1",
	}
	err = fw.ScanRecord(ctx, recGithubToken)
	if !errors.Is(err, security.ErrSecretDetected) {
		t.Fatalf("expected ErrSecretDetected for GitHub token in title, got: %v", err)
	}

	// 3. Canary secret in ExtMeta
	recCanaryInMeta := model.MemoryRecordV2{
		ID:        "MEM-SEC-03",
		ProjectID: "PROJ-1",
		Kind:      model.MemoryKindSemantic,
		Lifecycle: model.MemoryCandidate,
		Title:     "Clean title",
		Body:      "Clean body",
		Scope:     string(model.ScopeProject),
		ScopeID:   "PROJ-1",
		ExtMeta: map[string]any{
			"debug_token": "canary-super-secret-token-xyz",
		},
	}
	err = fw.ScanRecord(ctx, recCanaryInMeta)
	if !errors.Is(err, security.ErrSecretDetected) {
		t.Fatalf("expected ErrSecretDetected for canary in ExtMeta, got: %v", err)
	}

	// 4. Connection String with credentials
	recConnStr := model.MemoryRecordV2{
		ID:        "MEM-SEC-04",
		ProjectID: "PROJ-1",
		Kind:      model.MemoryKindSemantic,
		Lifecycle: model.MemoryCandidate,
		Title:     "Database configuration",
		Body:      "Connect using postgres://dbadmin:P@ssw0rd123!@db.internal:5432/prod",
		Scope:     string(model.ScopeProject),
		ScopeID:   "PROJ-1",
	}
	err = fw.ScanRecord(ctx, recConnStr)
	if !errors.Is(err, security.ErrSecretDetected) {
		t.Fatalf("expected ErrSecretDetected for database URI credentials, got: %v", err)
	}

	// 5. Clean record passes without error
	cleanRec := model.MemoryRecordV2{
		ID:        "MEM-SEC-05",
		ProjectID: "PROJ-1",
		Kind:      model.MemoryKindSemantic,
		Lifecycle: model.MemoryCandidate,
		Title:     "Clean Architecture Decision",
		Body:      "All state is stored in SQLite with WAL mode and foreign keys enabled.",
		Scope:     string(model.ScopeProject),
		ScopeID:   "PROJ-1",
	}
	if err := fw.ScanRecord(ctx, cleanRec); err != nil {
		t.Fatalf("clean record should pass firewall, got: %v", err)
	}
}

func TestFirewallRejectsRuntimeCredentialFormats(t *testing.T) {
	fw := security.NewFirewall(security.FirewallConfig{})
	for name, value := range map[string]string{
		"jwt":           "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1c2VyLTEyMyJ9.c2lnbmF0dXJlMTIzNDU2",
		"authorization": "Authorization: Bearer credential-value-1234567890",
		"cookie":        "Cookie: session_id=credential-value-1234567890",
		"oauth":         "ya29.A0ARrdaMcredentialvalue1234567890",
	} {
		t.Run(name, func(t *testing.T) {
			if err := fw.ScanText(value); !errors.Is(err, security.ErrSecretDetected) {
				t.Fatalf("credential was accepted: %v", err)
			}
		})
	}
}

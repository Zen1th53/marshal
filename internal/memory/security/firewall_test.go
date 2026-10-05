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

func TestFirewallRejectsAllCredentialShapes(t *testing.T) {
	fw := security.NewFirewall(security.FirewallConfig{})
	ctx := context.Background()

	cases := []struct {
		name        string
		secretVal   string
		setupRecord func(secret string) model.MemoryRecordV2
	}{
		{
			name:      "sk- api key in body",
			secretVal: "sk-proj-abc1234567890def1234567890",
			setupRecord: func(s string) model.MemoryRecordV2 {
				return model.MemoryRecordV2{
					ID: "M-1", ProjectID: "P-1", Title: "Title", Body: "key: " + s,
					Scope: string(model.ScopeProject), ScopeID: "P-1",
				}
			},
		},
		{
			name:      "ghp_ github token in title",
			secretVal: "ghp_0123456789abcdefghijklmnopqrstuvwxyz",
			setupRecord: func(s string) model.MemoryRecordV2 {
				return model.MemoryRecordV2{
					ID: "M-2", ProjectID: "P-1", Title: "token " + s, Body: "clean",
					Scope: string(model.ScopeProject), ScopeID: "P-1",
				}
			},
		},
		{
			name:      "github_pat_ fine-grained token in source reference",
			secretVal: "github_pat_11ABCD0123456789_abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
			setupRecord: func(s string) model.MemoryRecordV2 {
				return model.MemoryRecordV2{
					ID: "M-3", ProjectID: "P-1", Title: "Title", Body: "clean",
					Scope: string(model.ScopeProject), ScopeID: "P-1",
					Source: model.MemorySource{Kind: "commit", Reference: "ref-" + s},
				}
			},
		},
		{
			name:      "AKIA aws key in evidence ID",
			secretVal: "AKIAIOSFODNN7EXAMPLE",
			setupRecord: func(s string) model.MemoryRecordV2 {
				return model.MemoryRecordV2{
					ID: "M-4", ProjectID: "P-1", Title: "Title", Body: "clean",
					Scope: string(model.ScopeProject), ScopeID: "P-1",
					EvidenceIDs: []string{"ev-1", s},
				}
			},
		},
		{
			name:      "AKIA0 honeypot decoy in body",
			secretVal: "AKIA0ABCDEF234567890",
			setupRecord: func(s string) model.MemoryRecordV2 {
				return model.MemoryRecordV2{
					ID: "M-5", ProjectID: "P-1", Title: "Title", Body: "AWS_ACCESS_KEY_ID=" + s,
					Scope: string(model.ScopeProject), ScopeID: "P-1",
				}
			},
		},
		{
			name:      "xox slack token in metadata",
			secretVal: "xoxb-1234567890-abcdef123456",
			setupRecord: func(s string) model.MemoryRecordV2 {
				return model.MemoryRecordV2{
					ID: "M-6", ProjectID: "P-1", Title: "Title", Body: "clean",
					Scope: string(model.ScopeProject), ScopeID: "P-1",
					ExtMeta: map[string]any{"token": s},
				}
			},
		},
		{
			name:      "private key block in body",
			secretVal: "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNza...\n-----END OPENSSH PRIVATE KEY-----",
			setupRecord: func(s string) model.MemoryRecordV2 {
				return model.MemoryRecordV2{
					ID: "M-7", ProjectID: "P-1", Title: "Title", Body: s,
					Scope: string(model.ScopeProject), ScopeID: "P-1",
				}
			},
		},
		{
			name:      "bearer token in body",
			secretVal: "Bearer secret_bearer_token_12345",
			setupRecord: func(s string) model.MemoryRecordV2 {
				return model.MemoryRecordV2{
					ID: "M-8", ProjectID: "P-1", Title: "Title", Body: "auth: " + s,
					Scope: string(model.ScopeProject), ScopeID: "P-1",
				}
			},
		},
		{
			name:      "short password=hunter2 in body",
			secretVal: "password=hunter2",
			setupRecord: func(s string) model.MemoryRecordV2 {
				return model.MemoryRecordV2{
					ID: "M-9", ProjectID: "P-1", Title: "Title", Body: s,
					Scope: string(model.ScopeProject), ScopeID: "P-1",
				}
			},
		},
		{
			name:      ".netrc content in body",
			secretVal: "machine api.github.com login dev password mysecretnetrcpass",
			setupRecord: func(s string) model.MemoryRecordV2 {
				return model.MemoryRecordV2{
					ID: "M-10", ProjectID: "P-1", Title: "Title", Body: s,
					Scope: string(model.ScopeProject), ScopeID: "P-1",
				}
			},
		},
		{
			name:      "aws credentials file content in body",
			secretVal: "[default]\naws_access_key_id = AKIAIOSFODNN7EXAMPLE\naws_secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			setupRecord: func(s string) model.MemoryRecordV2 {
				return model.MemoryRecordV2{
					ID: "M-11", ProjectID: "P-1", Title: "Title", Body: s,
					Scope: string(model.ScopeProject), ScopeID: "P-1",
				}
			},
		},
		{
			name:      "hosts.yml content in body",
			secretVal: "github.com:\n    oauth_token: ghp_1234567890abcdefghijklmnopqrstuvwxyzAB\n    git_protocol: https",
			setupRecord: func(s string) model.MemoryRecordV2 {
				return model.MemoryRecordV2{
					ID: "M-12", ProjectID: "P-1", Title: "Title", Body: s,
					Scope: string(model.ScopeProject), ScopeID: "P-1",
				}
			},
		},
		{
			name:      "high entropy token next to key-like name",
			secretVal: "custom_app_token: a1b2c3d4e5f67890abcdef1234567890",
			setupRecord: func(s string) model.MemoryRecordV2 {
				return model.MemoryRecordV2{
					ID: "M-13", ProjectID: "P-1", Title: "Title", Body: s,
					Scope: string(model.ScopeProject), ScopeID: "P-1",
				}
			},
		},
		{
			name:      "honeypot .env file decoy",
			secretVal: "GITHUB_TOKEN=ghp_0123456789abcdef0123456789abcdef\nAWS_SECRET_ACCESS_KEY=0123456789abcdef0123456789abcdef01234567",
			setupRecord: func(s string) model.MemoryRecordV2 {
				return model.MemoryRecordV2{
					ID: "M-14", ProjectID: "P-1", Title: "Title", Body: s,
					Scope: string(model.ScopeProject), ScopeID: "P-1",
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := tc.setupRecord(tc.secretVal)
			err := fw.ScanRecord(ctx, rec)
			if !errors.Is(err, security.ErrSecretDetected) {
				t.Fatalf("expected ErrSecretDetected for %s, got: %v", tc.name, err)
			}
			// Crucial check: verify raw secret text never echoes in the error message
			if strings.Contains(err.Error(), tc.secretVal) {
				t.Fatalf("error echoed secret value %q: %v", tc.secretVal, err)
			}
		})
	}
}

func TestFirewallAllowsRuntimeIdentifiersAndDigests(t *testing.T) {
	fw := security.NewFirewall(security.FirewallConfig{})
	digest := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	rec := model.MemoryRecordV2{
		Title: "Rootless Execution", Body: "Use the approved worktree",
		WorktreeID: "WT-runtime-secret:" + digest, ScopeID: "PROJECT-secret:" + digest,
		EvidenceIDs: []string{"EVENT-token:" + digest}, ContentDigest: digest,
		ExtMeta: map[string]any{"provider_secret_digest": digest, "token_hash": digest},
	}
	if err := fw.ScanRecord(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"WT-runtime-secret:" + digest, "provider_secret_digest: " + digest, "token_hash=" + digest, "secret_id=" + digest, "worktree_key_digest=" + digest, "monkey=" + digest} {
		if err := fw.ScanText(text); err != nil {
			t.Fatalf("identifier %q rejected: %v", text, err)
		}
	}
	// Hexadecimal credentials remain credentials when assigned to credential names.
	for _, text := range []string{"secret=" + digest, "custom_app_token: " + digest, "password=hunter2"} {
		if !errors.Is(fw.ScanText(text), security.ErrSecretDetected) {
			t.Fatal("credential accepted")
		}
	}
}

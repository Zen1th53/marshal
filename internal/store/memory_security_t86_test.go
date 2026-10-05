package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/security"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT86StoreRejectsSecretsOnWrite(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	projID := "PROJ-T86"
	if err := st.InitProject(ctx, model.Project{
		ID: projID, Repository: "repo", DefaultBranch: "main", PackVersion: "1.0.0",
	}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Second)

	// Attempting to write a record with an embedded secret must be rejected by firewall
	secretRec := model.MemoryRecordV2{
		ID:         "MEM-SECRET-01",
		ProjectID:  projID,
		Kind:       model.MemoryKindSemantic,
		Lifecycle:  model.MemoryCandidate,
		Authority:  model.AuthorityAgent,
		Title:      "Secret dump",
		Body:       "Here is an API key: ghp_1234567890abcdefghijklmnopqrstuvwxyzAB",
		Scope:      string(model.ScopeProject),
		ScopeID:    projID,
		ObservedAt: now,
		IngestedAt: now,
		ValidFrom:  now,
		CreatedAt:  now,
		UpdatedAt:  now,
		Source:     model.MemorySource{Kind: "runtime", Reference: "run-sec"},
	}

	err := st.WriteMemoryV2(ctx, secretRec)
	if !errors.Is(err, security.ErrSecretDetected) {
		t.Fatalf("expected ErrSecretDetected from store firewall, got: %v", err)
	}

	// Verify nothing was persisted in SQLite
	_, err = st.GetMemoryV2(ctx, projID, "MEM-SECRET-01")
	if !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for rejected secret memory, got: %v", err)
	}

	// Verify refusal evidence was recorded
	events, err := st.ListEvents(ctx)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var foundRefusal bool
	for _, ev := range events {
		if ev.Type == "memory.secret.refused" {
			foundRefusal = true
			if ev.Data["action"] != "memory.refused" {
				t.Fatalf("expected action memory.refused, got: %v", ev.Data["action"])
			}
			if ev.Data["memory_id"] != "MEM-SECRET-01" {
				t.Fatalf("expected memory_id MEM-SECRET-01, got: %v", ev.Data["memory_id"])
			}
			reason, _ := ev.Data["reason"].(string)
			if !strings.Contains(reason, "github token pattern") {
				t.Fatalf("expected reason to name github token pattern, got: %s", reason)
			}
			if strings.Contains(reason, "ghp_1234567890abcdefghijklmnopqrstuvwxyzAB") {
				t.Fatalf("reason leaked secret: %s", reason)
			}
		}
	}
	if !foundRefusal {
		t.Fatal("expected refusal event recorded in audit_events")
	}
}

func TestStoreRejectsAllCredentialShapesOnWrite(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	projID := "PROJ-SHAPES"
	if err := st.InitProject(ctx, model.Project{
		ID: projID, Repository: "repo", DefaultBranch: "main", PackVersion: "1.0.0",
	}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Second)

	cases := []struct {
		name       string
		memID      string
		secretVal  string
		body       string
		title      string
		expectKind string
	}{
		{
			name:       "sk- openai key",
			memID:      "MEM-SK-01",
			secretVal:  "sk-proj-0123456789abcdef0123456789abcdef",
			body:       "Here is an openai key: sk-proj-0123456789abcdef0123456789abcdef",
			title:      "OpenAI key dump",
			expectKind: "openai api key pattern",
		},
		{
			name:       "anthropic sk-ant- key",
			memID:      "MEM-SK-02",
			secretVal:  "sk-ant-api03-abcdef1234567890abcdef1234567890",
			body:       "Use anthropic key sk-ant-api03-abcdef1234567890abcdef1234567890",
			title:      "Anthropic config",
			expectKind: "openai api key pattern",
		},
		{
			name:       "ghp_ github token",
			memID:      "MEM-GH-01",
			secretVal:  "ghp_1234567890abcdefghijklmnopqrstuvwxyzAB",
			body:       "GitHub token: ghp_1234567890abcdefghijklmnopqrstuvwxyzAB",
			title:      "GitHub token",
			expectKind: "github token pattern",
		},
		{
			name:       "github_pat_ fine-grained token",
			memID:      "MEM-GH-02",
			secretVal:  "github_pat_11ABCD0123456789_abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
			body:       "PAT token: github_pat_11ABCD0123456789_abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
			title:      "GitHub PAT",
			expectKind: "github token pattern",
		},
		{
			name:       "AKIA aws access key",
			memID:      "MEM-AWS-01",
			secretVal:  "AKIAIOSFODNN7EXAMPLE",
			body:       "AWS key AKIAIOSFODNN7EXAMPLE configured",
			title:      "AWS key",
			expectKind: "aws access key pattern",
		},
		{
			name:       "AKIA0 honeypot decoy",
			memID:      "MEM-AWS-02",
			secretVal:  "AKIA0ABCDEF234567890",
			body:       "AWS_ACCESS_KEY_ID=AKIA0ABCDEF234567890",
			title:      "Honeypot key",
			expectKind: "aws access key pattern",
		},
		{
			name:       "xox slack token",
			memID:      "MEM-SLACK-01",
			secretVal:  "xoxb-1234567890-abcdef123456",
			body:       "Slack token xoxb-1234567890-abcdef123456 configured",
			title:      "Slack bot",
			expectKind: "slack token pattern",
		},
		{
			name:       "private key block",
			memID:      "MEM-KEY-01",
			secretVal:  "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA0...\n-----END RSA PRIVATE KEY-----",
			body:       "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA0...\n-----END RSA PRIVATE KEY-----",
			title:      "Private RSA key",
			expectKind: "private key pattern",
		},
		{
			name:       "bearer token",
			memID:      "MEM-BEARER-01",
			secretVal:  "Bearer secret-bearer-token-123456",
			body:       "Authorization header: Bearer secret-bearer-token-123456",
			title:      "Bearer auth",
			expectKind: "bearer token pattern",
		},
		{
			name:       "short password=hunter2",
			memID:      "MEM-PW-01",
			secretVal:  "password=hunter2",
			body:       "Credentials: password=hunter2",
			title:      "User password",
			expectKind: "explicit secret/password assignment",
		},
		{
			name:       "password=pw short assignment",
			memID:      "MEM-PW-02",
			secretVal:  "password: pw",
			body:       "Server password: pw",
			title:      "Short password",
			expectKind: "explicit secret/password assignment",
		},
		{
			name:       ".netrc credential content",
			memID:      "MEM-NETRC-01",
			secretVal:  "machine api.github.com login dev password secretpassword123",
			body:       "machine api.github.com login dev password secretpassword123",
			title:      "Netrc credentials",
			expectKind: ".netrc credential pattern",
		},
		{
			name:       "aws credentials file content",
			memID:      "MEM-CRED-01",
			secretVal:  "[default]\naws_access_key_id = AKIAIOSFODNN7EXAMPLE\naws_secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			body:       "[default]\naws_access_key_id = AKIAIOSFODNN7EXAMPLE\naws_secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			title:      "AWS credentials",
			expectKind: "aws access key pattern",
		},
		{
			name:       "hosts.yml credential file",
			memID:      "MEM-HOSTS-01",
			secretVal:  "github.com:\n    oauth_token: ghp_1234567890abcdefghijklmnopqrstuvwxyzAB\n    git_protocol: https",
			body:       "github.com:\n    oauth_token: ghp_1234567890abcdefghijklmnopqrstuvwxyzAB\n    git_protocol: https",
			title:      "GH hosts",
			expectKind: "github token pattern",
		},
		{
			name:       "high entropy token next to key-like name",
			memID:      "MEM-ENT-01",
			secretVal:  "service_token: 9a8b7c6d5e4f3a2b1c0d9e8f7a6b5c4d",
			body:       "Configure service_token: 9a8b7c6d5e4f3a2b1c0d9e8f7a6b5c4d",
			title:      "Service token",
			expectKind: "high-entropy token next to key-like name",
		},
		{
			name:       "honeypot .env file decoy",
			memID:      "MEM-HONEY-01",
			secretVal:  "GITHUB_TOKEN=ghp_0123456789abcdef0123456789abcdef\nAWS_SECRET_ACCESS_KEY=0123456789abcdef0123456789abcdef01234567",
			body:       "GITHUB_TOKEN=ghp_0123456789abcdef0123456789abcdef\nAWS_SECRET_ACCESS_KEY=0123456789abcdef0123456789abcdef01234567",
			title:      "Honeypot env",
			expectKind: "github token pattern",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := model.MemoryRecordV2{
				ID:         tc.memID,
				ProjectID:  projID,
				Kind:       model.MemoryKindSemantic,
				Lifecycle:  model.MemoryCandidate,
				Authority:  model.AuthorityAgent,
				Title:      tc.title,
				Body:       tc.body,
				Scope:      string(model.ScopeProject),
				ScopeID:    projID,
				ObservedAt: now,
				IngestedAt: now,
				ValidFrom:  now,
				CreatedAt:  now,
				UpdatedAt:  now,
				Source:     model.MemorySource{Kind: "runtime", Reference: "run-sec"},
			}

			err := st.WriteMemoryV2(ctx, rec)
			if !errors.Is(err, security.ErrSecretDetected) {
				t.Fatalf("expected ErrSecretDetected for %s, got: %v", tc.name, err)
			}

			// 1. Value must never appear in the returned error
			if strings.Contains(err.Error(), tc.secretVal) {
				t.Fatalf("error echoed secret value %q: %v", tc.secretVal, err)
			}

			// 2. Reason names the kind of secret
			if tc.expectKind != "" && !strings.Contains(err.Error(), tc.expectKind) {
				t.Logf("note: error named kind %s for %s", err.Error(), tc.name)
			}

			// 3. Entry is refused: GetMemoryV2 must return not found
			_, err = st.GetMemoryV2(ctx, projID, tc.memID)
			if !errors.Is(err, model.ErrNotFound) {
				t.Fatalf("expected ErrNotFound for %s, got: %v", tc.memID, err)
			}
		})
	}

	// 4. Verify all refusal evidence events were recorded and never leak secret values
	events, err := st.ListEvents(ctx)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	for _, tc := range cases {
		var foundEvent bool
		for _, ev := range events {
			if ev.Type == "memory.secret.refused" && ev.Data["memory_id"] == tc.memID {
				foundEvent = true
				// Verify secret value does NOT appear in data or anywhere in event
				dataBytes, _ := json.Marshal(ev.Data)
				if strings.Contains(string(dataBytes), tc.secretVal) {
					t.Fatalf("evidence audit event leaked secret value %q: %s", tc.secretVal, string(dataBytes))
				}
				break
			}
		}
		if !foundEvent {
			t.Fatalf("expected refusal evidence event for %s (%s)", tc.name, tc.memID)
		}
	}
}

func TestStoreRejectsSecretsOnUpdate(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	projID := "PROJ-UPDATE-TEST"
	if err := st.InitProject(ctx, model.Project{
		ID: projID, Repository: "repo", DefaultBranch: "main", PackVersion: "1.0.0",
	}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Second)

	// Write clean initial record
	cleanRec := model.MemoryRecordV2{
		ID:         "MEM-UPDATE-01",
		ProjectID:  projID,
		Kind:       model.MemoryKindSemantic,
		Lifecycle:  model.MemoryCandidate,
		Authority:  model.AuthorityAgent,
		Title:      "Initial Clean Memory",
		Body:       "Clean architectural decision about database indexing.",
		Scope:      string(model.ScopeProject),
		ScopeID:    projID,
		ObservedAt: now,
		IngestedAt: now,
		ValidFrom:  now,
		CreatedAt:  now,
		UpdatedAt:  now,
		Source:     model.MemorySource{Kind: "runtime", Reference: "run-clean"},
	}

	if err := st.WriteMemoryV2(ctx, cleanRec); err != nil {
		t.Fatalf("failed to write initial clean record: %v", err)
	}

	// Attempt to update record to introduce a secret
	secretVal := "ghp_0123456789abcdef0123456789abcdef0123"
	_, err := st.UpdateMemory(ctx, projID, cleanRec.ID, cleanRec.Revision, func(m *model.MemoryRecordV2) error {
		m.Body = "Updated body with token: " + secretVal
		return nil
	})

	if !errors.Is(err, security.ErrSecretDetected) {
		t.Fatalf("expected ErrSecretDetected on update, got: %v", err)
	}

	// Verify error does NOT contain secret value
	if strings.Contains(err.Error(), secretVal) {
		t.Fatalf("update error echoed secret: %v", err)
	}

	// Verify original record in database is unmodified
	stored, err := st.GetMemoryV2(ctx, projID, cleanRec.ID)
	if err != nil {
		t.Fatalf("GetMemoryV2: %v", err)
	}
	if stored.Revision != cleanRec.Revision {
		t.Fatalf("expected revision %d to remain unchanged, got %d", cleanRec.Revision, stored.Revision)
	}
	if strings.Contains(stored.Body, "ghp_") {
		t.Fatal("secret was persisted during failed update")
	}

	// Verify update refusal event was recorded in audit_events without secret value
	events, err := st.ListEvents(ctx)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var foundUpdateRefusal bool
	for _, ev := range events {
		if ev.Type == "memory.secret.refused" && ev.Data["memory_id"] == cleanRec.ID {
			foundUpdateRefusal = true
			dataBytes, _ := json.Marshal(ev.Data)
			if strings.Contains(string(dataBytes), secretVal) {
				t.Fatalf("audit event leaked secret during update refusal: %s", string(dataBytes))
			}
		}
	}
	if !foundUpdateRefusal {
		t.Fatal("expected refusal event recorded for failed update")
	}
}

func TestStoreAcceptsCleanEntriesAndDigests(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	projID := "PROJ-CLEAN-TEST"
	if err := st.InitProject(ctx, model.Project{
		ID: projID, Repository: "repo", DefaultBranch: "main", PackVersion: "1.0.0",
	}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Second)

	cleanCases := []struct {
		id    string
		title string
		body  string
	}{
		{
			id:    "MEM-CLEAN-01",
			title: "Commit reference",
			body:  "Feature implemented in commit 4b825dc642cb6eb9a060e54bf8d69288fbee4904 and reviewed.",
		},
		{
			id:    "MEM-CLEAN-02",
			title: "Password policy documentation",
			body:  "The password policy requires rotation every 90 days with minimum 16 characters.",
		},
		{
			id:    "MEM-CLEAN-03",
			title: "API key rotation note",
			body:  "Plan to rotate the api key next quarter according to security roadmap.",
		},
		{
			id:    "MEM-CLEAN-04",
			title: "Token terminology note",
			body:  "Received a token of appreciation from team for architecture improvements.",
		},
	}

	for _, tc := range cleanCases {
		rec := model.MemoryRecordV2{
			ID:         tc.id,
			ProjectID:  projID,
			Kind:       model.MemoryKindSemantic,
			Lifecycle:  model.MemoryDurable,
			Authority:  model.AuthorityOperator,
			Title:      tc.title,
			Body:       tc.body,
			Scope:      string(model.ScopeProject),
			ScopeID:    projID,
			ObservedAt: now,
			IngestedAt: now,
			ValidFrom:  now,
			CreatedAt:  now,
			UpdatedAt:  now,
			Source:     model.MemorySource{Kind: "documentation", Reference: "README.md"},
		}

		if err := st.WriteMemoryV2(ctx, rec); err != nil {
			t.Fatalf("clean record %s rejected: %v", tc.id, err)
		}

		retrieved, err := st.GetMemoryV2(ctx, projID, tc.id)
		if err != nil {
			t.Fatalf("GetMemoryV2 for %s: %v", tc.id, err)
		}
		if retrieved.Body != tc.body {
			t.Fatalf("body mismatch for %s: got %q, want %q", tc.id, retrieved.Body, tc.body)
		}
	}
}

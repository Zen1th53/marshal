package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTokenLifecycleAndConstantTimeAuth(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(tempDir)

	token, record, err := mgr.CreateToken("mcp-client-1", KindMCPClient, []string{"task.read", "task.execute"})
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	if token == "" || record.ID == "" || record.Digest == "" {
		t.Fatalf("invalid record: %#v", record)
	}
	if !strings.HasPrefix(token, "marshal_token_") {
		t.Fatalf("token prefix = %q, want marshal_token_", token)
	}

	principal, err := mgr.Authenticate(token)
	if err != nil {
		t.Fatalf("Authenticate valid token: %v", err)
	}
	if principal.Name != "mcp-client-1" || principal.Kind != KindMCPClient {
		t.Fatalf("principal mismatch: %#v", principal)
	}

	if _, err := mgr.Authenticate("invalid-token"); err == nil {
		t.Fatal("expected authentication error for invalid token")
	}

	if err := mgr.RevokeToken(record.ID); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}

	if _, err := mgr.Authenticate(token); err == nil {
		t.Fatal("expected authentication error for revoked token")
	}
}

func TestTokenCreateIdempotencySurvivesManagerRestart(t *testing.T) {
	dir := t.TempDir()
	first := NewManager(dir)
	plaintext, record, created, err := first.CreateTokenIdempotent(
		"operator", KindLocalUser, []string{"all"}, "request-1")
	if err != nil || !created || plaintext == "" {
		t.Fatalf("first create = created %t plaintext=%t err=%v", created, plaintext != "", err)
	}

	restarted := NewManager(dir)
	replayedPlaintext, replayed, created, err := restarted.CreateTokenIdempotent(
		"operator", KindLocalUser, []string{"all"}, "request-1")
	if err != nil || created {
		t.Fatalf("replay = created %t err=%v", created, err)
	}
	if replayed.ID != record.ID {
		t.Fatalf("replay created/rebound token %q, want %q", replayed.ID, record.ID)
	}
	if replayedPlaintext != "" {
		t.Fatal("an idempotent replay re-exposed plaintext")
	}
	if _, _, _, err := restarted.CreateTokenIdempotent(
		"different", KindLocalUser, []string{"all"}, "request-1"); err == nil {
		t.Fatal("same idempotency key was rebound to a different request")
	}
}

func TestTokenCreateRejectsUnknownPrincipalKind(t *testing.T) {
	if _, _, _, err := NewManager(t.TempDir()).CreateTokenIdempotent(
		"bad", PrincipalKind("server_admin"), []string{"all"}, "request"); err == nil {
		t.Fatal("unknown principal kind was accepted")
	}
}

func TestRedactTokenMetadataRemovesCredentialAndCreationDigests(t *testing.T) {
	record := TokenRecord{ID: "TOKEN-1", Digest: "credential-digest", CreationKey: "request-digest", Capabilities: []string{"task.read"}}
	redacted := RedactTokenMetadata([]TokenRecord{record})
	if redacted[0].Digest != "" || redacted[0].CreationKey != "" {
		t.Fatalf("user-facing metadata retained internal digests: %#v", redacted[0])
	}
	redacted[0].Capabilities[0] = "changed"
	if record.Capabilities[0] != "task.read" {
		t.Fatal("metadata redaction aliased the canonical capability slice")
	}
}

func TestSecretResolverAndRedaction(t *testing.T) {
	os.Setenv("MARSHAL_TEST_SECRET", "super-secret-key-12345")
	t.Cleanup(func() { os.Unsetenv("MARSHAL_TEST_SECRET") })

	resolver := NewSecretResolver()
	val, err := resolver.Resolve("env:MARSHAL_TEST_SECRET")
	if err != nil || val != "super-secret-key-12345" {
		t.Fatalf("Resolve env secret failed: val=%q err=%v", val, err)
	}

	tempDir := t.TempDir()
	secretFile := filepath.Join(tempDir, "secret.txt")
	if err := os.WriteFile(secretFile, []byte("file-secret-value-99\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	fileVal, err := resolver.Resolve("file:" + secretFile)
	if err != nil || fileVal != "file-secret-value-99" {
		t.Fatalf("Resolve file secret failed: val=%q err=%v", fileVal, err)
	}

	output := []byte("Log line containing super-secret-key-12345 and normal text")
	redacted := RedactSecrets(output, []string{"super-secret-key-12345"})
	if string(redacted) != "Log line containing [REDACTED] and normal text" {
		t.Fatalf("RedactSecrets output mismatch: %s", string(redacted))
	}
}

func TestTokenFileInsecurePermissionsRejection(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(tempDir)

	_, _, err := mgr.CreateToken("mcp-client-1", KindMCPClient, []string{"task.read"})
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	// Change file permissions to insecure 0644
	tokenPath := filepath.Join(tempDir, "auth_tokens.json")
	if err := os.Chmod(tokenPath, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	// Authenticate should fail closed due to insecure permissions
	if _, err := mgr.Authenticate("any-token"); err == nil {
		t.Fatal("expected error due to insecure 0644 file permissions, got nil")
	} else if !strings.Contains(err.Error(), "insecure permissions") {
		t.Fatalf("expected insecure permissions error, got: %v", err)
	}

	// Fix permissions back to 0600
	if err := os.Chmod(tokenPath, 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}
}

package capability

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestGrantRejectsRawSecretMaterialBeforePersistence(t *testing.T) {
	now := time.Date(2026, 8, 16, 17, 0, 0, 0, time.UTC)
	repo := &memoryGrantRepository{grants: map[GrantID]Grant{}}
	engine := NewAuthorizedEngine(repo, func() time.Time { return now }, testAuthority{})
	marker := "MARSHAL_TEST_SECRET_T01_A07_9f2a"
	_, err := engine.Grant(context.Background(), GrantRequest{
		Subject: "agent-1", TaskID: "task-1", Kind: KindSecretUse,
		Scope:     Scope{Resource: marker, Actions: []string{"use"}},
		ExpiresAt: now.Add(time.Hour), Issuer: "broker", IdempotencyKey: "secret-request-1",
	})
	if !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("secret resource error=%v, want ErrInvalidScope", err)
	}
	if len(repo.grants) != 0 {
		t.Fatalf("secret resource mutated repository: %d rows", len(repo.grants))
	}
	if strings.Contains(err.Error(), marker) {
		t.Fatal("secret marker escaped in error")
	}
}

func TestFilesystemWriteDoesNotAuthorizeOtherCapabilityKinds(t *testing.T) {
	now := time.Date(2026, 8, 16, 17, 0, 0, 0, time.UTC)
	repo := &memoryGrantRepository{grants: map[GrantID]Grant{}}
	engine := NewAuthorizedEngine(repo, func() time.Time { return now }, testAuthority{})
	if _, err := engine.Grant(context.Background(), GrantRequest{
		Subject: "agent-1", TaskID: "task-1", Kind: KindFilesystemWrite,
		Scope:     Scope{Resource: "/workspace/task-1", Actions: []string{"write"}},
		ExpiresAt: now.Add(time.Hour), Issuer: "broker", IdempotencyKey: "write-request-1",
	}); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []CapabilityKind{KindFilesystemRead, KindGitPush, KindNetworkEgress, KindSecretUse} {
		decision, err := engine.Authorize(context.Background(), Query{
			Subject: "agent-1", TaskID: "task-1", Kind: kind,
			Resource: "/workspace/task-1", Action: "write", At: now,
		})
		if err != nil || decision.Outcome != OutcomeDeny || decision.Reason != CodeDenied {
			t.Errorf("kind=%s decision=%#v err=%v", kind, decision, err)
		}
	}
}

func FuzzNormalizeResource(f *testing.F) {
	for _, seed := range []string{"/workspace/task", "/workspace/task/../task", "../../outside", "secret://task", "\x00"} {
		f.Add(string(KindFilesystemRead), seed)
	}
	f.Fuzz(func(t *testing.T, kindText, resource string) {
		kind := CapabilityKind(kindText)
		first, firstErr := NormalizeResource(kind, resource)
		second, secondErr := NormalizeResource(kind, resource)
		if (firstErr == nil) != (secondErr == nil) || first != second {
			t.Fatalf("normalization is nondeterministic: first=%q/%v second=%q/%v", first, firstErr, second, secondErr)
		}
		if firstErr == nil && strings.ContainsRune(first, '\x00') {
			t.Fatal("NUL survived normalization")
		}
	})
}

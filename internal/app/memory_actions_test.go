package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/memory/security"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

func memoryActionFixture(t *testing.T) (*MemoryService, authz.Principal, model.MemoryRecordV2) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.InitProject(ctx, model.Project{ID: "project-memory-actions", Repository: "scratch", DefaultBranch: "main", PackVersion: "1"}); err != nil {
		t.Fatal(err)
	}
	s := NewMemoryService(st)
	p := authz.Principal{ID: "operator", Role: authz.Role{Name: "operator", Authorities: []authz.Authority{authz.AuthorityPolicyAdmin}}}
	rec, err := s.Remember(ctx, p, RememberRequest{ProjectID: "project-memory-actions", Title: "reviewed", Body: "reviewed content", ScopeID: "project-memory-actions", Kind: model.MemoryKindSemantic})
	if err != nil {
		t.Fatal(err)
	}
	rec, err = st.GetMemoryV2(ctx, rec.ProjectID, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s, p, rec
}

func TestImportConfirmationBindsFileDigest(t *testing.T) {
	r, ctx := memoryOperatorRuntime(t)
	path := "history.json"
	first := []byte(`{"session_id":"history","provider":"codex","messages":[{"role":"assistant","content":"Reviewed answer"}]}`)
	if err := os.WriteFile(filepath.Join(r.layout.Root, path), first, 0600); err != nil {
		t.Fatal(err)
	}
	bound, err := r.ReviewMemoryImport(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	e := CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "session", TargetID: bound.Digest, IdempotencyKey: "import-once"}
	req := MemoryImportRequest{File: bound, Format: "marshal-json"}
	if err = os.WriteFile(filepath.Join(r.layout.Root, path), []byte(`{"session_id":"other","provider":"codex","messages":[{"role":"assistant","content":"Unreviewed answer"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = r.CommandImportMemory(ctx, e, req); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("changed file accepted: %v", err)
	}
	if err = os.WriteFile(filepath.Join(r.layout.Root, path), first, 0600); err != nil {
		t.Fatal(err)
	}
	req.DryRun = true
	preview, err := r.CommandImportMemory(ctx, e, req)
	if err != nil || len(preview.Records) != 1 {
		t.Fatalf("dry run: %+v %v", preview, err)
	}
	if _, err = r.store.GetMemoryV2(ctx, r.ProjectID(), preview.Records[0].ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("dry run wrote: %v", err)
	}
	req.DryRun = false
	result, err := r.CommandImportMemory(ctx, e, req)
	if err != nil || len(result.Records) != 1 {
		t.Fatalf("import: %+v %v", result, err)
	}
	got, err := r.store.GetMemoryV2(ctx, r.ProjectID(), result.Records[0].ID)
	if err != nil || got.Lifecycle != model.MemoryCandidate || got.Authority != model.AuthorityAgent {
		t.Fatalf("candidate readback: %+v %v", got, err)
	}
	replay, err := r.CommandImportMemory(ctx, e, req)
	if err != nil || replay.Skipped != 1 {
		t.Fatalf("duplicate: %+v %v", replay, err)
	}
	if _, err = r.CommandImportMemory(context.Background(), e, req); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("anonymous import: %v", err)
	}
}

func TestMemoryImportRefusesSymlinkAndSecret(t *testing.T) {
	r, ctx := memoryOperatorRuntime(t)
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte("external"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(r.layout.Root, "link.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ReviewMemoryImport(ctx, "link.json"); err == nil {
		t.Fatal("symlink input accepted")
	}
	payload := []byte(`{"session_id":"secret","provider":"codex","messages":[{"role":"user","content":"AKIAIOSFODNN7EXAMPLE"}]}`)
	if err := os.WriteFile(filepath.Join(r.layout.Root, "secret.json"), payload, 0600); err != nil {
		t.Fatal(err)
	}
	bound, err := r.ReviewMemoryImport(ctx, "secret.json")
	if err != nil {
		t.Fatal(err)
	}
	result, err := r.CommandImportMemory(ctx, CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "session", TargetID: bound.Digest, IdempotencyKey: "secret"}, MemoryImportRequest{File: bound, Format: "marshal-json"})
	if security.RedactsSecrets {
		// Extreme imports the record with the secret replaced by a marker.
		if err != nil {
			t.Fatalf("Marshal refused a redactable import: %v", err)
		}
		raw, _ := json.Marshal(result)
		if strings.Contains(string(raw), "AKIAIOSFODNN7EXAMPLE") {
			t.Fatalf("secret imported: %s", raw)
		}
		return
	}
	if err == nil {
		t.Fatal("secret import accepted")
	}
}

func TestPromotionUsesReviewedRevisionAtCAS(t *testing.T) {
	s, p, reviewed := memoryActionFixture(t)
	ctx := context.Background()
	if _, err := s.store.UpdateMemory(ctx, reviewed.ProjectID, reviewed.ID, reviewed.Revision, func(r *model.MemoryRecordV2) error { r.Body = "unreviewed replacement"; return nil }); err != nil {
		t.Fatal(err)
	}
	_, err := s.Promote(ctx, p, PromoteRequest{ProjectID: reviewed.ProjectID, MemoryID: reviewed.ID, ScopeID: reviewed.ScopeID, ExpectedRevision: &reviewed.Revision})
	if !errors.Is(err, model.ErrConflict) {
		t.Fatalf("stale review accepted: %v", err)
	}
	got, err := s.store.GetMemoryV2(ctx, reviewed.ProjectID, reviewed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Lifecycle != model.MemoryCandidate {
		t.Fatalf("stale promotion mutated record: %+v", got)
	}
}

func TestMemoryReferenceRevalidationRejectsStaleAndRevoked(t *testing.T) {
	s, p, rec := memoryActionFixture(t)
	ctx := context.Background()
	refs := []MemoryReference{{ID: rec.ID, Revision: rec.Revision, Digest: rec.ContentDigest}}
	if _, err := s.RevalidateReferences(ctx, p, MemoryReferenceRequest{ProjectID: rec.ProjectID, References: refs}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.TombstoneMemory(ctx, rec.ProjectID, rec.ID, rec.Revision, "revoked"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RevalidateReferences(ctx, p, MemoryReferenceRequest{ProjectID: rec.ProjectID, References: refs}); err == nil {
		t.Fatal("revoked reference accepted")
	}
}

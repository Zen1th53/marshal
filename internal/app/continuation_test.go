package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/store"
	"os"
	"path/filepath"
	"testing"
)

func TestContinuationReadRequiresRecordedOperatorGrant(t *testing.T) {
	r := openNetpolRuntime(t)
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "history.jsonl")
	os.WriteFile(path, []byte("hello"), 0600)
	req := permission.Request{Kind: "read", Object: dir, Scope: "this session only, read-only", Who: "Marshal", Reason: "continue"}
	if _, err := r.ReadGranted(ctx, path); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("ungranted read: %v", err)
	}
	if err := r.CommandPermission(ctx, req, true, "model says Allow"); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("model allowed: %v", err)
	}
	c, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.CommandPermission(c.Context(ctx), req, true, "operator command"); err != nil {
		t.Fatal(err)
	}
	if b, err := r.ReadGranted(ctx, path); err != nil || string(b) != "hello" {
		t.Fatalf("granted read: %s %v", b, err)
	}
	other := t.TempDir()
	os.WriteFile(filepath.Join(other, "secret"), []byte("not granted"), 0600)
	os.Symlink(other, filepath.Join(dir, "escape"))
	if _, err := r.ReadGranted(ctx, filepath.Join(dir, "escape", "secret")); !errors.Is(err, authz.ErrDenied) {
		t.Fatal("symlink escaped grant")
	}
	if err := r.CommandPermission(c.Context(ctx), req, false, "operator command"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ReadGranted(ctx, path); !errors.Is(err, authz.ErrDenied) {
		t.Fatal("denied read retained")
	}
}
func TestContinuationCandidatesDropSecretsAndWriteOnlyAfterAllow(t *testing.T) {
	r := openNetpolRuntime(t)
	ctx := context.Background()
	tr := importer.SessionTranscript{SessionID: "earlier", Provider: "claude", CWD: r.ProjectRoot(), Messages: []importer.Message{{Role: "assistant", Content: "Convention: keep functions small"}, {Role: "user", Content: "api_key=super-secret-value-123456"}}}
	records, dropped, err := r.ProposeContinuation(ctx, tr)
	if err != nil || !dropped || len(records) != 1 {
		t.Fatalf("propose %v %v %v", records, dropped, err)
	}
	all, _ := r.store.ListMemoryV2(ctx, store.MemoryQueryFilter{ProjectID: r.ProjectID()})
	if len(all) != 0 {
		t.Fatal("candidate persisted before permission")
	}
	req := permission.Request{Kind: "memory", Object: records[0].ID, Scope: "project memory, persistent", Who: "Marshal", Reason: "retain convention"}
	if err := r.CommandPermission(ctx, req, true, "Allow"); !errors.Is(err, authz.ErrDenied) {
		t.Fatal("model text allowed write")
	}
	c, _ := r.OpenLocalControl(ctx)
	if err := r.CommandPermission(c.Context(ctx), req, false, "operator command"); err != nil {
		t.Fatal(err)
	}
	all, _ = r.store.ListMemoryV2(ctx, store.MemoryQueryFilter{ProjectID: r.ProjectID()})
	if len(all) != 0 {
		t.Fatal("denied entry persisted")
	}
	if err := r.CommandPermission(c.Context(ctx), req, true, "operator command"); err != nil {
		t.Fatal(err)
	}
	all, _ = r.store.ListMemoryV2(ctx, store.MemoryQueryFilter{ProjectID: r.ProjectID()})
	if len(all) != 1 {
		t.Fatal("allowed entry missing")
	}
	tr.CWD = t.TempDir()
	if _, _, err := r.ProposeContinuation(ctx, tr); err == nil {
		t.Fatal("other project's history accepted")
	}
}

func TestRuntimeRememberRequiresOperatorDecision(t *testing.T) {
	r := openNetpolRuntime(t)
	ctx := context.Background()
	rec, err := r.Memory().Remember(ctx, testPrincipal("marshal"), RememberRequest{ProjectID: r.ProjectID(), Title: "convention", Body: "Keep interfaces small"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.store.GetMemoryV2(ctx, r.ProjectID(), rec.ID); err == nil {
		t.Fatal("remember persisted before operator approval")
	}
	approveTestCandidates(t, r)
	if _, err := r.store.GetMemoryV2(ctx, r.ProjectID(), rec.ID); err != nil {
		t.Fatal(err)
	}
}
func TestReadContinuationOnlyProjectDataAndProvenance(t *testing.T) {
	r := openNetpolRuntime(t)
	ctx := context.Background()
	dir := t.TempDir()
	grantTestRead(t, r, dir)
	for _, tc := range []struct{ name, cwd, text string }{{"local", r.ProjectRoot(), "Pending: implement feature"}, {"foreign", t.TempDir(), "FOREIGN_CONTENT"}} {
		data := fmt.Sprintf("{\"type\":\"session_meta\",\"timestamp\":\"2026-10-01T10:00:00Z\",\"payload\":{\"id\":%q,\"cwd\":%q}}\n{\"type\":\"response_item\",\"payload\":{\"type\":\"message\",\"role\":\"assistant\",\"phase\":\"final_answer\",\"content\":[{\"type\":\"output_text\",\"text\":%q}]}}\n", tc.name, tc.cwd, tc.text)
		os.WriteFile(filepath.Join(dir, tc.name+".jsonl"), []byte(data), 0600)
	}
	records, _, err := r.ReadContinuation(ctx, "codex", dir)
	if err != nil || len(records) != 1 {
		t.Fatalf("project filter: %v %v", records, err)
	}
	if records[0].SessionID != "local" || records[0].ExtMeta["provider"] != "codex" || records[0].ObservedAt.Format("2006-01-02") != "2026-10-01" {
		t.Fatalf("provenance lost: %+v", records[0])
	}
}

func TestApprovedImportedMemoryIsActiveAndRecallable(t *testing.T) {
	r := openNetpolRuntime(t)
	ctx := context.Background()
	records, _, err := r.ProposeContinuation(ctx, importer.SessionTranscript{SessionID: "prior-session", Provider: "claude", CWD: r.ProjectRoot(), Branch: "original-branch", Messages: []importer.Message{{Role: "assistant", Content: "Handoff: keep functions small"}}})
	if err != nil || len(records) != 1 {
		t.Fatalf("propose: %v %v", records, err)
	}
	control, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.CommandPermission(control.Context(ctx), permission.Request{Kind: "memory", Object: records[0].ID}, true, "operator memory review"); err != nil {
		t.Fatal(err)
	}
	rec, err := r.store.GetMemoryV2(ctx, r.ProjectID(), records[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Lifecycle != model.MemoryDurable || rec.ScopeID != r.ProjectID() || rec.SessionID != "prior-session" || rec.ExtMeta["provider"] != "claude" || rec.BranchName != records[0].BranchName || rec.Source != records[0].Source || rec.ContentDigest != rec.CanonicalDigest() {
		t.Fatalf("approval/provenance: %+v", rec)
	}
	for _, query := range []string{"", "handoff"} {
		response, err := r.Memory().Recall(ctx, testPrincipal("marshal"), RecallRequest{ProjectID: r.ProjectID(), Query: query})
		if err != nil || len(response.Results) != 1 || response.Results[0].ID != rec.ID {
			t.Fatalf("recall %q: %+v %v", query, response, err)
		}
	}
}

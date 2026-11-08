package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/permission"
)

func TestContinuationNestedDenialOverridesParentGrant(t *testing.T) {
	r := openNetpolRuntime(t)
	ctx := context.Background()
	folder := t.TempDir()
	child := filepath.Join(folder, "denied")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(child, "history.jsonl")
	if err := os.WriteFile(path, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	grantTestRead(t, r, folder)
	control, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.CommandPermission(control.Context(ctx), permission.Request{Kind: "read", Object: child, Scope: "this session only, read-only", Who: "operator"}, false, "operator command"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ReadGranted(ctx, path); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("nested denied read: %v", err)
	}
}

func TestContinuationGrantedSymlinkBindsToOriginalFolder(t *testing.T) {
	r := openNetpolRuntime(t)
	folder, other, links := t.TempDir(), t.TempDir(), t.TempDir()
	link := filepath.Join(links, "history")
	if err := os.Symlink(folder, link); err != nil {
		t.Fatal(err)
	}
	grantTestRead(t, r, link)
	if !r.HasReadGrant(folder) {
		t.Fatal("canonical folder not granted")
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, link); err != nil {
		t.Fatal(err)
	}
	// Undecided folders are readable by default only in the private edition.
	if !unrestrictedEdition && r.HasReadGrant(link) {
		t.Fatal("grant followed replaced symlink")
	}
}

func TestContinuationGrantPersistsAcrossRuntimeProcessesAndRevoke(t *testing.T) {
	ctx := context.Background()
	r := openNetpolRuntime(t)
	root := r.ProjectRoot()
	folder := t.TempDir()
	path := filepath.Join(folder, "history.jsonl")
	if err := os.WriteFile(path, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	grantTestRead(t, r, folder)
	child := filepath.Join(folder, "denied")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	childPath := filepath.Join(child, "history.jsonl")
	if err := os.WriteFile(childPath, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	control, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.CommandPermission(control.Context(ctx), permission.Request{Kind: "read", Object: child, Scope: "project", Who: "operator"}, false, "operator command"); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if !second.HasReadGrant(path) {
		t.Fatal("read grant did not persist across runtime processes")
	}
	if second.HasReadGrant(childPath) {
		t.Fatal("nested denial did not persist or override parent grant")
	}
	control, err = second.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.CommandRevokeReadGrant(control.Context(ctx), child); err != nil {
		t.Fatal(err)
	}
	if !second.HasReadGrant(childPath) {
		t.Fatal("revoking the nested denial did not restore the parent grant")
	}
	if err := second.CommandRevokeReadGrant(control.Context(ctx), folder); err != nil {
		t.Fatal(err)
	}
	// Undecided folders are readable by default only in the private edition.
	if !unrestrictedEdition && second.HasReadGrant(path) {
		t.Fatal("revoked grant still authorized a read")
	}
}

func TestMCPAndWorkerContextsCannotCreateReadGrant(t *testing.T) {
	r := openNetpolRuntime(t)
	folder := t.TempDir()
	req := permission.Request{Kind: "read", Object: folder, Scope: "project", Who: "agent"}
	for _, name := range []string{"MCP tool", "A2A worker", "governed worker"} {
		t.Run(name, func(t *testing.T) {
			if err := r.CommandPermission(context.Background(), req, true, name); !errors.Is(err, authz.ErrDenied) {
				t.Fatalf("unauthenticated grant returned %v", err)
			}
			if grants, err := r.OperatorReadGrants(context.Background()); err != nil || len(grants) != 0 {
				t.Fatalf("unauthenticated caller created a grant: %+v %v", grants, err)
			}
		})
	}
}

func TestContinuationSecretMetadataIsDropped(t *testing.T) {
	r := openNetpolRuntime(t)
	tr := importer.SessionTranscript{SessionID: "api_key=super-secret-value-123456", Provider: "claude", CWD: r.ProjectRoot(), Messages: []importer.Message{{Role: "assistant", Content: "Keep functions small"}}}
	records, dropped, err := r.ProposeContinuation(context.Background(), tr)
	if err != nil || !dropped || len(records) != 0 || len(r.ContinuationCandidates()) != 0 {
		t.Fatalf("secret metadata: records=%v dropped=%v err=%v", records, dropped, err)
	}
}

func TestConfirmedMemoryReviewRequiresOperatorAndDoesNotQueuePopup(t *testing.T) {
	r := openNetpolRuntime(t)
	ctx := context.Background()
	req := RememberRequest{ProjectID: r.ProjectID(), Title: "reviewed convention", Body: "Keep interfaces small"}
	if _, err := r.CommandRemember(ctx, req); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("model review allowed: %v", err)
	}
	queued := false
	r.SetPermissionSink(func(permission.Request) { queued = true })
	control, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := r.CommandRemember(control.Context(ctx), req)
	if err != nil {
		t.Fatal(err)
	}
	if queued {
		t.Fatal("confirmed operator review requested a duplicate popup")
	}
	if _, err := r.Store().GetMemoryV2(ctx, r.ProjectID(), rec.ID); err != nil {
		t.Fatal(err)
	}
	events, err := r.Store().ListEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Type == "PERMISSION_DECIDED" && event.Data["object"] == rec.ID && event.Data["allow"] == true {
			found = true
		}
	}
	if !found {
		t.Fatal("review decision evidence missing")
	}
}

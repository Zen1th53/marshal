package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/memory/security"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContinuationReadRequiresRecordedOperatorGrant(t *testing.T) {
	r := openNetpolRuntime(t)
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "history.jsonl")
	os.WriteFile(path, []byte("hello"), 0600)
	req := permission.Request{Kind: "read", Object: dir, Scope: "this session only, read-only", Who: "Marshal", Reason: "continue"}
	if _, err := r.ReadGranted(ctx, path); unrestrictedEdition && err != nil || !unrestrictedEdition && !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("undecided read: %v", err)
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
	// An escape resolves to an undecided folder, which only the private
	// edition reads by default.
	if _, err := r.ReadGranted(ctx, filepath.Join(dir, "escape", "secret")); !unrestrictedEdition && !errors.Is(err, authz.ErrDenied) {
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
	// Community drops the secret-bearing message; Extreme keeps it with the
	// secret replaced by a marker. Both report that secrets were met. Either
	// way the session is one bounded episode candidate, not one per message.
	want := 1
	if err != nil || !dropped || len(records) != want {
		t.Fatalf("propose %v %v %v", records, dropped, err)
	}
	for _, rec := range records {
		if strings.Contains(rec.Body, "super-secret-value") {
			t.Fatalf("secret kept: %s", rec.Body)
		}
		if security.RedactsSecrets != (strings.Count(rec.Body, "[user]") == 1) {
			t.Fatalf("redacted message handling: %s", rec.Body)
		}
	}
	all, _ := r.store.ListMemoryV2(ctx, store.MemoryQueryFilter{ProjectID: r.ProjectID(), Lifecycle: model.MemoryCandidate})
	if len(all) != want || (all[0].ID != records[0].ID && all[len(all)-1].ID != records[0].ID) {
		t.Fatal("sanitized candidate was not persisted for review")
	}
	req := permission.Request{Kind: "memory", Object: records[0].ID, Scope: "project memory, persistent", Who: "Marshal", Reason: "retain convention"}
	if err := r.CommandPermission(ctx, req, true, "Allow"); !errors.Is(err, authz.ErrDenied) {
		t.Fatal("model text allowed write")
	}
	c, _ := r.OpenLocalControl(ctx)
	if err := r.CommandPermission(c.Context(ctx), req, false, "operator command"); err != nil {
		t.Fatal(err)
	}
	rejected, err := r.store.GetMemoryV2(ctx, r.ProjectID(), records[0].ID)
	if err != nil || rejected.Lifecycle != model.MemoryRejected {
		t.Fatalf("denial did not reject candidate: record=%+v err=%v", rejected, err)
	}
	approvedRecords, _, err := r.ProposeContinuation(ctx, importer.SessionTranscript{SessionID: "earlier-approved", Provider: "claude", CWD: r.ProjectRoot(), Messages: []importer.Message{{Role: "assistant", Content: "Convention: keep functions small"}}})
	if err != nil || len(approvedRecords) != 1 {
		t.Fatalf("second proposal %v %v", approvedRecords, err)
	}
	req.Object = approvedRecords[0].ID
	if err := r.CommandPermission(c.Context(ctx), req, true, "operator command"); err != nil {
		t.Fatal(err)
	}
	all, _ = r.store.ListMemoryV2(ctx, store.MemoryQueryFilter{ProjectID: r.ProjectID()})
	// The rejected session episode and the retained one.
	if len(all) != want+1 {
		t.Fatal("rejected and allowed entries missing")
	}
	approved, err := r.store.GetMemoryV2(ctx, r.ProjectID(), approvedRecords[0].ID)
	if err != nil || approved.Lifecycle != model.MemoryDurable {
		t.Fatalf("approved candidate lifecycle = %s, err=%v; want durable", approved.Lifecycle, err)
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
	stored, err := r.store.GetMemoryV2(ctx, r.ProjectID(), rec.ID)
	if err != nil || stored.Lifecycle != model.MemoryCandidate {
		t.Fatalf("remember candidate not durable across process state: record=%#v err=%v", stored, err)
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

func TestImportedCandidateCanBePromotedAcrossProcesses(t *testing.T) {
	r := openNetpolRuntime(t)
	ctx := context.Background()
	records, _, err := r.ProposeContinuation(ctx, importer.SessionTranscript{SessionID: "oneshot-session", Provider: "codex", CWD: r.ProjectRoot(), Messages: []importer.Message{{Role: "assistant", Content: "Use the canonical memory store for shared conventions"}}})
	if err != nil || len(records) != 1 {
		t.Fatalf("propose: records=%v err=%v", records, err)
	}
	operator := authz.Principal{ID: "cli-operator", Role: authz.Role{Name: "operator", Authorities: []authz.Authority{authz.AuthorityPolicyAdmin}}}
	promoted, err := r.Memory().Promote(ctx, operator, PromoteRequest{ProjectID: r.ProjectID(), MemoryID: records[0].ID})
	if err != nil || promoted.Lifecycle != model.MemoryDurable || promoted.ScopeID != r.ProjectID() {
		t.Fatalf("promote imported candidate: record=%+v err=%v", promoted, err)
	}
	response, err := r.Memory().Recall(ctx, testPrincipal("operator"), RecallRequest{ProjectID: r.ProjectID(), Query: "canonical memory store"})
	if err != nil || len(response.Results) != 1 || response.Results[0].ID != promoted.ID {
		t.Fatalf("recall promoted candidate: response=%+v err=%v", response, err)
	}
}

func TestProposedCandidateRecallableAfterCrossProcessPromotion(t *testing.T) {
	// A candidate remembered by this runtime must remain discoverable after it
	// is promoted by a separate process (the CLI `memory promote` opens its own
	// runtime and never touches this runtime's lexical index). The candidate is
	// indexed at proposal time; recall reloads the now-durable row from the store.
	r := openNetpolRuntime(t)
	ctx := context.Background()
	rec, err := r.Memory().Remember(ctx, testPrincipal("mcp-client"), RememberRequest{
		ProjectID: r.ProjectID(), ScopeID: r.ProjectID(),
		Title: "cross process promote", Body: "durable promotion survives the lexical projection",
		Kind: model.MemoryKindSemantic,
	})
	if err != nil {
		t.Fatalf("remember: %v", err)
	}

	// Simulate the CLI promote running in another process: mutate the canonical
	// row directly through the store, without calling Memory().Promote (which
	// would also refresh this runtime's in-memory index).
	stored, err := r.Store().GetMemoryV2(ctx, r.ProjectID(), rec.ID)
	if err != nil {
		t.Fatalf("reload candidate: %v", err)
	}
	durable, err := r.Store().UpdateMemory(ctx, r.ProjectID(), rec.ID, stored.Revision, func(existing *model.MemoryRecordV2) error {
		existing.Lifecycle = model.MemoryDurable
		existing.Authority = model.AuthorityOperator
		return nil
	})
	if err != nil {
		t.Fatalf("cross-process promote: %v", err)
	}

	response, err := r.Memory().Recall(ctx, testPrincipal("operator"), RecallRequest{ProjectID: r.ProjectID(), Query: "lexical projection"})
	if err != nil || len(response.Results) != 1 || response.Results[0].ID != durable.ID {
		t.Fatalf("recall after cross-process promote: response=%+v err=%v", response, err)
	}
}

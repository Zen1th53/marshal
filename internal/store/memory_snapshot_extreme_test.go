//go:build !governed

package store

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
)

func memorySnapshotFixture(t *testing.T) (*Store, string, MemorySnapshotBinding, []string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.InitProject(t.Context(), model.Project{ID: "project", Repository: "repo", DefaultBranch: "main", PackVersion: "test"}); err != nil {
		t.Fatal(err)
	}
	b := MemorySnapshotBinding{ProjectID: "project", SessionID: "session", Actor: "operator", HeadCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	ids := []string{"MEM-one", "MEM-two"}
	now := time.Now().UTC()
	for _, id := range ids {
		rec := model.MemoryRecordV2{ID: id, ProjectID: b.ProjectID, Kind: model.MemoryKindDecision, Lifecycle: model.MemoryDurable, Confidence: model.ConfidenceVerified, Authority: model.AuthorityVerified, Title: id, Body: "snapshot body " + id, Scope: "project", ScopeID: b.ProjectID, Source: model.MemorySource{Kind: "test", Reference: "original"}, ObservedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now}
		if err := s.WriteMemoryV2(t.Context(), rec); err != nil {
			t.Fatal(err)
		}
	}
	return s, path, b, ids
}

func TestMemorySnapshotRestoreReopensWithFullBodiesAndPreRestoreHistory(t *testing.T) {
	s, path, b, ids := memorySnapshotFixture(t)
	snapshot, err := s.CreateMemorySnapshot(t.Context(), b, "SNAP-one", ids)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if _, err := s.UpdateMemory(t.Context(), b.ProjectID, id, 0, func(r *model.MemoryRecordV2) error { r.Body = "later body " + id; return nil }); err != nil {
			t.Fatal(err)
		}
	}
	preview, err := s.PreviewMemorySnapshot(t.Context(), b, snapshot.ID, snapshot.Version, snapshot.Digest, "PREVIEW-one")
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.RestoreMemorySnapshot(t.Context(), b, preview)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Revisions) != 2 {
		t.Fatalf("restore result=%+v", result)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	retained, err := reopened.GetMemorySnapshot(t.Context(), b, snapshot.ID, snapshot.Version)
	if err != nil || len(retained.Records) != 2 || retained.Records[0].Body != "snapshot body MEM-one" {
		t.Fatalf("snapshot body not durable: %+v %v", retained, err)
	}
	history, err := reopened.GetMemoryRestoreHistory(t.Context(), b, preview.ID)
	if err != nil || len(history) != 2 {
		t.Fatalf("restore history=%+v %v", history, err)
	}
	for i, id := range ids {
		rec, err := reopened.GetMemoryV2(t.Context(), b.ProjectID, id)
		if err != nil || rec.Body != "snapshot body "+id || rec.Revision != 2 || rec.Lifecycle != model.MemoryCandidate || rec.Confidence != model.ConfidenceUnverified || rec.LastVerifiedAt != nil || rec.Source.Kind != "memory_restore" {
			t.Fatalf("restored canonical record=%+v %v", rec, err)
		}
		if history[i].Before.Body != "later body "+id || history[i].Before.Revision != 1 || history[i].After.Body != rec.Body || history[i].After.Revision != rec.Revision {
			t.Fatalf("pre-restore history lost: %+v", history[i])
		}
	}
}

func TestMemorySnapshotMetadataDiscoveryIsBoundedAndExactlyScoped(t *testing.T) {
	s, _, b, ids := memorySnapshotFixture(t)
	for _, id := range []string{"SNAP-first", "SNAP-second"} {
		if _, err := s.CreateMemorySnapshot(t.Context(), b, id, ids); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ListMemorySnapshots(t.Context(), b, 1)
	if err != nil || len(got) != 1 || got[0].ID != "SNAP-second" || got[0].RecordCount != 2 || len(got[0].Records) != 0 {
		t.Fatalf("metadata=%+v %v", got, err)
	}
	for _, scope := range []string{"project", "storage-project", "session", "actor"} {
		foreign := b
		switch scope {
		case "project":
			foreign.ProjectID = "foreign"
		case "storage-project":
			foreign.StorageProjectID = "foreign"
		case "session":
			foreign.SessionID = "foreign"
		case "actor":
			foreign.Actor = "foreign"
		}
		if got, err := s.ListMemorySnapshots(t.Context(), foreign, 10); err != nil || len(got) != 0 {
			t.Fatalf("foreign %s discovery=%+v %v", scope, got, err)
		}
	}
	for _, limit := range []int{0, -1, 51} {
		if _, err := s.ListMemorySnapshots(t.Context(), b, limit); !errors.Is(err, model.ErrInvalid) {
			t.Fatalf("limit %d=%v", limit, err)
		}
	}
}

func TestMemorySnapshotRestoreCASAndAtomicRollback(t *testing.T) {
	for _, failure := range []string{"body-cas", "acl-cas", "metadata-cas", "write-fault", "cancelled"} {
		t.Run(failure, func(t *testing.T) {
			s, _, b, ids := memorySnapshotFixture(t)
			snapshot, err := s.CreateMemorySnapshot(t.Context(), b, "SNAP-one", ids)
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range ids {
				if _, err := s.UpdateMemory(t.Context(), b.ProjectID, id, 0, func(r *model.MemoryRecordV2) error { r.Body = "later body " + id; return nil }); err != nil {
					t.Fatal(err)
				}
			}
			preview, err := s.PreviewMemorySnapshot(t.Context(), b, snapshot.ID, 1, snapshot.Digest, "PREVIEW-one")
			if err != nil {
				t.Fatal(err)
			}
			ctx := t.Context()
			switch failure {
			case "body-cas", "acl-cas":
				_, err = s.UpdateMemory(ctx, b.ProjectID, ids[1], 1, func(r *model.MemoryRecordV2) error {
					if failure == "body-cas" {
						r.Body = "concurrent edit"
					} else {
						r.ACLScope = "different operator"
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			case "write-fault":
				if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER snapshot_restore_fault BEFORE UPDATE ON memory_records_v2 WHEN NEW.memory_id='MEM-two' AND NEW.lifecycle='candidate' BEGIN SELECT RAISE(ABORT,'fixture write fault'); END`); err != nil {
					t.Fatal(err)
				}
			case "metadata-cas":
				if _, err := s.db.ExecContext(ctx, `UPDATE memory_records_v2 SET ext_meta_json='{"snapshot_dirty":true}' WHERE memory_id='MEM-two'`); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				cancelCtx, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelCtx
			}
			if _, err := s.RestoreMemorySnapshot(ctx, b, preview); err == nil {
				t.Fatal("unsafe restore succeeded")
			}
			first, err := s.GetMemoryV2(t.Context(), b.ProjectID, ids[0])
			if err != nil || first.Body != "later body "+ids[0] || first.Revision != 1 {
				t.Fatalf("partial restore persisted: %+v %v", first, err)
			}
			var history int
			if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM extreme_memory_restore_history").Scan(&history); err != nil || history != 0 {
				t.Fatalf("rollback retained false history=%d %v", history, err)
			}
			if failure == "write-fault" {
				if _, err := s.db.ExecContext(t.Context(), "DROP TRIGGER snapshot_restore_fault"); err != nil {
					t.Fatal(err)
				}
				if _, err := s.RestoreMemorySnapshot(t.Context(), b, preview); err != nil {
					t.Fatalf("failed transaction consumed preview: %v", err)
				}
				if _, err := s.RestoreMemorySnapshot(t.Context(), b, preview); !errors.Is(err, model.ErrConflict) {
					t.Fatalf("spent preview replay: %v", err)
				}
			}
		})
	}
}

func TestMemorySnapshotIdentityBoundsAndImmutableCapture(t *testing.T) {
	s, _, binding, ids := memorySnapshotFixture(t)
	snapshot, err := s.CreateMemorySnapshot(t.Context(), binding, "SNAP-one", ids)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Records[0].Body = "mutated returned object"
	retained, err := s.GetMemorySnapshot(t.Context(), binding, "SNAP-one", 1)
	if err != nil || retained.Records[0].Body != "snapshot body MEM-one" {
		t.Fatalf("snapshot aliases returned object: %+v %v", retained, err)
	}
	for _, kind := range []string{"project", "storage-project", "session", "actor", "version"} {
		wrong, version := binding, int64(1)
		switch kind {
		case "project":
			wrong.ProjectID = "another-project"
		case "storage-project":
			wrong.StorageProjectID = "another-project"
		case "session":
			wrong.SessionID = "another-session"
		case "actor":
			wrong.Actor = "another-operator"
		case "version":
			version = 2
		}
		if _, err := s.GetMemorySnapshot(t.Context(), wrong, "SNAP-one", version); err == nil {
			t.Fatalf("foreign %s snapshot body disclosed", kind)
		}
	}
	if _, err := s.db.ExecContext(t.Context(), "UPDATE extreme_memory_snapshots SET digest='tampered'"); err == nil {
		t.Fatal("immutable snapshot altered")
	}
	if _, err := s.CreateMemorySnapshot(t.Context(), binding, "SNAP-duplicate", []string{ids[0], ids[0]}); err == nil {
		t.Fatal("duplicate record selection accepted")
	}
	if _, err := s.CreateMemorySnapshot(t.Context(), binding, "SNAP-many", make([]string, 129)); err == nil {
		t.Fatal("unbounded record selection accepted")
	}
	if _, err := s.UpdateMemory(t.Context(), binding.ProjectID, ids[0], 0, func(rec *model.MemoryRecordV2) error { rec.Body = strings.Repeat("memory body ", 100000); return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateMemorySnapshot(t.Context(), binding, "SNAP-oversized", ids); err == nil {
		t.Fatal("oversized source body accepted")
	}
}

func TestMemorySnapshotPreviewAndRestoredHistoryAreImmutable(t *testing.T) {
	s, _, b, ids := memorySnapshotFixture(t)
	snapshot, err := s.CreateMemorySnapshot(t.Context(), b, "SNAP-one", ids)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewMemorySnapshot(t.Context(), b, snapshot.ID, 1, snapshot.Digest, "PREVIEW-one")
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`UPDATE extreme_memory_restore_previews SET preview_json='{}'`,
		`UPDATE extreme_memory_restore_previews SET actor='foreign'`,
		`UPDATE extreme_memory_restore_previews SET command_key='foreign'`,
		`DELETE FROM extreme_memory_restore_previews`,
	} {
		if _, err := s.db.ExecContext(t.Context(), query); err == nil {
			t.Fatalf("immutable preview accepted %s", query)
		}
	}
	if _, err := s.RestoreMemorySnapshot(t.Context(), b, preview); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`UPDATE extreme_memory_restore_previews SET consumed_at=NULL`,
		`UPDATE extreme_memory_restore_history SET before_json='{}'`,
		`DELETE FROM extreme_memory_restore_history`,
	} {
		if _, err := s.db.ExecContext(t.Context(), query); err == nil {
			t.Fatalf("immutable restore receipt accepted %s", query)
		}
	}
}

func TestMemorySnapshotRestoreCrashBeforeCommitReopensWithoutPartialState(t *testing.T) {
	const crashEnv = "MARSHAL_MEMORY_SNAPSHOT_CRASH_FIXTURE"
	if path := os.Getenv(crashEnv); path != "" {
		s, err := Open(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		b := MemorySnapshotBinding{ProjectID: "project", SessionID: "session", Actor: "operator", HeadCommit: strings.Repeat("a", 40)}
		preview, _, err := loadMemoryPreview(t.Context(), s.db, b, "PREVIEW-crash")
		if err != nil {
			t.Fatal(err)
		}
		// The production restore already wrote every selected row/history and
		// consumed the preview inside its transaction at this point. Process
		// termination intentionally bypasses all deferred rollback/close calls.
		ctx := WithMemorySnapshotPrecommit(t.Context(), func(context.Context) error { os.Exit(73); return nil })
		if _, err := s.RestoreMemorySnapshot(ctx, b, preview); err != nil {
			t.Fatal(err)
		}
		t.Fatal("crash boundary was not reached")
	}
	s, path, b, ids := memorySnapshotFixture(t)
	snapshot, err := s.CreateMemorySnapshot(t.Context(), b, "SNAP-crash", ids)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if _, err := s.UpdateMemory(t.Context(), b.ProjectID, id, 0, func(m *model.MemoryRecordV2) error { m.Body = "later body " + id; return nil }); err != nil {
			t.Fatal(err)
		}
	}
	preview, err := s.PreviewMemorySnapshot(t.Context(), b, snapshot.ID, 1, snapshot.Digest, "PREVIEW-crash")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	child := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestMemorySnapshotRestoreCrashBeforeCommitReopensWithoutPartialState$")
	child.Env = append(os.Environ(), crashEnv+"="+path)
	output, err := child.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 73 {
		t.Fatalf("crash did not reach actual restore commit boundary: %v %s", err, output)
	}
	reopened, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	for _, id := range ids {
		rec, err := reopened.GetMemoryV2(t.Context(), b.ProjectID, id)
		if err != nil || rec.Revision != 1 || rec.Body != "later body "+id {
			t.Fatalf("process crash retained partial restore: %+v %v", rec, err)
		}
	}
	var count int
	if err := reopened.db.QueryRowContext(t.Context(), `SELECT count(*) FROM extreme_memory_restore_history`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("process crash retained history: %d %v", count, err)
	}
	if _, consumed, err := loadMemoryPreview(t.Context(), reopened.db, b, preview.ID); err != nil || consumed {
		t.Fatalf("process crash consumed preview: %v %v", consumed, err)
	}
	if _, err := reopened.RestoreMemorySnapshot(t.Context(), b, preview); err != nil {
		t.Fatalf("pre-crash exact preview no longer restorable: %v", err)
	}
}

func TestMemorySnapshotRestoresOnlySelectedRecordsAndBoundsAggregateBodies(t *testing.T) {
	s, _, b, ids := memorySnapshotFixture(t)
	snapshot, err := s.CreateMemorySnapshot(t.Context(), b, "SNAP-selected", ids[:1])
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if _, err := s.UpdateMemory(t.Context(), b.ProjectID, id, 0, func(m *model.MemoryRecordV2) error { m.Body = "later " + id; return nil }); err != nil {
			t.Fatal(err)
		}
	}
	newRecord, err := s.GetMemoryV2(t.Context(), b.ProjectID, ids[1])
	if err != nil {
		t.Fatal(err)
	}
	newRecord.ID, newRecord.Revision, newRecord.Body, newRecord.ContentDigest = "MEM-post-snapshot", 0, "post snapshot body", ""
	if err := s.WriteMemoryV2(t.Context(), newRecord); err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewMemorySnapshot(t.Context(), b, snapshot.ID, 1, snapshot.Digest, "PREVIEW-selected")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RestoreMemorySnapshot(t.Context(), b, preview); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{ids[1], newRecord.ID} {
		rec, err := s.GetMemoryV2(t.Context(), b.ProjectID, id)
		expected := "later " + id
		if id == newRecord.ID {
			expected = "post snapshot body"
		}
		if err != nil || rec.Body != expected {
			t.Fatalf("selected restore touched %s: %+v %v", id, rec, err)
		}
	}
	var largeIDs []string
	for _, id := range []string{"MEM-large-a", "MEM-large-b", "MEM-large-c", "MEM-large-d", "MEM-large-e"} {
		large := newRecord
		large.ID, large.Body, large.ContentDigest = id, strings.Repeat("bounded memory body ", 45000), ""
		if err := s.WriteMemoryV2(t.Context(), large); err != nil {
			t.Fatal(err)
		}
		largeIDs = append(largeIDs, id)
	}
	if _, err := s.CreateMemorySnapshot(t.Context(), b, "SNAP-aggregate-too-large", largeIDs); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("aggregate body limit bypass: %v", err)
	}
}

func TestMemorySnapshotScopeRefusalAndBoundedPreviewKeepCurrentAccessControls(t *testing.T) {
	for _, scope := range []string{"acl", "session", "operator_private", "agent"} {
		t.Run(scope, func(t *testing.T) {
			s, _, b, ids := memorySnapshotFixture(t)
			if _, err := s.UpdateMemory(t.Context(), b.ProjectID, ids[0], 0, func(m *model.MemoryRecordV2) error {
				if scope == "acl" {
					m.ACLScope = "foreign"
				} else {
					m.Scope = scope
					m.ScopeID = "foreign"
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreateMemorySnapshot(t.Context(), b, "SNAP-forbidden", ids); !errors.Is(err, authz.ErrDenied) {
				t.Fatalf("foreign %s body captured: %v", scope, err)
			}
		})
	}
	s, _, b, ids := memorySnapshotFixture(t)
	if _, err := s.UpdateMemory(t.Context(), b.ProjectID, ids[0], 0, func(m *model.MemoryRecordV2) error { m.Body = strings.Repeat("古い内容", 300); return nil }); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.CreateMemorySnapshot(t.Context(), b, "SNAP-preview", ids[:1])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateMemory(t.Context(), b.ProjectID, ids[0], 1, func(m *model.MemoryRecordV2) error {
		m.Body = strings.Repeat("新しい内容", 300)
		m.ACLScope = b.Actor
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewMemorySnapshot(t.Context(), b, snapshot.ID, 1, snapshot.Digest, "PREVIEW-bounded")
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Changes) != 1 || !preview.Changes[0].Truncated || len(preview.Changes[0].BeforeBody) > 512 || len(preview.Changes[0].AfterBody) > 512 || !utf8.ValidString(preview.Changes[0].BeforeBody) || !utf8.ValidString(preview.Changes[0].AfterBody) {
		t.Fatalf("unbounded/invalid preview: %+v", preview)
	}
	if _, err := s.RestoreMemorySnapshot(t.Context(), b, preview); err != nil {
		t.Fatal(err)
	}
	current, err := s.GetMemoryV2(t.Context(), b.ProjectID, ids[0])
	if err != nil || current.ACLScope != b.Actor || current.Revision != 3 || current.Body != snapshot.Records[0].Body {
		t.Fatalf("restore lost current access control or body: %+v %v", current, err)
	}
}

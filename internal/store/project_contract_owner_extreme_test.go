//go:build !governed

package store

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

func TestProjectContractTaskMetadataDoesNotCrossStorageProjects(t *testing.T) {
	s := projectStore(t)
	const local = "PROJECT-local"
	if err := s.InitProject(t.Context(), model.Project{ID: "PROJECT-other", Repository: "other", DefaultBranch: "main", PackVersion: "1"}); err != nil {
		t.Fatal(err)
	}
	for _, task := range []struct{ id, project string }{{"TASK-alpha", local}, {"TASK-beta", local}, {"TASK-foreign", "PROJECT-other"}} {
		_, err := s.db.ExecContext(t.Context(), `INSERT INTO tasks(task_id,project_id,title,status,risk,revision,created_at,updated_at) VALUES(?,?,'Task','proposed','R0',0,'2026-10-12T00:00:00Z','2026-10-12T00:00:00Z')`, task.id, task.project)
		if err != nil {
			t.Fatal(err)
		}
		r := contextStoreRecord("MEM-"+task.id, time.Now().UTC().Add(-time.Second))
		r.Scope = "task"
		r.ScopeID = task.id
		r.ContentDigest = r.CanonicalDigest()
		if err = s.WriteMemoryV2(t.Context(), r); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := s.ProjectContractTaskScopes(t.Context(), local)
	if err != nil || !reflect.DeepEqual(ids, []string{"TASK-alpha", "TASK-beta"}) {
		t.Fatalf("task scopes crossed project: %v %v", ids, err)
	}
	binding := projectcontext.ProjectBinding{CanonicalID: "canonical-local", StorageID: local}
	task, err := s.ReadProjectContextTask(t.Context(), binding, "TASK-alpha")
	if err != nil || task.ID != "TASK-alpha" {
		t.Fatal(task, err)
	}
	if _, err = s.ReadProjectContextTask(t.Context(), binding, "TASK-foreign"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("foreign task metadata disclosed: %v", err)
	}
	if _, err = s.ProjectContractTaskScopes(t.Context(), ""); !errors.Is(err, projectcontext.ErrInvalid) {
		t.Fatal("empty project accepted", err)
	}
	if _, err = s.ReadProjectContextTask(t.Context(), projectcontext.ProjectBinding{}, "TASK-alpha"); !errors.Is(err, projectcontext.ErrInvalid) {
		t.Fatal("empty binding accepted", err)
	}
	if _, err = s.ReadProjectContractOwnerMemory(t.Context(), local, "owner", "MEM-TASK-alpha"); err != nil {
		t.Fatal("canonical exact task row unreadable", err)
	}
	if _, err = s.ReadProjectContractOwnerMemory(t.Context(), local, "owner", "MEM-TASK-foreign"); !errors.Is(err, authz.ErrUnauthorized) {
		t.Fatal("foreign task body disclosed", err)
	}
	r, err := s.GetMemoryV2(t.Context(), local, "MEM-TASK-alpha")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateMemory(t.Context(), local, r.ID, r.Revision, func(m *model.MemoryRecordV2) error { m.ACLScope = "other-owner"; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReadProjectContractOwnerMemory(t.Context(), local, "owner", r.ID); !errors.Is(err, authz.ErrUnauthorized) {
		t.Fatal("private ACL bypassed", err)
	}
}

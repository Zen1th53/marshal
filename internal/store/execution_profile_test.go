package store

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestExecutionModelPreferenceIsDurableAndCASProtected(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.InitProject(ctx, model.Project{
		ID: "PRJ-model-preference", Repository: "/tmp/model-preference", DefaultBranch: "main", PackVersion: "test",
	}); err != nil {
		t.Fatalf("initialize project: %v", err)
	}

	first, err := st.SetExecutionModelPreference(ctx, model.ExecutionModelPreference{
		ProjectID: "PRJ-model-preference", Adapter: "codex", Model: "gpt-5.6-terra",
	}, 0)
	if err != nil {
		t.Fatalf("create preference: %v", err)
	}
	if first.Revision != 1 || first.UpdatedAt.IsZero() {
		t.Fatalf("created preference = %+v, want revision 1 and timestamp", first)
	}

	reread, err := st.GetExecutionModelPreference(ctx, "PRJ-model-preference", "codex")
	if err != nil {
		t.Fatalf("reread preference: %v", err)
	}
	if reread != first {
		t.Fatalf("reread preference = %+v, want %+v", reread, first)
	}

	updated, err := st.SetExecutionModelPreference(ctx, model.ExecutionModelPreference{
		ProjectID: "PRJ-model-preference", Adapter: "codex", Model: "gpt-6-astra",
	}, first.Revision)
	if err != nil {
		t.Fatalf("update preference: %v", err)
	}
	if updated.Revision != 2 || updated.Model != "gpt-6-astra" {
		t.Fatalf("updated preference = %+v", updated)
	}

	_, err = st.SetExecutionModelPreference(ctx, model.ExecutionModelPreference{
		ProjectID: "PRJ-model-preference", Adapter: "codex", Model: "gpt-5.6-terra",
	}, first.Revision)
	if !errors.Is(err, model.ErrConflict) {
		t.Fatalf("stale preference update error = %v, want ErrConflict", err)
	}
	after, err := st.GetExecutionModelPreference(ctx, "PRJ-model-preference", "codex")
	if err != nil || after != updated {
		t.Fatalf("stale update changed preference: %+v, err=%v", after, err)
	}
}

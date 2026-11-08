//go:build !governed

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/plan"
)

func TestResolveMarshalWorkerUsesApprovedModelInsteadOfProfileDefault(t *testing.T) {
	r, draft, profile := canonicalNativeBridgeFixture(t)
	profile.DefaultModel = "other-default"
	profile.SupportedModels = []string{"other-default", "approved-model"}
	profile.ExpiresAt = time.Now().Add(time.Hour)
	if err := r.Store().SaveHarnessProfile(t.Context(), profile); err != nil {
		t.Fatal(err)
	}
	canonical, err := r.PrepareNativeMarshalDraft(t.Context(), "SESSION-required", draft)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Marshal().StartPlanningFromDraft(t.Context(), "selected-worker", "Fix the documented typo.", canonical, marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	p, err := r.Store().GetPlan(t.Context(), canonical.Plan.ID, canonical.Plan.Version)
	if err != nil {
		t.Fatal(err)
	}
	route := p.Routes["fix"]
	route.Model = "approved-model"
	p.Routes["fix"] = route
	p.State = plan.StateApproved
	if err := r.Store().SavePlan(t.Context(), p, p.Version); err != nil {
		t.Fatal(err)
	}
	record, err := r.Store().GetMarshalRun(t.Context(), r.Marshal().ProjectID, "selected-worker")
	if err != nil {
		t.Fatal(err)
	}
	record.Value.PlanVersion++
	if _, err := r.Store().SetMarshalRun(t.Context(), r.Marshal().ProjectID, "selected-worker", record.Value, record.Revision); err != nil {
		t.Fatal(err)
	}
	req := driver.Request{RunID: "selected-worker", Task: canonical.Tasks[0]}
	selected, err := r.ResolveMarshalWorkerModel(t.Context(), req)
	if err != nil || selected != "approved-model" {
		t.Fatalf("approved selection lost: %q %v", selected, err)
	}
	// Attach the runtime resolver to the production native driver and observe
	// the argv of a real supervised subprocess.
	binary := filepath.Join(t.TempDir(), "native-codex")
	capture := filepath.Join(t.TempDir(), "argv")
	t.Setenv("MARSHAL_TEST_ARGV", capture)
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$MARSHAL_TEST_ARGV\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	native := driver.Codex(binary)
	native.ResolveModel = r.ResolveMarshalWorkerModel
	req.Worktree = r.ProjectRoot()
	req.Brief = "work"
	req.Task.BaseCommit, err = gitMarshal(t.Context(), r.ProjectRoot(), "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	handle, err := native.Launch(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	<-handle.Done()
	raw, err := os.ReadFile(capture)
	if err != nil || !strings.Contains(string(raw), "--model\napproved-model\n") {
		t.Fatalf("runtime selection lost before argv: %q %v", raw, err)
	}
	req.Model = "other-default"
	if _, err := r.ResolveMarshalWorkerModel(t.Context(), req); err == nil {
		t.Fatal("request overrode approved model")
	}
	req.Model = ""
	profile.ExpiresAt = time.Now().Add(-time.Minute)
	profile.ProbedAt = time.Now().Add(-25 * time.Hour)
	if err := r.Store().SaveHarnessProfile(t.Context(), profile); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ResolveMarshalWorkerModel(t.Context(), req); err == nil {
		t.Fatal("expired model selected")
	}
}

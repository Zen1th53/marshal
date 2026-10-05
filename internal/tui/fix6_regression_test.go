package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/projectid"
)

func TestContinuationGrantDeliversProjectDataToMarshal(t *testing.T) {
	w, rt := realControlWorkspace(t, "SESSION-fix6-continuation")
	ctx := context.Background()
	brief, err := marshalRoleBriefing([]string{"codex"}, marshal.DefaultSettings(), marshal.Standard)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{".marshal/inbox/marshal.md", "Read that file", "labelled untrusted data"} {
		if !strings.Contains(brief, want) {
			t.Errorf("Marshal was not told how to read continuation: %q", want)
		}
	}
	folder := t.TempDir()
	denied := filepath.Join(folder, "denied")
	if err := os.Mkdir(denied, 0700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, cwd, text string }{
		{filepath.Join(folder, "local.jsonl"), rt.ProjectRoot(), "Pending: implement feature"},
		{filepath.Join(folder, "foreign.jsonl"), t.TempDir(), "FOREIGN_CONTENT"},
		{filepath.Join(denied, "hidden.jsonl"), rt.ProjectRoot(), "DENIED_CONTENT"},
	} {
		data := fmt.Sprintf("{\"type\":\"session_meta\",\"timestamp\":\"2026-10-01T10:00:00Z\",\"payload\":{\"id\":\"earlier\",\"cwd\":%q}}\n{\"type\":\"response_item\",\"payload\":{\"type\":\"message\",\"role\":\"assistant\",\"phase\":\"final_answer\",\"content\":[{\"type\":\"output_text\",\"text\":%q}]}}\n", tc.cwd, tc.text)
		if err := os.WriteFile(tc.path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.cmd.handleContinue(ctx, []string{"codex", folder}); err != nil {
		t.Fatal(err)
	}
	path := inboxPath(rt.ProjectRoot(), "marshal")
	before, _ := os.ReadFile(path)
	if strings.Contains(string(before), "Pending: implement feature") {
		t.Fatal("ungranted content delivered")
	}
	if err := w.decidePermission(ctx, permission.Request{Kind: "read", Object: denied, Scope: "session", Who: "operator"}, false, "test"); err != nil {
		t.Fatal(err)
	}
	if err := w.decidePermission(ctx, permission.Request{Kind: "read", Object: folder, Scope: "session", Who: "Marshal"}, true, "test"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("granted continuation never reached native Marshal: %v", err)
	}
	for _, want := range []string{"untrusted data", folder, "Pending: implement feature", "session=earlier", "date=2026-10-01", "Summarise"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("Marshal delivery missing %q: %s", want, data)
		}
	}
	for _, hidden := range []string{"FOREIGN_CONTENT", "DENIED_CONTENT"} {
		if strings.Contains(string(data), hidden) {
			t.Fatalf("unavailable content delivered: %s", hidden)
		}
	}
}

type fix6CodexAdapter struct {
	adapter.Adapter
	runs int
}

func (f *fix6CodexAdapter) ValidateModel(_ context.Context, name string) error {
	if name != "test-model" {
		return model.ErrInvalid
	}
	return nil
}
func (f *fix6CodexAdapter) Probe(context.Context) (adapter.Probe, error) {
	return adapter.Probe{Name: "codex", Available: true, Version: "test"}, nil
}
func (f *fix6CodexAdapter) Run(_ context.Context, req adapter.Request) (adapter.Result, error) {
	f.runs++
	if err := os.WriteFile(filepath.Join(req.Worktree, "output.txt"), []byte("done\n"), 0600); err != nil {
		return adapter.Result{}, err
	}
	return adapter.Result{Adapter: "codex", Status: adapter.StatusSuccess, SessionID: "test-thread", Model: req.Model}, nil
}

func TestCodexCommandDispatchesAssignedTaskAndReleasesFailedClaim(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(fmt.Sprint("invalid-model=", invalid), func(t *testing.T) {
			w, original := realControlWorkspace(t, "SESSION-fix6-dispatch")
			root := original.ProjectRoot()
			if err := original.Close(); err != nil {
				t.Fatal(err)
			}
			fake := &fix6CodexAdapter{}
			rt, err := app.OpenWithOptions(t.Context(), root, app.Options{Adapters: map[string]adapter.Adapter{"codex": fake}})
			if err != nil {
				t.Fatal(err)
			}
			defer rt.Close()
			w.AttachRuntime(rt, projectid.ID(rt.ProjectIdentity()))
			agent, err := rt.RegisterAgent(t.Context(), app.RegisterAgentRequest{Name: "Codex worker", Role: model.RoleDeveloper, ModelProvider: "codex"})
			if err != nil {
				t.Fatal(err)
			}
			// The assignment identifies the worker even when the provider has
			// several registered agents; dispatch must not guess or re-claim.
			if _, err := rt.RegisterAgent(t.Context(), app.RegisterAgentRequest{Name: "Other Codex worker", Role: model.RoleDeveloper, ModelProvider: "codex"}); err != nil {
				t.Fatal(err)
			}
			local, err := rt.OpenLocalControl(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			ctx := local.Context(t.Context())
			envelope := app.CommandEnvelope{ProjectID: rt.ProjectIdentity(), SessionID: "SESSION-fix6-dispatch", TargetID: "TASK-fix6", IdempotencyKey: "create"}
			task, err := rt.CommandTask(ctx, envelope, app.TaskCommand{Operation: "create", Title: "write output.txt"})
			if err != nil {
				t.Fatal(err)
			}
			envelope.ExpectedVersion, envelope.IdempotencyKey = task.Revision, "assign"
			task, err = rt.CommandTask(ctx, envelope, app.TaskCommand{Operation: "assign", AgentID: agent.ID})
			if err != nil {
				t.Fatal(err)
			}
			lease, err := rt.Store().ActiveLease(ctx, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			name := "test-model"
			if invalid {
				name = "invalid"
			}
			out, err := w.ExecuteCommand(ctx, "/codex run "+task.ID+" "+name)
			if err != nil {
				t.Fatal(err)
			}
			if invalid {
				if !strings.Contains(out, "dispatch failed") {
					t.Fatalf("invalid model accepted: %s", out)
				}
			} else if !strings.Contains(out, "Dispatched task") || fake.runs != 1 {
				t.Errorf("assigned Codex task did not dispatch: %s, runs=%d", out, fake.runs)
			}
			if _, err := rt.Store().ActiveLease(ctx, task.ID); !errors.Is(err, model.ErrNotFound) {
				t.Errorf("dispatch retained claim: %v", err)
			}
			session, err := rt.Store().GetSession(ctx, lease.Lease.SessionID)
			if err != nil || session.Status != model.SessionTerminated || session.TaskID != nil {
				t.Errorf("claim session retained: %+v %v", session, err)
			}
			if invalid {
				task, err = rt.Task(ctx, task.ID)
				if err != nil {
					t.Fatal(err)
				}
				envelope.ExpectedVersion, envelope.IdempotencyKey = task.Revision, "reassign"
				if _, err := rt.CommandTask(ctx, envelope, app.TaskCommand{Operation: "assign", AgentID: agent.ID}); err != nil {
					t.Fatalf("failed dispatch prevents reassignment: %v", err)
				}
			}
		})
	}
}

package app

import (
	"context"
	"github.com/Zen1th53/marshal/internal/statepath"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/project"
)

func TestMissingArchitectureDoesNotRequestRootMarkdown(t *testing.T) {
	if strings.Contains(ProjectArchitectureRule, "record it in PROJECT.md and ARCHITECTURE.md") {
		t.Fatal("knowledge rule asks agents to create root planning documents")
	}
	if !strings.Contains(ProjectArchitectureRule, ".marshal/marshal/plan/REQUIREMENTS.md") {
		t.Fatal("missing purpose and architecture must stay in the private reviewed plan")
	}
}

func TestWiredMarshalRoleTurnsCarryArchitecture(t *testing.T) {
	stub := filepath.Join(t.TempDir(), "codex")
	script := "#!/bin/sh\nlast=''; for arg; do last=$arg; done; if [ \"$last\" = '-' ]; then while IFS= read -r line || [ -n \"$line\" ]; do printf '%s\\n' \"$line\"; done > \"$KNOWLEDGE_PROMPT_FILE\"; else printf '%s\\n' \"$@\" > \"$KNOWLEDGE_PROMPT_FILE\"; fi\nprintf '%s\\n' '{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"{}\"}}'\n"
	if err := os.WriteFile(stub, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	cli := &MarshalCLI{Provider: "codex", Binary: stub}
	ctx := requiredMarshalTestContext(t, cli)
	rt := cli.runtime
	if rt == nil {
		rt = browserContextRuntime(t)
	}
	root := rt.ProjectRoot()
	for name, body := range map[string]string{"README.md": "# Atlas\n\nKeep engineering decisions.\n", "ARCHITECTURE.md": "# Architecture\n\nOnly the runtime owns durable state.\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	marshalGit(t, root, "add", "README.md", "ARCHITECTURE.md")
	marshalGit(t, root, "commit", "-m", "architecture fixture")
	service, err := rt.MarshalWired(MarshalWiring{Provider: "codex", Approver: func(context.Context, string, string) (string, error) { return "operator", nil }})
	if err != nil {
		t.Fatal(err)
	}
	cli = service.Model.(*MarshalCLI)
	cli.Binary = stub
	promptFile := filepath.Join(t.TempDir(), "prompt.txt")
	t.Setenv("KNOWLEDGE_PROMPT_FILE", promptFile)
	for _, schema := range []string{marshalDraftSchema, marshalReviewSchema, marshalVerifySchema} {
		var result map[string]any
		if err := cli.turn(ctx, "Inspect cache behavior.", schema, &result); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(promptFile)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "Only the runtime owns durable state") {
			t.Fatal("structured role turn omitted architecture")
		}
	}
}

func TestWorkerKnowledgeFollowsPreparedSnapshot(t *testing.T) {
	s, root := marshalFixture(t, 1)
	s.Worktrees = filepath.Join(root, statepath.DirName, "worktrees")
	if err := os.WriteFile(filepath.Join(root, "ARCHITECTURE.md"), []byte("# Architecture\n\nThe store owns durable state.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	marshalGit(t, root, "add", "ARCHITECTURE.md")
	marshalGit(t, root, "commit", "-m", "architecture")
	marshalGit(t, root, "switch", "--detach")
	rt := &Runtime{store: s.Store, memoryService: NewMemoryService(s.Store), layout: project.Layout{Root: root, RuntimeDir: filepath.Join(root, statepath.DirName), Worktrees: s.Worktrees}}
	rt.bindMarshalProjectKnowledge(s)
	contextReader := s.ProjectContext
	s.ProjectContext = func(ctx context.Context, dir, query string) (string, error) {
		packet, err := contextReader(ctx, dir, query)
		if err != nil {
			cmd := exec.Command("git", "-C", dir, "status", "--porcelain=v1", "--untracked-files=no")
			output, _ := cmd.CombinedOutput()
			t.Logf("failed knowledge source status: %s", output)
		}
		return packet, err
	}
	if _, err := s.StartPlanning(t.Context(), "run", "write a.txt", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(t.Context(), "run"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ARCHITECTURE.md"), []byte("# Architecture\n\nUnapproved checkout edits replace the store.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	seen := false
	s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(_ context.Context, request driver.Request) ([]marshal.CommandRecord, error) {
		seen = true
		if !strings.Contains(request.Brief, "The store owns durable state") || strings.Contains(request.Brief, "Unapproved checkout edits") {
			t.Error("worker received architecture from a different checkout state")
		}
		return nil, os.WriteFile(filepath.Join(request.Worktree, "a.txt"), []byte("done\n"), 0600)
	}}
	if _, err := s.Execute(t.Context(), "run", marshalBrief, nil); err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("worker was not invoked")
	}
}
